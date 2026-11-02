package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

// 调度器默认 30 秒扫一次：定时备份的最小粒度是分钟，30 秒足够准时，
// 又不会让 SQLite 台账被无意义地反复查询。
const defaultSchedulerEvery = 30 * time.Second

// systemIdentity 是调度器写审计/任务时用的身份。
var systemIdentity = identity{UserID: "system", Username: "system"}

// policyPatch 是 PUT 入参：指针用于区分"没传"与"传了零值"。
//
// 执行结果类字段（lastRunAt / lastResult / nextRunAt）与 id 是只读的，但前端
// 会把整条策略回传，所以必须接住：不接就成了"未知字段"，保存永远失败。
type policyPatch struct {
	Enabled        *bool   `json:"enabled"`
	Schedule       *string `json:"schedule"`
	Time           *string `json:"time"`
	Weekday        *int    `json:"weekday"`
	RetentionCount *int    `json:"retentionCount"`
	RetentionDays  *int    `json:"retentionDays"`
	TargetKind     *string `json:"targetKind"`
	TargetRef      *string `json:"targetRef"`
	Encrypt        *bool   `json:"encrypt"`
	ID             *string `json:"id"`
	InstanceID     *string `json:"instanceId"`
	LastRunAt      *int64  `json:"lastRunAt"`
	LastResult     *string `json:"lastResult"`
	NextRunAt      *int64  `json:"nextRunAt"`
}

// defaultPolicy 给没配过的实例一份可直接保存的默认值：
// 默认关闭，避免用户只是点开看一眼就被自动备份"惊喜"到。
func (s *Server) defaultPolicy(ctx context.Context, instanceID string) store.BackupPolicy {
	return store.BackupPolicy{
		ID:             instanceID,
		InstanceID:     instanceID,
		Enabled:        false,
		Schedule:       "daily",
		Time:           "02:00",
		Weekday:        0,
		RetentionCount: s.backupRetentionCount(ctx),
		RetentionDays:  30,
		TargetKind:     "share",
		TargetRef:      s.paths.Backups,
	}
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	p, err := s.st.GetPolicy(r.Context(), in.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeOK(w, r, s.defaultPolicy(r.Context(), in.ID))
			return
		}
		s.fail(w, r, errInternal("读取备份策略失败"))
		return
	}
	writeOK(w, r, p)
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var patch policyPatch
	if err := decodeJSON(r, &patch); err != nil {
		s.fail(w, r, err)
		return
	}

	p, err := s.st.GetPolicy(r.Context(), in.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, errInternal("读取备份策略失败"))
			return
		}
		p = s.defaultPolicy(r.Context(), in.ID)
	}
	wasEnabled := p.Enabled

	if patch.Schedule != nil {
		switch *patch.Schedule {
		case "daily", "weekly":
			p.Schedule = *patch.Schedule
		default:
			s.fail(w, r, errBadRequest("执行频率只能是 daily 或 weekly"))
			return
		}
	}
	if patch.Time != nil {
		if _, _, err := parseClock(*patch.Time); err != nil {
			s.fail(w, r, errBadRequest("执行时刻格式应为 HH:mm，例如 02:30"))
			return
		}
		p.Time = *patch.Time
	}
	if patch.Weekday != nil {
		if *patch.Weekday < 0 || *patch.Weekday > 6 {
			s.fail(w, r, errBadRequest("星期只能是 0–6（0 表示周日）"))
			return
		}
		p.Weekday = *patch.Weekday
	}
	if patch.RetentionCount != nil {
		if *patch.RetentionCount < 0 || *patch.RetentionCount > 100 {
			s.fail(w, r, errBadRequest("保留份数只能是 0–100（0 表示不按份数清理）"))
			return
		}
		p.RetentionCount = *patch.RetentionCount
	}
	if patch.RetentionDays != nil {
		if *patch.RetentionDays < 0 || *patch.RetentionDays > 3650 {
			s.fail(w, r, errBadRequest("保留天数只能是 0–3650（0 表示不按天数清理）"))
			return
		}
		p.RetentionDays = *patch.RetentionDays
	}
	if patch.TargetKind != nil {
		kind := strings.TrimSpace(*patch.TargetKind)
		if kind != "share" && kind != "local" {
			s.fail(w, r, errBadRequest("备份目标只能是 share（共享目录）或 local（应用私有目录）"))
			return
		}
		p.TargetKind = kind
	}
	if patch.TargetRef != nil {
		p.TargetRef = strings.TrimSpace(*patch.TargetRef)
	}
	if patch.Encrypt != nil {
		if *patch.Encrypt {
			// 加密要配套的密钥管理与解密流程，v1 不做半成品：明确拒绝比默默忽略好
			s.fail(w, r, errBadRequest("备份加密尚未支持，请先关闭该选项"))
			return
		}
		p.Encrypt = false
	}
	if patch.Enabled != nil {
		p.Enabled = *patch.Enabled
	}

	now := time.Now()
	if p.Enabled {
		next, err := nextBackupTime(p, now)
		if err != nil {
			s.fail(w, r, errBadRequest(err.Error()))
			return
		}
		p.NextRunAt = next.UnixMilli()
	} else {
		// 关闭后清掉下次执行时间，界面上不再显示"下次 02:00"
		p.NextRunAt = 0
	}

	if err := s.st.PutPolicy(r.Context(), p); err != nil {
		s.fail(w, r, errInternal("保存备份策略失败"))
		return
	}

	detail := fmt.Sprintf("enabled=%t，%s %s", p.Enabled, p.Schedule, p.Time)
	if p.Enabled && !wasEnabled {
		detail += "，下次执行 " + time.UnixMilli(p.NextRunAt).Format("2006-01-02 15:04")
	}
	s.audit(r, id, "backup.policy", "instance", in.ID, "ok", detail)
	writeOK(w, r, p)
}

