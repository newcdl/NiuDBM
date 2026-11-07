package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

type rootAccessReq struct {
	Remote bool `json:"remote"`
}

// handleGetRootAccess 告诉前端 root 现在是否允许远程连接。
func (s *Server) handleGetRootAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库的账号由原数据库管理，本应用不修改其 root/超级用户"))
		return
	}
	if in.Engine == enginePostgres {
		s.fail(w, r, errBadRequest("PostgreSQL 没有 root@host 这种账号：来源由 pg_hba.conf 控制，默认已允许密码登录"))
		return
	}
	spec, err := s.mysqlSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	remote, err := spec.RootRemote()
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，启动后才能查看远程访问设置"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取 root 远程访问设置失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"remote": remote})
}

// handleSetRootAccess 开关 root 的远程访问。
//
// 关掉之后外部只能用业务账号连，root 只在容器内可用；这是收敛攻击面的常规做法，
// 但开了又关时 root@'%' 会被删掉，因此界面上必须明确提示。
func (s *Server) handleSetRootAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库的账号由原数据库管理，本应用不修改其 root/超级用户"))
		return
	}
	var req rootAccessReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Engine == enginePostgres {
		s.fail(w, r, errBadRequest("PostgreSQL 没有 root@host 这种账号：来源由 pg_hba.conf 控制，默认已允许密码登录"))
		return
	}
	spec, err := s.mysqlSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	password := s.readRootSecret(in.ID)
	if err := spec.SetRootRemote(req.Remote, password); err != nil {
		if isNotRunning(err) {
			s.fail(w, r, errState("实例未运行，启动后才能修改远程访问"))
			return
		}
		s.audit(r, id, "instance.root.remote", "instance", in.ID, "failed", err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	state := "已关闭"
	if req.Remote {
		state = "已开启"
	}
	s.audit(r, id, "instance.root.remote", "instance", in.ID, "ok", "root 远程访问"+state)
	writeOK(w, r, map[string]any{"remote": req.Remote, "message": "root 远程访问" + state})
}

type rootPasswordReq struct {
	Password string `json:"password"`
}

// handleSetRootPassword 重设 root 密码。
//
// 改完把新密码写回实例的 root-secret，概览与连接信息里显示的就是它；
// 容器环境变量里的旧密码不会覆盖已初始化的数据目录，所以重启也不会回退。
func (s *Server) handleSetRootPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库的账号由原数据库管理，本应用不修改其 root/超级用户"))
		return
	}
	var req rootPasswordReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	password := strings.TrimSpace(req.Password)
	if len(password) < 8 {
		s.fail(w, r, errBadRequest("root 密码至少 8 位"))
		return
	}
	if strings.ContainsAny(password, "&;$'`\"()<>|") {
		// 密码要塞进命令行与 SQL 字面量，这些字符在容器内初始化脚本里会被解释
		s.fail(w, r, errBadRequest("密码不能包含 & ; $ ' ` \" ( ) < > | 这些字符"))
		return
	}

	if in.Engine == enginePostgres {
		s.fail(w, r, errBadRequest("PostgreSQL 没有 root@host 这种账号：来源由 pg_hba.conf 控制，默认已允许密码登录"))
		return
	}
	spec, err := s.mysqlSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	if err := spec.SetRootPassword(password); err != nil {
		if isNotRunning(err) {
			s.fail(w, r, errState("实例未运行，启动后才能修改 root 密码"))
			return
		}
		s.audit(r, id, "instance.root.password", "instance", in.ID, "failed", err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	if err := s.writeRootSecret(in.ID, password); err != nil {
		s.fail(w, r, errInternal("密码已修改，但本地记录保存失败："+err.Error()))
		return
	}
	s.audit(r, id, "instance.root.password", "instance", in.ID, "ok", "重设 root 密码")
	writeOK(w, r, map[string]any{"message": "root 密码已更新"})
}

// ---------- 配置文件原文 ----------

// configFileMax 限制配置文件大小：conf.d 里的片段正常只有几十行，
// 超过这个量级基本是误粘贴（比如把整个 SQL 脚本贴进来了）。
const configFileMax = 64 * 1024

// confFilePath 与 mysqlSpec 里的 ConfDir 保持一致：conf/niudbm.cnf 挂进容器就是 conf.d
func (s *Server) confFilePath(in store.Instance) string {
	return filepath.Join(s.instanceDir(in), "conf", "niudbm.cnf")
}

// handleGetConfigFile 返回实例的配置文件原文。
func (s *Server) handleGetConfigFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库的配置文件不在本机，暂不支持在线修改"))
		return
	}
	if in.Engine == enginePostgres {
		s.servePgConfigFile(w, r, in)
		return
	}
	if in.Engine == engineMongoDB {
		s.serveMongoConfigFile(w, r, in)
		return
	}
	path := s.confFilePath(in)
	content := ""
	if raw, err := os.ReadFile(path); err == nil {
		content = string(raw)
	} else if !os.IsNotExist(err) {
		s.fail(w, r, errInternal("读取配置文件失败："+err.Error()))
		return
	}
	_, backupErr := os.Stat(path + ".bak")
	// 推荐模板与缺失项都不落盘：老实例的文件可能只有几行，前端据此提示并可一键补全
	suggested := ""
	missing := []string{}
	if spec, err := s.mysqlSpec(in); err == nil && in.Engine != enginePostgres {
		suggested = spec.CnfTemplate()
		missing = spec.CnfMissing(content)
	}
	writeOK(w, r, map[string]any{
		"path":         path,
		"content":      content,
		"backupExists": backupErr == nil,
		"suggested":    suggested,
		"missing":      missing,
		"running":      true,
		// 与 PG 的响应形状对齐：界面用同一套"文件清单 + 当前文件"来渲染，
		// MySQL 只有一个文件，PG 有三个
		"selected": "niudbm.cnf",
		"files": []map[string]any{{
			"name": "niudbm.cnf", "label": "niudbm.cnf", "writable": true,
			"note": "conf.d 片段：覆盖默认参数，重启后生效",
		}},
	})
}

