package httpapi

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niudbm/server/internal/dockerctl"
	"niudbm/server/internal/images"
	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

// 运行方式只剩容器一种。
//
// 以前的 native 分支（下载引擎二进制、在本机起 mariadbd、要降权与 pid/socket）
// 已全部删除：它把每个功能都拆成两条验证路径，而 NAS 上多一层容器几乎没有代价。
const runtimeDocker = "docker"

// 数据落盘方式：命名卷（默认）或宿主目录绑挂。
const (
	storageVolume = "volume"
	storageBind   = "bind"
)

// 容器内的固定参数：端口与数据目录不随版本变化。
const (
	containerMySQLPort    = 3306
	containerDataPath     = "/var/lib/mysql"
	containerPostgresPort = 5432
	containerMongoPort    = 27017
	// PG 官方镜像的数据目录；不要在它下面再套一层，否则 entrypoint 会重复 initdb
	containerPgDataPath = "/var/lib/postgresql/data"
	containerMongoPath  = "/data/db"
	// containerConfDir 是 MySQL 系镜像读额外配置的目录，宿主的 conf 目录绑挂到这里。
	// PG 没有 conf.d：它的参数走 ALTER SYSTEM，因此不挂这个目录。
	containerConfDir = "/etc/mysql/conf.d"
)

// dockerClient 返回 docker 客户端（按 ResolveBinary 找 docker CLI）。
func (s *Server) dockerClient() *dockerctl.Client {
	return dockerctl.New("")
}

// containerNameOf 用「引擎-版本-端口」命名容器，例如 mysql-8.4-33000。
//
// 用 ID 命名在 docker ps 里什么也看不出来；用端口命名后，用户在 Docker 面板里
// 能直接把容器和页面上的服务对上。代价是改端口要重建容器——这个代价可以接受，
// 改端口本来就得更改进程的监听端口。
func containerNameOf(in store.Instance) string {
	version := strings.TrimSpace(in.EngineVersion)
	if version == "" {
		version = "any"
	}
	return fmt.Sprintf("%s-%s-%d", strings.ToLower(in.Engine), version, in.Port)
}

// volumeName 是实例数据卷的名字；卷由 docker 管属主，删除实例时一起删。
func volumeName(id string) string { return "niudbm-" + strings.ToLower(id) + "-data" }

// dockerReady 检查 docker 可用，不可用时给一句能照做的提示。
func (s *Server) dockerReady(ctx context.Context) error {
	_, err := s.dockerClient().Available(ctx)
	if err != nil {
		return errBadRequest("这一版本只能用 Docker 运行，但" + err.Error())
	}
	return nil
}

// dockerPlan 描述一个数据库容器怎么起。
type dockerPlan struct {
	Image         string
	Container     string
	ContainerPort int
	DataPath      string // 容器内数据目录
	Env           map[string]string
	Command       []string
	ConfDir       string // 非空时把宿主配置目录挂进容器的 ConfTarget
	ConfTarget    string
	ValidateCnf   bool // 仅 MySQL/MariaDB 需要 mysqld --verbose --help 预检
}

