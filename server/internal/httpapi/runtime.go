package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/pgctl"
	"niudbm/server/internal/store"
)

// isAlreadyRunning / isNotRunning 只判断容器状态机里的两种"正常"拒绝：
// 已经开着再 start、本来没开再 stop，都不该报错。
func isAlreadyRunning(err error) bool {
	return errors.Is(err, mysqlctl.ErrAlreadyRunning)
}

func isNotRunning(err error) bool {
	return errors.Is(err, mysqlctl.ErrNotRunning)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库不能由本应用启动，请到原数据库主机处理"))
		return
	}
	// 部署没成功的实例是一份半成品（可能只初始化了一半、账号还没建），
	// 直接启动只会让用户以为它好了；引导他看日志、删掉重来。
	if in.Status == "failed" || in.Status == "creating" {
		s.fail(w, r, errRejected("这个实例还没有部署成功，请查看部署日志后删除重来"))
		return
	}
	rt, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	started := time.Now()
	if in.Engine == engineMongoDB {
		if startErr := s.rebuildDocker(r.Context(), in); startErr != nil {
			hint := "MongoDB 重建容器失败：" + startErr.Error()
			_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
			s.audit(r, id, "instance.start", "instance", in.ID, "failed", hint)
			s.recordJob(id, jobInstanceStart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
			s.fail(w, r, errState(hint))
			return
		}
		if waitErr := rt.WaitReady(5 * time.Minute); waitErr != nil {
			hint := "MongoDB 启动后未就绪：" + waitErr.Error()
			_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
			s.audit(r, id, "instance.start", "instance", in.ID, "failed", hint)
			s.recordJob(id, jobInstanceStart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
			s.fail(w, r, errState(hint))
			return
		}
		_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
		s.audit(r, id, "instance.start", "instance", in.ID, "ok", "MongoDB 容器已按当前配置重建")
		s.recordJob(id, jobInstanceStart, "instance", in.ID, "runtime="+in.Runtime, started, "已启动", nil)
		writeOK(w, r, map[string]any{"id": in.ID, "status": "running"})
		return
	}
	if startErr := rt.Start(); startErr != nil && !isAlreadyRunning(startErr) {
		// 端口被别的程序抢走是 NAS 上最常见的启动失败：给一句能直接照做的提示，
		// 而不是把 "bind: address already in use" 原样丢给用户
		hint := startErr.Error()
		if !rt.Running() && portInUse(in.Port) {
			hint = "端口 " + strconv.Itoa(in.Port) + " 已被其他程序占用（可能是别的数据库或容器），请释放该端口后重试"
		}
		_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
		s.audit(r, id, "instance.start", "instance", in.ID, "failed", hint)
		s.recordJob(id, jobInstanceStart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
		s.fail(w, r, errState("启动失败："+hint))
		return
	}
	_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
	s.audit(r, id, "instance.start", "instance", in.ID, "ok", "端口 "+strconv.Itoa(in.Port))
	s.recordJob(id, jobInstanceStart, "instance", in.ID, "runtime="+in.Runtime, started,
		"端口 "+strconv.Itoa(in.Port), nil)
	writeOK(w, r, map[string]any{"id": in.ID, "status": "running"})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库不能由本应用停止，请到原数据库主机处理"))
		return
	}
	rt, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	started := time.Now()
	if in.Engine == engineMongoDB {
		if restartErr := s.rebuildDocker(r.Context(), in); restartErr != nil {
			hint := "MongoDB 重建容器失败：" + restartErr.Error()
			_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
			s.audit(r, id, "instance.restart", "instance", in.ID, "failed", hint)
			s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
			s.fail(w, r, errState(hint))
			return
		}
		if waitErr := rt.WaitReady(5 * time.Minute); waitErr != nil {
			hint := "MongoDB 重启后未就绪：" + waitErr.Error()
			_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
			s.audit(r, id, "instance.restart", "instance", in.ID, "failed", hint)
			s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
			s.fail(w, r, errState(hint))
			return
		}
		_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
		s.audit(r, id, "instance.restart", "instance", in.ID, "ok", "MongoDB 容器已按当前配置重建")
		s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "已重启", nil)
		writeOK(w, r, map[string]any{"id": in.ID, "status": "running"})
		return
	}
	if stopErr := rt.Stop(); stopErr != nil && !isNotRunning(stopErr) {
		s.audit(r, id, "instance.stop", "instance", in.ID, "failed", stopErr.Error())
		s.recordJob(id, jobInstanceStop, "instance", in.ID, "runtime="+in.Runtime, started, "", stopErr)
		s.fail(w, r, errState("停止失败："+stopErr.Error()))
		return
	}
	_ = s.st.SetStatus(r.Context(), in.ID, "stopped", "")
	s.audit(r, id, "instance.stop", "instance", in.ID, "ok", "")
	s.recordJob(id, jobInstanceStop, "instance", in.ID, "runtime="+in.Runtime, started, "已停止", nil)
	writeOK(w, r, map[string]any{"id": in.ID, "status": "stopped"})
}

