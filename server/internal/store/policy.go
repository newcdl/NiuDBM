package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// BackupPolicy 是某一实例的定时备份策略；一个实例最多一条。
//
// 策略与备份记录分表存放的原因：策略是"意图"，备份是"结果"，
// 混在一起后每次执行都要写同一行，读列表时还要滤掉自己的元数据。
type BackupPolicy struct {
	// ID 与 InstanceID 相同：前端契约里有 id 字段，实例维度一一对应最省事
	ID             string `json:"id"`
	InstanceID     string `json:"instanceId"`
	Enabled        bool   `json:"enabled"`
	Schedule       string `json:"schedule"` // daily | weekly
	Time           string `json:"time"`     // HH:mm
	Weekday        int    `json:"weekday"`  // 0=周日，仅 weekly 有意义
	RetentionCount int    `json:"retentionCount"`
	RetentionDays  int    `json:"retentionDays"`
	TargetKind     string `json:"targetKind"`
	TargetRef      string `json:"targetRef"`
	Encrypt        bool   `json:"encrypt"`
	LastRunAt      int64  `json:"lastRunAt,omitempty"`
	LastResult     string `json:"lastResult,omitempty"`
	NextRunAt      int64  `json:"nextRunAt,omitempty"`
	UpdatedAt      int64  `json:"-"`
}

// Empty 判断这条策略是不是"没配过"，用于 GET 时给前端一份默认值。
func (p BackupPolicy) Empty() bool { return p.InstanceID == "" }

// GetPolicy 读实例的策略；没配过返回 ErrNotFound。
func (s *Store) GetPolicy(ctx context.Context, instanceID string) (BackupPolicy, error) {
	var p BackupPolicy
	var enabled, encrypt int
	err := s.db.QueryRowContext(ctx,
		`SELECT instance_id, enabled, schedule, time, weekday, retention_count, retention_days,
			target_kind, target_ref, encrypt, last_run_at, last_result, next_run_at, updated_at
		 FROM backup_policies WHERE instance_id = ?`, instanceID).
		Scan(&p.InstanceID, &enabled, &p.Schedule, &p.Time, &p.Weekday, &p.RetentionCount,
			&p.RetentionDays, &p.TargetKind, &p.TargetRef, &encrypt, &p.LastRunAt,
			&p.LastResult, &p.NextRunAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupPolicy{}, ErrNotFound
	}
	if err != nil {
		return BackupPolicy{}, fmt.Errorf("读取备份策略失败：%w", err)
	}
	p.ID = p.InstanceID
	p.Enabled = enabled == 1
	p.Encrypt = encrypt == 1
	return p, nil
}

// PutPolicy 写入或覆盖一条策略（按实例维度 upsert）。
func (s *Store) PutPolicy(ctx context.Context, p BackupPolicy) error {
	if p.UpdatedAt == 0 {
		p.UpdatedAt = nowMs()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_policies (instance_id, enabled, schedule, time, weekday, retention_count,
			retention_days, target_kind, target_ref, encrypt, last_run_at, last_result, next_run_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(instance_id) DO UPDATE SET
			enabled = excluded.enabled, schedule = excluded.schedule, time = excluded.time,
			weekday = excluded.weekday, retention_count = excluded.retention_count,
			retention_days = excluded.retention_days, target_kind = excluded.target_kind,
			target_ref = excluded.target_ref, encrypt = excluded.encrypt,
			next_run_at = excluded.next_run_at, updated_at = excluded.updated_at`,
		p.InstanceID, boolInt(p.Enabled), p.Schedule, p.Time, p.Weekday, p.RetentionCount,
		p.RetentionDays, p.TargetKind, p.TargetRef, boolInt(p.Encrypt),
		p.LastRunAt, p.LastResult, p.NextRunAt, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("保存备份策略失败：%w", err)
	}
	return nil
}

// ListPolicies 返回全部策略（含未启用的，调用方自己筛）。
func (s *Store) ListPolicies(ctx context.Context) ([]BackupPolicy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT instance_id, enabled, schedule, time, weekday, retention_count, retention_days,
			target_kind, target_ref, encrypt, last_run_at, last_result, next_run_at, updated_at
		 FROM backup_policies ORDER BY instance_id`)
	if err != nil {
		return nil, fmt.Errorf("读取备份策略失败：%w", err)
	}
	defer rows.Close()

	var out []BackupPolicy
	for rows.Next() {
		var p BackupPolicy
		var enabled, encrypt int
		if err := rows.Scan(&p.InstanceID, &enabled, &p.Schedule, &p.Time, &p.Weekday,
			&p.RetentionCount, &p.RetentionDays, &p.TargetKind, &p.TargetRef, &encrypt,
			&p.LastRunAt, &p.LastResult, &p.NextRunAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.ID = p.InstanceID
		p.Enabled = enabled == 1
		p.Encrypt = encrypt == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPolicyRun 回填一次执行结果与下次触发时间。
func (s *Store) MarkPolicyRun(ctx context.Context, instanceID string, lastRunAt int64, result string, nextRunAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE backup_policies SET last_run_at = ?, last_result = ?, next_run_at = ?, updated_at = ?
		 WHERE instance_id = ?`,
		lastRunAt, result, nextRunAt, nowMs(), instanceID)
	if err != nil {
		return fmt.Errorf("更新备份策略失败：%w", err)
	}
	return nil
}

// DeletePolicy 删除策略（实例被删除时调用）。
func (s *Store) DeletePolicy(ctx context.Context, instanceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backup_policies WHERE instance_id = ?`, instanceID)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
