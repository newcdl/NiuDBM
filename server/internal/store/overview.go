package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// BackupStat 是备份的汇总数字，概览页用一次查询拿全。
type BackupStat struct {
	Total    int   `json:"total"`
	Failed   int   `json:"failed"`
	LatestAt int64 `json:"latestAt"`
}

// BackupWithInstance 把备份记录和实例名拼在一起：概览页要显示"哪个库的备份"，
// 逐条回查实例名会在列表越长时越慢。
type BackupWithInstance struct {
	Backup
	InstanceName string `json:"instanceName"`
	Engine       string `json:"engine"`
}

// BackupStats 统计备份总数、失败数与最近一次时间；since 为 0 表示不限时间。
func (s *Store) BackupStats(ctx context.Context, since int64) (BackupStat, error) {
	var st BackupStat
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(started_at), 0)
		 FROM backup_records WHERE started_at >= ?`, since).Scan(&st.Total, &st.Failed, &st.LatestAt)
	if err != nil {
		return st, fmt.Errorf("统计备份失败：%w", err)
	}
	return st, nil
}

// ListRecentBackups 返回最近的备份（跨实例），按时间倒序。
func (s *Store) ListRecentBackups(ctx context.Context, limit int) ([]BackupWithInstance, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.id, b.instance_id, b.type, b.status, b.size_bytes, b.checksum, b.storage_path,
			b.note, b.started_at, b.finished_at, b.error_code, b.error_hint,
			COALESCE(i.name, ''), COALESCE(i.engine, '')
		 FROM backup_records b LEFT JOIN instances i ON i.id = b.instance_id
		 ORDER BY b.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("读取最近备份失败：%w", err)
	}
	defer rows.Close()

	var out []BackupWithInstance
	for rows.Next() {
		var b BackupWithInstance
		if err := rows.Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Note, &b.StartedAt, &b.FinishedAt, &b.ErrorCode, &b.ErrorHint,
			&b.InstanceName, &b.Engine); err != nil {
			return nil, err
		}
		b.FileName = filepath.Base(b.StoragePath)
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountAuditSince 统计某时间点之后的操作数（概览页的"今日操作"）。
func (s *Store) CountAuditSince(ctx context.Context, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE ts >= ?`, since).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// CountJobsSince 统计某时间点之后某状态的任务数。
func (s *Store) CountJobsSince(ctx context.Context, since int64, status string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status = ? AND created_at >= ?`, status, since).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (s *Store) BackupStatsForInstances(ctx context.Context, instanceIDs []string, since int64) (BackupStat, error) {
	var st BackupStat
	if len(instanceIDs) == 0 {
		return st, nil
	}
	args := make([]any, 0, len(instanceIDs)+1)
	args = append(args, since)
	for _, id := range instanceIDs {
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(MAX(started_at), 0) FROM backup_records
		WHERE started_at >= ? AND instance_id IN (%s)`, placeholders(len(instanceIDs)))
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&st.Total, &st.Failed, &st.LatestAt); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) ListRecentBackupsForInstances(ctx context.Context, instanceIDs []string, limit int) ([]BackupWithInstance, error) {
	if len(instanceIDs) == 0 {
		return []BackupWithInstance{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	args := make([]any, 0, len(instanceIDs)+1)
	for _, id := range instanceIDs {
		args = append(args, id)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT b.id, b.instance_id, b.type, b.status, b.size_bytes, b.checksum,
		b.storage_path, b.database_name, b.note, b.started_at, b.finished_at, b.error_code, b.error_hint,
		COALESCE(i.name, ''), COALESCE(i.engine, '')
		FROM backup_records b LEFT JOIN instances i ON i.id = b.instance_id
		WHERE b.instance_id IN (%s)
		ORDER BY b.started_at DESC LIMIT ?`, placeholders(len(instanceIDs)))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupWithInstance{}
	for rows.Next() {
		var b BackupWithInstance
		if err := rows.Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Database, &b.Note, &b.StartedAt, &b.FinishedAt,
			&b.ErrorCode, &b.ErrorHint, &b.InstanceName, &b.Engine); err != nil {
			return nil, err
		}
		b.FileName = filepath.Base(b.StoragePath)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) CountAuditSinceForInstances(ctx context.Context, since int64, instanceIDs []string) (int, error) {
	if len(instanceIDs) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(instanceIDs)+1)
	args = append(args, since)
	for _, id := range instanceIDs {
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM audit_logs
		WHERE ts >= ? AND target_kind = 'instance' AND target_id IN (%s)`, placeholders(len(instanceIDs)))
	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (s *Store) ListRecentAuditForInstances(ctx context.Context, instanceIDs []string, limit int) ([]Audit, error) {
	if len(instanceIDs) == 0 {
		return []Audit{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	args := make([]any, 0, len(instanceIDs)+1)
	for _, id := range instanceIDs {
		args = append(args, id)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT id, ts, user_id, username, action, target_kind, target_id, result, detail
		FROM audit_logs WHERE target_kind = 'instance' AND target_id IN (%s)
		ORDER BY id DESC LIMIT ?`, placeholders(len(instanceIDs)))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.Ts, &a.UserID, &a.Username, &a.Action,
			&a.TargetKind, &a.TargetID, &a.Result, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
