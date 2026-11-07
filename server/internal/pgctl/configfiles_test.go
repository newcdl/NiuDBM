package pgctl

import "testing"

// TestConfigFilesShape 配置文件清单是界面的唯一来源，形状变了前端就不认。
func TestConfigFilesShape(t *testing.T) {
	files := Spec{}.ConfigFiles()
	if len(files) != 3 {
		t.Fatalf("应有 3 个配置文件，得到 %d", len(files))
	}
	want := map[string]bool{
		"postgresql.conf":      true,
		"pg_hba.conf":          true,
		"postgresql.auto.conf": false,
	}
	for _, f := range files {
		writable, ok := want[f.Name]
		if !ok {
			t.Fatalf("多了意外文件：%s", f.Name)
		}
		if f.Writable != writable {
			t.Fatalf("%s 的 writable 应为 %v", f.Name, writable)
		}
		if f.Label == "" || f.Note == "" {
			t.Fatalf("%s 缺少标签或说明", f.Name)
		}
	}
}

// TestConfigFileNameWhitelist 文件名白名单挡住路径穿越与误改数据文件。
func TestConfigFileNameWhitelist(t *testing.T) {
	for _, ok := range []string{"postgresql.conf", "pg_hba.conf", "postgresql.auto.conf"} {
		if !validConfigName(ok) {
			t.Fatalf("%s 应被允许", ok)
		}
	}
	for _, bad := range []string{"", "../postgresql.conf", "/etc/passwd", "PG_VERSION", "base/1/2", "pg_hba.conf.bak"} {
		if validConfigName(bad) {
			t.Fatalf("%q 不应被允许", bad)
		}
	}
}

// TestBaseName 报错里只显示文件名，不泄露容器内完整路径。
func TestBaseName(t *testing.T) {
	if got := baseName("/var/lib/postgresql/data/postgresql.conf"); got != "postgresql.conf" {
		t.Fatalf("baseName 取错：%s", got)
	}
	if got := baseName("postgresql.conf"); got != "postgresql.conf" {
		t.Fatalf("baseName 不该改动无路径的输入：%s", got)
	}
}

func TestAutoConfCannotBeRestored(t *testing.T) {
	if _, err := (Spec{}).RestoreConfigFile("postgresql.auto.conf"); err == nil {
		t.Fatal("postgresql.auto.conf 由 ALTER SYSTEM 管理，不能从备份覆盖")
	}
}
