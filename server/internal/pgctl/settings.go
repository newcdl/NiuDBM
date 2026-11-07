package pgctl

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"niudbm/server/internal/mysqlctl"
)

// settingDef 是一个对用户开放的参数。
//
// 只开放白名单：postgresql.conf 里几千个参数，随手改一个就可能让实例起不来。
// 这里挑的是"调了有意义、调错也不至于炸"的那十来个。
type settingDef struct {
	key   string
	label string
	kind  string // int | number | size | enum | bool | text
	apply string // reload | restart
	opts  []string
	hint  string
}

var settingDefs = []settingDef{
	{key: "max_connections", label: "最大连接数", kind: "int", apply: "restart",
		hint: "按应用连接池上限估：每连接约 5~10MB 内存，改完要重启"},
	{key: "shared_buffers", label: "共享缓冲池", kind: "size", apply: "restart",
		hint: "一般给内存的 25%；这是 PG 最重要的性能参数，改完要重启"},
	{key: "effective_cache_size", label: "可用文件缓存", kind: "size", apply: "reload",
		hint: "给系统+PG 总共能用来缓存的量（通常内存的 50~75%），只影响优化器估算"},
	{key: "work_mem", label: "单次排序内存", kind: "size", apply: "reload",
		hint: "每个排序/哈希操作可用内存；复杂查询多时适当加大，过大会被并发撑爆"},
	{key: "maintenance_work_mem", label: "维护操作内存", kind: "size", apply: "reload",
		hint: "VACUUM、建索引用；机械盘上给大一点能明显加快"},
	{key: "max_wal_size", label: "WAL 上限", kind: "size", apply: "reload",
		hint: "越大写入越少触发检查点，但崩溃恢复越慢"},
	{key: "checkpoint_timeout", label: "检查点间隔", kind: "text", apply: "reload",
		hint: "例如 5min / 15min；间隔太短会持续刷盘"},
	{key: "random_page_cost", label: "随机读代价", kind: "number", apply: "reload",
		hint: "SSD 可降到 1.1，机械盘保持 4；影响优化器是否走索引"},
	{key: "log_min_duration_statement", label: "慢查询阈值", kind: "text", apply: "reload",
		hint: "超过这个时长的语句写日志，例如 500ms；-1 表示关闭"},
	{key: "statement_timeout", label: "语句超时", kind: "text", apply: "reload",
		hint: "单条语句最长执行时间，例如 30s；0 表示不限制"},
	{key: "idle_in_transaction_session_timeout", label: "空闲事务超时", kind: "text", apply: "reload",
		hint: "事务开着一动不动超过这个时长就断开，防止锁表；建议 60s"},
	{key: "autovacuum", label: "自动清理", kind: "bool", apply: "reload",
		hint: "关掉会让表膨胀、查询越来越慢，除非确有原因否则保持开启"},
	{key: "log_connections", label: "记录连接日志", kind: "bool", apply: "reload",
		hint: "排查「谁在什么时候连进来」时打开"},
	{key: "timezone", label: "时区", kind: "text", apply: "reload",
		hint: "例如 Asia/Shanghai；影响 now() 与时间列的显示"},
}

// settingKeys 返回白名单里的参数名，供查询用。
func settingKeys() []string {
	out := make([]string, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, d.key)
	}
	return out
}

