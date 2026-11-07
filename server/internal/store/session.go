package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session 是控制台的一次登录会话。
//
// 只存令牌的 SHA-256，明文令牌仅在登录响应里出现一次：元数据库会被备份、
// 也可能被用户拷走，里面留着可直接使用的令牌等于把登录态一起送出去。
type Session struct {
	TokenHash string `json:"-"`
	UserID    string `json:"uid"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"isAdmin"`
	UserAgent string `json:"userAgent,omitempty"`
	IP        string `json:"ip,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// CreateSession 写入一条会话。
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	if sess.TokenHash == "" {
		return errors.New("会话缺少令牌")
	}
	if sess.CreatedAt == 0 {
		sess.CreatedAt = nowMs()
	}
	if sess.ExpiresAt == 0 {
		sess.ExpiresAt = time.Now().Add(7 * 24 * time.Hour).UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(token_hash, user_id, username, is_admin, user_agent, ip, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.Username, boolInt(sess.IsAdmin),
		sess.UserAgent, sess.IP, sess.CreatedAt, sess.ExpiresAt)
	return err
}

// GetSession 按令牌哈希取会话；过期视为不存在（顺手删掉，免得表无限长）。
func (s *Store) GetSession(ctx context.Context, tokenHash string) (Session, error) {
	var (
		sess    Session
		isAdmin int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, username, is_admin, user_agent, ip, created_at, expires_at
		FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &sess.Username, &isAdmin,
			&sess.UserAgent, &sess.IP, &sess.CreatedAt, &sess.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if sess.ExpiresAt <= nowMs() {
		_ = s.DeleteSession(ctx, tokenHash)
		return Session{}, ErrNotFound
	}
	sess.IsAdmin = isAdmin != 0
	return sess, nil
}

// DeleteSession 注销一条会话；不存在也当成功（退出登录要幂等）。
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PurgeSessions 清掉已过期的会话，启动时与定期各跑一次。
func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, nowMs())
	return err
}
