package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"niudbm/server/internal/images"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

const maxNameRunes = 32

type instanceView struct {
	store.Instance
	SizeBytes int64 `json:"sizeBytes"`
	// SizeUnknown 表示数据在 docker 卷里、宿主上看不到体积：
	// 这时候显示 0 B 是在撒谎，界面要显示"未知"
	SizeUnknown bool   `json:"sizeUnknown,omitempty"`
	FilePath    string `json:"filePath"`
	TableCount  *int   `json:"tableCount,omitempty"`
	// 备份概况：列表页要能看出"这个库有没有在备份"
	BackupCount  int   `json:"backupCount"`
	LastBackupAt int64 `json:"lastBackupAt,omitempty"`
	// Capabilities 是界面生成操作按钮的唯一依据。
	// 外部连接与托管服务的差异不再靠前端各自的 if 判断。
	Capabilities instanceCapabilities `json:"capabilities"`
	AccessRole   string               `json:"accessRole"`
}

type instanceCapabilities struct {
	StartStop       bool `json:"startStop"`
	Container       bool `json:"container"`
	Config          bool `json:"config"`
	ConfigFile      bool `json:"configFile"`
	Backup          bool `json:"backup"`
	Users           bool `json:"users"`
	Databases       bool `json:"databases"`
	Transfer        bool `json:"transfer"`
	TestConnection  bool `json:"testConnection"`
	RevealRoot      bool `json:"revealRoot"`
	DeletePermanent bool `json:"deletePermanent"`
	Members         bool `json:"members"`
}

type createInstanceReq struct {
	Name string `json:"name"`
	// Engine 只接受 mysql；保留字段是为了给老客户端一个明确错误，而不是静默建错库
	Engine string `json:"engine"`
	// EngineVersion 是镜像 tag（8.4 / 8.0 / 11.4 / 17），留空用默认版本
	EngineVersion string `json:"engineVersion"`
	// Image 是可选的自定义镜像地址（含私有仓库或加速站前缀，必须带版本标签）。
	// 国内网络拉不动 Docker Hub 时，用户可以在部署页直接改这一项。
	Image   string `json:"image"`
	Profile string `json:"profile"`
	SeedSQL string `json:"seedSql"`
	Note    string `json:"note"`
	// Users 是建库时一并创建的接入账号：让“建库 + 发账号”一次做完，
	// 不必先建库再用系统超级用户登录改权限。
	Users []createUserReq `json:"users"`
	// RootPassword 是容器 root 密码；留空自动生成。只在 docker 方式下生效。
	RootPassword string `json:"rootPassword"`
	// CPULimit / MemLimitMB 是资源配额，0 表示不限制（只对 docker 方式生效）
	CPULimit   float64 `json:"cpuLimit"`
	MemLimitMB int64   `json:"memLimitMB"`
	// StorageKind 是数据落盘方式：volume（Docker 命名卷，默认）/ bind（宿主机目录）
	StorageKind string `json:"storageKind"`
	// StoragePath 在 bind 方式下必填
	StoragePath string `json:"storagePath"`
	// Port 是指定的监听端口，0 表示自动从端口池分配。
	// 一个服务占一个端口，部署多个服务就是多个不同端口。
	Port int `json:"port"`
}

// createOpts 是建服务时与资源/存储相关的选项。
type createOpts struct {
	// Image 是用户指定的完整镜像地址；空表示用清单里的官方名
	Image        string
	RootPassword string
	CPULimit     float64
	MemLimitMB   int64
	StorageKind  string
	StoragePath  string
	Port         int
}

type updateInstanceReq struct {
	Name string `json:"name"`
	// 配额只在传了指针时更新：老客户端只发 name，不该把配额清零
	CPULimit   *float64 `json:"cpuLimit"`
	MemLimitMB *int64   `json:"memLimitMB"`
	// Port 改了要重建容器：容器名与端口映射都是创建时定死的
	Port *int `json:"port"`
}

