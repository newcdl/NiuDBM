package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"niudbm/server/internal/store"
)

// decodeData 把统一响应体里的 data 解成具体类型，避免每个用例都手写 Unmarshal。
func decodeData[T any](t *testing.T, resp apiResp) T {
	t.Helper()
	var out T
	if len(resp.Data) == 0 {
		t.Fatalf("响应没有 data：code=%d message=%s", resp.Code, resp.Message)
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("解析 data 失败：%v（%s）", err, resp.Data)
	}
	return out
}

// TestOverviewAggregates 概览页一次请求要拿到实例、镜像、备份、任务、审计的汇总。
func TestOverviewAggregates(t *testing.T) {
	env := newTestEnv(t, false)
	in := env.seedInstance(t, "概览库", "running")

	out := decodeData[overviewResult](t, env.must(t, 200, "GET", "/api/v1/overview", nil))
	if out.Instances.Total < 1 {
		t.Fatalf("实例总数应≥1，实际 %+v", out.Instances)
	}
	if len(out.Instances.Items) == 0 || out.Instances.Items[0].ID != in.ID {
		t.Fatalf("最近创建的实例应排在最前：%+v", out.Instances.Items)
	}
	if len(out.Images) == 0 {
		t.Fatal("概览应带可部署镜像清单")
	}
}

// TestSettingsDefaultsAndPatch 守住设置的默认值与校验：
// 默认值写错会让"保留 7 份"变成"永不清理"，Webhook 不校验则会推出 20 条失败日志。
func TestSettingsDefaultsAndPatch(t *testing.T) {
	env := newTestEnv(t, false)

	got := decodeData[appSettings](t, env.must(t, 200, "GET", "/api/v1/settings", nil))
	if got.BackupRetentionCount != defaultBackupRetention || got.AuditRetentionDays != defaultAuditRetention {
		t.Fatalf("默认保留策略不对：%+v", got)
	}
	if got.BackupDir != env.paths.Backups {
		t.Fatalf("备份目录应来自服务端路径，实际 %q", got.BackupDir)
	}

	env.must(t, 400, "PATCH", "/api/v1/settings", map[string]any{"notifyWebhook": "ftp://example.com/x"})
	env.must(t, 400, "PATCH", "/api/v1/settings", map[string]any{"backupRetentionCount": 999})
	env.must(t, 400, "PATCH", "/api/v1/settings", map[string]any{"auditRetentionDays": -1})

	env.must(t, 200, "PATCH", "/api/v1/settings", map[string]any{
		"backupRetentionCount":  3,
		"auditRetentionDays":    30,
		"notifyWebhook":         "http://127.0.0.1:1/hook",
		"notifyBackupSucceeded": true,
	})
	after := decodeData[appSettings](t, env.must(t, 200, "GET", "/api/v1/settings", nil))
	if after.BackupRetentionCount != 3 || after.AuditRetentionDays != 30 || !after.NotifyBackupSucceeded {
		t.Fatalf("设置没有持久化：%+v", after)
	}

	// 打不通的地址要如实报错，而不是假装"已发送"
	env.must(t, 400, "POST", "/api/v1/settings/notify/test", nil)

	env.must(t, 200, "PATCH", "/api/v1/settings", map[string]any{"notifyWebhook": ""})
	env.must(t, 400, "POST", "/api/v1/settings/notify/test", nil)
}

// TestSettingsPatchAcceptsWholeObject 守住"界面把整份设置回传"这条真实路径。
//
// 解析开了 DisallowUnknownFields，只读字段（version/backupDir）不接住就会 400，
// 用户看到的是"保存失败"，而接口日志里只有一句"请求体不是合法 JSON"。
func TestSettingsPatchAcceptsWholeObject(t *testing.T) {
	env := newTestEnv(t, false)
	full := decodeData[appSettings](t, env.must(t, 200, "GET", "/api/v1/settings", nil))
	full.BackupRetentionCount = 9
	full.AuditRetentionDays = 45

	saved := decodeData[appSettings](t, env.must(t, 200, "PATCH", "/api/v1/settings", full))
	if saved.BackupRetentionCount != 9 || saved.AuditRetentionDays != 45 {
		t.Fatalf("整份回传时应按提交值保存：%+v", saved)
	}
}

// TestJobsListAndFilters 任务中心在没有任何操作时也要正常返回，
// 并且状态过滤不会把空列表变成错误。
func TestJobsListAndFilters(t *testing.T) {
	env := newTestEnv(t, false)

	list := decodeData[struct {
		Items  []store.Job `json:"items"`
		Total  int         `json:"total"`
		Active int         `json:"active"`
	}](t, env.must(t, 200, "GET", "/api/v1/jobs", nil))
	if list.Total != 0 || list.Active != 0 || len(list.Items) != 0 {
		t.Fatalf("刚启动时不该有任务：%+v", list)
	}

	failed := decodeData[struct {
		Items []store.Job `json:"items"`
	}](t, env.must(t, 200, "GET", "/api/v1/jobs?status=failed", nil))
	if len(failed.Items) != 0 {
		t.Fatalf("不该有失败任务：%+v", failed.Items)
	}

	// 不存在的任务号：查询与取消都要 404
	env.must(t, 404, "GET", "/api/v1/jobs/NOPE", nil)
	env.must(t, 404, "POST", "/api/v1/jobs/NOPE/cancel", nil)
}