// parseClock 解析 HH:mm；返回小时与分钟。
func parseClock(raw string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("格式应为 HH:mm")
	}
	hh, err := strconv.Atoi(parts[0])
	if err != nil || hh < 0 || hh > 23 {
		return 0, 0, errors.New("小时应为 0–23")
	}
	mm, err := strconv.Atoi(parts[1])
	if err != nil || mm < 0 || mm > 59 {
		return 0, 0, errors.New("分钟应为 0–59")
	}
	return hh, mm, nil
}

// nextBackupTime 计算下一次执行时间。
//
// 用本地时区而不是 UTC：用户填的是"凌晨两点"，指的是 NAS 上的两点。
func nextBackupTime(p store.BackupPolicy, now time.Time) (time.Time, error) {
	hh, mm, err := parseClock(p.Time)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if p.Schedule == "weekly" {
		delta := (p.Weekday - int(next.Weekday()) + 7) % 7
		next = next.AddDate(0, 0, delta)
		if !next.After(now) {
			next = next.AddDate(0, 0, 7)
		}
		return next, nil
	}
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}

// StartScheduler 启动后台巡检：到期备份、保留清理、实例掉线通知。
func (s *Server) StartScheduler(ctx context.Context) {
	if s.schedEvery <= 0 {
		s.schedEvery = defaultSchedulerEvery
	}
	go s.schedulerLoop(ctx)
}

func (s *Server) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(s.schedEvery)
	defer ticker.Stop()

	// 启动先扫一遍：进程停过一阵时，错过的备份要补上，不能等下一个整点
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Server) tick(ctx context.Context) {
	s.runDuePolicies(ctx)
	s.checkInstancesDown(ctx)
	s.cleanupOldJobsAndAudit(ctx)
}

// runDuePolicies 找出到期的策略并逐个执行。
func (s *Server) runDuePolicies(ctx context.Context) {
	policies, err := s.st.ListPolicies(ctx)
	if err != nil {
		log.Printf("定时备份：读取策略失败：%v", err)
		return
	}
	now := time.Now().UnixMilli()
	for _, p := range policies {
		if !p.Enabled || p.NextRunAt == 0 || p.NextRunAt > now {
			continue
		}
		// 同一实例上一次还没跑完就跳过：备份本就串行，重复触发只会排队堆积
		if _, busy := s.policyRunning.LoadOrStore(p.InstanceID, true); busy {
			continue
		}
		go func(p store.BackupPolicy) {
			defer s.policyRunning.Delete(p.InstanceID)
			s.runPolicy(ctx, p)
		}(p)
	}
}

