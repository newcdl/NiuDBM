package mysqlctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// config 组装 DSN。容器里的库只能通过宿主机映射端口访问，
// 管理连接也从 TCP 走；只监听 127.0.0.1 的映射由 docker 负责。
func (s Spec) config(user, password, dbname string) mysql.Config {
	return mysql.Config{
		User:                 user,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("127.0.0.1:%d", s.Port),
		DBName:               dbname,
		AllowNativePasswords: true,
		// Provision 阶段要一次执行整段初始化脚本，这里统一放开多语句；
		// 只用于可信的初始化脚本，不用于用户任意输入。
		MultiStatements: true,
		Timeout:         5 * time.Second,
	}
}

// legacyAuth 判断要不要用 MySQL 5.7 之前的账号语法。
//
// CREATE USER IF NOT EXISTS 与 ALTER USER 都是 5.7.6 才引入的；
// 5.5/5.6 只能用 GRANT ... IDENTIFIED BY 改密码、SET PASSWORD 改口令。
// Series 为空（老台账）时按新版处理，避免把 MariaDB 误判成老 MySQL。
func (s Spec) legacyAuth() bool {
	if strings.TrimSpace(s.Series) == "" {
		return false
	}
	return compareSeries(s.Series, "5.7") < 0
}

// setPasswordSQL 生成改密码语句，按版本挑 5.7 前后两种写法。
func (s Spec) setPasswordSQL(acct, password string) string {
	if s.legacyAuth() {
		return "SET PASSWORD FOR " + acct + " = PASSWORD(" + quoteLiteral(password) + ")"
	}
	return "ALTER USER " + acct + " IDENTIFIED BY " + quoteLiteral(password)
}

// openRoot 用 root 空密码连接，只在 Provision 阶段使用。
func (s Spec) openRoot() (*sql.DB, error) {
	if !s.Running() {
		return nil, ErrNotRunning
	}
	cfg := s.config(RootUser, s.Docker.RootPassword, "")
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

// open 用管理账号连接指定库。
func (s Spec) open(dbname string) (*sql.DB, error) {
	if !s.Running() {
		return nil, ErrNotRunning
	}
	cfg := s.config(SuperUser, s.Password, dbname)
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

// ping 确认实例真的能接受查询。
func (s Spec) ping() error {
	db, err := s.openRoot()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// Provision 建管理账号、创建默认业务库，并在库里执行初始化脚本。
//
// 此时实例刚初始化完，只有 root 空密码可用；建好 niudbm 后，
// 后续所有操作都走 niudbm（密码由上层保存），页面永远不需要 root。
func (s Spec) Provision(password, seedSQL string) error {
	db, err := s.openRoot()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 管理连接从容器外经 TCP 进来，来源主机是宿主机地址，必须按 % 授权
	acct := quoteLiteral(SuperUser) + "@" + quoteLiteral("%")
	stmts := make([]string, 0, 3)
	if s.legacyAuth() {
		// 5.5/5.6 没有 CREATE USER IF NOT EXISTS：用 GRANT 一步把账号建出来。
		// 镜像启动时 MYSQL_USER 可能已建过同名账号，这条对它就是改密码 + 提权。
		stmts = append(stmts, "GRANT ALL PRIVILEGES ON *.* TO "+acct+
			" IDENTIFIED BY "+quoteLiteral(password)+" WITH GRANT OPTION")
	} else {
		stmts = append(stmts,
			"CREATE USER IF NOT EXISTS "+acct+" IDENTIFIED BY "+quoteLiteral(password),
			"GRANT ALL PRIVILEGES ON *.* TO "+acct+" WITH GRANT OPTION")
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("初始化管理账号失败：%w", translate(err))
		}
	}

	// 服务建出来是空的：不再自动建一个 app 库。
	// "默认库"会让人以为建服务就等于建好了一个库，而它删不掉、改名也没意义。
	// 只有带种子 SQL（内部测试与自动化用）时才建一个库来承载它。
	seed := strings.TrimSpace(seedSQL)
	if seed != "" {
		create := "CREATE DATABASE IF NOT EXISTS " + quoteIdent(DefaultDB) +
			" CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
		if _, err := db.ExecContext(ctx, create); err != nil {
			return fmt.Errorf("创建初始化库失败：%w", translate(err))
		}
		// 库名来自常量，直接拼进脚本是安全的
		script := "USE " + quoteIdent(DefaultDB) + ";\n" + seed
		if _, err := db.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("初始化 SQL 失败：%w", translate(err))
		}
	}
	if _, err := db.ExecContext(ctx, "FLUSH PRIVILEGES"); err != nil {
		return fmt.Errorf("刷新权限失败：%w", translate(err))
	}
	return nil
}

// CountTables 数这个服务下所有业务库的表，给列表页用。
//
// 不按某个"默认库"数：服务里可能一个库都没有，也可能有好几个。
func (s Spec) CountTables() (int, error) {
	db, err := s.open("")
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		  AND table_type = 'BASE TABLE'`).Scan(&n); err != nil {
		return 0, translate(err)
	}
	return n, nil
}

// quoteIdent 用反引号包住标识符。
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// quoteLiteral 用单引号包住字面量，同时处理单引号与反斜杠。
func quoteLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + s + "'"
}

// translate 把驱动错误压成一句能直接展示的话。
func translate(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("执行超时，请缩小操作范围")
	}
	msg := strings.TrimSpace(err.Error())
	// 驱动会把 DSN（含密码）拼进部分错误信息里，这里只保留最后一段可读消息
	if idx := strings.LastIndex(msg, "): "); idx >= 0 {
		msg = msg[idx+3:]
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return errors.New(msg)
}
