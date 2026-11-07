package httpapi

import (
	"net/http"
	"os"
	"path/filepath"

	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/store"
)

func (s *Server) mongoConfigPath(in store.Instance) string {
	return filepath.Join(s.instanceDir(in), "conf", mongoctl.ConfigFileName)
}

func (s *Server) ensureMongoConfig(in store.Instance) (string, error) {
	path := s.mongoConfigPath(in)
	if err := mongoctl.EnsureConfig(path, in.EngineVersion); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Server) serveMongoConfigFile(w http.ResponseWriter, r *http.Request, in store.Instance) {
	path, err := s.ensureMongoConfig(in)
	if err != nil {
		s.fail(w, r, errInternal("准备 mongod.conf 失败："+err.Error()))
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		s.fail(w, r, errInternal("读取 mongod.conf 失败："+err.Error()))
		return
	}
	_, backupErr := os.Stat(path + ".bak")
	writeOK(w, r, map[string]any{
		"path":         mongoctl.ContainerConfFile,
		"content":      string(raw),
		"backupExists": backupErr == nil,
		"suggested":    mongoctl.ConfigTemplate(in.EngineVersion),
		"missing":      []string{},
		"running":      true,
		"selected":     mongoctl.ConfigFileName,
		"files": []map[string]any{{
			"name": mongoctl.ConfigFileName, "label": "mongod.conf", "writable": true,
			"note": "MongoDB YAML 配置：数据目录和容器端口固定，保存后需要重启服务",
		}},
	})
}

func (s *Server) saveMongoConfigFile(w http.ResponseWriter, r *http.Request, id identity, in store.Instance) {
	var req configFileReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Content) > configFileMax {
		s.fail(w, r, errBadRequest("mongod.conf 超过 64KB，请确认没有误粘贴"))
		return
	}
	if err := mongoctl.ValidateConfig(req.Content); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	path, err := s.ensureMongoConfig(in)
	if err != nil {
		s.fail(w, r, errInternal("准备 mongod.conf 失败："+err.Error()))
		return
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", raw, 0o640)
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		s.fail(w, r, errInternal("写入 mongod.conf 失败："+err.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "编辑 mongod.conf")
	writeOK(w, r, map[string]any{
		"needRestart": true,
		"message":     "mongod.conf 已保存，重启服务后生效",
	})
}

func (s *Server) restoreMongoConfigFile(w http.ResponseWriter, r *http.Request, id identity, in store.Instance) {
	path, err := s.ensureMongoConfig(in)
	if err != nil {
		s.fail(w, r, errInternal("准备 mongod.conf 失败："+err.Error()))
		return
	}
	raw, err := os.ReadFile(path + ".bak")
	if err != nil {
		s.fail(w, r, errNotFound("没有可恢复的上一版 mongod.conf"))
		return
	}
	if err := mongoctl.ValidateConfig(string(raw)); err != nil {
		s.fail(w, r, errBadRequest("备份文件校验失败："+err.Error()))
		return
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		s.fail(w, r, errInternal("恢复 mongod.conf 失败："+err.Error()))
		return
	}
	s.audit(r, id, "instance.config.file", "instance", in.ID, "ok", "恢复 mongod.conf")
	writeOK(w, r, map[string]any{
		"needRestart": true,
		"message":     "已恢复上一版 mongod.conf，重启服务后生效",
	})
}
