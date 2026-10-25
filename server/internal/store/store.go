// Package store 管理元数据库（var/niudbm.db）：实例台账、备份记录、审计。
//
// 约定：元数据库是唯一真相，实例文件只是"被管理的对象"；
// 所有写操作都要能被审计，因此这里不提供"静默更新"。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound 表示台账里没有这条记录。
var ErrNotFound = errors.New("记录不存在")

// ErrNameTaken 表示实例名已被占用。
var ErrNameTaken = errors.New("实例名已存在")

// Instance 是 instances 表的一行。
type Instance struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engineVersion"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	StatusReason  string `json:"statusReason,omitempty"`
	DataDir       string `json:"dataDir"`
	Profile       string `json:"profile"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// Backup 是 backup_records 表的一行。
type Backup struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instanceId"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"sizeBytes"`
	Checksum    string `json:"checksum"`
	StoragePath string `json:"-"`
	FileName    string `json:"fileName"`
	Note        string `json:"note,omitempty"`
	StartedAt   int64  `json:"startedAt"`
	FinishedAt  int64  `json:"finishedAt"`
	ErrorCode   string `json:"errorCode,omitempty"`
	ErrorHint   string `json:"errorHint,omitempty"`
}

// Audit 是 audit_logs 表的一行。
type Audit struct {
	ID         int64  `json:"id"`
	Ts         int64  `json:"ts"`
	UserID     string `json:"userId"`
	Username   string `json:"username"`
	Action     string `json:"action"`
	TargetKind string `json:"targetKind,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	Result     string `json:"result"`
	Detail     string `json:"detail,omitempty"`
}

// Store 持有元数据库连接。
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建）元数据库并执行迁移。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?mode=rwc&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭连接。
func (s *Store) Close() error { return s.db.Close() }

// Path 返回元数据库路径，自检与日志会用到。
func (s *Store) Path() string { return s.path }

func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS instances (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			engine TEXT NOT NULL,
			engine_version TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'managed',
			status TEXT NOT NULL DEFAULT 'running',
			status_reason TEXT NOT NULL DEFAULT '',
			data_dir TEXT NOT NULL,
			profile TEXT NOT NULL DEFAULT 'low',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			deleted_at INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_status ON instances(status)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_engine ON instances(engine)`,
		`CREATE TABLE IF NOT EXISTS backup_records (
			id TEXT PRIMARY KEY,
			instance_id TEXT NOT NULL,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			checksum TEXT NOT NULL DEFAULT '',
			storage_path TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL,
			finished_at INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT '',
			error_hint TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backups_instance ON backup_records(instance_id, started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target_kind TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_logs(target_id)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("迁移失败：%w", err)
		}
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&applied); err != nil {
		return err
	}
	if applied == 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?)`, nowMs())
		return err
	}
	return nil
}

// ---------- 实例 ----------