type deleteInstanceReq struct {
	ConfirmName string `json:"confirmName"`
	KeepData    *bool  `json:"keepData"`
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, false)
	if !ok {
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	list, err := s.st.ListInstances(r.Context(), q, status)
	if err != nil {
		s.fail(w, r, errInternal("读取实例列表失败"))
		return
	}
	list, _, err = s.visibleInstances(r.Context(), id, list)
	if err != nil {
		s.fail(w, r, errInternal("读取服务权限失败"))
		return
	}

	items := make([]instanceView, 0, len(list))
	for _, in := range list {
		v := s.view(in)
		s.applyAccess(r, id, &v)
		items = append(items, v)
	}
	// 列表上直接给出"几份备份、最近一次何时"：用户扫一眼就知道谁没在备份
	s.attachBackupStats(r.Context(), items)
	writeOK(w, r, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	started := time.Now()

	var req createInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validateName(name); err != nil {
		s.fail(w, r, err)
		return
	}
	profile := req.Profile
	if profile == "" {
		profile = "low"
	}
	if !validProfile(profile) {
		s.fail(w, r, errBadRequest("资源档位只能是 low / standard / performance"))
		return
	}
	if len(req.SeedSQL) > 64*1024 {
		s.fail(w, r, errBadRequest("初始化 SQL 过长"))
		return
	}
	opts, oerr := normalizeCreateOpts(req)
	if oerr != nil {
		s.fail(w, r, errBadRequest(oerr.Error()))
		return
	}
	if err := s.validateStoragePath(opts.StoragePath); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	engineName := strings.TrimSpace(req.Engine)
	if engineName == "" {
		// 老前端不带 engine，仍按 MySQL 处理
		engineName = engineMySQL
	}
	if engineName != engineMySQL && engineName != enginePostgres && engineName != engineMongoDB {
		s.fail(w, r, errBadRequest("不支持的引擎："+engineName+"；可选 MySQL / PostgreSQL / MongoDB"))
		return
	}

	// 重名要在动手之前就拒掉：拉起容器几十秒后才报"名字已存在"，用户只会白等一场
	taken, terr := s.st.NameTaken(r.Context(), name)
	if terr != nil {
		s.fail(w, r, errInternal("检查实例名失败"))
		return
	}
	if taken {
		s.fail(w, r, errBadRequest("实例名已存在，请换一个"))
		return
	}

	detail := "名称 " + name + "，引擎 " + engineLabel(engineName)
	if req.EngineVersion != "" {
		detail += " " + req.EngineVersion
	}
	detail += "，档位 " + profile
	if opts.CPULimit > 0 || opts.MemLimitMB > 0 {
		detail += "，配额 " + quotaText(opts.CPULimit, opts.MemLimitMB)
	}
	if opts.StorageKind == storageBind {
		detail += "，数据目录 " + opts.StoragePath
	}
	if opts.Image != "" {
		detail += "，镜像 " + opts.Image
	}
	if req.Note != "" {
		detail += "，备注 " + truncate(req.Note, 200)
	}

	// 服务建出来是空的：部署阶段没有库可授权，账号请在服务里建库之后再加。
	// 以前允许在部署时建账号，那些账号只授在自动创建的 app 库上，删库就全废。
	if len(req.Users) > 0 {
		s.fail(w, r, errBadRequest("部署阶段不建账号：先建服务，再在「数据库与账号」里建库并发账号"))
		return
	}

	// 参数级错误必须在异步之前挡住：版本不存在、Docker 不可用这类问题
	// 是立刻能判定的，让用户等一个注定失败的后台任务只会更困惑。
	img, verr := s.pickVersionFor(engineName, req.EngineVersion)
	if verr != nil {
		s.fail(w, r, errBadRequest(verr.Error()))
		return
	}
	// 自定义镜像地址：校验格式，并把版本号对齐到地址里的标签，
	// 免得界面显示 17、实际拉的是 16（或反过来）
	if ref := strings.TrimSpace(req.Image); ref != "" {
		clean, ierr := normalizeImageRef(ref)
		if ierr != nil {
			s.fail(w, r, errBadRequest(ierr.Error()))
			return
		}
		opts.Image = clean
		if tag := imageTag(clean); tag != "" && tag != img.Tag {
			if picked, ok := images.Find(tag); ok && picked.Family == familyOf(engineName) {
				img = picked
			}
		}
	}
	if derr := s.dockerReady(r.Context()); derr != nil {
		s.fail(w, r, derr)
		return
	}
	if opts.Port > 0 {
		if _, perr := s.pickPort(r.Context(), opts.Port); perr != nil {
			s.fail(w, r, perr)
			return
		}
	}

	// 部署要几十秒到几分钟（拉镜像、初始化、建账号）：
	// 立刻回任务号，真正的活在后台跑，前端轮询阶段。
	task := s.startDeployTask(name, engineName)
	// handler 返回后 r.Context() 会被取消，后台任务必须换一个不取消的 ctx，
	// 否则拉镜像/启动容器会中途被掐断，留下半成品。
	// 后台任务用不会随请求结束而取消的 req（审计要 *http.Request，取库里要 ctx）
	bg := r.Clone(context.WithoutCancel(r.Context()))
	go func() {
		phase := func(p, text string) { s.deployPhase(task.ID, p, text) }
		in, perr := s.createService(bg.Context(), engineName, name, profile, req.SeedSQL, img.Tag, opts, phase)
		if perr != nil {
			// 失败时台账里已经留着这条（status=failed）记录，把 ID 一并交给前端，
			// 用户才能在列表里点开看部署日志、删掉重来。
			s.audit(bg, id, "instance.create", "instance", in.ID, "failed", detail+"；"+perr.Error())
			s.recordJob(id, jobInstanceCreate, "instance", in.ID, detail, started, "", perr)
			s.deployDone(task.ID, in.ID, perr)
			return
		}
		s.audit(bg, id, "instance.create", "instance", in.ID, "ok", detail)
		s.recordJob(id, jobInstanceCreate, "instance", in.ID, detail, started,
			"服务 "+in.ID+" 已创建", nil)
		s.deployDone(task.ID, in.ID, nil)
	}()
	writeOK(w, r, map[string]any{"taskId": task.ID, "name": name, "async": true})
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, false)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	v := s.viewWithStats(r.Context(), in)
	s.applyAccess(r, id, &v)
	writeOK(w, r, v)
}

