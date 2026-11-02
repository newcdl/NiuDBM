package httpapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"niudbm/server/internal/images"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

// mysqlSpec 把台账记录还原成容器操作需要的参数。
//
// 唯一的事实来源是镜像 tag：Series/Flavor 都从 images 清单查出来，
// 台账里不再单独存"引擎"与"运行方式"——这一版本只有 MySQL + Docker。
func (s *Server) mysqlSpec(in store.Instance) (mysqlctl.Spec, error) {
	img, ok := images.Find(in.EngineVersion)
	if !ok {
		return mysqlctl.Spec{}, errors.New("找不到版本 " + in.EngineVersion + " 的镜像信息")
	}
	base := s.instanceDir(in)
	// 数据目录就是宿主机上能看到的那个目录（绑挂方式）；
	// 命名卷由 docker 管路径，这里只作为体积统计的兜底
	dataDir := s.storageDir(in)
	if dataDir == "" {
		dataDir = filepath.Join(base, "data")
	}
	spec := mysqlctl.Spec{
		Series:   img.Tag,
		Flavor:   img.Family,
		DataDir:  dataDir,
		Port:     in.Port,
		Password: s.readSecret(in.ID),
		ConfDir:  filepath.Join(base, "conf"),
		Docker: &mysqlctl.DockerRuntime{
			Client:    s.dockerClient(),
			Container: containerNameOf(in),
			// root 与 niudbm 是两套密码：用户可能只改了 root
			RootPassword: s.readRootSecret(in.ID),
		},
	}
	// 配置文件跟着服务一起存在：之后无论部署、改参数还是打开「配置文件」页，
	// 看到的都是一份有默认值的完整文件，而不是等第一次保存才有两行。
	_ = spec.EnsureCnf()
	return spec, nil
}

// createMySQL 建一个 MySQL 服务：
// 定镜像 → 分配端口 → 落台账(creating) → 拉镜像起容器 → 等就绪 → 建库建账号 → 标 running。
//
// 失败不清理现场：容器只停不删，日志写进实例目录（见 markDeployFailed），
// 用户能在列表里看到这条失败记录、点开看日志、删掉重来。
func (s *Server) createMySQL(ctx context.Context, name, profile, seedSQL, wantVersion string,
	opts createOpts, phase func(phase, text string)) (store.Instance, error) {
	if phase == nil {
		phase = func(string, string) {}
	}
	// 同一时刻只允许一个部署在跑：并行拉镜像会把 NAS 的 IO 与带宽打满，
	// 而且用户在一次操作里点两次"部署"本来就该串行。
	s.createMySQLMu.Lock()
	defer s.createMySQLMu.Unlock()

	img, err := s.pickVersion(wantVersion)
	if err != nil {
		return store.Instance{}, errBadRequest(err.Error())
	}
	if err := s.dockerReady(ctx); err != nil {
		return store.Instance{}, err
	}
	phase("prepare", "正在检查端口与实例名")
	taken, err := s.st.NameTaken(ctx, name)
	if err != nil {
		return store.Instance{}, errInternal("检查实例名失败")
	}
	if taken {
		return store.Instance{}, errBadRequest("实例名已存在，请换一个")
	}
	port, err := s.pickPort(ctx, opts.Port)
	if err != nil {
		return store.Instance{}, err
	}

	id := ulid.New()
	now := time.Now().UnixMilli()
	in := store.Instance{
		ID:            id,
		Name:          name,
		Engine:        mysqlctl.Engine,
		EngineVersion: img.Tag,
		Mode:          "managed",
		Status:        "creating",
		// DataDir 记 "<id>/mysql"：instanceDir 取父目录，配置/密码/日志都在那里
		DataDir:    id + "/mysql",
		Profile:    profile,
		Port:       port,
		Runtime:    runtimeDocker,
		CPULimit:   opts.CPULimit,
		MemLimitMB: opts.MemLimitMB,
		// 默认用宿主目录：数据看得见、算得出体积、删除时能连数据一起清掉。
		// 命名卷留给"不想关心路径"的用户，在向导里可以选。
		StorageKind: storageBind,
		StoragePath: opts.StoragePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	password, err := newPassword()
	if err != nil {
		return store.Instance{}, errInternal("生成密码失败")
	}
	if err := s.writeSecret(id, password); err != nil {
		return store.Instance{}, errInternal("保存连接密码失败")
	}
	rootPassword := password
	if opts.StorageKind == storageVolume {
		in.StorageKind = storageVolume
	}
	if opts.RootPassword != "" {
		rootPassword = opts.RootPassword
	}
	if err := s.writeRootSecret(id, rootPassword); err != nil {
		_ = os.Remove(s.secretPath(id))
		return store.Instance{}, errInternal("保存 root 密码失败")
	}

	// 先落台账再动手：部署要几十秒到几分钟，这段时间用户回到列表
	// 必须能看到"创建中"这一行，而不是什么都没有、只能去操作记录里翻。
	if err := s.st.CreateInstance(ctx, in); err != nil {
		_ = os.Remove(s.secretPath(id))
		_ = os.Remove(s.rootSecretPath(id))
		if errors.Is(err, store.ErrNameTaken) {
			return store.Instance{}, errBadRequest("实例名已存在，请换一个")
		}
		return store.Instance{}, errInternal("写入台账失败")
	}

	spec, err := s.mysqlSpec(in)
	if err != nil {
		s.markDeployFailed(ctx, in, "prepare", err.Error())
		return in, errInternal(err.Error())
	}

	// 失败不再清场：容器只停不删，目录、卷、密码都留着，
	// 容器日志写进 deploy.log，台账标 failed 并写原因。
	fail := func(stage string, cause error) (store.Instance, error) {
		_ = spec.Stop()
		s.markDeployFailed(ctx, in, stage, cause.Error())
		return in, errInternal(cause.Error())
	}

	plan, perr := s.planFor(in, img, rootPassword, password)
	if perr != nil {
		return fail("prepare", perr)
	}

	phase("pull", "拉取镜像并启动容器（首次可能要几分钟）")
	if perr := s.startDocker(ctx, in, plan); perr != nil {
		return fail("docker", perr)
	}

	phase("start", "等待数据库初始化完成")
	// startDocker 已经用 docker run 把容器拉起来了，这里通常是"已在运行"；
	// 真正要做的是等镜像的 entrypoint 初始化完成，别把正常状态当失败。
	if serr := spec.Start(); serr != nil {
		if !errors.Is(serr, mysqlctl.ErrAlreadyRunning) {
			return fail("start", serr)
		}
		if werr := spec.WaitReady(5 * time.Minute); werr != nil {
			return fail("start", werr)
		}
	}

	phase("init", "初始化数据库账号与系统库")
	if perr := spec.Provision(password, seedSQL); perr != nil {
		return fail("provision", perr)
	}

	spec.Password = password

	phase("finalize", "收尾：服务已就绪")
	in.Status = "running"
	in.StatusReason = ""
	if err := s.st.SetStatus(ctx, in.ID, "running", ""); err != nil {
		return in, errInternal("更新台账状态失败")
	}
	return in, nil
}
