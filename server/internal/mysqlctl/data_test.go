package mysqlctl

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// TestTCPAccessSmoke 由双架构容器冒烟注入连接信息，验证真实 TCP 密码认证与库级授权。
//
// 与 PostgreSQL 探针同一思路：不用管理账号 SET ROLE 冒充，而是真的用接入账号走一次
// 网络连接，这样密码错、档位没生效这类问题才会暴露出来。
func TestTCPAccessSmoke(t *testing.T) {
	port := os.Getenv("MYSQL_SMOKE_PORT")
	if port == "" {
		t.Skip("仅在真实引擎冒烟中执行")
	}
	cfg := mysql.Config{
		User:   os.Getenv("MYSQL_SMOKE_USER"),
		Passwd: os.Getenv("MYSQL_SMOKE_PASSWORD"),
		Net:    "tcp",
		Addr:   "127.0.0.1:" + port,
		// 服务不再自带默认库：探针连哪个库由冒烟脚本指定
		DBName:               os.Getenv("MYSQL_SMOKE_DB"),
		AllowNativePasswords: true,
		Timeout:              5 * time.Second,
		ReadTimeout:          10 * time.Second,
		// 多语句用于冒烟脚本里 BEGIN/ROLLBACK 这种一次性验证
		MultiStatements: true,
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = db.PingContext(ctx)
	if err == nil {
		_, err = db.ExecContext(ctx, os.Getenv("MYSQL_SMOKE_SQL"))
	}

	want := os.Getenv("MYSQL_SMOKE_ERROR")
	if want == "" {
		if err != nil {
			t.Fatalf("接入用户操作失败：%v", err)
		}
		return
	}
	code, convErr := strconv.Atoi(want)
	if convErr != nil {
		t.Fatalf("期望的错误码不是数字：%s", want)
	}
	var myErr *mysql.MySQLError
	if !errors.As(err, &myErr) {
		t.Fatalf("期望 MySQL 错误 %d，实际 %v", code, err)
	}
	if myErr.Number != uint16(code) {
		t.Fatalf("期望 MySQL 错误 %d，实际 %d（%s）", code, myErr.Number, myErr.Message)
	}
}
