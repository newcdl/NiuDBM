package store

import (
	"context"
	"database/sql"
	"errors"
)

type DeployTask struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	Phase      string `json:"phase"`
	PhaseText  string `json:"phaseText"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

func (s *Store) CreateDeployTask(ctx context.Context, task DeployTask) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_tasks(id, name, engine, phase, phase_text, status, error, instance_id, started_at, finished_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Name, task.Engine, task.Phase, task.PhaseText, task.Status,
		task.Error, task.InstanceID, task.StartedAt, task.FinishedAt)
	return err
}

func (s *Store) UpdateDeployTask(ctx context.Context, task DeployTask) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE deploy_tasks SET phase = ?, phase_text = ?, status = ?, error = ?, instance_id = ?, finished_at = ?
		WHERE id = ?`,
		task.Phase, task.PhaseText, task.Status, task.Error, task.InstanceID, task.FinishedAt, task.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetDeployTask(ctx context.Context, id string) (DeployTask, error) {
	var task DeployTask
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, engine, phase, phase_text, status, error, instance_id, started_at, finished_at
		FROM deploy_tasks WHERE id = ?`, id).
		Scan(&task.ID, &task.Name, &task.Engine, &task.Phase, &task.PhaseText, &task.Status,
			&task.Error, &task.InstanceID, &task.StartedAt, &task.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeployTask{}, ErrNotFound
	}
	return task, err
}

func (s *Store) FailRunningDeployTasks(ctx context.Context, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_tasks SET status = 'failed', phase = 'failed', phase_text = '部署中断',
			error = ?, finished_at = ? WHERE status = 'running'`,
		reason, nowMs())
	return err
}
