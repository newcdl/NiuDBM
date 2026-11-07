// Package pgctl 管理 PostgreSQL 实例的容器侧操作：启停、建库、账号、参数、备份与导入导出。
//
// 与 mysqlctl 的分工一样：只做 Docker 一种运行方式，管理操作走 TCP 或镜像自带的
// psql/pg_dump。对外暴露的方法名与 mysqlctl 保持一致，这样上层的 handler 只需要
// 换一个 spec，不必为两个引擎各写一套分支。
package pgctl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"niudbm/server/internal/dockerctl"
	"niudbm/server/internal/mysqlctl"
)

const (
	// Engine 是台账里的引擎标识，与前端、镜像清单的 family 一致。
	Engine = "postgres"
	// Label 是界面上显示的名字。
	Label = "PostgreSQL"
	// SuperUser 是本应用建的管理账号（超级用户），后续所有管理操作都走它。
	SuperUser = "niudbm"
	// RootUser 是镜像自带的超级用户，连接信息里展示给用户。
	RootUser = "postgres"
	// DefaultDB 是初始化脚本用的承载库：只有带种子 SQL 时才会建。
	DefaultDB = "app"
)

var (
	// ErrAlreadyRunning / ErrNotRunning 与 mysqlctl 同义，上层只判这两个。
	ErrAlreadyRunning = mysqlctl.ErrAlreadyRunning
	ErrNotRunning     = mysqlctl.ErrNotRunning
)

// 授权档位与 MySQL 一套名字：前端按 read/write/all/none 渲染，
// 两个引擎各叫一套会让"同一个按钮两种含义"。
const (
	LevelNone  = mysqlctl.LevelNone
	LevelRead  = mysqlctl.LevelRead
	LevelWrite = mysqlctl.LevelWrite
	LevelAll   = mysqlctl.LevelAll
)

// Spec 是一个 PostgreSQL 实例的连接与目录参数（容器方式）。
type Spec struct {
	// Series 是镜像 tag（17 / 16 / 15），配置项可用范围按它过滤。
	Series string
	// Flavor 固定为 postgres；保留字段是为了和 mysqlctl.Spec 形状一致。
	Flavor string
	// Host / User / External / TLSMode 用于用户已有 PostgreSQL 的直连管理。
	Host     string
	User     string
	External bool
	TLSMode  string
	// DataDir 是宿主侧数据目录：命名卷时只用于展示体积，bind 时是真实目录。
	DataDir string
	// ConfDir 是宿主侧目录，用来放导出的初始化脚本等附属文件（PG 没有 conf.d）。
	ConfDir string
	Port    int
	// Password 是管理账号 niudbm 的密码。
	Password string
	Docker   *DockerRuntime
}

// DockerRuntime 是容器运行方式需要的额外信息。
type DockerRuntime struct {
	Client    *dockerctl.Client
	Container string
	// RootPassword 是镜像初始化时给 postgres 设的密码（POSTGRES_PASSWORD）。
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
// 首次启动要 initdb 并回放镜像的初始化脚本，机械盘上几分钟很常见，
// 等待窗口与 MySQL 保持一致（5 分钟）。
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
	return s.WaitReady(5 * time.Minute)
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

// WaitReady 等到实例能执行查询为止。
func (s Spec) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.ping() == nil {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("%s 内实例没就绪，请看容器日志：docker logs %s", timeout, s.Container())
}