// planFor 根据镜像与账号密码算出容器参数。
//
// 配置差异集中在这里：MySQL 与 MariaDB 的初始化变量名不同
// （MYSQL_* / MARIADB_*），Oracle MySQL 8 还要关掉 mysqlx，避免多占一个端口。
func (s *Server) planFor(in store.Instance, img images.Image, rootPassword, appPassword string) (dockerPlan, error) {
	base := s.instanceDir(in)
	plan := dockerPlan{
		// 这里只记官方镜像名；要不要走加速站在 ensureImage 里决定
		Image:         img.Name,
		Container:     containerNameOf(in),
		ContainerPort: containerMySQLPort,
		DataPath:      containerDataPath,
		ConfDir:       filepath.Join(base, "conf"),
		ConfTarget:    containerConfDir,
		ValidateCnf:   true,
	}
	if img.Family == images.FamilyMongo {
		plan.ContainerPort = containerMongoPort
		plan.DataPath = containerMongoPath
		plan.ConfDir = filepath.Join(base, "conf")
		plan.ConfTarget = mongoctl.ContainerConfDir
		plan.ValidateCnf = false
		if err := mongoctl.EnsureConfig(filepath.Join(plan.ConfDir, mongoctl.ConfigFileName), img.Tag); err != nil {
			return dockerPlan{}, fmt.Errorf("创建 mongod.conf 失败：%w", err)
		}
		plan.Command = []string{"mongod", "--config", mongoctl.ContainerConfFile}
		plan.Env = map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": mongoctl.RootUser,
			"MONGO_INITDB_ROOT_PASSWORD": rootPassword,
		}
		return plan, nil
	}
	if img.Family == images.FamilyPostgres {
		// PG 的初始化只有一件事：给 postgres 超级用户设密码。
		// 不传 POSTGRES_DB：传了会顺手建一个库，用户会以为"建服务"就等于"建好一个库"。
		plan.ContainerPort = containerPostgresPort
		plan.DataPath = containerPgDataPath
		// 没有 conf.d：参数由 ALTER SYSTEM 写进 postgresql.auto.conf
		plan.ConfDir = ""
		plan.ConfTarget = ""
		plan.ValidateCnf = false
		plan.Env = map[string]string{
			"POSTGRES_PASSWORD": rootPassword,
			// 显式指定认证方式：老镜像默认 md5，新版默认 scram，统一成 scram 更安全
			"POSTGRES_HOST_AUTH_METHOD": "scram-sha-256",
		}
		plan.Command = nil
		return plan, nil
	}
	if img.Family == images.FamilyMySQL {
		// 不传 MYSQL_DATABASE：建出来的服务是空的，库由用户在页面里自己建。
		// 传了它镜像会顺手建一个库出来，用户会以为"建服务"就等于"建好一个库"，
		// 然后发现删不掉、也没法改名。
		plan.Env = map[string]string{
			"MYSQL_ROOT_PASSWORD": rootPassword,
			"MYSQL_ROOT_HOST":     "%",
			"MYSQL_USER":          mysqlctl.SuperUser,
			"MYSQL_PASSWORD":      appPassword,
		}
		// 字符集与排序规则不在这里传：命令行的优先级高于配置文件，留在这里会盖掉
		// 用户在「配置」页改的 character_set_server / collation_server。这两项由
		// conf.d/niudbm.cnf 负责，Spec.EnsureCnf 已保证文件存在且带默认值。
		plan.Command = nil
		// 不给 mysqld 传任何追加参数。
		//
		// 以前的 --mysqlx=0（后改 OFF）看着人畜无害：只是让 X Plugin 别在容器内监听
		// 33060，而宿主根本没映射这个端口。但它会作为一个未知/非法选项让
		// `mysqld --mysqlx=... --verbose --help` 直接退出——entrypoint 正是靠这条命令
		// 的输出反推 datadir/socket，一旦空掉，`dirname ""` 得到 "."，紧接着就是对
		// 容器根目录做 `find . -exec chown`，日志被 /sys 的只读报错刷屏，实例永远
		// 就绪不了。省一个用不上的容器内端口，不值得赌这个风险。
		plan.Command = nil
		return plan, nil
	}
	plan.Env = map[string]string{
		"MARIADB_ROOT_PASSWORD": rootPassword,
		"MARIADB_ROOT_HOST":     "%",
		"MARIADB_USER":          mysqlctl.SuperUser,
		"MARIADB_PASSWORD":      appPassword,
	}
	// 同上：MariaDB 的字符集与排序规则也交给配置文件
	plan.Command = nil
	return plan, nil
}

// openConfDirForContainer 让容器内的 mysqld 能读到挂载进去的配置文件。
//
// 配置目录是 bind mount 进来的，属主是宿主上的应用用户；而容器里的 mysqld 以 mysql
// 用户运行，uid 随基础镜像变化（Oracle Linux 9 是 27，Debian 是 999），宿主属主永远
// 对不上。权限一旦是 0750/0640，启动就直接报
// `Can't read dir of '/etc/mysql/conf.d/' (OS errno 13 - Permission denied)`。
// 这里统一放开到"其他用户可读"：目录 0755、文件 0644（配置文件里不含密码）。
func openConfDirForContainer(dir string) {
	_ = os.Chmod(dir, 0o755)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = os.Chmod(filepath.Join(dir, e.Name()), 0o644)
	}
}

