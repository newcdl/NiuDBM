package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"niudbm/server/internal/store"
)

func TestMongoConfigFileAPI(t *testing.T) {
	e := newTestEnv(t, false)
	id := "01MONGOOCONFIG000000000000"
	in := store.Instance{
		ID: id, Name: "mongo-config", Engine: engineMongoDB, EngineVersion: "8.0",
		Mode: instanceModeManaged, Status: "running", DataDir: id + "/mongodb",
		Profile: "standard", Port: 27017, Runtime: runtimeDocker,
		StorageKind: storageVolume, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(),
	}
	if err := e.st.CreateInstance(t.Context(), in); err != nil {
		t.Fatal(err)
	}

	resp := e.must(t, 200, "GET", "/api/v1/instances/"+id+"/config/file", nil)
	var file struct {
		Content  string           `json:"content"`
		Selected string           `json:"selected"`
		Files    []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(resp.Data, &file); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "dbPath: /data/db") ||
		!strings.Contains(file.Content, "authorization: enabled") {
		t.Fatalf("MongoDB 默认配置不正确：%s", file.Content)
	}
	if file.Selected != "mongod.conf" || len(file.Files) != 1 {
		t.Fatalf("MongoDB 文件清单不正确：%+v", file)
	}

	resp = e.must(t, 200, "PUT", "/api/v1/instances/"+id+"/config/file", map[string]any{
		"content": "storage:\n  dbPath: /data/db\nnet:\n  bindIp: 0.0.0.0\n  port: 27017\nsecurity:\n  authorization: enabled\n",
	})
	var result struct {
		NeedRestart bool `json:"needRestart"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.NeedRestart {
		t.Fatal("修改 mongod.conf 后必须提示重启")
	}

	code, resp := e.call(t, "PUT", "/api/v1/instances/"+id+"/config/file", nil, map[string]any{
		"content": "storage:\n  dbPath: /tmp/db\nnet:\n  port: 27017\nsecurity:\n  authorization: enabled\n",
	})
	if code != 400 || !strings.Contains(resp.Message, "dbPath") {
		t.Fatalf("危险 dbPath 应被拒绝，实际 %d：%s", code, resp.Message)
	}
}
