// Package mysqlctl 管理 MySQL 协议系实例的容器侧操作：
// 启停、建库、账号、参数、备份与导入导出。
//
// 本版本只有 Docker：一个实例就是一个 mysql:8.4 / mariadb:11.4 容器，
// 管理操作走镜像自带的客户端（docker exec）或 TCP，不再有宿主进程、pid 文件、
// unix socket 与降权用户——那些分支只会让每个功能都要验证两遍。
package mysqlctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niudbm/server/internal/dockerctl"
)

const (
	// Engine 是对外展示的引擎标识，台账里也用它区分实例类型。
	Engine = "mysql"
	// Label 是界面上显示的名字。
	Label = "MySQL"
	// SuperUser 是本应用建的管理账号，拥有本实例的全部权限。
	SuperUser = "niudbm"
	// SuperUserRoot 是镜像自带的 root：连接信息里展示、可开关远程访问的那个。
	SuperUserRoot = "root"
	// DefaultDB 是初始化时自动创建的库，用户拿到的连接信息指向它。
	DefaultDB = "app"
	// RootUser 是镜像初始化时建出来的超级用户，仅 Provision 阶段用来建管理账号。
	RootUser = "root"
)

var (
	// ErrAlreadyRunning 表示实例已在运行。
	ErrAlreadyRunning = errors.New("实例已在运行")
	// ErrNotRunning 表示实例没在运行。
	ErrNotRunning = errors.New("实例当前未运行")
)

// Spec 是一个 MySQL 实例的连接与目录参数（容器方式）。
type Spec struct {
	// Series 是镜像 tag（8.4 / 11.4）；Flavor 是实现（mysql / mariadb）。
	// 配置项可用范围与老版本账号语法都按这两者判断。
	Series string
	Flavor string
	// Host / User / External / TLSMode 用于用户已有 MySQL 的直连管理。
	// 托管容器不填这些字段，仍走 127.0.0.1 与 niudbm 管理账号。
	Host     string
	User     string
	External bool
	TLSMode  string
	// DataDir 是宿主侧数据目录：命名卷时只用于展示体积，bind 时是真实目录。
	DataDir string
	// ConfDir 是宿主侧的 conf.d 目录，挂进容器后改参数重启即生效。
	ConfDir string
	Port    int
	// Password 是管理账号 niudbm 的密码，所有管理操作都用它。
	Password string
	Docker   *DockerRuntime
}

// DockerRuntime 是容器运行方式需要的额外信息。
type DockerRuntime struct {
	Client *dockerctl.Client
	// Container 是容器名。
	Container string
	// RootPassword 是镜像初始化时给 root 设的密码（MARIADB_ROOT_PASSWORD / MYSQL_ROOT_PASSWORD）。
	RootPassword string
}

func (s Spec) dockerMode() bool { return s.Docker != nil && s.Docker.Client != nil }

func (s Spec) host() string {
	if strings.TrimSpace(s.Host) != "" {
		return strings.TrimSpace(s.Host)
	}
	return "127.0.0.1"
}

func (s Spec) adminUser() string {
	if strings.TrimSpace(s.User) != "" {
		return strings.TrimSpace(s.User)
	}
	return SuperUser
}

// Container 返回容器名。
func (s Spec) Container() string {
	if s.Docker == nil {
		return ""
	}
	return s.Docker.Container
}

// DirSize 统计目录实际占用，列表页用来显示磁盘用量。
func DirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// Running 判断容器是否在跑。
func (s Spec) Running() bool {
	if s.External {
		return s.checkConnection() == nil
	}
	if !s.dockerMode() {
		return false
	}
	return s.Docker.Client.Running(context.Background(), s.Docker.Container)
}

// Start 拉起容器并等到能执行查询。
//
// 首次启动要初始化系统表、可能还要给绑挂目录 chown：NAS 机械盘上几分钟很常见，
// 所以等待窗口给到 5 分钟，等不到就报错并把现场留给上层。
func (s Spec) Start() error {
	if !s.dockerMode() {
		return errors.New("实例没有配置容器运行时")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if s.Docker.Client.Running(ctx, s.Docker.Container) {
		return ErrAlreadyRunning
	}
	if err := s.Docker.Client.Start(ctx, s.Docker.Container); err != nil {
		return fmt.Errorf("启动容器失败：%w", err)
	}
	return s.waitReady(5 * time.Minute)
}

// Stop 停止容器；没在运行返回 ErrNotRunning。
func (s Spec) Stop() error {
	if !s.dockerMode() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !s.Docker.Client.Running(ctx, s.Docker.Container) {
		return ErrNotRunning
	}
	return s.Docker.Client.Stop(ctx, s.Docker.Container)
}

// WaitReady 等到实例能执行查询为止，供"容器已经起来了"的场景使用。
func (s Spec) WaitReady(timeout time.Duration) error {
	return s.waitReady(timeout)
}

// waitReady 轮询到实例真的能执行查询为止。
//
// 只看容器 running 不够：镜像 entrypoint 会先起一个临时实例做初始化，
// 这段"running 但连不上"的窗口里建库必然失败。
func (s Spec) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.ping() == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("%s 内实例没就绪，请看容器日志：docker logs %s", timeout, s.Container())
}
