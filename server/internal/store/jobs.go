package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// 任务状态。与前端契约保持一致，取值不能再加不加地改。
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"
)

// ErrJobFinished 表示任务已经结束，不能再取消。
var ErrJobFinished = errors.New("任务已结束，不能取消")

// Job 是 jobs 表的一行：任务中心展示的"操作履历"。
//
// 之所以落库而不是只放内存：用户点的是"我昨天那次恢复成功了没"，
// 进程重启后历史就没了的话，这个页面等于没用。
type Job struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	TargetKind string `json:"targetKind,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	Status     string `json:"status"`
	// Progress 是 0–100 的粗粒度进度：本应用的操作都是分钟级，够用即可
	Progress int `json:"progress"`
	// Params / Result 都是脱敏后的快照，绝不写密码
	Params     string `json:"params,omitempty"`
	Result     string `json:"result,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	ErrorHint  string `json:"errorHint,omitempty"`
	Attempts   int    `json:"attempts"`
	CreatedBy  string `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

// Active 判断任务是否还没结束。
func (j Job) Active() bool { return j.Status == JobQueued || j.Status == JobRunning }

// CreateJob 写入一条任务记录。
func (s *Store) CreateJob(ctx context.Context, job Job) error {
	if job.Status == "" {
		job.Status = JobQueued
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, target_kind, target_id, status, progress, params, result,
			error_code, error_hint, attempts, created_by, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Type, job.TargetKind, job.TargetID, job.Status, job.Progress, job.Params,
		job.Result, job.ErrorCode, job.ErrorHint, job.Attempts, job.CreatedBy, job.CreatedAt,
		job.StartedAt, job.FinishedAt)
	if err != nil {
		return fmt.Errorf("写入任务失败：%w", err)
	}
	return nil
}

// FinishJob 把任务落到终态：成功/失败要同时更新进度与结束时间。
func (s *Store) FinishJob(ctx context.Context, job Job) error {
	progress := job.Progress
	if job.Status == JobSucceeded {
		progress = 100
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, progress = ?, result = ?, error_code = ?, error_hint = ?, finished_at = ?
		 WHERE id = ?`,
		job.Status, progress, job.Result, job.ErrorCode, job.ErrorHint, job.FinishedAt, job.ID)
	if err != nil {
		return fmt.Errorf("更新任务失败：%w", err)
	}
	return nil
}

// SetJobProgress 更新进度；只对未结束的任务生效，避免把终态改回 running。
func (s *Store) SetJobProgress(ctx context.Context, id string, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 99 {
		progress = 99
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET progress = ? WHERE id = ? AND status IN (?, ?)`,
		progress, id, JobQueued, JobRunning)
	return err
}

// ListJobs 按状态过滤，按时间倒序返回；limit <= 0 时用默认 100。
func (s *Store) ListJobs(ctx context.Context, status string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT id, type, target_kind, target_id, status, progress, params, result,
		error_code, error_hint, attempts, created_by, created_at, started_at, finished_at
		FROM jobs`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取任务失败：%w", err)
	}
	defer rows.Close()

	out := make([]Job, 0, limit)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ActiveJobCount 返回还没结束的任务数，顶栏角标用。
func (s *Store) ActiveJobCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN (?, ?)`, JobQueued, JobRunning).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// GetJob 取单条任务。
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx,
		`SELECT id, type, target_kind, target_id, status, progress, params, result,
			error_code, error_hint, attempts, created_by, created_at, started_at, finished_at
		 FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// CancelJob 把一个未结束的任务标记为取消。已结束的任务返回 ErrJobFinished。
//
// 这里只改状态，真正的"停下来"由调用方负责（例如取消引擎下载的上下文），
// 因为台账不该知道任何执行细节。
func (s *Store) CancelJob(ctx context.Context, id string) (Job, error) {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !job.Active() {
		return job, ErrJobFinished
	}
	job.Status = JobCanceled
	job.FinishedAt = nowMs()
	if err := s.FinishJob(ctx, job); err != nil {
		return Job{}, err
	}
	return job, nil
}

// DeleteJobsBefore 清理旧任务，返回删除条数。
func (s *Store) DeleteJobsBefore(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE created_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.Type, &j.TargetKind, &j.TargetID, &j.Status, &j.Progress,
		&j.Params, &j.Result, &j.ErrorCode, &j.ErrorHint, &j.Attempts, &j.CreatedBy,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if err != nil {
		return Job{}, err
	}
	return j, nil
}
