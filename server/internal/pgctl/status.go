package pgctl

import (
	"context"
	"fmt"
	"time"

	"niudbm/server/internal/mysqlctl"
)

// Status 读一遍运行状态。
//
// 与 MySQL 那边同一套指标口径：连接、事务、缓存命中、库体积、死元组
// （PG 的 autovacuum 跟不上时最先堆的就是它），都在服务端算好比值。
func (s Spec) Status() ([]mysqlctl.StatusMetric, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		uptimeSec                int64
		conns                    int
		maxConns                 int
		xactCommit, xactRollback int64
		blksHit, blksRead        int64
		tupReturned, tupFetched  int64
		deadTuples, liveTuples   int64
		totalBytes               int64
	)
	if err := db.QueryRowContext(ctx, `
		SELECT EXTRACT(EPOCH FROM (now() - pg_postmaster_start_time()))::bigint,
		       (SELECT count(*) FROM pg_stat_activity),
		       current_setting('max_connections')::int,
		       COALESCE(SUM(xact_commit), 0),
		       COALESCE(SUM(xact_rollback), 0),
		       COALESCE(SUM(blks_hit), 0),
		       COALESCE(SUM(blks_read), 0),
		       COALESCE(SUM(tup_returned), 0),
		       COALESCE(SUM(tup_fetched), 0),
		       COALESCE((SELECT SUM(n_dead_tup) FROM pg_stat_user_tables), 0),
		       COALESCE((SELECT SUM(n_live_tup) FROM pg_stat_user_tables), 0),
		       COALESCE((SELECT SUM(pg_database_size(datname)) FROM pg_database
		                 WHERE datallowconn AND NOT datistemplate AND datname <> 'postgres'), 0)
		FROM pg_stat_database`).
		Scan(&uptimeSec, &conns, &maxConns, &xactCommit, &xactRollback,
			&blksHit, &blksRead, &tupReturned, &tupFetched, &deadTuples, &liveTuples, &totalBytes); err != nil {
		return nil, translate(err)
	}

	hitRate := "—"
	if total := blksHit + blksRead; total > 0 {
		hitRate = fmt.Sprintf("%.2f%%", float64(blksHit)/float64(total)*100)
	}

	out := []mysqlctl.StatusMetric{
		{Key: "uptime", Label: "运行时长", Value: humanUptime(float64(uptimeSec))},
		{Key: "connections", Label: "当前连接", Value: fmt.Sprintf("%d / %d", conns, maxConns),
			Hint: "接近上限时新连接会被拒，可考虑调大 max_connections"},
		{Key: "xact_commit", Label: "已提交事务", Value: fmt.Sprint(xactCommit)},
		{Key: "xact_rollback", Label: "回滚事务", Value: fmt.Sprint(xactRollback),
			Hint: "回滚比例高通常意味着应用侧有大量失败写入"},
		{Key: "cache_hit", Label: "缓存命中率", Value: hitRate,
			Hint: "低于 99%（OLTP）说明 shared_buffers 偏小或存在大量全表扫描"},
		{Key: "tuples", Label: "读出行数", Value: fmt.Sprintf("返回 %d / 取回 %d", tupReturned, tupFetched)},
		{Key: "dead_tuples", Label: "死元组 / 活元组", Value: fmt.Sprintf("%d / %d", deadTuples, liveTuples),
			Hint: "死元组显著多于活元组说明 autovacuum 跟不上，表会持续膨胀"},
		{Key: "data_size", Label: "数据体积", Value: humanBytes(float64(totalBytes))},
	}
	return out, nil
}

func humanUptime(sec float64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时 %d 分钟", hours, mins)
	}
	return fmt.Sprintf("%d 分钟", mins)
}

func humanBytes(v float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
