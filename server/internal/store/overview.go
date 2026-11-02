package store

import (
	"context"
	"fmt"
	"path/filepath"
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
			COALESCE(i.name, '')
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
			&b.InstanceName); err != nil {
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
