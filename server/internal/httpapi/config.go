package httpapi

import (
	"errors"
	"net/http"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/pgctl"
)

// configReq 是配置页提交的内容：只传改动过的键，其它键保持原值。
type configReq struct {
	Values map[string]string `json:"values"`
}

// handleGetConfig 返回该实例的可调项。
//
// 参数从容器里真实运行的实例读（`SHOW VARIABLES`），不是从台账猜；
// 实例没在跑就如实报错，给一份假数据只会让用户改完发现没生效。
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库的配置文件不在本机，暂不支持在线修改"))
		return
	}

	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	items, err := spec.Settings()
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，请先启动再查看配置"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取配置失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{
		"engine":   in.Engine,
		"items":    items,
		"readonly": false,
	})
}

// handleUpdateConfig 保存配置：写进实例的 conf.d，需要重启的项即时告诉用户。
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库的配置文件不在本机，暂不支持在线修改"))
		return
	}

	var req configReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Values) == 0 {
		s.fail(w, r, errBadRequest("没有需要保存的配置"))
		return
	}

	// 配置写入要和备份/恢复互斥：恢复会重启容器，写配置并发会互相覆盖
	unlock := s.lockInstance(in.ID)
	defer unlock()

	detail := describeChanges(req.Values)

	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	needRestart, err := spec.ApplySettings(req.Values)
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，请先启动再改配置"))
		return
	}
	if err != nil {
		s.audit(r, id, "instance.config.update", "instance", in.ID, "failed", detail+"；"+err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	s.audit(r, id, "instance.config.update", "instance", in.ID, "ok", detail)
	writeOK(w, r, map[string]any{
		"needRestart": needRestart,
		"message":     restartHint(needRestart),
	})
}

// restartHint 把"要不要重启"翻译成一句人话，前端直接展示。
func restartHint(needRestart bool) string {
	if needRestart {
		return "配置已保存，其中有参数必须重启实例才生效"
	}
	return "配置已保存并已生效"
}

// handleConfigPresets 返回按内存档位算好的参数组合，供"性能调整"页一键填表。
func (s *Server) handleConfigPresets(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) || in.Engine == engineMongoDB {
		s.fail(w, r, errBadRequest("外部数据库暂不提供参数档位"))
		return
	}
	// 档位是"按内存算参数"，两套参数名完全不同，不能混着给
	items := mysqlctl.TuningPresets()
	if in.Engine == enginePostgres {
		items = pgctl.Presets()
	}
	writeOK(w, r, map[string]any{"items": items})
}

// handleInstanceStatus 返回运行状态指标（SHOW GLOBAL STATUS 里最常用的那些）。
func (s *Server) handleInstanceStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	spec, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	items, err := spec.Status()
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，启动后才能看运行状态"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取运行状态失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"items": items})
}
