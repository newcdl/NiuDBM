package pgctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// dsn 组装连接串。库只能通过宿主映射端口访问，管理连接同样走 TCP。
//
// sslmode=disable 是刻意选的：容器里的 PG 默认没开 TLS，而驱动默认要求
// sslmode=require，不改就会得到一句 "pq: SSL is not enabled on the server"，
// 用户看到的却是"连不上"。
func (s Spec) dsn(user, password, dbname string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   s.host() + ":" + strconv.Itoa(s.Port),
		Path:   "/" + dbname,
	}
	q := url.Values{}
	if s.External && s.TLSMode == "require" {
		q.Set("sslmode", "require")
	} else {
		q.Set("sslmode", "disable")
	}
	q.Set("connect_timeout", "5")
	u.RawQuery = q.Encode()
	return u.String()
}

// openRoot 用 postgres 超级用户连接，只在 Provision 阶段使用。
func (s Spec) openRoot(dbname string) (*sql.DB, error) {
	if !s.Running() {
		return nil, ErrNotRunning
	}
	db, err := sql.Open("postgres", s.dsn(RootUser, s.Docker.RootPassword, dbname))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

// open 用管理账号连接指定库；dbname 为空时连 postgres 维护库。
func (s Spec) open(dbname string) (*sql.DB, error) {
	if !s.External && !s.Running() {
		return nil, ErrNotRunning
	}
	if strings.TrimSpace(dbname) == "" {
		dbname = RootUser
	}
	db, err := sql.Open("postgres", s.dsn(s.adminUser(), s.Password, dbname))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

// ping 确认实例真的能接受查询。初始化期间容器是 running 但连不上，
// 只看容器状态会把"还在 initdb"当成"已就绪"。
func (s Spec) ping() error {
	if s.External {
		return s.CheckConnection()
	}
	db, err := s.openRoot(RootUser)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

func (s Spec) checkConnection() error {
	db, err := sql.Open("postgres", s.dsn(s.adminUser(), s.Password, RootUser))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return translate(err)
	}
	return nil
}

// CheckConnection 验证外部连接参数是否可用。
func (s Spec) CheckConnection() error {
	if !s.External {
		return errors.New("该服务不是外部连接")
	}
	return s.checkConnection()
}

// Provision 建管理账号，并在带种子脚本时建一个承载库执行它。
//
// 镜像已经用 POSTGRES_PASSWORD 建好了 postgres 超级用户，这里再建一个平级的
// niudbm：之后所有操作都用它，页面永远不需要 postgres 的密码。
func (s Spec) Provision(password, seedSQL string) error {
	db, err := s.openRoot(RootUser)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stmts := []string{
		"CREATE ROLE " + quoteIdent(SuperUser) + " WITH LOGIN SUPERUSER CREATEDB PASSWORD " + quoteLiteral(password),
		"ALTER ROLE " + quoteIdent(SuperUser) + " SET search_path = public",
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("初始化管理账号失败：%w", translate(err))
		}
	}

	// 服务建出来是空的：不带种子脚本就不建业务库，库交给用户在页面里自己建。
	seed := strings.TrimSpace(seedSQL)
	if seed != "" {
		if _, err := db.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(DefaultDB)+" ENCODING 'UTF8'"); err != nil {
			return fmt.Errorf("创建初始化库失败：%w", translate(err))
		}
		seedDB, serr := sql.Open("postgres", s.dsn(SuperUser, password, DefaultDB))
		if serr != nil {
			return serr
		}
		defer seedDB.Close()
		if _, err := seedDB.ExecContext(ctx, seed); err != nil {
			return fmt.Errorf("初始化 SQL 失败：%w", translate(err))
		}
	}
	return nil
}

// CountTables 数所有业务库里的表，给列表页用。
//
// PG 不能跨库查询，只能逐个库连过去数；库数量在 NAS 场景下很小，
// 但单个库连不上不该让整个列表失败，因此出错就跳过。
func (s Spec) CountTables() (int, error) {
	dbs, err := s.ListDatabases()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, d := range dbs {
		total += d.TableCount
	}
	return total, nil
}

// quoteIdent 用双引号包住标识符。
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral 用单引号包住字面量。
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// translate 把驱动错误压成一句能直接展示的话。
//
// pq 的错误里经常带着 DSN 与内部细节，界面上只需要最后那句人话。
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("执行超时，请缩小操作范围")
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
