package store

import (
	"context"
	"fmt"
)

// SettingsKV 读出全部全局设置项。
//
// 设置项按 key 存：新增一个开关不需要改表结构，也不影响老数据。
func (s *Store) SettingsKV(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("读取设置失败：%w", err)
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

// PutSettingsKV 原子写入若干设置项。
func (s *Store) PutSettingsKV(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowMs()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			k, v, now); err != nil {
			return fmt.Errorf("保存设置失败：%w", err)
		}
	}
	return tx.Commit()
}

// DeleteAuditBefore 清理旧审计，返回删除条数。
//
// 审计是无上限增长的表：不留清理入口的话，跑上一年半载就会把元数据库撑大。
func (s *Store) DeleteAuditBefore(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
