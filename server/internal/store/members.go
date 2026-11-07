package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	MemberRoleViewer   = "viewer"
	MemberRoleOperator = "operator"
	MemberRoleAdmin    = "admin"
)

type InstanceMember struct {
	InstanceID string `json:"instanceId"`
	UserID     string `json:"userId"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func (s *Store) UpsertInstanceMember(ctx context.Context, member InstanceMember) error {
	now := nowMs()
	if member.CreatedAt == 0 {
		member.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO instance_members(instance_id, user_id, username, role, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(instance_id, user_id) DO UPDATE SET
			username = excluded.username,
			role = excluded.role,
			updated_at = excluded.updated_at`,
		member.InstanceID, member.UserID, member.Username, member.Role,
		member.CreatedAt, now)
	return err
}

func (s *Store) DeleteInstanceMember(ctx context.Context, instanceID, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM instance_members WHERE instance_id = ? AND user_id = ?`,
		instanceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListInstanceMembers(ctx context.Context, instanceID string) ([]InstanceMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT instance_id, user_id, username, role, created_at, updated_at
		FROM instance_members WHERE instance_id = ? ORDER BY created_at`,
		instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstanceMember{}
	for rows.Next() {
		var m InstanceMember
		if err := rows.Scan(&m.InstanceID, &m.UserID, &m.Username, &m.Role, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) InstanceMemberRole(ctx context.Context, instanceID, userID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `
		SELECT role FROM instance_members WHERE instance_id = ? AND user_id = ?`,
		instanceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (s *Store) InstanceIDsForMember(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT instance_id, role FROM instance_members WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		out[id] = role
	}
	return out, rows.Err()
}