// startDocker 先把容器建起来（MYSQL_DATABASE 之类只在这里生效），再交给 Start 等待就绪。
//
// 镜像不存在时先拉：NAS 上第一次创建会慢几分钟，进度通过实例状态与日志体现。
func (s *Server) startDocker(ctx context.Context, in store.Instance, plan dockerPlan) error {
	client := s.dockerClient()
	if _, err := client.Available(ctx); err != nil {
		return err
	}
	// 实例目录留着放配置与日志：删除实例时也要按它归档/清理
	if err := os.MkdirAll(s.instanceDir(in), 0o755); err != nil {
		return fmt.Errorf("创建实例目录失败：%w", err)
	}
	if plan.ConfDir != "" {
		if err := os.MkdirAll(plan.ConfDir, 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败：%w", err)
		}
		openConfDirForContainer(plan.ConfDir)
	}

	image, err := s.ensureImage(ctx, client, plan.Image)
	if err != nil {
		return err
	}

	// 配置先让 mysqld 自己过一遍：参数不被这个镜像接受（典型是版本对不上的
	// innodb_redo_log_capacity）就在这里挡住。否则容器的 entrypoint 解析不到
	// datadir/socket，会退化成对整个容器根目录 chown，部署日志里只剩 /sys 的
	// 只读报错，真正的原因（unknown variable xxx）反而看不到。
	if plan.ConfDir != "" {
		checkCtx, checkCancel := context.WithTimeout(ctx, time.Minute)
		if plan.ValidateCnf {
			err := client.CheckCnf(checkCtx, image, plan.ConfDir, plan.ConfTarget, plan.Command)
			checkCancel()
			if err != nil {
				return err
			}
		} else {
			checkCancel()
		}
	}

	// 上一次失败留下的同名容器必须先清掉，否则 docker run 会直接报名字冲突
	_ = client.Remove(ctx, plan.Container)

	// 数据落盘方式：默认命名卷（Docker 管属主，跨平台最稳）；
	// 用户选了宿主目录就绑挂过去，好处是自己能找到文件、方便单独备份。
	opt := dockerctl.RunOptions{
		Name:          plan.Container,
		Image:         image,
		InstanceID:    in.ID,
		Engine:        in.Engine,
		Port:          in.Port,
		ContainerPort: plan.ContainerPort,
		DataDir:       volumeName(in.ID),
		DataPath:      plan.DataPath,
		UseVolume:     true,
		Env:           plan.Env,
		Command:       plan.Command,
		CPUs:          in.CPULimit,
		MemoryMB:      in.MemLimitMB,
		// 用户手动停止的容器不会被拉起，NAS 重启后运行中的服务自己回来
		Restart: "unless-stopped",
	}
	if in.StorageKind != storageVolume {
		// 绑挂目录由官方镜像的 entrypoint 做 chown，这里只保证目录存在
		dir := s.storageDir(in)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录 %s 失败：%w", dir, err)
		}
		opt.DataDir = dir
		opt.UseVolume = false
	}
	if plan.ConfDir != "" && plan.ConfTarget != "" {
		// 配置目录挂载后，页面上写入的文件容器重启后依然生效
		opt.VolumeExtra = [][2]string{{plan.ConfDir, plan.ConfTarget}}
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := client.Run(runCtx, opt); err != nil {
		return fmt.Errorf("创建容器失败：%w", err)
	}
	return nil
}

// imageCandidates 返回按顺序尝试的镜像名：配置了加速站就先用它，最后一定退回官方名。
//
// 国内直连 Docker Hub 经常在拉清单阶段就超时，所以真实机上默认走加速站；
// 加速站同时挂掉时还要能退回官方源，不然一次镜像站故障就会让整个功能不可用。
func (s *Server) imageCandidates(image string) []string {
	out := make([]string, 0, 2)
	// 只给"官方短名"（mysql:8.4）加加速站前缀。
	// 已经带仓库的地址（docker.m.daocloud.io/library/postgres:17、私有 registry:5000/db:17）
	// 再拼一次就成了 registry/library/registry/...，拉取必然失败。
	if s.env.DockerMirror != "" && !strings.Contains(image, "/") {
		out = append(out, s.env.DockerMirror+"library/"+image)
	}
	return append(out, image)
}

// ensureImage 保证镜像在本地可用，返回 docker run 该用的镜像名。
//
// 从加速站拉到的是带前缀的标签，拉完统一 tag 成官方名：这样 docker ps 里显示的
// 就是界面上的 mysql:8.4，后续实例也能直接命中本地镜像，不必再拉一次。
func (s *Server) ensureImage(ctx context.Context, client *dockerctl.Client, image string) (string, error) {
	candidates := s.imageCandidates(image)
	for _, cand := range candidates {
		if client.ImageExists(ctx, cand) {
			return cand, nil
		}
	}

	var lastErr error
	for _, cand := range candidates {
		pullCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		err := client.Pull(pullCtx, cand)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if cand == image {
			return cand, nil
		}
		if err := client.Tag(ctx, cand, image); err != nil {
			// 打标签只是为了名字好认，失败也不该让建库失败：直接用加速站的名字跑
			log.Printf("镜像重命名失败（%s → %s）：%v", cand, image, err)
			return cand, nil
		}
		return image, nil
	}
	return "", fmt.Errorf("拉取镜像 %s 失败：%w", image, lastErr)
}