// handleRestart 先停再起：改完 my.cnf 与资源配额后，用户只该点一次按钮。
//
// 容器方式没有单独的"重启"概念也能照样工作：Stop 停容器、Start 起容器，
// 顺带把上一次失败留下的半死状态收干净。
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	if isExternal(in) {
		s.fail(w, r, errBadRequest("外部数据库不能由本应用重启，请到原数据库主机处理"))
		return
	}
	rt, err := s.dbSpec(in)
	if err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	started := time.Now()
	if stopErr := rt.Stop(); stopErr != nil && !isNotRunning(stopErr) {
		s.audit(r, id, "instance.restart", "instance", in.ID, "failed", "停止阶段失败："+stopErr.Error())
		s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "", stopErr)
		s.fail(w, r, errState("重启失败（停止阶段）："+stopErr.Error()))
		return
	}
	if startErr := rt.Start(); startErr != nil && !isAlreadyRunning(startErr) {
		hint := startErr.Error()
		if !rt.Running() && portInUse(in.Port) {
			hint = "端口 " + strconv.Itoa(in.Port) + " 已被其他程序占用，请释放该端口后重试"
		}
		_ = s.st.SetStatus(r.Context(), in.ID, "error", hint)
		s.audit(r, id, "instance.restart", "instance", in.ID, "failed", "启动阶段失败："+hint)
		s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "", errState(hint))
		s.fail(w, r, errState("重启失败（启动阶段）："+hint))
		return
	}
	_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
	s.audit(r, id, "instance.restart", "instance", in.ID, "ok", "")
	s.recordJob(id, jobInstanceRestart, "instance", in.ID, "runtime="+in.Runtime, started, "已重启", nil)
	writeOK(w, r, map[string]any{"id": in.ID, "status": "running"})
}

// handleConnection 返回连接信息。密码只在这里返回，列表接口永远不带。
func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, false)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	running := false
	if rt, err := s.dbSpec(in); err == nil {
		running = rt.Running()
	}
	w.Header().Set("Cache-Control", "no-store")
	canManage := s.canOperateInstance(r, id, in.ID)
	if isExternal(in) {
		out := map[string]any{
			"engine":  in.Engine,
			"version": in.EngineVersion,
			"host":    in.Host,
			"port":    in.Port,
			"user":    in.Username,
			"password": func() string {
				if canManage {
					return s.readExternalSecret(in.ID)
				}
				return ""
			}(),
			"running":  running,
			"external": true,
		}
		if in.Engine == enginePostgres {
			out["database"] = pgctl.RootUser
		}
		if in.Engine == engineMongoDB {
			out["database"] = "admin"
		}
		writeOK(w, r, out)
		return
	}
	host := s.accessHost()
	// 连接信息给的是 root：用户要的是"能直接连进去建库、建账号"的入口，
	// 业务账号是库级授权的产物，在「数据库与账号」里按库管理，不该摆在概览上让人当管理员用。
	// 也不返回库名：一个服务下可以有多个库，硬给一个"默认库"反而误导。
	out := map[string]any{
		"engine":  in.Engine,
		"version": in.EngineVersion,
		"host":    host,
		"port":    in.Port,
		// 容器内地址：同一 Docker 网络里的应用（PHP、其他容器）用它连，不用绕出去
		"containerHost": containerNameOf(in),
		"containerPort": containerMySQLPort,
		"user":          mysqlctl.RootUser,
		// root 密码与业务账号密码是两套：用户建服务时填的就是它
		"password": func() string {
			if canManage {
				return s.readRootSecret(in.ID)
			}
			return ""
		}(),
		"running": running,
	}
	if in.Engine == enginePostgres {
		out["containerPort"] = containerPostgresPort
		out["user"] = pgctl.RootUser
		// PG 的客户端必须指定库：postgres 是维护库，一定存在，
		// 比硬给一个"默认业务库"更不容易误导
		out["database"] = pgctl.RootUser
	}
	if in.Engine == engineMongoDB {
		out["containerPort"] = containerMongoPort
		out["user"] = mongoctl.RootUser
		out["database"] = "admin"
	}
	writeOK(w, r, out)
}

