package mysqlctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 官方 entrypoint 会把 slow_query_log_file 等配置值 dirname 之后加进待 chown 的
// 目录集合。相对路径 "slow.log" 的 dirname 是 "."，等于让它对整个容器根目录
// find + chown——/sys 只读刷屏，实例永远不就绪。启动时要把这类值改成绝对路径。
func TestFixRelativeCnfPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "niudbm.cnf")
	raw := "[mysqld]\n" +
		"slow_query_log = OFF\n" +
		"slow_query_log_file = slow.log\n" +
		"pid_file = /var/run/mysqld/mysqld.pid\n" +
		"# slow_query_log_file = 注释里的不该动\n"
	if err := os.WriteFile(file, []byte(raw), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := fixRelativeCnfPaths(file); err != nil {
		t.Fatalf("改写失败：%v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)

	if strings.Contains(text, "slow_query_log_file = slow.log") {
		t.Fatalf("相对路径没被改写：\n%s", text)
	}
	if !strings.Contains(text, "slow_query_log_file = /var/lib/mysql/slow.log") {
		t.Fatalf("应改写成容器内绝对路径：\n%s", text)
	}
	if !strings.Contains(text, "pid_file = /var/run/mysqld/mysqld.pid") {
		t.Fatalf("绝对路径不该被动：\n%s", text)
	}
	if !strings.Contains(text, "slow_query_log = OFF") {
		t.Fatalf("其它配置行丢了：\n%s", text)
	}
	if !strings.Contains(text, "# slow_query_log_file = 注释里的不该动") {
		t.Fatalf("注释行不该被改写：\n%s", text)
	}
}

// 配置文件曾经只有"用户改过的那几项"：早期版本建的文件只有一个段头，
// 保存参数时只把改动的键写回去。mergeCnfDefaults 负责把缺的补上。
func TestMergeCnfDefaults(t *testing.T) {
	entries := map[string]string{
		"max_connections":         "256",
		"innodb_buffer_pool_size": "2G",
		"innodb_page_size":        "8K", // 手写的白名单外参数
	}
	defaults := map[string]string{
		"max_connections":         "151", // 已有值不能被默认值盖掉
		"innodb_buffer_pool_size": "128M",
		"sort_buffer_size":        "256K", // 缺失项要补上
		"wait_timeout":            "28800",
	}
	mergeCnfDefaults(entries, defaults)

	if entries["max_connections"] != "256" || entries["innodb_buffer_pool_size"] != "2G" {
		t.Fatalf("已有值被覆盖了：%v", entries)
	}
	if entries["sort_buffer_size"] != "256K" || entries["wait_timeout"] != "28800" {
		t.Fatalf("缺失项没补上：%v", entries)
	}
	if entries["innodb_page_size"] != "8K" {
		t.Fatalf("手写的额外参数被删了：%v", entries)
	}
}

// 8.4 上 default_authentication_plugin 已移除、innodb_redo_log_capacity 才有；
// 缺失项清单必须跟着版本走，否则会提示用户去补一个这版根本没有的参数。
func TestCnfMissingBySeries(t *testing.T) {
	spec := Spec{Flavor: "mysql", Series: "8.4"}
	missing := spec.CnfMissing("[mysqld]\nmax_connections = 151\n")
	got := map[string]bool{}
	for _, k := range missing {
		got[k] = true
	}
	if got["max_connections"] {
		t.Error("已有的 max_connections 不该算缺失")
	}
	if !got["innodb_buffer_pool_size"] {
		t.Error("8.4 应该缺 innodb_buffer_pool_size")
	}
	if !got["innodb_redo_log_capacity"] {
		t.Error("8.4 应该有 innodb_redo_log_capacity")
	}
	if got["default_authentication_plugin"] {
		t.Error("8.4 已移除 default_authentication_plugin，不该要求补它")
	}

	// 老版本 8.0 反过来
	old := Spec{Flavor: "mysql", Series: "8.0"}
	oldMissing := map[string]bool{}
	for _, k := range old.CnfMissing("") {
		oldMissing[k] = true
	}
	if !oldMissing["default_authentication_plugin"] {
		t.Error("8.0 应该有 default_authentication_plugin")
	}
}

// 模板里必须带上分组与全部默认值，前端「载入推荐模板」直接用它。
func TestCnfTemplateHasGroups(t *testing.T) {
	spec := Spec{Flavor: "mysql", Series: "8.4"}
	text := spec.CnfTemplate()
	for _, want := range []string{"[mysqld]", "# ---- 连接与超时 ----", "# ---- InnoDB：内存与刷盘 ----", "innodb_redo_log_capacity"} {
		if !strings.Contains(text, want) {
			t.Errorf("模板缺少 %q", want)
		}
	}
	if strings.Contains(text, "default_authentication_plugin") {
		t.Error("8.4 的模板不该出现 default_authentication_plugin")
	}
}

// 手写内容里的 [client] 段不该被当成 [mysqld] 的参数
func TestParseCnfIgnoresOtherSections(t *testing.T) {
	got := parseCnf("[client]\nport = 3306\n[mysqld]\nmax_connections = 200\n")
	if got["port"] == "3306" {
		t.Error("[client] 段的参数被误读成 mysqld 的")
	}
	if got["max_connections"] != "200" {
		t.Errorf("mysqld 段没解析出来：%v", got)
	}
}
