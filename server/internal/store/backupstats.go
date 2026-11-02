package store

import (
	"context"
	"fmt"
)

// InstanceBackupStat 是单个实例的备份概况，列表页用来显示"几份、最近一次什么时候"。
type InstanceBackupStat struct {
	Count  int   `json:"count"`
	LastAt int64 `json:"lastAt"`
}

// InstanceBackupStats 一次查出所有实例的备份概况。
//
// 逐实例查（N+1）在实例多、备份多时会明显变慢；列表页每刷一次就查一轮，
// 用一条 GROUP BY 更稳妥。
func (s *Store) InstanceBackupStats(ctx context.Context) (map[string]InstanceBackupStat, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT instance_id, COUNT(*), COALESCE(MAX(started_at), 0)
		 FROM backup_records GROUP BY instance_id`)
	if err != nil {
		return nil, fmt.Errorf("统计实例备份失败：%w", err)
	}
	defer rows.Close()

	out := map[string]InstanceBackupStat{}
	for rows.Next() {
		var id string
		var st InstanceBackupStat
		if err := rows.Scan(&id, &st.Count, &st.LastAt); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