// attachBackupStats 把备份份数与最近备份时间补到视图上；查不到就留零值，不影响主流程。
func (s *Server) attachBackupStats(ctx context.Context, items []instanceView) {
	if len(items) == 0 {
		return
	}
	stats, err := s.st.InstanceBackupStats(ctx)
	if err != nil {
		return
	}
	for i := range items {
		if st, ok := stats[items[i].ID]; ok {
			items[i].BackupCount = st.Count
			items[i].LastBackupAt = st.LastAt
		}
	}
}

// viewWithStats 是详情页用的：单实例也要带上备份概况。
func (s *Server) viewWithStats(ctx context.Context, in store.Instance) instanceView {
	v := s.view(in)
	stats, err := s.st.InstanceBackupStats(ctx)
	if err == nil {
		if st, ok := stats[in.ID]; ok {
			v.BackupCount = st.Count
			v.LastBackupAt = st.LastAt
		}
	}
	return v
}

// view 把台账记录补上体积与表数量；列表、详情、创建共用同一套算法。
//
// 表数量只是概览信息（多少张表值得关注），不拿字段与行数：那是数据库客户端的活。
// 状态以容器真实情况为准，台账里的 status 只表示"应该处于什么状态"。
func (s *Server) view(in store.Instance) instanceView {
	v := instanceView{Instance: in, Capabilities: capabilitiesOf(in)}
	v.Status = s.liveStatus(in)
	spec, err := s.dbSpec(in)
	if err != nil {
		return v
	}
	if isExternal(in) {
		v.FilePath = net.JoinHostPort(in.Host, strconv.Itoa(in.Port))
		v.SizeUnknown = true
		if v.Status == "error" {
			v.StatusReason = "外部数据库连接失败，请检查地址、账号、网络与 TLS 设置"
		}
		return v
	}
	v.FilePath = s.storageLabel(in)
	if dir := s.storageDir(in); dir != "" {
		v.SizeBytes = mysqlctl.DirSize(dir)
	} else {
		// 命名卷：宿主路径由 docker 管，读不到就如实标未知
		v.SizeUnknown = true
	}
	if n, cerr := spec.CountTables(); cerr == nil {
		v.TableCount = &n
	}
	return v
}