type configFileReq struct {
	Content string `json:"content"`
}

// handlePutConfigFile 保存配置文件原文。
//
// 这里是"直接改 my.cnf"的逃生口：白名单之外要写的参数（比如 innodb_page_size）
// 只能从这里进去。写坏了实例会起不来，所以保存前先留一份 .bak，并提供恢复入口。
func (s *Server) handlePutConfigFile(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库的配置文件不在本机，暂不支持在线修改"))
		return
	}
	if in.Engine == enginePostgres {
		s.savePgConfigFile(w, r, in)
		return
	}
	if in.Engine == engineMongoDB {
		s.saveMongoConfigFile(w, r, id, in)
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
	if err := validateCnfText(req.Content); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	path := s.confFilePath(in)
	if raw, err := os.ReadFile(path); err == nil {
		// 留一份上一版：改坏了还能一键回到改之前
		_ = os.WriteFile(path+".bak", raw, 0o640)
	} else if !os.IsNotExist(err) {
		s.fail(w, r, errInternal("读取原配置失败："+err.Error()))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		s.fail(w, r, errInternal("创建配置目录失败："+err.Error()))
		return
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o640); err != nil {
		s.fail(w, r, errInternal("写入配置文件失败："+err.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "手写配置文件 "+path)
	writeOK(w, r, map[string]any{
		"needRestart": true,
		"message":     "配置文件已保存，重启服务后生效",
	})
}

// handleRestoreConfigFile 用 .bak 覆盖回来。
func (s *Server) handleRestoreConfigFile(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库的配置文件不在本机，暂不支持在线修改"))
		return
	}
	if in.Engine == enginePostgres {
		s.restorePgConfigFile(w, r, in)
		return
	}
	if in.Engine == engineMongoDB {
		s.restoreMongoConfigFile(w, r, id, in)
		return
	}
	path := s.confFilePath(in)
	raw, err := os.ReadFile(path + ".bak")
	if err != nil {
		s.fail(w, r, errNotFound("没有可恢复的上一版配置"))
		return
	}
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		s.fail(w, r, errInternal("恢复配置失败："+err.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "恢复上一版配置")
	writeOK(w, r, map[string]any{
		"needRestart": true,
		"message":     "已恢复上一版配置，重启服务后生效",
	})
}

// validateCnfText 做最基本的形状检查，挡住"整段脚本误粘贴"这类明显错误。
func validateCnfText(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return errors.New("配置文件不能为空：留空会导致实例启动失败")
	}
	hasSection := false
	for _, line := range strings.Split(trimmed, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			hasSection = true
			continue
		}
		if !strings.Contains(t, "=") {
			return errors.New("第 " + t + " 行不是 key = value，my.cnf 只接受参数与段落")
		}
	}
	if !hasSection {
		return errors.New("缺少段落头，至少要有一行 [mysqld]")
	}
	return nil
}