// runPolicy 执行一次定时备份，并把结果写回策略与任务台账。
func (s *Server) runPolicy(ctx context.Context, p store.BackupPolicy) {
	in, err := s.st.GetInstance(ctx, p.InstanceID)
	if err != nil {
		// 实例没了，策略留着只会每晚报错一次
		_ = s.st.DeletePolicy(ctx, p.InstanceID)
		return
	}
	jobID := s.beginJob(systemIdentity, jobBackupSchedule, "instance", in.ID,
		jobParams(map[string]any{"schedule": p.Schedule, "time": p.Time}))

	unlock := s.lockInstance(in.ID)
	defer unlock()

	// 策略挂在服务上，而服务里可能有多个库：每个库各备一份，别让用户以为只备了某一个。
	var rec store.Backup
	var backupErr error
	var done []string
	spec, specErr := s.mysqlSpec(in)
	targets := []string{}
	if specErr == nil {
		if dbs, derr := spec.ListDatabases(); derr == nil {
			for _, d := range dbs {
				targets = append(targets, d.Name)
			}
		} else {
			backupErr = derr
		}
	} else {
		backupErr = specErr
	}
	if backupErr == nil && len(targets) == 0 {
		backupErr = errors.New("服务里还没有库，没有可备份的内容")
	}
	for _, db := range targets {
		if backupErr != nil {
			break
		}
		var r store.Backup
		r, backupErr = s.makeBackup(ctx, in, "scheduled", "定时备份", db)
		if backupErr != nil {
			break
		}
		rec = r
		done = append(done, r.Database)
	}
	result := "succeeded"
	if backupErr != nil {
		result = "failed"
		s.finishJob(jobID, store.JobFailed, "", backupErr.Error())
	} else {
		s.finishJob(jobID, store.JobSucceeded, fmt.Sprintf("%d 个库（%s）", len(done), strings.Join(done, "、")), "")
		s.pruneBackups(ctx, in.ID)
	}

	now := time.Now()
	next, nerr := nextBackupTime(p, now)
	var nextMs int64
	if nerr == nil {
		nextMs = next.UnixMilli()
	}
	if err := s.st.MarkPolicyRun(ctx, p.InstanceID, now.UnixMilli(), result, nextMs); err != nil {
		log.Printf("定时备份：回填策略失败：%v", err)
	}

	detail := fmt.Sprintf("定时备份成功 %d 个库：%s（最后一份 %s）", len(done), strings.Join(done, "、"), rec.FileName)
	if backupErr != nil {
		detail = "定时备份失败：" + backupErr.Error()
	}
	s.auditSystem("backup.scheduled", "instance", in.ID, result, detail)

	cfg := s.loadSettings(ctx)
	if (backupErr == nil && cfg.NotifyBackupSucceeded) || (backupErr != nil && cfg.NotifyBackupFailed) {
		ev := notifyEvent{
			Event:        "backup." + result,
			Title:        "备份" + map[bool]string{true: "成功", false: "失败"}[backupErr == nil],
			Message:      detail,
			InstanceID:   in.ID,
			InstanceName: in.Name,
		}
		if nerr := s.notify(ctx, cfg, ev); nerr != nil {
			log.Printf("通知推送失败：%v", nerr)
		}
	}
}