func capabilitiesOf(in store.Instance) instanceCapabilities {
	if isExternal(in) {
		return instanceCapabilities{
			Users:          true,
			Databases:      true,
			Transfer:       in.Engine != engineMongoDB,
			TestConnection: true,
			Members:        true,
		}
	}
	if in.Engine == engineMongoDB {
		return instanceCapabilities{
			StartStop: true, Container: true, Backup: true,
			Users: true, Databases: true, TestConnection: true, RevealRoot: true,
			DeletePermanent: true, ConfigFile: true,
			Members: true,
		}
	}
	return instanceCapabilities{
		StartStop:       true,
		Container:       true,
		Config:          true,
		ConfigFile:      true,
		Backup:          true,
		Users:           true,
		Databases:       true,
		Transfer:        true,
		TestConnection:  true,
		RevealRoot:      true,
		DeletePermanent: true,
		Members:         true,
	}
}

// instanceSize 返回实例体积（数据目录合计），用于删除确认与概览统计。
func (s *Server) instanceSize(in store.Instance) int64 {
	dir := s.storageDir(in)
	if dir == "" {
		return 0
	}
	return mysqlctl.DirSize(dir)
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req updateInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	touchQuota := req.CPULimit != nil || req.MemLimitMB != nil
	touchPort := req.Port != nil && *req.Port != in.Port
	if isExternal(in) && (touchQuota || touchPort) {
		s.fail(w, r, errBadRequest("外部连接不支持修改容器配额或端口，请修改连接配置"))
		return
	}
	if name == "" && !touchQuota && !touchPort {
		s.fail(w, r, errBadRequest("没有需要修改的字段"))
		return
	}
	if name != "" {
		if name != in.Name {
			if err := validateName(name); err != nil {
				s.fail(w, r, err)
				return
			}
			if err := s.st.RenameInstance(r.Context(), in.ID, name); err != nil {
				if errors.Is(err, store.ErrNameTaken) {
					s.fail(w, r, errBadRequest("实例名已存在，请换一个"))
					return
				}
				s.fail(w, r, errInternal("改名失败"))
				return
			}
			s.audit(r, id, "instance.rename", "instance", in.ID, "ok", in.Name+" → "+name)
			in.Name = name
		}
	}

	// 端口：容器名和端口映射都是创建时定死的，所以只能删掉旧容器用新名字重建。
	// 数据在卷里，删容器不等于删数据。
	if touchPort {
		newPort := *req.Port
		if newPort < 1024 || newPort > 65535 {
			s.fail(w, r, errBadRequest("端口只能是 1024 ~ 65535"))
			return
		}
		used, uerr := s.st.UsedPorts(r.Context())
		if uerr != nil {
			s.fail(w, r, errInternal("读取端口分配失败"))
			return
		}
		if used[newPort] {
			s.fail(w, r, errBadRequest("端口 "+strconv.Itoa(newPort)+" 已被其他数据库占用"))
			return
		}
		if portInUse(newPort) {
			s.fail(w, r, errBadRequest("端口 "+strconv.Itoa(newPort)+" 已被系统里的其他程序占用"))
			return
		}

		old := in
		wasRunning := s.liveStatus(in) == "running"
		if err := s.dockerClient().Remove(r.Context(), containerNameOf(old)); err != nil {
			s.fail(w, r, errState("停不掉旧容器，端口未修改："+err.Error()))
			return
		}
		if err := s.st.UpdatePort(r.Context(), in.ID, newPort); err != nil {
			s.fail(w, r, errInternal("保存端口失败"))
			return
		}
		in.Port = newPort
		if wasRunning {
			if err := s.rebuildDocker(r.Context(), in); err != nil {
				// 回滚：端口写回去，再按旧端口把容器拉起来，别把用户的服务弄丢
				_ = s.st.UpdatePort(r.Context(), in.ID, old.Port)
				in.Port = old.Port
				if rerr := s.rebuildDocker(r.Context(), in); rerr != nil {
					_ = s.st.SetStatus(r.Context(), in.ID, "error", "换端口失败且未能恢复："+rerr.Error())
					s.fail(w, r, errState("换端口失败，服务未能恢复，请查看日志："+rerr.Error()))
					return
				}
				s.fail(w, r, errState("新端口启动失败，已回滚到 "+strconv.Itoa(old.Port)+"："+err.Error()))
				return
			}
			_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
		}
		s.audit(r, id, "instance.port", "instance", in.ID, "ok",
			strconv.Itoa(old.Port)+" → "+strconv.Itoa(newPort))
	}

	if touchQuota {
		cpu := in.CPULimit
		memMB := in.MemLimitMB
		if req.CPULimit != nil {
			cpu = *req.CPULimit
		}
		if req.MemLimitMB != nil {
			memMB = *req.MemLimitMB
		}
		if err := validateQuota(cpu, memMB); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.st.UpdateLimits(r.Context(), in.ID, cpu, memMB); err != nil {
			s.fail(w, r, errInternal("保存资源配额失败"))
			return
		}
		// 容器已经起来了就直接热更新，不必让用户手动重启
		if in.Runtime == runtimeDocker {
			if err := s.dockerClient().UpdateLimits(r.Context(), containerNameOf(in), cpu, memMB); err != nil {
				s.audit(r, id, "instance.limits", "instance", in.ID, "failed",
					"配额已落库，但热更新容器失败："+err.Error())
			}
		}
		s.audit(r, id, "instance.limits", "instance", in.ID, "ok", quotaText(cpu, memMB))
		in.CPULimit = cpu
		in.MemLimitMB = memMB
	}

	writeOK(w, r, s.view(in))
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req deleteInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ConfirmName == "" {
		req.ConfirmName = r.URL.Query().Get("confirmName")
	}
	if req.KeepData == nil {
		if raw := r.URL.Query().Get("keepData"); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				s.fail(w, r, errBadRequest("keepData 只能是 true 或 false"))
				return
			}
			req.KeepData = &v
		}
	}
	if req.ConfirmName != in.Name {
		s.fail(w, r, errRejected("请输入实例名以确认删除"))
		return
	}
	if req.KeepData == nil {
		s.fail(w, r, errBadRequest("必须明确 keepData（是否保留数据文件）"))
		return
	}

	var moved string
	unlock := s.lockInstance(in.ID)
	defer unlock()

	// 实例要消失，容器不能留着：否则端口与内存被一个界面上看不见的服务占着。
	// 保留数据时也要停：数据卷留着不等于让它继续跑。
	// keepData=true 时连卷一起留着：以前这里无条件删卷，"保留数据"其实把数据删了
	if isExternal(in) {
		_ = removeIfExists(s.externalSecretPath(in.ID))
	} else {
		if err := s.removeDocker(r.Context(), in, !*req.KeepData); err != nil {
			s.fail(w, r, errState("删除容器失败，服务仍保留："+err.Error()))
			return
		}
		_ = os.Remove(s.secretPath(in.ID))
		_ = os.Remove(s.rootSecretPath(in.ID))
	}
	// 配置行也属于这个实例：留着会让同名新实例继承上一份配置
	if err := s.st.DeleteInstanceSettings(r.Context(), in.ID); err != nil {
		s.fail(w, r, errInternal("清理配置失败"))
		return
	}
	// 定时备份策略同理：实例没了还要策略，调度器会一直报"实例不存在"
	_ = s.st.DeletePolicy(r.Context(), in.ID)

	if !*req.KeepData && !isExternal(in) {
		src := s.instanceDir(in)
		dst := filepath.Join(s.paths.Trash, in.ID+"-"+strconv.FormatInt(time.Now().Unix(), 10))
		if err := os.MkdirAll(s.paths.Trash, 0o750); err != nil {
			s.fail(w, r, errInternal("无法创建回收目录"))
			return
		}
		if _, statErr := os.Stat(src); os.IsNotExist(statErr) {
			// 容器方式的数据在 docker 卷里，实例目录可能只有配置与日志、甚至还没建；
			// 目录不存在不算失败，数据已经随卷一起删掉了
			moved = ""
		} else if err := os.Rename(src, dst); err != nil {
			// 跨分区等情况下不静默丢数据：宁可失败让用户选择保留数据
			s.fail(w, r, errInternal("移动数据文件失败，请改用“保留数据”后手动清理："+err.Error()))
			return
		}
		moved = dst
	}

	if err := s.st.SoftDelete(r.Context(), in); err != nil {
		s.fail(w, r, errInternal("更新台账失败"))
		return
	}

	detail := "keepData=" + strconv.FormatBool(*req.KeepData)
	if moved != "" {
		detail += "，已移入回收站 " + moved
	}
	// 绑挂到宿主目录的数据我们不动手：那是用户自己的目录，可能在别的应用里也用着，
	// 界面删掉实例就顺手删掉一整个宿主目录太危险，宁可留一句可执行的提示。
	if !isExternal(in) && in.StorageKind != storageVolume && !*req.KeepData {
		// 绑挂目录可能被用户放在别处（例如 /vol1/mysql/blog），
		// 只删我们自己管理的默认目录，别的地方给一句可执行提示。
		if in.StoragePath == "" {
			detail += "，数据目录已随实例目录清理"
		} else {
			detail += "，宿主目录 " + in.StoragePath + " 未删除，请按需自行清理"
		}
	}
	if !isExternal(in) && *req.KeepData {
		detail += "，数据保留在 " + s.storageLabel(in)
	}
	s.audit(r, id, "instance.delete", "instance", in.ID, "ok", detail)
	s.recordJob(id, jobInstanceDelete, "instance", in.ID, detail, time.Now(), "实例已删除", nil)

	writeOK(w, r, map[string]any{"id": in.ID, "keepData": *req.KeepData, "trashPath": moved})
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, true); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListAudit(r.Context(), limit)
	if err != nil {
		s.fail(w, r, errInternal("读取审计失败"))
		return
	}
	writeOK(w, r, map[string]any{"items": list, "total": len(list)})
}

