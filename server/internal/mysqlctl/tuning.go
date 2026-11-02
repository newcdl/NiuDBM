package mysqlctl

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// TuningPreset 是一套按机器内存规模算好的参数组合。
//
// 用户通常不知道该给 innodb_buffer_pool_size 填多少：填小了浪费内存，填大了实例起不来。
// 这里按"这台 NAS 有多少内存"给出五档，一键填表后仍可逐项微调。
type TuningPreset struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	MemoryMB int               `json:"memoryMB"`
	Values   map[string]string `json:"values"`
	Hint     string            `json:"hint"`
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sizeValue 把 MB 数写成 my.cnf 里的写法：能整成 G 就用 G，读起来更直观。
func sizeValue(mb int) string {
	if mb >= 1024 && mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + "G"
	}
	return strconv.Itoa(mb) + "M"
}

// TuningPresets 返回按内存档位预先算好的一组参数。
//
// 取值原则（保守优先，目标是"改完能稳定跑起来"）：
//   - InnoDB 缓冲池 ≈ 机器内存的 50%：剩下的一半要留给系统、连接栈和临时表。
//   - 临时表与内存表上限给到 1/64，最多 256M：再大容易和缓冲池抢内存。
//   - 连接数按内存/16 估算，给 100~800 的区间：每个连接都有固定开销。
func TuningPresets() []TuningPreset {
	tiers := []struct {
		key, label string
		memMB      int
	}{
		{"1-2", "1-2GB", 2048},
		{"2-4", "2-4GB", 4096},
		{"4-8", "4-8GB", 8192},
		{"8-16", "8-16GB", 16384},
		{"16-32", "16-32GB", 32768},
	}

	out := make([]TuningPreset, 0, len(tiers))
	for _, t := range tiers {
		pool := t.memMB / 2
		tmp := clampInt(t.memMB/64, 16, 256)
		conn := clampInt(t.memMB/16, 100, 800)
		threadCache := clampInt(t.memMB/512, 8, 64)
		logBuf := clampInt(t.memMB/128, 16, 128)
		out = append(out, TuningPreset{
			Key:      t.key,
			Label:    t.label,
			MemoryMB: t.memMB,
			Values: map[string]string{
				"innodb_buffer_pool_size": sizeValue(pool),
				"innodb_log_buffer_size":  sizeValue(logBuf),
				"tmp_table_size":          sizeValue(tmp),
				"max_heap_table_size":     sizeValue(tmp),
				"max_connections":         strconv.Itoa(conn),
				"thread_cache_size":       strconv.Itoa(threadCache),
				"sort_buffer_size":        "256K",
				"read_buffer_size":        "128K",
				"read_rnd_buffer_size":    "256K",
				"join_buffer_size":        "256K",
				"binlog_cache_size":       "32K",
			},
			Hint: fmt.Sprintf("按机器 %s 内存估算：缓冲池 %s、连接上限 %d", t.label, sizeValue(pool), conn),
		})
	}
	return out
}

// StatusMetric 是一条运行状态指标：值 + 一句"什么时候该管它"。
type StatusMetric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint,omitempty"`
}

// Status 读一遍运行状态。
//
// 取的都是 SHOW GLOBAL STATUS 里判断"这台库健康不健康"最常用的几项，
// 命中率这类比值在服务端算好，前端不用再拼。
func (s Spec) Status() ([]StatusMetric, error) {
	db, err := s.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, translate(err)
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	num := func(key string) float64 {
		f, _ := strconv.ParseFloat(raw[key], 64)
		return f
	}
	uptime := num("Uptime")
	if uptime < 1 {
		uptime = 1
	}
	ratio := func(hit, total float64) string {
		if total <= 0 {
			return "—"
		}
		return fmt.Sprintf("%.2f%%", hit/total*100)
	}
	perSec := func(total float64) string { return fmt.Sprintf("%.2f", total/uptime) }

	// 缓冲池命中率：逻辑读里有多少不用落盘。低于 95% 通常是缓冲池太小
	bpRatio := ratio(num("Innodb_buffer_pool_read_requests")-num("Innodb_buffer_pool_reads"), num("Innodb_buffer_pool_read_requests"))

	items := []StatusMetric{
		{Key: "uptime", Label: "运行时长", Value: humanUptime(uptime), Hint: "实例已经连续跑了多久"},
		{Key: "threads_connected", Label: "当前连接", Value: raw["Threads_connected"], Hint: "接近 max_connections 时新连接会被拒绝"},
		{Key: "questions", Label: "累计语句数", Value: raw["Questions"], Hint: "包含客户端与内部语句"},
		{Key: "qps", Label: "每秒语句", Value: perSec(num("Questions")), Hint: "启动以来的平均值"},
		{Key: "bytes_sent", Label: "发送", Value: humanBytes(num("Bytes_sent")), Hint: "服务端返回给客户端的数据量"},
		{Key: "bytes_received", Label: "接收", Value: humanBytes(num("Bytes_received")), Hint: "客户端发来的数据量"},
		{Key: "com_select", Label: "SELECT 次数", Value: raw["Com_select"]},
		{Key: "com_insert", Label: "INSERT 次数", Value: raw["Com_insert"]},
		{Key: "com_update", Label: "UPDATE 次数", Value: raw["Com_update"]},
		{Key: "com_delete", Label: "DELETE 次数", Value: raw["Com_delete"]},
		{Key: "slow_queries", Label: "慢查询数", Value: raw["Slow_queries"], Hint: "超过 long_query_time 的语句数，记得开慢查询日志"},
		{Key: "buffer_pool_hit", Label: "缓冲池命中率", Value: bpRatio, Hint: "低于 95% 说明缓冲池偏小，读要去磁盘"},
		{Key: "tmp_disk_tables", Label: "磁盘临时表", Value: raw["Created_tmp_disk_tables"], Hint: "大量出现时调大 tmp_table_size，并检查是否用了大字段排序"},
		{Key: "opened_tables", Label: "已打开的表", Value: raw["Opened_tables"], Hint: "持续快速增长时调大 table_open_cache"},
		{Key: "select_full_join", Label: "无索引 JOIN", Value: raw["Select_full_join"], Hint: "不为 0 说明有联表查询缺索引"},
		{Key: "sort_merge_passes", Label: "排序合并次数", Value: raw["Sort_merge_passes"], Hint: "数值大说明排序缓冲不够，ORDER BY 走了磁盘"},
	}
	return items, nil
}

// humanUptime 把秒数写成人能读的时长。
func humanUptime(sec float64) string {
	total := int64(sec)
	days := total / 86400
	hours := (total % 86400) / 3600
	mins := (total % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	default:
		return fmt.Sprintf("%d 分", mins)
	}
}

// humanBytes 把字节数写成 MB / GB。
func humanBytes(v float64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case v >= gb:
		return fmt.Sprintf("%.2f GB", v/gb)
	case v >= mb:
		return fmt.Sprintf("%.2f MB", v/mb)
	default:
		return fmt.Sprintf("%.0f B", v)
	}
}