// removeDocker 删除实例的容器；removeVolume 为真时连命名卷一起删。
//
// "保留数据"只对卷有意义：绑挂目录是用户自己的路径，删不删由调用方决定。
func (s *Server) removeDocker(ctx context.Context, in store.Instance, removeVolume bool) error {
	if isExternal(in) {
		return nil
	}
	client := s.dockerClient()
	if _, err := client.Available(ctx); err != nil {
		// Docker 没起来时容器也跑不了，残留文件会让下次 docker run 失败；
		// 这里只记录不阻塞删除，用户可以自己 docker rm。
		return nil
	}
	if err := client.Remove(ctx, containerNameOf(in)); err != nil {
		return err
	}
	if !removeVolume {
		return nil
	}
	return client.RemoveVolume(ctx, volumeName(in.ID))
}

// dockerPlanFor 按台账重建一份 docker 运行计划（改端口、改配额后重建容器用）。
func (s *Server) dockerPlanFor(in store.Instance) (dockerPlan, error) {
	img, ok := images.FindFamily(familyOf(in.Engine), in.EngineVersion)
	if !ok {
		return dockerPlan{}, fmt.Errorf("找不到版本 %s 的镜像信息，无法重建容器", in.EngineVersion)
	}
	plan, err := s.planFor(in, img, s.readRootSecret(in.ID), s.readSecret(in.ID))
	if err != nil {
		return dockerPlan{}, err
	}
	// 重建必须沿用它当初用的地址：用户填过加速站/私有仓库，这里回退到官方名
	// 会在国内网络下直接拉不动，看起来就像"改个端口把服务搞坏了"
	if in.Image != "" {
		plan.Image = in.Image
	}
	return plan, nil
}

// rebuildDocker 用台账里的最新配置重建容器并拉起。
// 数据在命名卷/宿主目录里，删容器不动数据，所以改端口、改存储都可以走这条路。
func (s *Server) rebuildDocker(ctx context.Context, in store.Instance) error {
	plan, err := s.dockerPlanFor(in)
	if err != nil {
		return err
	}
	return s.startDocker(ctx, in, plan)
}

// familyOf 把引擎标识映射到镜像清单的 family。
func familyOf(engine string) string {
	switch engine {
	case enginePostgres:
		return images.FamilyPostgres
	case engineMongoDB:
		return images.FamilyMongo
	default:
		return images.FamilyMySQL
	}
}

// pickVersionFor 按引擎解析版本。
//
// 两个引擎的版本号会撞（比如都叫"16"），所以必须带上 family 一起判断：
// 否则在 PG 里填一个 MySQL 的版本号会被"找到"并真去拉错镜像。
func (s *Server) pickVersionFor(engine, want string) (images.Image, error) {
	family := familyOf(engine)
	if strings.TrimSpace(want) == "" {
		img, ok := images.DefaultFor(family)
		if !ok {
			return images.Image{}, fmt.Errorf("镜像清单里没有 %s 的可用版本", engineLabel(engine))
		}
		return img, nil
	}
	img, ok := images.FindFamily(family, want)
	if !ok {
		names := make([]string, 0, 4)
		for _, item := range images.ListFamily(family) {
			names = append(names, item.Tag)
		}
		return images.Image{}, fmt.Errorf("不支持这个版本：%s；%s 可选 %s",
			want, engineLabel(engine), strings.Join(names, " / "))
	}
	if !img.Supports(s.arch()) {
		return images.Image{}, fmt.Errorf("%s 的官方镜像没有 %s 架构，请换一个版本", img.Label, s.arch())
	}
	return img, nil
}

// dockerPortOwner 查端口是否被容器占用，用于分配端口时避开别人的容器。
func (s *Server) dockerPortOwner(ctx context.Context, port int) string {
	client := s.dockerClient()
	if _, err := client.Available(ctx); err != nil {
		return ""
	}
	return client.PortOwner(ctx, port)
}

// dockerLogTail 取容器日志尾部，实例日志接口与失败现场都用它。
func (s *Server) dockerLogTail(ctx context.Context, in store.Instance, lines int) (string, error) {
	client := s.dockerClient()
	if _, err := client.Available(ctx); err != nil {
		return "", err
	}
	return client.Logs(ctx, containerNameOf(in), lines)
}