// pruneBackups 按保留策略清理某实例的备份文件。
//
// 先按份数留最新的，再按天数删过期的；两者同时配置时取并集，
// 这样"留 7 份"与"留 30 天"不会互相抵消。
func (s *Server) pruneBackups(ctx context.Context, instanceID string) {
	keep := s.backupRetentionCount(ctx)
	days := 0
	if p, err := s.st.GetPolicy(ctx, instanceID); err == nil {
		if p.RetentionCount > 0 {
			keep = p.RetentionCount
		}
		days = p.RetentionDays
	}
	if keep <= 0 && days <= 0 {
		return
	}

	list, err := s.st.ListBackups(ctx, instanceID)
	if err != nil {
		return
	}
	deadline := time.Now().AddDate(0, 0, -days)
	for i, b := range list {
		if b.Status == "running" {
			continue
		}
		expiredByCount := keep > 0 && i >= keep
		expiredByDays := days > 0 && b.StartedAt > 0 && time.UnixMilli(b.StartedAt).Before(deadline)
		if !expiredByCount && !expiredByDays {
			continue
		}
		if err := os.Remove(b.StoragePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("清理备份文件失败 %s：%v", b.FileName, err)
			continue
		}
		if err := s.st.DeleteBackup(ctx, b.ID); err != nil {
			log.Printf("删除备份记录失败 %s：%v", b.ID, err)
		}
	}
}

// checkInstancesDown 对"本该在跑却没跑"的实例推一次通知。
//
// 只推一次：每次巡检都推会把用户淹没，恢复运行后清掉标记才能再次推送。
func (s *Server) checkInstancesDown(ctx context.Context) {
	cfg := s.loadSettings(ctx)
	if cfg.NotifyWebhook == "" || !cfg.NotifyInstanceDown {
		return
	}
	list, err := s.st.ListInstances(ctx, "", "running")
	if err != nil {
		return
	}
	for _, in := range list {
		if in.Engine != mysqlctl.Engine {
			continue
		}
		spec, rerr := s.mysqlSpec(in)
		if rerr != nil {
			continue
		}
		if spec.Running() {
			s.downNotified.Delete(in.ID)
			continue
		}
		if _, seen := s.downNotified.LoadOrStore(in.ID, true); seen {
			continue
		}
		ev := notifyEvent{
			Event:        "instance.down",
			Title:        "实例未在运行",
			Message:      fmt.Sprintf("%s（%s）应当运行，但进程/容器不在", in.Name, in.Engine),
			InstanceID:   in.ID,
			InstanceName: in.Name,
		}
		if nerr := s.notify(ctx, cfg, ev); nerr != nil {
			log.Printf("实例异常通知推送失败：%v", nerr)
		}
	}
}

// cleanupOldJobsAndAudit 每 6 小时最多清一次历史，避免台账无限增长。
func (s *Server) cleanupOldJobsAndAudit(ctx context.Context) {
	now := time.Now()
	s.cleanupMu.Lock()
	if now.Sub(s.lastCleanup) < 6*time.Hour {
		s.cleanupMu.Unlock()
		return
	}
	s.lastCleanup = now
	s.cleanupMu.Unlock()

	// 任务与审计的保留期都跟审计设置走：用户理解的"保留 N 天"包含这两类记录
	days := s.auditRetentionDays(ctx)
	if days <= 0 {
		return
	}
	before := now.AddDate(0, 0, -days).UnixMilli()
	if n, err := s.st.DeleteAuditBefore(ctx, before); err != nil {
		log.Printf("清理审计失败：%v", err)
	} else if n > 0 {
		log.Printf("已清理 %d 条过期审计（保留 %d 天）", n, days)
	}
	if n, err := s.st.DeleteJobsBefore(ctx, before); err != nil {
		log.Printf("清理任务记录失败：%v", err)
	} else if n > 0 {
		log.Printf("已清理 %d 条过期任务（保留 %d 天）", n, days)
	}
}

// auditSystem 是调度器写审计的入口：没有 HTTP 请求，身份固定为 system。
func (s *Server) auditSystem(action, kind, target, result, detail string) {
	_ = s.st.AddAudit(context.Background(), store.Audit{
		Ts:         time.Now().UnixMilli(),
		UserID:     systemIdentity.UserID,
		Username:   systemIdentity.Username,
		Action:     action,
		TargetKind: kind,
		TargetID:   target,
		Result:     result,
		Detail:     detail,
	})
}
