package httpapi

import (
	"net/http"
	"strings"

	"niudbm/server/internal/pgctl"
	"niudbm/server/internal/store"
)

// PostgreSQL 的配置文件页。
//
// 与 MySQL 的差别不只是"文件在哪"：PG 的参数分两处——postgresql.conf 是主配置，
// postgresql.auto.conf 是 ALTER SYSTEM 写的，pg_hba.conf 决定谁能连。
// 用户要的是"能直接在页面上改"，所以这里做三件事：
//   1. 列出三个文件，标明哪个能改（auto.conf 不行，会被 ALTER SYSTEM 覆盖）；
//   2. 读原文、写原文，写之前留一份 .niudbm.bak；
//   3. 写完立刻 reload，并把 pg_file_settings 里的解析错误原样回给前端——
//      写坏了当场知道错在第几行，而不是等下次重启才发现起不来。
// 数据目录可能在 Docker 卷里，因此读写都通过容器，而不是宿主路径。

// pgConfigName 从查询参数取文件名，默认 postgresql.conf。
func pgConfigName(r *http.Request) string {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		return "postgresql.conf"
	}
	return name
}

// pgConfigView 是界面上配置文件页需要的一次性数据。
func pgConfigFilesJSON() []map[string]any {
	files := pgctl.Spec{}.ConfigFiles()
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		out = append(out, map[string]any{
			"name": f.Name, "label": f.Label, "writable": f.Writable, "note": f.Note,
		})
	}
	return out
}

func (s *Server) servePgConfigFile(w http.ResponseWriter, r *http.Request, in store.Instance) {
	spec, err := s.pgSpec(in)
	if err != nil {
		s.fail(w, r, errInternal(err.Error()))
		return
	}
	name := pgConfigName(r)
	content, rerr := spec.ReadConfigFile(name)
	if rerr != nil {
		// 读不到也要把文件清单给出去，否则界面连切换文件都做不了
		writeOK(w, r, map[string]any{
			"path":         pgPathOf(name),
			"content":      "",
			"selected":     name,
			"files":        pgConfigFilesJSON(),
			"backupExists": false,
			"readError":    rerr.Error(),
		})
		return
	}
	writeOK(w, r, map[string]any{
		"path":         pgPathOf(name),
		"content":      content,
		"selected":     name,
		"files":        pgConfigFilesJSON(),
		"backupExists": spec.HasBackup(name),
		"running":      spec.Running(),
	})
}

func (s *Server) savePgConfigFile(w http.ResponseWriter, r *http.Request, in store.Instance) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	var req configFileReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Content) > configFileMax {
		s.fail(w, r, errBadRequest("配置文件超过 64KB，请确认没有误粘贴"))
		return
	}
	if strings.ContainsRune(req.Content, 0) {
		s.fail(w, r, errBadRequest("配置文件里不能有二进制内容"))
		return
	}
	spec, err := s.pgSpec(in)
	if err != nil {
		s.fail(w, r, errInternal(err.Error()))
		return
	}
	name := pgConfigName(r)
	warnings, werr := spec.WriteConfigFile(name, req.Content)
	if werr != nil {
		s.fail(w, r, errBadRequest(werr.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "编辑配置文件 "+name)
	msg := "已保存并已重载（reload）"
	if len(warnings) > 0 {
		msg = "已保存，但有几处配置没通过解析，仍在使用改动前的值"
	}
	writeOK(w, r, map[string]any{
		"needRestart": spec.PendingRestart(),
		"warnings":    warnings,
		"message":     msg,
	})
}

func (s *Server) restorePgConfigFile(w http.ResponseWriter, r *http.Request, in store.Instance) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	spec, err := s.pgSpec(in)
	if err != nil {
		s.fail(w, r, errInternal(err.Error()))
		return
	}
	name := pgConfigName(r)
	warnings, rerr := spec.RestoreConfigFile(name)
	if rerr != nil {
		s.fail(w, r, errBadRequest(rerr.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "恢复上一版配置 "+name)
	writeOK(w, r, map[string]any{
		"needRestart": spec.PendingRestart(),
		"warnings":    warnings,
		"message":     "已恢复上一版配置并重载",
	})
}

func pgPathOf(name string) string {
	return "/var/lib/postgresql/data/" + name
}
