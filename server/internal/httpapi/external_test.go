package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niudbm/server/internal/platform"
)

func TestExternalConnectAndCapabilities(t *testing.T) {
	e := newTestEnv(t, false)
	var checked externalEndpoint
	e.handler.externalTest = func(endpoint externalEndpoint) error {
		checked = endpoint
		return nil
	}

	resp := e.must(t, 201, "POST", "/api/v1/instances/connect", map[string]any{
		"name":          "生产 MySQL",
		"engine":        "mysql",
		"engineVersion": "8.4",
		"host":          "192.168.1.20",
		"port":          3306,
		"username":      "dbadmin",
		"password":      "OnlyForTest_42",
		"tlsMode":       "require",
	})
	var view instanceView
	if err := json.Unmarshal(resp.Data, &view); err != nil {
		t.Fatal(err)
	}
	if view.Mode != instanceModeExternal || view.Status != "running" {
		t.Fatalf("外部连接状态不对：mode=%q status=%q", view.Mode, view.Status)
	}
	if view.Host != "192.168.1.20" || view.Username != "dbadmin" || view.TLSMode != "require" {
		t.Fatalf("连接字段没有正确返回：%+v", view.Instance)
	}
	if view.Capabilities.StartStop || view.Capabilities.Config || view.Capabilities.Backup {
		t.Fatalf("外部连接不应获得托管能力：%+v", view.Capabilities)
	}
	if !view.Capabilities.Databases || !view.Capabilities.Users || !view.Capabilities.TestConnection {
		t.Fatalf("外部连接缺少基础管理能力：%+v", view.Capabilities)
	}
	if checked.Password != "OnlyForTest_42" || checked.TLSMode != "require" {
		t.Fatalf("连接测试参数丢失：%+v", checked)
	}

	raw, err := os.ReadFile(e.handler.externalSecretPath(view.ID))
	if err != nil {
		t.Fatalf("凭据没有落盘：%v", err)
	}
	if strings.Contains(string(raw), "OnlyForTest_42") {
		t.Fatal("外部连接密码不应以明文落盘")
	}
	if got := e.handler.readExternalSecret(view.ID); got != "OnlyForTest_42" {
		t.Fatalf("凭据解密失败：%q", got)
	}

	e.must(t, 200, "GET", "/api/v1/instances", nil)
	e.must(t, 200, "POST", "/api/v1/instances/"+view.ID+"/test-connection", nil)
	e.must(t, 200, "DELETE", "/api/v1/instances/"+view.ID, map[string]any{
		"confirmName": "生产 MySQL",
		"keepData":    true,
	})
	if _, err := os.Stat(e.handler.externalSecretPath(view.ID)); !os.IsNotExist(err) {
		t.Fatalf("移除连接后凭据应删除，实际 err=%v", err)
	}
}

func TestExternalConnectValidation(t *testing.T) {
	e := newTestEnv(t, false)
	e.handler.externalTest = func(externalEndpoint) error { return nil }

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "缺主机",
			body: map[string]any{"name": "a", "engine": "mysql", "port": 3306, "username": "u", "password": "12345678"},
			want: "地址不能为空",
		},
		{
			name: "带协议",
			body: map[string]any{"name": "a", "engine": "mysql", "host": "mysql://db", "port": 3306, "username": "u", "password": "12345678"},
			want: "不要带协议",
		},
		{
			name: "端口错误",
			body: map[string]any{"name": "a", "engine": "mysql", "host": "db.local", "port": 0, "username": "u", "password": "12345678"},
			want: "端口",
		},
		{
			name: "TLS 错误",
			body: map[string]any{"name": "a", "engine": "mysql", "host": "db.local", "port": 3306, "username": "u", "password": "12345678", "tlsMode": "verify-full"},
			want: "TLS",
		},
		{
			name: "不支持引擎",
			body: map[string]any{"name": "a", "engine": "redis", "host": "db.local", "port": 6379, "username": "u", "password": "12345678"},
			want: "支持 MySQL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, resp := e.call(t, "POST", "/api/v1/instances/connect", nil, tc.body)
			if code != 400 || !strings.Contains(resp.Message, tc.want) {
				t.Fatalf("应返回 400 且包含 %q，实际 %d：%s", tc.want, code, resp.Message)
			}
		})
	}
}

func TestExternalConnectionFailureDoesNotPersist(t *testing.T) {
	e := newTestEnv(t, false)
	e.handler.externalTest = func(externalEndpoint) error {
		return os.ErrPermission
	}

	code, resp := e.call(t, "POST", "/api/v1/instances/connect", nil, map[string]any{
		"name":     "无法连接",
		"engine":   "postgres",
		"host":     "192.168.1.30",
		"port":     5432,
		"username": "postgres",
		"password": "OnlyForTest_42",
	})
	if code != 409 || !strings.Contains(resp.Message, "连接失败") {
		t.Fatalf("连接失败应返回 409，实际 %d：%s", code, resp.Message)
	}
	taken, err := e.st.NameTaken(t.Context(), "无法连接")
	if err != nil {
		t.Fatal(err)
	}
	if taken {
		t.Fatal("连接失败时不应留下台账记录")
	}
}

func TestExternalMongoDBCapabilities(t *testing.T) {
	e := newTestEnv(t, false)
	e.handler.externalTest = func(externalEndpoint) error { return nil }
	resp := e.must(t, 201, "POST", "/api/v1/instances/connect", map[string]any{
		"name": "Mongo 主库", "engine": "mongodb", "engineVersion": "8.0",
		"host": "192.168.1.40", "port": 27017, "username": "root",
		"password": "OnlyForTest_42", "tlsMode": "disable",
	})
	var view instanceView
	if err := json.Unmarshal(resp.Data, &view); err != nil {
		t.Fatal(err)
	}
	if view.Engine != engineMongoDB || !view.Capabilities.Users || !view.Capabilities.Databases {
		t.Fatalf("MongoDB 外部连接能力不对：%+v", view)
	}
	if view.Capabilities.Backup || view.Capabilities.Transfer || view.Capabilities.Config {
		t.Fatalf("MongoDB 外部连接不应显示不支持能力：%+v", view.Capabilities)
	}
}

func TestManagedStoragePathAllowlist(t *testing.T) {
	root := t.TempDir()
	share := filepath.Join(root, "share", "niudbm", "data")
	env := platform.Env{PkgVar: filepath.Join(root, "var"), DataSharePaths: []string{share}}
	s := New(env, "", nil, Paths{})

	for _, path := range []string{
		filepath.Join(share, "mysql", "blog"),
		filepath.Join(env.PkgVar, "instances", "default-app"),
	} {
		if err := s.validateStoragePath(path); err != nil {
			t.Fatalf("授权目录应允许 %s：%v", path, err)
		}
	}
	for _, path := range []string{"/etc", "/", filepath.Join(share, "..", "other")} {
		if err := s.validateStoragePath(path); err == nil {
			t.Fatalf("危险目录应拒绝：%s", path)
		}
	}
	escape := filepath.Join(share, "escape")
	if err := os.MkdirAll(share, 0o755); err == nil {
		if err := os.Symlink("/etc", escape); err == nil {
			if err := s.validateStoragePath(filepath.Join(escape, "hosts")); err == nil {
				t.Fatal("授权目录内指向系统目录的软链接应拒绝")
			}
		}
	}
}