// ---------- 公共小工具 ----------

// load 取路径参数里的实例，不存在时已写好响应。
func (s *Server) load(w http.ResponseWriter, r *http.Request) (store.Instance, bool) {
	instID := r.PathValue("id")
	if instID == "" {
		writeErr(w, r, errInstanceNotFound())
		return store.Instance{}, false
	}
	in, err := s.st.GetInstance(r.Context(), instID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, r, errInstanceNotFound())
			return store.Instance{}, false
		}
		writeErr(w, r, errInternal("读取实例失败"))
		return store.Instance{}, false
	}
	return in, true
}

func (s *Server) audit(r *http.Request, id identity, action, kind, target, result, detail string) {
	ctx := r.Context()
	_ = s.st.AddAudit(ctx, store.Audit{
		Ts:         time.Now().UnixMilli(),
		UserID:     id.UserID,
		Username:   id.Username,
		Action:     action,
		TargetKind: kind,
		TargetID:   target,
		Result:     result,
		Detail:     detail,
	})
}

// validateName 校验实例名。名字只用于展示，从不参与拼路径，
// 但仍要挡住控制字符与路径分隔符，避免日志注入与误导。
func validateName(name string) error {
	if name == "" {
		return errBadRequest("实例名不能为空")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return errBadRequest("实例名最多 32 个字符")
	}
	if strings.Contains(name, "..") {
		return errBadRequest("实例名不能包含 ..")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errBadRequest("实例名不能包含控制字符")
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return errBadRequest(`实例名不能包含 / \ : * ? " < > |`)
		}
	}
	if strings.TrimSpace(name) != name {
		return errBadRequest("实例名首尾不能有空格")
	}
	return nil
}

