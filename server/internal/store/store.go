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
	// Mode 区分本应用托管的容器服务与用户已有的外部数据库。
	// 空值按 managed 处理，兼容旧台账。
	Mode string `json:"mode"`
	// Host / Username / TLSMode 只在外部连接模式使用。
	Host     string `json:"host,omitempty"`
	Username string `json:"username,omitempty"`
	TLSMode  string `json:"tlsMode,omitempty"`
	// Image 是实际拉取/运行的镜像地址（含加速站前缀或用户手填的私有仓库）。
	// 与 EngineVersion 分开记：版本是给人看的（17 / 8.4），地址是给 Docker 用的，
	// 国内网络下这两者经常不是同一个字符串（docker.m.daocloud.io/library/postgres:17）。
	Image        string `json:"image,omitempty"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`
	DataDir      string `json:"dataDir"`
	Profile      string `json:"profile"`
	// Port 是服务占用的宿主端口
	Port int `json:"port,omitempty"`
	// Runtime 固定为 docker；保留字段是为了识别升级上来的老记录
	Runtime string `json:"runtime"`
	// CPULimit 是容器可用的 CPU 核数上限；0 表示不限制（只对 docker 方式生效）
	CPULimit float64 `json:"cpuLimit"`
	// MemLimitMB 是容器可用的内存上限（MB）；0 表示不限制
	MemLimitMB int64 `json:"memLimitMB"`
	// StorageKind 是数据落盘方式：volume（Docker 命名卷，默认）或 bind（宿主机目录）
	StorageKind string `json:"storageKind"`
	// StoragePath 只在 bind 方式下有值，是宿主机上的数据目录
	StoragePath string `json:"storagePath,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
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
	// Database 是这次备份的库名：一个服务里可以有多个库，备份与恢复都按库进行。
	// 老记录（0.1.2 之前）是整实例备份，这一列为空，界面上按"默认库"呈现。
	Database   string `json:"database,omitempty"`
	Note       string `json:"note,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	ErrorCode  string `json:"errorCode,omitempty"`
	ErrorHint  string `json:"errorHint,omitempty"`
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
			host TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			tls_mode TEXT NOT NULL DEFAULT 'disable',
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
			database_name TEXT NOT NULL DEFAULT '',
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
		// 引擎配置按实例存：不同引擎的可调项完全不同，不能固定列
		`CREATE TABLE IF NOT EXISTS instance_settings (
			instance_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (instance_id, key)
		)`,
		// 任务中心：操作履历要跨重启保留，否则"昨天那次恢复成功没"就查不到了
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			target_kind TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			params TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			error_hint TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			created_by TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			started_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at DESC)`,
		// 定时备份策略：一实例一条，next_run_at 供调度器直接筛
		`CREATE TABLE IF NOT EXISTS backup_policies (
			instance_id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			schedule TEXT NOT NULL DEFAULT 'daily',
			time TEXT NOT NULL DEFAULT '02:00',
			weekday INTEGER NOT NULL DEFAULT 0,
			retention_count INTEGER NOT NULL DEFAULT 7,
			retention_days INTEGER NOT NULL DEFAULT 30,
			target_kind TEXT NOT NULL DEFAULT 'share',
			target_ref TEXT NOT NULL DEFAULT '',
			encrypt INTEGER NOT NULL DEFAULT 0,
			last_run_at INTEGER NOT NULL DEFAULT 0,
			last_result TEXT NOT NULL DEFAULT '',
			next_run_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_policies_next ON backup_policies(enabled, next_run_at)`,
		// 控制台登录会话：只存令牌哈希。落库而不是放内存，是为了应用重启/升级后
		// 用户不必重新登录一次——升级时停服务是常态，掉登录态会让人以为是坏了。
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			is_admin INTEGER NOT NULL DEFAULT 1,
			user_agent TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS instance_members (
			instance_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (instance_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_members_user ON instance_members(user_id, instance_id)`,
		`CREATE TABLE IF NOT EXISTS deploy_tasks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			engine TEXT NOT NULL,
			phase TEXT NOT NULL,
			phase_text TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			instance_id TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL,
			finished_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_tasks_started ON deploy_tasks(started_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("迁移失败：%w", err)
		}
	}
	// SQLite 没有 ADD COLUMN IF NOT EXISTS，重复执行会报 duplicate column，忽略即可
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE instances ADD COLUMN port INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("迁移失败（port）：%w", err)
	}
	// 运行方式：老实例都是本机进程，默认 native
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE instances ADD COLUMN runtime TEXT NOT NULL DEFAULT 'native'`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("迁移失败（runtime）：%w", err)
	}
	// 资源配额与存储方式：0 表示不限制，volume 表示用 Docker 命名卷
	for _, alter := range []struct{ name, stmt string }{
		{"cpu_limit", `ALTER TABLE instances ADD COLUMN cpu_limit REAL NOT NULL DEFAULT 0`},
		{"mem_limit_mb", `ALTER TABLE instances ADD COLUMN mem_limit_mb INTEGER NOT NULL DEFAULT 0`},
		{"storage_kind", `ALTER TABLE instances ADD COLUMN storage_kind TEXT NOT NULL DEFAULT 'volume'`},
		{"storage_path", `ALTER TABLE instances ADD COLUMN storage_path TEXT NOT NULL DEFAULT ''`},
		{"image", `ALTER TABLE instances ADD COLUMN image TEXT NOT NULL DEFAULT ''`},
		{"host", `ALTER TABLE instances ADD COLUMN host TEXT NOT NULL DEFAULT ''`},
		{"username", `ALTER TABLE instances ADD COLUMN username TEXT NOT NULL DEFAULT ''`},
		{"tls_mode", `ALTER TABLE instances ADD COLUMN tls_mode TEXT NOT NULL DEFAULT 'disable'`},
		{"bitmap_database", `ALTER TABLE backup_records ADD COLUMN database_name TEXT NOT NULL DEFAULT ''`},
	} {
		if _, err := s.db.ExecContext(ctx, alter.stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("迁移失败（%s）：%w", alter.name, err)
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

// runtimeOf 把空值收敛成 native：老数据与手工构造的结构体都不带这个字段。
func runtimeOf(runtime string) string {
	if runtime == "" {
		return "native"
	}
	return runtime
}

func modeOf(mode string) string {
	if mode == "" {
		return "managed"
	}
	return mode
}

func tlsModeOf(mode string) string {
	if mode == "require" {
		return "require"
	}
	return "disable"
}

// storageKindOf 把空值收敛成 volume：老实例的数据都在命名卷里。
func storageKindOf(kind string) string {
	if kind == "" {
		return "volume"
	}
	return kind
}

// instanceColumns 是 instances 表的读取列顺序，避免三处 SELECT 各写一份走样。
const instanceColumns = `id, name, engine, engine_version, mode, host, username, tls_mode,
	image, status, status_reason, data_dir, profile, port, runtime,
	cpu_limit, mem_limit_mb, storage_kind, storage_path, created_at, updated_at`

// scanInstance 按 instanceColumns 的顺序扫一行。
func scanInstance(sc interface{ Scan(...any) error }) (Instance, error) {
	var in Instance
	err := sc.Scan(&in.ID, &in.Name, &in.Engine, &in.EngineVersion, &in.Mode, &in.Host, &in.Username,
		&in.TLSMode, &in.Image, &in.Status, &in.StatusReason, &in.DataDir, &in.Profile, &in.Port, &in.Runtime,
		&in.CPULimit, &in.MemLimitMB, &in.StorageKind, &in.StoragePath, &in.CreatedAt, &in.UpdatedAt)
	if err == nil {
		in.Mode = modeOf(in.Mode)
		in.TLSMode = tlsModeOf(in.TLSMode)
	}
	return in, err
}

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
		(id, name, engine, engine_version, mode, host, username, tls_mode,
		 image, status, status_reason, data_dir, profile, port, runtime,
		 cpu_limit, mem_limit_mb, storage_kind, storage_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Name, in.Engine, in.EngineVersion, modeOf(in.Mode), in.Host, in.Username, tlsModeOf(in.TLSMode),
		in.Image, in.Status, in.StatusReason,
		in.DataDir, in.Profile, in.Port, runtimeOf(in.Runtime),
		in.CPULimit, in.MemLimitMB, storageKindOf(in.StorageKind), in.StoragePath,
		in.CreatedAt, in.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: instances.name") {
		return ErrNameTaken
	}
	return err
}

// ListInstances 返回未删除的实例；q 对名称做模糊匹配。
// HideLegacy 把不再支持的实例从界面收起来。
//
// 当前只认 MySQL / PostgreSQL / MongoDB 的 Docker 托管服务与外部连接；
// 开发期间遗留的其它实例从界面收起，避免用户误以为服务损坏。
// 只软删除台账记录，数据目录与容器一律不动——用户可能还要自己导出。
func (s *Store) HideLegacy(ctx context.Context) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`UPDATE instances SET deleted_at = ?, updated_at = ?
		 WHERE deleted_at IS NULL
		   AND (engine NOT IN ('mysql', 'postgres', 'mongodb')
		        OR (mode <> 'external' AND runtime <> 'docker'))`,
		now, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) ListInstances(ctx context.Context, q, status string) ([]Instance, error) {
	sqlStr := `SELECT ` + instanceColumns + `
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
		in, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// GetInstance 按 id 取实例。
func (s *Store) GetInstance(ctx context.Context, id string) (Instance, error) {
	in, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+`
		FROM instances WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, ErrNotFound
	}
	return in, err
}

