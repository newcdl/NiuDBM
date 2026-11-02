package mysqlctl

import "testing"

// TestSettingAppliesByFlavorAndVersion 守住 MySQL 家族的配置差异：
// MariaDB 没有 innodb_redo_log_capacity，MySQL 8.4 没有 default_authentication_plugin。
func TestSettingAppliesByFlavorAndVersion(t *testing.T) {
	defs := map[string]settingDef{}
	for _, d := range settingDefs {
		defs[d.key] = d
	}

	redo, ok := defs["innodb_redo_log_capacity"]
	if !ok {
		t.Fatal("应登记 innodb_redo_log_capacity")
	}
	if redo.applies("mariadb", "11.4") {
		t.Fatal("MariaDB 没有 innodb_redo_log_capacity")
	}
	if !redo.applies("mysql", "8.4") || !redo.applies("mysql", "8.0.32") {
		t.Fatal("MySQL 8.0.30+ 应有 innodb_redo_log_capacity")
	}
	// 8.0.30 才引入这个变量。两段标签 "8.0" 无法确定小版本，
	// 宁可不写：写错的代价是 mysqld 以 unknown variable 起不来。
	if redo.applies("mysql", "8.0.20") || redo.applies("mysql", "8.0") {
		t.Fatal("MySQL 8.0.30 之前不该有 innodb_redo_log_capacity")
	}

	auth, ok := defs["default_authentication_plugin"]
	if !ok {
		t.Fatal("应登记 default_authentication_plugin")
	}
	if !auth.applies("mysql", "8.0") {
		t.Fatal("MySQL 8.0 支持 default_authentication_plugin")
	}
	if auth.applies("mysql", "8.4") {
		t.Fatal("MySQL 8.4 已移除 default_authentication_plugin")
	}
	if auth.applies("mariadb", "11.4") {
		t.Fatal("MariaDB 没有 default_authentication_plugin")
	}
	// 5.5/5.6 没有这个参数：写进 my.cnf 会让实例起不来，必须按版本挡住
	if auth.applies("mysql", "5.6") || auth.applies("mysql", "5.5") {
		t.Fatal("MySQL 5.5/5.6 没有 default_authentication_plugin")
	}
	if !auth.applies("mysql", "5.7") {
		t.Fatal("MySQL 5.7 支持 default_authentication_plugin")
	}
	aria, ok := defs["aria_pagecache_buffer_size"]
	if !ok {
		t.Fatal("应登记 aria_pagecache_buffer_size")
	}
	if !aria.applies("mariadb", "10.11") || aria.applies("mysql", "8.0") {
		t.Fatal("aria_pagecache_buffer_size 只在 MariaDB 上存在")
	}

	// 通用参数不受实现与版本影响
	if !defs["max_connections"].applies("mysql", "8.4") || !defs["max_connections"].applies("mariadb", "10.6") {
		t.Fatal("max_connections 应两种实现都支持")
	}
}
