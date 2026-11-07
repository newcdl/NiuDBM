package pgctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niudbm/server/internal/mysqlctl"
)

// TestValidDBName 库名既要挡住破坏语句的字符，也要放行 PG 真正允许的写法。
//
// MySQL 那套规则不认短横线和大写，直接复用会把 PG 能建的库挡在门外。
func TestValidDBName(t *testing.T) {
	ok := []string{"app", "my-db", "MyApp", "a_b9"}
	for _, name := range ok {
		if err := ValidDBName(name); err != nil {
			t.Fatalf("%q 应该合法：%v", name, err)
		}
	}
	bad := []string{"", "9db", "a b", `a"b`, "a;drop", "postgres", "template0", strings.Repeat("x", 64)}
	for _, name := range bad {
		if err := ValidDBName(name); err == nil {
			t.Fatalf("%q 应该被拒绝", name)
		}
	}
}

// TestValidTableName 表名来自前端，拼进 COPY 之前必须挡住引号与分号。
func TestValidTableName(t *testing.T) {
	if err := ValidTableName("orders_2024"); err != nil {
		t.Fatalf("普通表名应通过：%v", err)
	}
	for _, name := range []string{"", `a"b`, "a;b", "a b", "表"} {
		if err := ValidTableName(name); err == nil {
			t.Fatalf("%q 应该被拒绝", name)
		}
	}
}

func TestExternalConnectionFailure(t *testing.T) {
	spec := Spec{
		Host: "127.0.0.1", Port: 1, User: "postgres", Password: "test",
		External: true, TLSMode: "disable",
	}
	if err := spec.CheckConnection(); err == nil {
		t.Fatal("连接不存在的外部 PostgreSQL 应返回错误")
	}
}

// TestPresetsUseWhitelistedKeys 档位里出现的键必须都在白名单内，
// 否则一键调整会直接报"不支持修改参数"。
func TestPresetsUseWhitelistedKeys(t *testing.T) {
	allowed := map[string]bool{}
	for _, d := range settingDefs {
		allowed[d.key] = true
	}
	presets := Presets()
	if len(presets) == 0 {
		t.Fatal("档位不能为空")
	}
	for _, p := range presets {
		for key := range p.Values {
			if !allowed[key] {
				t.Fatalf("档位 %s 里的 %s 不在白名单内", p.Key, key)
			}
		}
	}
}

// TestVerifyBackup 备份校验要能分清"正常导出"与"半截/根本不是备份"。
func TestVerifyBackup(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.sql")
	content := "--\n-- PostgreSQL database dump\n--\n\nCREATE DATABASE app;\n"
	if err := os.WriteFile(good, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(good); err != nil {
		t.Fatalf("正常备份应通过：%v", err)
	}

	truncated := filepath.Join(dir, "truncated.sql")
	if err := os.WriteFile(truncated, []byte("--\n-- PostgreSQL database dump\n--\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(truncated); err == nil {
		t.Fatal("没有建库建表语句应被拒绝")
	}

	junk := filepath.Join(dir, "junk.sql")
	if err := os.WriteFile(junk, []byte("CREATE TABLE t(id int);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(junk); err == nil {
		t.Fatal("缺少 PG 导出头应被拒绝")
	}
}

// TestLevelsMatchMySQL 档位常量必须与 MySQL 一致：
// 前端按 read/write/all/none 渲染，两个引擎不能各叫一套。
func TestLevelsMatchMySQL(t *testing.T) {
	cases := map[string]string{
		LevelRead:  mysqlctl.LevelRead,
		LevelWrite: mysqlctl.LevelWrite,
		LevelAll:   mysqlctl.LevelAll,
		LevelNone:  mysqlctl.LevelNone,
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("档位不一致：%q vs %q", got, want)
		}
	}
}