// Reconcile 在服务启动时把"应该运行"的实例拉起来。
//
// 平台重启后子进程不会自己回来，这一步不做的话界面上显示运行中、
// 实际连不上，比显示已停止更难排查。用户主动停过的实例不能拉起来。
func (s *Server) Reconcile(ctx context.Context) {
	_ = s.st.FailRunningDeployTasks(ctx, "服务重启，部署任务中断；请检查服务列表中的失败记录")
	list, err := s.st.ListInstances(ctx, "", "")
	if err != nil {
		log.Printf("自动拉起数据库失败（读取台账）：%v", err)
		return
	}
	for _, in := range list {
		s.reconcileOne(ctx, in)
	}
}

func (s *Server) reconcileOne(ctx context.Context, in store.Instance) {
	// 与接口里的启停串行：否则用户刚点“停止”，这里又把它拉起来
	unlock := s.lockInstance(in.ID)
	defer unlock()

	// 开发期间遗留的非容器实例不再支持：只从列表里收起来，
	// 数据目录与容器原样保留，用户想导出还能自己找得到。
	if n, err := s.st.HideLegacy(ctx); err != nil {
		log.Printf("隐藏不再支持的实例失败：%v", err)
	} else if n > 0 {
		log.Printf("已隐藏 %d 个不再支持的遗留实例，数据未删除", n)
	}

	// 部署任务只存在内存里：服务一重启，残留的 creating 永远不会再有人推进，
	// 必须当场标成 failed，否则列表里会挂一个永远"创建中"的假实例。
	if in.Status == "creating" {
		_ = s.st.SetStatus(ctx, in.ID, "failed", "服务重启，部署中断；失败现场与日志已保留，可查看后删除重来")
		return
	}
	// failed 是用户需要看到的结论，不能因为容器碰巧还在跑就被翻成 running
	if in.Status == "failed" {
		return
	}

	spec, err := s.dbSpec(in)
	if err != nil {
		_ = s.st.SetStatus(ctx, in.ID, "error", "镜像信息不可用："+err.Error())
		return
	}
	rt := spec
	if isExternal(in) {
		if rt.Running() {
			_ = s.st.SetStatus(ctx, in.ID, "running", "")
		} else {
			_ = s.st.SetStatus(ctx, in.ID, "error", "外部数据库连接失败")
		}
		return
	}
	if rt.Running() {
		_ = s.st.SetStatus(ctx, in.ID, "running", "")
		return
	}
	if in.Status != "running" {
		// 用户停过、或上次就失败了，保持原状态，不替用户做决定
		return
	}
	if startErr := rt.Start(); startErr != nil {
		log.Printf("自动拉起实例 %s 失败：%v", in.Name, startErr)
		_ = s.st.SetStatus(ctx, in.ID, "error", "重启后未能自动启动："+startErr.Error())
		return
	}
	_ = s.st.SetStatus(ctx, in.ID, "running", "")
}

// Shutdown 停止所有常驻实例，服务退出前调用，避免留下孤儿进程。
func (s *Server) Shutdown(ctx context.Context) {
	list, err := s.st.ListInstances(ctx, "", "")
	if err != nil {
		return
	}
	for _, in := range list {
		unlock := s.lockInstance(in.ID)
		if spec, rerr := s.dbSpec(in); rerr == nil {
			_ = spec.Stop()
		}
		unlock()
	}
}

// liveStatus 返回实例的真实状态，用于列表/详情覆盖台账里的旧值。
func (s *Server) liveStatus(in store.Instance) string {
	// 部署进行中/部署失败是台账里的事实，不能拿"进程没在跑"盖掉：
	// creating 会被翻成 stopped，failed 会被翻成 error，用户在列表里就分不清
	// "没建完"和"原本跑着现在挂了"，也看不到失败原因。
	if in.Status == "creating" || in.Status == "failed" {
		return in.Status
	}
	if isExternal(in) {
		if s.externalTest != nil {
			if err := s.externalTest(s.externalEndpointOf(in)); err == nil {
				return "running"
			}
			return "error"
		}
		spec, err := s.dbSpec(in)
		if err != nil || !spec.Running() {
			return "error"
		}
		return "running"
	}
	spec, err := s.dbSpec(in)
	if err != nil {
		return "error"
	}
	if spec.Running() {
		return "running"
	}
	if in.Status == "error" {
		return "error"
	}
	return "stopped"
}
