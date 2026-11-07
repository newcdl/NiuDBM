// Package mongoctl 管理 MongoDB 服务与外部连接。
package mongoctl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"niudbm/server/internal/dockerctl"
	"niudbm/server/internal/mysqlctl"
)

const (
	Engine    = "mongodb"
	Label     = "MongoDB"
	RootUser  = "root"
	DefaultDB = "admin"
)

var (
	ErrAlreadyRunning = mysqlctl.ErrAlreadyRunning
	ErrNotRunning     = mysqlctl.ErrNotRunning
)

type Spec struct {
	Series   string
	Flavor   string
	Host     string
	User     string
	Password string
	Port     int
	External bool
	TLSMode  string
	Docker   *DockerRuntime
}

type DockerRuntime struct {
	Client       *dockerctl.Client
	Container    string
	RootPassword string
}

func (s Spec) dockerMode() bool { return s.Docker != nil && s.Docker.Client != nil }

func (s Spec) host() string {
	if strings.TrimSpace(s.Host) != "" {
		return strings.TrimSpace(s.Host)
	}
	return "127.0.0.1"
}

func (s Spec) user() string {
	if strings.TrimSpace(s.User) != "" {
		return strings.TrimSpace(s.User)
	}
	return RootUser
}

func (s Spec) uri() string {
	u := url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(s.host(), strconv.Itoa(s.Port)),
	}
	if s.user() != "" {
		u.User = url.UserPassword(s.user(), s.Password)
	}
	q := url.Values{}
	q.Set("authSource", "admin")
	q.Set("connectTimeoutMS", "5000")
	q.Set("serverSelectionTimeoutMS", "6000")
	if s.External && s.TLSMode == "require" {
		q.Set("tls", "true")
		q.Set("tlsInsecure", "true")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s Spec) client(ctx context.Context) (*mongo.Client, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(s.uri()))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s Spec) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	return translate(client.Ping(ctx, readpref.Primary()))
}

func (s Spec) CheckConnection() error { return s.ping() }

func (s Spec) Running() bool {
	if s.External {
		return s.ping() == nil
	}
	if !s.dockerMode() {
		return false
	}
	return s.Docker.Client.Running(context.Background(), s.Docker.Container)
}

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
		return fmt.Errorf("启动 MongoDB 失败：%w", err)
	}
	return s.WaitReady(5 * time.Minute)
}

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

func (s Spec) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.ping() == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s 内 MongoDB 没就绪", timeout)
}

func (s Spec) Provision(password, seedSQL string) error {
	if strings.TrimSpace(seedSQL) != "" {
		return errors.New("MongoDB 初始化脚本暂不支持 SQL；请建好服务后通过集合工具导入")
	}
	return nil
}

func (s Spec) Container() string {
	if s.Docker == nil {
		return ""
	}
	return s.Docker.Container
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("连接 MongoDB 超时")
	}
	msg := strings.TrimSpace(err.Error())
	if idx := strings.LastIndex(msg, "): "); idx >= 0 {
		msg = msg[idx+3:]
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return errors.New(msg)
}