// CreateInstance 插入一条实例台账。
func (s *Store) CreateInstance(ctx context.Context, in Instance) error {
	now := nowMs()
	if in.CreatedAt == 0 {
		in.CreatedAt = now
	}
	if in.UpdatedAt == 0 {
		in.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO instances
		(id, name, engine, engine_version, mode, status, status_reason, data_dir, profile, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Name, in.Engine, in.EngineVersion, in.Mode, in.Status, in.StatusReason,
		in.DataDir, in.Profile, in.CreatedAt, in.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: instances.name") {
		return ErrNameTaken
	}
	return err
}

// ListInstances 返回未删除的实例；q 对名称做模糊匹配。
func (s *Store) ListInstances(ctx context.Context, q, status string) ([]Instance, error) {
	sqlStr := `SELECT id, name, engine, engine_version, mode, status, status_reason, data_dir, profile, created_at, updated_at
		FROM instances WHERE deleted_at IS NULL`
	args := []any{}
	if q != "" {
		sqlStr += ` AND name LIKE ?`
		args = append(args, "%"+q+"%")
	}
	if status != "" {
		sqlStr += ` AND status = ?`
		args = append(args, status)
	}
	sqlStr += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Instance{}
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.Name, &in.Engine, &in.EngineVersion, &in.Mode, &in.Status,
			&in.StatusReason, &in.DataDir, &in.Profile, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// GetInstance 按 id 取实例。
func (s *Store) GetInstance(ctx context.Context, id string) (Instance, error) {
	var in Instance
	err := s.db.QueryRowContext(ctx, `SELECT id, name, engine, engine_version, mode, status, status_reason,
			data_dir, profile, created_at, updated_at
		FROM instances WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&in.ID, &in.Name, &in.Engine, &in.EngineVersion, &in.Mode, &in.Status,
			&in.StatusReason, &in.DataDir, &in.Profile, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, ErrNotFound
	}
	return in, err
}

// RenameInstance 改名，同时更新 updated_at。
func (s *Store) RenameInstance(ctx context.Context, id, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE instances SET name = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		name, nowMs(), id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrNameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus 由服务端根据真实观测结果回填状态。
func (s *Store) SetStatus(ctx context.Context, id, status, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET status = ?, status_reason = ?, updated_at = ? WHERE id = ?`,
		status, reason, nowMs(), id)
	return err
}

// SoftDelete 标记删除。名字会被改写，让同名实例可以重新创建。
func (s *Store) SoftDelete(ctx context.Context, in Instance) error {
	renamed := in.Name + "#deleted-" + fmt.Sprint(nowMs())
	_, err := s.db.ExecContext(ctx,
		`UPDATE instances SET deleted_at = ?, status = 'deleted', name = ?, updated_at = ? WHERE id = ?`,
		nowMs(), renamed, nowMs(), in.ID)
	return err
}

// CountInstances 返回未删除实例数。
func (s *Store) CountInstances(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}

// ---------- 备份 ----------

// CreateBackup 先落一条 running 记录，失败也能在列表里看到原因。
func (s *Store) CreateBackup(ctx context.Context, b Backup) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO backup_records
		(id, instance_id, type, status, size_bytes, checksum, storage_path, note, started_at, finished_at, error_code, error_hint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.InstanceID, b.Type, b.Status, b.SizeBytes, b.Checksum, b.StoragePath, b.Note,
		b.StartedAt, b.FinishedAt, b.ErrorCode, b.ErrorHint)
	return err
}

// FinishBackup 回填备份结果。
func (s *Store) FinishBackup(ctx context.Context, b Backup) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_records
		SET status = ?, size_bytes = ?, checksum = ?, finished_at = ?, error_code = ?, error_hint = ?
		WHERE id = ?`,
		b.Status, b.SizeBytes, b.Checksum, b.FinishedAt, b.ErrorCode, b.ErrorHint, b.ID)
	return err
}

// ListBackups 按实例列出备份，新的在前。
func (s *Store) ListBackups(ctx context.Context, instanceID string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, instance_id, type, status, size_bytes, checksum,
			storage_path, note, started_at, finished_at, error_code, error_hint
		FROM backup_records WHERE instance_id = ? ORDER BY started_at DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Backup{}
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Note, &b.StartedAt, &b.FinishedAt, &b.ErrorCode, &b.ErrorHint); err != nil {
			return nil, err
		}
		b.FileName = filepath.Base(b.StoragePath)
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBackup 取一条备份记录（需同时匹配实例，防跨实例访问）。
func (s *Store) GetBackup(ctx context.Context, instanceID, id string) (Backup, error) {
	var b Backup
	err := s.db.QueryRowContext(ctx, `SELECT id, instance_id, type, status, size_bytes, checksum,
			storage_path, note, started_at, finished_at, error_code, error_hint
		FROM backup_records WHERE id = ? AND instance_id = ?`, id, instanceID).
		Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Note, &b.StartedAt, &b.FinishedAt, &b.ErrorCode, &b.ErrorHint)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	b.FileName = filepath.Base(b.StoragePath)
	return b, err
}

// DeleteBackup 删除备份记录（文件由调用方处理）。
func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backup_records WHERE id = ?`, id)
	return err
}

// ---------- 审计 ----------

// AddAudit 追加一条审计。detail 里禁止出现密码等敏感内容。
func (s *Store) AddAudit(ctx context.Context, a Audit) error {
	if a.Ts == 0 {
		a.Ts = nowMs()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs
		(ts, user_id, username, action, target_kind, target_id, result, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Ts, a.UserID, a.Username, a.Action, a.TargetKind, a.TargetID, a.Result, a.Detail)
	return err
}

// ListAudit 返回最近的审计记录。
func (s *Store) ListAudit(ctx context.Context, limit int) ([]Audit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, user_id, username, action, target_kind, target_id, result, detail
		FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.Ts, &a.UserID, &a.Username, &a.Action, &a.TargetKind, &a.TargetID, &a.Result, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func nowMs() int64 { return time.Now().UnixMilli() }