// Settings 读出白名单参数的当前值。
//
// current_setting 返回的是带单位的可读值（如 128MB），不用自己按 unit 换算；
// 换算错了会让界面显示一个错的数，用户照着改就更糟。
func (s Spec) Settings() ([]mysqlctl.Setting, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// lib/pq 不认 []string：必须用 pq.Array 包一层，否则会报
	// "unsupported type []string"（这个错很隐蔽，界面只会显示"读取配置失败"）
	rows, err := db.QueryContext(ctx, `
		SELECT name, current_setting(name), context, boot_val
		FROM pg_settings WHERE name = ANY($1)`, pq.Array(settingKeys()))
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	current := map[string]string{}
	needsRestart := map[string]bool{}
	boot := map[string]string{}
	for rows.Next() {
		var name, value, ctxName, bootVal string
		if err := rows.Scan(&name, &value, &ctxName, &bootVal); err != nil {
			return nil, translate(err)
		}
		current[name] = value
		needsRestart[name] = ctxName == "postgres"
		boot[name] = bootVal
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	out := make([]mysqlctl.Setting, 0, len(settingDefs))
	for _, d := range settingDefs {
		value, ok := current[d.key]
		if !ok {
			continue
		}
		apply := d.apply
		if needsRestart[d.key] {
			apply = "restart"
		}
		def := boot[d.key]
		if d.kind == "size" || d.kind == "bool" {
			// boot_val 对这些类型是原始单位（8kB 块 / on-off 数字），
			// 直接展示会误导，宁可留空
			def = ""
		}
		out = append(out, mysqlctl.Setting{
			Key:     d.key,
			Label:   d.label,
			Value:   value,
			Default: def,
			Kind:    d.kind,
			Options: d.opts,
			Apply:   apply,
			Hint:    d.hint,
		})
	}
	return out, nil
}

// ApplySettings 把改动写进 pg 的 auto.conf 并 reload。
//
// 用 ALTER SYSTEM 而不是改文件：postgresql.conf 在数据卷里、由容器内用户拥有，
// 从宿主改要处理属主与重启；ALTER SYSTEM 写的是 postgresql.auto.conf，
// 是 PG 官方给"由外部工具改参数"留的口子，优先级也更高。
//
// 返回 needRestart 表示其中有必须重启才生效的项。
func (s Spec) ApplySettings(values map[string]string) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	defs := map[string]settingDef{}
	for _, d := range settingDefs {
		defs[d.key] = d
	}

	// 只认白名单，未知键直接拒：拼进 ALTER SYSTEM 的键名不做二次校验，
	// 一旦放开就等于给了任意 SQL 拼接能力
	keys := make([]string, 0, len(values))
	for k := range values {
		if _, ok := defs[k]; !ok {
			return false, fmt.Errorf("不支持修改参数 %s", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	db, err := s.open(RootUser)
	if err != nil {
		return false, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var needRestart bool
	for _, key := range keys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		// 只有纯整数才不带引号：带单位的值（8MB / 500ms）不加引号会被当成
		// 数字字面量，"8MB" 直接语法错误（trailing junk after numeric literal）
		literal := quoteLiteral(value)
		if defs[key].kind == "int" || defs[key].kind == "number" {
			if _, err := strconv.ParseFloat(value, 64); err == nil {
				literal = value
			}
		}
		stmt := "ALTER SYSTEM SET " + quoteIdent(key) + " = " + literal
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return needRestart, fmt.Errorf("保存 %s 失败：%w", key, translate(err))
		}
		var ctxName string
		if err := db.QueryRowContext(ctx, `SELECT context FROM pg_settings WHERE name = $1`, key).Scan(&ctxName); err == nil {
			if ctxName == "postgres" {
				needRestart = true
			}
		}
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_reload_conf()"); err != nil {
		return needRestart, fmt.Errorf("重载配置失败：%w", translate(err))
	}
	return needRestart, nil
}

// Presets 返回按内存档位算好的参数组合，供"性能调整"一键填表。
//
// 取值原则与 MySQL 那套一致：共享缓冲池 ≈ 25%，缓存估算 ≈ 60%，
// 单次排序给 4~64MB，连接数按内存估算。目标是"填完能稳定跑起来"。
func Presets() []mysqlctl.TuningPreset {
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

	mb := func(v int) string { return fmt.Sprintf("%dMB", v) }
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}

	out := make([]mysqlctl.TuningPreset, 0, len(tiers))
	for _, t := range tiers {
		shared := clamp(t.memMB/4, 128, 8192)
		cache := clamp(t.memMB*3/5, 512, 24576)
		work := clamp(t.memMB/128, 4, 64)
		conn := clamp(t.memMB/16, 100, 500)
		out = append(out, mysqlctl.TuningPreset{
			Key:      t.key,
			Label:    t.label,
			MemoryMB: t.memMB,
			Values: map[string]string{
				"shared_buffers":             mb(shared),
				"effective_cache_size":       mb(cache),
				"work_mem":                   mb(work),
				"maintenance_work_mem":       mb(clamp(t.memMB/16, 64, 1024)),
				"max_connections":            fmt.Sprint(conn),
				"max_wal_size":               "1GB",
				"checkpoint_timeout":         "15min",
				"random_page_cost":           "1.1",
				"autovacuum":                 "on",
				"log_min_duration_statement": "500ms",
			},
			Hint: fmt.Sprintf("按机器 %s 内存估算：共享缓冲池 %s、缓存估算 %s、连接上限 %d",
				t.label, mb(shared), mb(cache), conn),
		})
	}
	return out
}