func validProfile(p string) bool {
	switch p {
	case "low", "standard", "performance":
		return true
	}
	return false
}

// 配额边界：上限只挡住明显填错的输入（例如把 MB 填成字节），
// 真正的"能不能跑得动"由创建时的容器报错与界面水位提示暴露。
const (
	maxCPULimit   = 64.0
	minMemLimitMB = 128
	maxMemLimitMB = 1024 * 1024
)

// validateQuota 校验 CPU/内存上限；0 表示不限制。
func validateQuota(cpu float64, memMB int64) error {
	if cpu < 0 || cpu > maxCPULimit {
		return errBadRequest("CPU 上限只能是 0（不限制）或 0.1 ~ 64 之间的核数")
	}
	if memMB < 0 || (memMB > 0 && memMB < minMemLimitMB) {
		return errBadRequest("内存上限只能填 0（不限制）或不少于 128MB")
	}
	if memMB > maxMemLimitMB {
		return errBadRequest("内存上限过大")
	}
	return nil
}

// normalizeCreateOpts 校验并收敛资源/存储选项，把空值补成默认。
func normalizeCreateOpts(req createInstanceReq) (createOpts, error) {
	opts := createOpts{
		RootPassword: req.RootPassword,
		CPULimit:     req.CPULimit,
		MemLimitMB:   req.MemLimitMB,
		StorageKind:  strings.TrimSpace(req.StorageKind),
		StoragePath:  strings.TrimSpace(req.StoragePath),
		Port:         req.Port,
	}
	if opts.RootPassword != "" && len(opts.RootPassword) < 8 {
		return opts, errBadRequest("root 密码至少 8 位")
	}
	// 端口 0 表示交给端口池自动分配；填了就必须是能对外服务的普通端口
	if opts.Port != 0 && (opts.Port < 1024 || opts.Port > 65535) {
		return opts, errBadRequest("端口只能是 1024 ~ 65535，留空则自动分配")
	}
	if err := validateQuota(opts.CPULimit, opts.MemLimitMB); err != nil {
		return opts, err
	}
	// 留空 = 默认宿主目录（实例目录下的 data）：数据看得见、算得出体积、能随实例一起清掉
	if opts.StorageKind == "" {
		opts.StorageKind = storageBind
	}
	if opts.StorageKind == storageVolume {
		opts.StoragePath = ""
		return opts, nil
	}
	if opts.StorageKind != storageBind {
		return opts, errBadRequest("存储方式只能是 volume 或 bind")
	}
	if opts.StoragePath == "" {
		return opts, nil
	}
	if !filepath.IsAbs(opts.StoragePath) {
		return opts, errBadRequest("宿主目录必须是绝对路径")
	}
	opts.StoragePath = filepath.Clean(opts.StoragePath)
	if opts.StoragePath == "/" {
		return opts, errBadRequest("不能把根目录当作数据目录")
	}
	return opts, nil
}

