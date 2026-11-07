package mongoctl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidDBName(t *testing.T) {
	for _, name := range []string{"blog", "orders-2026", "my_app"} {
		if err := ValidDBName(name); err != nil {
			t.Fatalf("%q 应合法：%v", name, err)
		}
	}
	for _, name := range []string{"", "admin", "a.b", "a/b", "a;b"} {
		if err := ValidDBName(name); err == nil {
			t.Fatalf("%q 应被拒绝", name)
		}
	}
}

func TestVerifyBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mongo.archive.gz")
	if err := os.WriteFile(path, append([]byte{0x1f, 0x8b}, make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Spec{}).VerifyBackup(path); err != nil {
		t.Fatalf("合法 gzip archive 应通过：%v", err)
	}
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Spec{}).VerifyBackup(path); err == nil {
		t.Fatal("非 gzip 备份应被拒绝")
	}
}

func TestExternalConnectionFailure(t *testing.T) {
	spec := Spec{
		Host: "127.0.0.1", Port: 1, User: "root", Password: "test",
		External: true, TLSMode: "disable",
	}
	if err := spec.CheckConnection(); err == nil {
		t.Fatal("连接不存在的外部 MongoDB 应返回错误")
	}
}
