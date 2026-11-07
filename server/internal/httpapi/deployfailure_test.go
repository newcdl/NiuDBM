package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"niudbm/server/internal/store"
)

// 失败即保留现场：之前的实现是失败就删容器、删目录，用户只剩一句"没就绪"，
// docker logs 也查不到。这条测试守住三件事：台账标 failed、原因里有日志路径、
// 日志文件真的落盘。
func TestMarkDeployFailedKeepsLogAndStatus(t *testing.T) {
	e := newTestEnv(t, false)
	ctx := context.Background()

	in := store.Instance{
		ID:            "01FAILTEST0000000000000000",
		Name:          "失败现场",
		Engine:        "mysql",
		EngineVersion: "8.4",
		Mode:          "managed",
		Status:        "creating",
		DataDir:       "01FAILTEST0000000000000000/mysql",
		Profile:       "standard",
		Port:          3306,
		Runtime:       "docker",
		StorageKind:   "volume",
	}
	if err := e.st.CreateInstance(ctx, in); err != nil {
		t.Fatalf("写入台账失败：%v", err)
	}

	e.handler.markDeployFailed(ctx, in, "start", "5m0s 内实例没就绪")

	got, err := e.st.GetInstance(ctx, in.ID)
	if err != nil {
		t.Fatalf("读台账失败：%v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("部署失败后状态应为 failed，实际 %q", got.Status)
	}
	if !strings.Contains(got.StatusReason, "deploy.log") {
		t.Fatalf("失败原因里要给出日志位置，实际 %q", got.StatusReason)
	}
	if !strings.Contains(got.StatusReason, "没就绪") {
		t.Fatalf("失败原因丢了根因：%q", got.StatusReason)
	}

	raw, err := os.ReadFile(e.handler.deployLogPath(in))
	if err != nil {
		t.Fatalf("失败现场没有落盘日志：%v", err)
	}
	log := string(raw)
	if !strings.Contains(log, "阶段 start 失败") || !strings.Contains(log, "没就绪") {
		t.Fatalf("日志内容不对：%s", log)
	}

	// 列表这一层不能再把 failed 翻成 error/stopped：否则用户分不清
	// "部署没成功"和"跑起来之后挂了"，也看不到失败原因。
	if st := e.handler.liveStatus(got); st != "failed" {
		t.Fatalf("liveStatus 不该改写 failed，实际 %q", st)
	}

	// 接口要能把日志交给界面
	code, resp := e.call(t, "GET", "/api/v1/instances/"+in.ID+"/deploy-log", nil, nil)
	if code != 200 || resp.Code != 0 {
		t.Fatalf("读部署日志应成功，实际 HTTP %d / code %d：%s", code, resp.Code, resp.Message)
	}
	var body struct {
		Available bool   `json:"available"`
		Path      string `json:"path"`
		Log       string `json:"log"`
	}
	if err := json.Unmarshal(resp.Data, &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if !body.Available || !strings.Contains(body.Log, "没就绪") {
		t.Fatalf("接口没返回失败日志：%+v", body)
	}
}

// 没有部署日志的实例（正常建成的）要明确说没有，而不是报错。
func TestDeployLogWithoutFailure(t *testing.T) {
	e := newTestEnv(t, false)
	ctx := context.Background()

	in := store.Instance{
		ID:            "01OKTEST000000000000000000",
		Name:          "正常实例",
		Engine:        "mysql",
		EngineVersion: "8.4",
		Mode:          "managed",
		Status:        "running",
		DataDir:       "01OKTEST000000000000000000/mysql",
		Profile:       "standard",
		Runtime:       runtimeDocker,
		StorageKind:   "volume",
	}
	if err := e.st.CreateInstance(ctx, in); err != nil {
		t.Fatalf("写入台账失败：%v", err)
	}

	code, resp := e.call(t, "GET", "/api/v1/instances/"+in.ID+"/deploy-log", nil, nil)
	if code != 200 || resp.Code != 0 {
		t.Fatalf("没有日志也应正常返回，实际 HTTP %d / code %d", code, resp.Code)
	}
	var body struct {
		Available bool   `json:"available"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(resp.Data, &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if body.Available || body.Message == "" {
		t.Fatalf("没有日志时要 available=false 并说明原因：%+v", body)
	}
}