// validateStoragePath 只允许把数据放到应用私有目录或飞牛授权的数据共享目录。
//
// Docker bind mount 等于把宿主目录交给容器里的数据库进程。入口镜像通常会对数据目录
// 做 chown，如果允许任意绝对路径，一次手误就可能把系统目录或别的应用目录交给容器。
func (s *Server) validateStoragePath(path string) error {
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	if clean == "/" {
		return errBadRequest("不能把根目录当作数据目录")
	}
	roots := []string{filepath.Join(s.env.PkgVar, "instances")}
	roots = append(roots, s.env.DataSharePaths...)
	if len(s.env.DataSharePaths) == 0 && !s.env.InPlatform {
		roots = append(roots, filepath.Join(os.TempDir(), "niudbm-dev"))
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		cleanRoot, rerr := resolvePath(root)
		cleanCandidate, cerr := resolvePath(clean)
		if rerr != nil || cerr != nil {
			continue
		}
		if cleanCandidate == cleanRoot {
			return errBadRequest("请选择授权目录下的子目录，不要直接使用目录根路径")
		}
		if pathWithin(cleanRoot, cleanCandidate) {
			return nil
		}
	}
	return errBadRequest("数据目录必须位于飞牛授权的 niudbm/data 或应用私有数据目录下")
}

// resolvePath 解析路径中已经存在的部分，再拼回尚不存在的目录。
// 这样既能处理"首次部署目录还不存在"，也能拦住授权目录里指向 /etc 的软链接。
func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := absolute
	var missing []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return rel != "." && !filepath.IsAbs(rel)
}

// quotaText 把配额格式化成审计与界面都能读的一句话。
func quotaText(cpu float64, memMB int64) string {
	parts := make([]string, 0, 2)
	if cpu > 0 {
		parts = append(parts, strconv.FormatFloat(cpu, 'f', -1, 64)+" 核")
	}
	if memMB > 0 {
		parts = append(parts, fmt.Sprintf("%dMB", memMB))
	}
	if len(parts) == 0 {
		return "不限制"
	}
	return strings.Join(parts, " / ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