// TestPolicyDefaultsAndValidation 覆盖策略的默认值、校验与下次执行时间。
func TestPolicyDefaultsAndValidation(t *testing.T) {
	env := newTestEnv(t, false)
	in := env.seedInstance(t, "策略库", "running")

	def := decodeData[store.BackupPolicy](t, env.must(t, 200, "GET", "/api/v1/instances/"+in.ID+"/backup-policy", nil))
	if def.Enabled {
		t.Fatal("没配过的策略默认必须是关闭的，不能被自动备份惊喜到")
	}
	if def.RetentionCount != defaultBackupRetention || def.TargetRef != env.paths.Backups {
		t.Fatalf("默认策略不对：%+v", def)
	}

	env.must(t, 400, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", map[string]any{"time": "25:00"})
	env.must(t, 400, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", map[string]any{"schedule": "hourly"})
	env.must(t, 400, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", map[string]any{"weekday": 9})
	// 加密还没做，明确拒绝比默默忽略好
	env.must(t, 400, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", map[string]any{"encrypt": true})

	saved := decodeData[store.BackupPolicy](t, env.must(t, 200, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy",
		map[string]any{"enabled": true, "schedule": "weekly", "time": "03:30", "weekday": 1,
			"retentionCount": 2, "retentionDays": 10}))
	if !saved.Enabled || saved.Schedule != "weekly" || saved.Weekday != 1 {
		t.Fatalf("策略没保存：%+v", saved)
	}
	if saved.NextRunAt <= time.Now().UnixMilli() {
		t.Fatalf("启用后应算出下次执行时间：%+v", saved)
	}

	off := decodeData[store.BackupPolicy](t, env.must(t, 200, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy",
		map[string]any{"enabled": false}))
	if off.NextRunAt != 0 {
		t.Fatalf("关闭后应清掉下次执行时间：%+v", off)
	}
}

// TestPolicyPutAcceptsWholeObject 守住"策略面板整条回传"：
// id / lastRunAt / lastResult / nextRunAt 是只读字段，但前端会连它们一起发。
func TestPolicyPutAcceptsWholeObject(t *testing.T) {
	env := newTestEnv(t, false)
	in := env.seedInstance(t, "整条回传", "running")

	env.must(t, 200, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", map[string]any{
		"enabled": true, "schedule": "daily", "time": "04:00", "retentionCount": 4,
	})
	current := decodeData[store.BackupPolicy](t, env.must(t, 200, "GET", "/api/v1/instances/"+in.ID+"/backup-policy", nil))

	current.RetentionCount = 6
	saved := decodeData[store.BackupPolicy](t, env.must(t, 200, "PUT", "/api/v1/instances/"+in.ID+"/backup-policy", current))
	if saved.RetentionCount != 6 {
		t.Fatalf("整条回传时应按提交值保存：%+v", saved)
	}
}

// TestNextBackupTime 守住"下一次几点"的算法：算错会导致备份推迟一天或立刻重跑。
func TestNextBackupTime(t *testing.T) {
	loc := time.Local
	mk := func(schedule, clock string, weekday int) store.BackupPolicy {
		return store.BackupPolicy{Schedule: schedule, Time: clock, Weekday: weekday}
	}

	// 当天还没到点 → 就是今天
	now := time.Date(2026, 10, 28, 1, 0, 0, 0, loc)
	next, err := nextBackupTime(mk("daily", "02:00", 0), now)
	if err != nil || !next.Equal(time.Date(2026, 10, 28, 2, 0, 0, 0, loc)) {
		t.Fatalf("daily 当天应排到今天 02:00，实际 %v（%v）", next, err)
	}

	// 已经过点 → 顺延到明天
	now = time.Date(2026, 10, 28, 3, 0, 0, 0, loc)
	next, _ = nextBackupTime(mk("daily", "02:00", 0), now)
	if !next.Equal(time.Date(2026, 10, 29, 2, 0, 0, 0, loc)) {
		t.Fatalf("daily 过点应顺延到明天，实际 %v", next)
	}

	// 周三排"周一 02:00" → 下周一
	now = time.Date(2026, 10, 28, 3, 0, 0, 0, loc)
	next, _ = nextBackupTime(mk("weekly", "02:00", 1), now)
	if next.Weekday() != time.Monday || !next.After(now) {
		t.Fatalf("weekly 应落到下周一，实际 %v", next)
	}
	if next.Sub(now) > 7*24*time.Hour {
		t.Fatalf("weekly 不该超过一周，实际 %v", next.Sub(now))
	}

	if _, _, err := parseClock("2点"); err == nil {
		t.Fatal("非法时刻应被拒绝")
	}
	if _, _, err := parseClock("12:60"); err == nil {
		t.Fatal("分钟越界应被拒绝")
	}
}
