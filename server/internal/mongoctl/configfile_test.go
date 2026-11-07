package mongoctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMongoConfigTemplate(t *testing.T) {
	for _, version := range []string{"6.0", "7.0", "8.0"} {
		content := ConfigTemplate(version)
		if err := ValidateConfig(content); err != nil {
			t.Fatalf("%s 模板不合法：%v\n%s", version, err, content)
		}
		if !strings.Contains(content, "dbPath: /data/db") || !strings.Contains(content, "authorization: enabled") {
			t.Fatalf("%s 模板缺少关键配置：%s", version, content)
		}
	}
}

func TestValidateMongoConfigRejectsUnsafeChanges(t *testing.T) {
	cases := []string{
		"storage:\n  dbPath: /tmp/db\nnet:\n  port: 27017\nsecurity:\n  authorization: enabled\n",
		"storage:\n  dbPath: /data/db\nnet:\n  port: 27018\nsecurity:\n  authorization: enabled\n",
		"storage:\n  dbPath: /data/db\nnet:\n  port: 27017\nsecurity:\n  authorization: disabled\n",
		"storage: [",
	}
	for _, content := range cases {
		if err := ValidateConfig(content); err == nil {
			t.Fatalf("危险配置应被拒绝：\n%s", content)
		}
	}
}

func TestEnsureMongoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", ConfigFileName)
	if err := EnsureConfig(path, "8.0"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(string(raw)); err != nil {
		t.Fatalf("落盘配置不合法：%v", err)
	}
}