// UpdateLimits 改资源配额（0 表示不限制）。
func (s *Store) UpdateLimits(ctx context.Context, id string, cpu float64, memMB int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE instances SET cpu_limit = ?, mem_limit_mb = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		cpu, memMB, nowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePort 改监听端口。容器名里带端口，所以调用方改完要重建容器。
func (s *Store) UpdatePort(ctx context.Context, id string, port int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE instances SET port = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		port, nowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
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

// NameTaken 判断名字是否已被占用，另用于“先查再建”的场景：
// 建 PG 实例要先初始化数据目录再写台账，等到写台账才发现重名就白做了一遍初始化。
func (s *Store) NameTaken(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM instances WHERE name = ? AND deleted_at IS NULL`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UsedPorts 返回已被占用的端口集合，端口分配时要避开它们。
func (s *Store) UsedPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT port FROM instances WHERE deleted_at IS NULL AND port > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		used[p] = true
	}
	return used, rows.Err()
}

// ---------- 备份 ----------

// CreateBackup 先落一条 running 记录，失败也能在列表里看到原因。
func (s *Store) CreateBackup(ctx context.Context, b Backup) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO backup_records
		(id, instance_id, type, status, size_bytes, checksum, storage_path, database_name, note, started_at, finished_at, error_code, error_hint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.InstanceID, b.Type, b.Status, b.SizeBytes, b.Checksum, b.StoragePath, b.Database, b.Note,
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
			storage_path, database_name, note, started_at, finished_at, error_code, error_hint
		FROM backup_records WHERE instance_id = ? ORDER BY started_at DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Backup{}
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Database, &b.Note, &b.StartedAt, &b.FinishedAt, &b.ErrorCode, &b.ErrorHint); err != nil {
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
			storage_path, database_name, note, started_at, finished_at, error_code, error_hint
		FROM backup_records WHERE id = ? AND instance_id = ?`, id, instanceID).
		Scan(&b.ID, &b.InstanceID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum,
			&b.StoragePath, &b.Database, &b.Note, &b.StartedAt, &b.FinishedAt, &b.ErrorCode, &b.ErrorHint)
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

// ---------- 实例配置 ----------

// InstanceSettings 读一个实例的全部配置项（key → value）。
func (s *Store) InstanceSettings(ctx context.Context, id string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM instance_settings WHERE instance_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetInstanceSettings 写入一个实例的配置：只 upsert 传进来的 key，未传的原值不动。
func (s *Store) SetInstanceSettings(ctx context.Context, id string, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowMs()
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_settings (instance_id, key, value, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(instance_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			id, k, v, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteInstanceSettings 清掉一个实例的配置（删除实例时调用，避免残留脏数据）。
func (s *Store) DeleteInstanceSettings(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM instance_settings WHERE instance_id = ?`, id)
	return err
}

func nowMs() int64 { return time.Now().UnixMilli() }
