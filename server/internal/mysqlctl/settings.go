package mysqlctl

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Setting 是一个对用户开放的可调参数。
//
// 只开放白名单，不提供裸的 my.cnf 编辑：一个拼写错误就能让实例起不来。
type Setting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Value   string   `json:"value"`
	Default string   `json:"default"`
	Kind    string   `json:"kind"` // int | number | size | enum | bool
	Options []string `json:"options,omitempty"`
	// Apply 说明改完怎么生效：reload 立即生效，restart 需要重启实例。
	Apply string `json:"apply"`
	Hint  string `json:"hint,omitempty"`
}

type settingDef struct {
	key   string
	label string
	kind  string
	apply string
	def   string
	// defs 覆盖 def：key 形如 "mysql"（整个实现）或 "mysql/8.0"（实现+大版本）。
	// 同一个参数在不同引擎上默认值并不一样（key_buffer_size 在 MySQL 是 8M、
	// MariaDB 是 128M），写死一个数会让"恢复默认"把库改到一个并不默认的值。
	defs map[string]string
	// options 是枚举项的可选值
	options []string
	hint    string
	// Flavors 限定实现（mariadb / mysql），空表示两者都支持。
	// Since / Until 限定版本范围：Oracle MySQL 8.4 删掉了 default_authentication_plugin，
	// MariaDB 没有 innodb_redo_log_capacity，这类差异必须按版本/实现过滤。
	Flavors []string
	Since   string
	Until   string
}

// applies 判断某个配置项在当前实现与版本上是否存在。
func (d settingDef) applies(flavor, series string) bool {
	if len(d.Flavors) > 0 {
		matched := false
		for _, f := range d.Flavors {
			if f == flavor {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if series == "" {
		return true
	}
	// 这里必须保留完整的版本精度：innodb_redo_log_capacity 是 8.0.30 才有的参数，
	// 先过一遍 seriesBase 会把 8.0.20 和 8.0.32 一起截成 8.0，两者再也分不开，
	// 于是老镜像也会被写下这个参数——mysqld 直接以 unknown variable 退出。
	series = strings.TrimSpace(series)
	if d.Since != "" && compareSeries(series, d.Since) < 0 {
		return false
	}
	if d.Until != "" && compareSeries(series, d.Until) > 0 {
		return false
	}
	return true
}

// compareSeries 按数字段比较版本号，缺的段按 0 处理。
// seriesBase 把 tag 归一成大版本号（8.4.3 → 8.4，11 → 11），
// 配置项的适用版本都是按大版本写的。
func seriesBase(series string) string {
	series = strings.TrimSpace(series)
	parts := strings.Split(series, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return series
}

func compareSeries(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// defaultFor 取某个实现/版本下的默认值，找不到就用兜底值。
func defaultFor(d settingDef, flavor, series string) string {
	if len(d.defs) > 0 {
		base := seriesBase(series)
		if v, ok := d.defs[flavor+"/"+base]; ok {
			return v
		}
		if v, ok := d.defs[flavor]; ok {
			return v
		}
	}
	return d.def
}

// settingDefs 是有序的：界面按这个顺序渲染，保存也按这个顺序写。
var settingDefs = []settingDef{
	{
		key: "max_connections", label: "最大连接数", kind: "int", apply: "restart", def: "151",
		hint: "所有客户端加起来的连接上限。调大要同时确认内存够用，每个连接都有额外开销。",
	},
	{
		key: "innodb_buffer_pool_size", label: "InnoDB 缓冲池", kind: "size", apply: "restart", def: "128M",
		hint: "InnoDB 最重要的内存区域，建议设为机器内存的 50%~70%，例如 512M。",
	},
	{
		key: "key_buffer_size", label: "MyISAM 索引缓冲", kind: "size", apply: "restart", def: "8M",
		defs: map[string]string{"mariadb": "128M"},
		hint: "只对 MyISAM 表生效。全部是 InnoDB 表时可以调小，省下来的内存给缓冲池。",
	},
	{
		key: "table_open_cache", label: "表缓存", kind: "int", apply: "restart", def: "4000",
		defs: map[string]string{"mysql/5.7": "2000", "mariadb": "2000"},
		hint: "同时可打开的表数量上限。表很多、并发高时适当调大。",
	},
	{
		key: "max_allowed_packet", label: "最大包大小", kind: "size", apply: "restart", def: "64M",
		defs: map[string]string{"mysql/5.7": "4M", "mariadb": "16M"},
		hint: "单条 SQL 或单个字段的上限。导入大文本或大 BLOB 时容易撞到，可以调到 64M。",
	},
	{
		key: "long_query_time", label: "慢查询阈值（秒）", kind: "number", apply: "reload", def: "10",
		hint: "超过该耗时的语句会记进慢查询日志。支持小数，例如 0.5。",
	},
	{
		key: "slow_query_log", label: "慢查询日志", kind: "bool", apply: "reload", def: "OFF",
		hint: "打开后慢查询写进容器数据目录（宿主机上就是该服务的数据目录），文件名默认以容器 hostname 开头。",
	},
	{
		key: "character_set_server", label: "服务端字符集", kind: "enum", apply: "restart", def: "utf8mb4",
		defs:    map[string]string{"mysql/5.7": "latin1"},
		options: []string{"utf8mb4", "utf8", "latin1"},
		hint:    "新建库默认用的字符集。中文与 emoji 建议保持 utf8mb4。",
	},
	{
		key: "collation_server", label: "服务端排序规则", kind: "enum", apply: "restart", def: "utf8mb4_unicode_ci",
		defs: map[string]string{"mysql/5.7": "utf8mb4_general_ci"},
		options: []string{
			"utf8mb4_unicode_ci", "utf8mb4_general_ci", "utf8mb4_bin",
			"utf8mb4_0900_ai_ci", "latin1_swedish_ci",
		},
		hint:    "必须与「服务端字符集」匹配（utf8mb4 配 utf8mb4_*，latin1 配 latin1_*），否则实例起不来。",
		Flavors: []string{"mysql", "mariadb"},
	},
	{
		key: "innodb_redo_log_capacity", label: "InnoDB 日志容量", kind: "size", apply: "restart", def: "100M",
		hint:    "MySQL 8.0.30 起用这个参数替代 innodb_log_file_size；写放大的库可以调大。",
		Flavors: []string{"mysql"},
		// 8.0.30 之前这个变量不存在，写进配置会让 mysqld 起不来（unknown variable）
		Since: "8.0.30",
	},
	{
		key: "default_authentication_plugin", label: "默认认证插件", kind: "enum", apply: "restart",
		def:     "caching_sha2_password",
		defs:    map[string]string{"mysql/5.7": "mysql_native_password"},
		options: []string{"caching_sha2_password", "mysql_native_password"},
		hint:    "老客户端连不上 8.0 时常见原因。8.4 已移除该参数，5.7 默认是 mysql_native_password。",
		Flavors: []string{"mysql"},
		// 5.7.6 才引入；8.4 又删掉了，所以是个闭区间
		Since: "5.7",
		Until: "8.0",
	},
	{
		key: "aria_pagecache_buffer_size", label: "Aria 页缓存", kind: "size", apply: "restart", def: "128M",
		hint:    "MariaDB 内部临时表与系统表用的缓存，纯 InnoDB 业务可以保持默认。",
		Flavors: []string{"mariadb"},
		Since:   "10.3",
	},
	{
		key: "sort_buffer_size", label: "排序缓冲", kind: "size", apply: "restart", def: "256K",
		hint: "每个需要排序的连接各占一份。并发高时按连接数 × 该值估算内存，不要盲目调大。",
	},
	{
		key: "read_buffer_size", label: "顺序读缓冲", kind: "size", apply: "restart", def: "128K",
		hint: "全表扫描时每个连接各自申请，大表顺序扫描多时可以调大。",
	},
	{
		key: "read_rnd_buffer_size", label: "随机读缓冲", kind: "size", apply: "restart", def: "256K",
		hint: "排序后按行随机读取时用，参与排序的查询多时有效。",
	},
	{
		key: "join_buffer_size", label: "关联缓冲", kind: "size", apply: "restart", def: "256K",
		hint: "没有索引的 JOIN 才会用到，真正的解法是给关联字段建索引。",
	},
	{
		key: "tmp_table_size", label: "内存临时表上限", kind: "size", apply: "restart", def: "16M",
		hint: "超过上限的临时表会落到磁盘。与 max_heap_table_size 保持一致更稳妥。",
	},
	{
		key: "max_heap_table_size", label: "内存表上限", kind: "size", apply: "restart", def: "16M",
		hint: "单个 MEMORY 表与内部临时表的共同上限，建议与 tmp_table_size 相同。",
	},
	{
		key: "innodb_log_buffer_size", label: "InnoDB 日志缓冲", kind: "size", apply: "restart", def: "16M",
		hint: "事务提交前先写这里。有大事务（批量导入）时可以调大，减少刷盘。",
	},
	{
		key: "thread_cache_size", label: "线程缓存", kind: "int", apply: "restart", def: "8",
		hint: "缓存的空闲线程数。连接频繁建立断开时调大可以少创建线程。",
	},
	{
		key: "binlog_cache_size", label: "二进制日志缓存", kind: "size", apply: "restart", def: "32K",
		hint: "单个事务的 binlog 缓冲，大事务多时会溢出到磁盘临时文件。",
	},
	{
		key: "wait_timeout", label: "空闲连接超时（秒）", kind: "int", apply: "restart", def: "28800",
		hint: "连接空闲超过这个时长会被断开。应用连接池一般自己保活，可以调小到 600。",
	},
	{
		key: "innodb_flush_log_at_trx_commit", label: "提交刷盘策略", kind: "enum", apply: "restart", def: "1",
		options: []string{"1", "2", "0"},
		hint:    "1 最安全（每次提交都刷盘）；2 只写系统缓存，断电可能丢 1 秒；0 性能最好但风险最高。",
	},
	{
		key: "innodb_io_capacity", label: "InnoDB IOPS 基准", kind: "int", apply: "restart", def: "200",
		hint: "告诉 InnoDB 后端存储的吞吐量。机械盘 200，SSD 可以设 1000~2000。",
	},
	{
		key: "slow_query_log_file", label: "慢查询日志路径", kind: "string", apply: "restart",
		// 必须是容器内的绝对路径：官方 entrypoint 会把这个值 dirname 之后加进待 chown
		// 的目录集合，相对路径的 dirname 是 "."，等于让它对容器根目录 find + chown。
		def:  "/var/lib/mysql/slow.log",
		hint: "必须写容器内绝对路径（默认 /var/lib/mysql/slow.log）。写相对路径会让容器启动时遍历整个文件系统。",
	},
	{
		key: "lower_case_table_names", label: "表名大小写策略", kind: "enum", apply: "restart", def: "0",
		options: []string{"0", "1"},
		hint:    "0 区分大小写（Linux 默认），1 全部转小写。从 Windows 迁库过来时才会用到，改这个要先清空数据目录。",
	},
}

// optionRule 说明某个枚举值只在特定实现/版本上存在。
type optionRule struct {
	Value   string
	Flavors []string
	Since   string
	Until   string
}

// optionRules 是枚举值的额外可用性限制：d.options 里列出的候选并不都全版本通用。
// 典型是 utf8mb4_0900_ai_ci —— MySQL 8.0 才引入，5.7 与 MariaDB 都没有，
// 选进 my.cnf 就是 mysqld 直接起不来，而界面原本会把它当成合法值。
var optionRules = map[string][]optionRule{
	"collation_server": {
		{Value: "utf8mb4_0900_ai_ci", Flavors: []string{"mysql"}, Since: "8.0"},
	},
}

// optionsFor 返回某个实现/版本上真正可用的枚举值。
func optionsFor(d settingDef, flavor, series string) []string {
	rules, ok := optionRules[d.key]
	if !ok {
		return d.options
	}
	out := make([]string, 0, len(d.options))
	for _, opt := range d.options {
		if optionAvailable(opt, rules, flavor, series) {
			out = append(out, opt)
		}
	}
	return out
}

func optionAvailable(value string, rules []optionRule, flavor, series string) bool {
	for _, r := range rules {
		if r.Value != value {
			continue
		}
		if len(r.Flavors) > 0 {
			matched := false
			for _, f := range r.Flavors {
				if f == flavor {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		if series != "" {
			s := strings.TrimSpace(series)
			if r.Since != "" && compareSeries(s, r.Since) < 0 {
				return false
			}
			if r.Until != "" && compareSeries(s, r.Until) > 0 {
				return false
			}
		}
	}
	return true
}

var settingByKey = func() map[string]settingDef {
	out := make(map[string]settingDef, len(settingDefs))
	for _, d := range settingDefs {
		out[d.key] = d
	}
	return out
}()

// SettingKeys 返回全部白名单参数名。
func SettingKeys() []string {
	out := make([]string, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, d.key)
	}
	return out
}

// Settings 读取当前值。实例没在运行时返回 ErrNotRunning。
func (s Spec) Settings() ([]Setting, error) {
	db, err := s.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keys := make([]string, 0, len(settingDefs))
	for _, d := range settingDefs {
		if !d.applies(s.Flavor, s.Series) {
			continue
		}
		keys = append(keys, quoteLiteral(d.key))
	}
	rows, err := db.QueryContext(ctx,
		"SHOW VARIABLES WHERE Variable_name IN ("+strings.Join(keys, ",")+")")
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, translate(err)
		}
		got[strings.ToLower(name)] = value
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	out := make([]Setting, 0, len(settingDefs))
	for _, d := range settingDefs {
		if !d.applies(s.Flavor, s.Series) {
			continue
		}
		value, ok := got[d.key]
		if !ok {
			continue
		}
		out = append(out, Setting{
			Key:     d.key,
			Label:   d.label,
			Value:   humanValue(d, value),
			Default: defaultFor(d, s.Flavor, s.Series),
			Kind:    d.kind,
			Options: optionsFor(d, s.Flavor, s.Series),
			Apply:   d.apply,
			Hint:    d.hint,
		})
	}
	return out, nil
}

// ApplySettings 把一批参数写进实例的 my.cnf，并对能在线修改的参数立即生效。
//
// MySQL 大部分参数要重启才稳，这里如实返回 needRestart，让界面提示用户。
func (s Spec) ApplySettings(values map[string]string) (needRestart bool, err error) {
	normalized := make(map[string]string, len(values))
	for key, raw := range values {
		d, ok := settingByKey[key]
		if !ok {
			return false, fmt.Errorf("不支持的配置项：%s", key)
		}
		if !d.applies(s.Flavor, s.Series) {
			return false, fmt.Errorf("%s 在 %s 上不存在，不能设置", d.label, s.Series)
		}
		value := strings.TrimSpace(raw)
		if value != "" {
			value, err = validateSetting(d, s.Flavor, s.Series, value)
			if err != nil {
				return false, err
			}
		}
		normalized[key] = value
	}

	entries, err := readCnf(s.cnfPath())
	if err != nil {
		return false, err
	}
	// 补齐缺失项：早期版本建的文件只有一个段头，用户改一项参数后文件里就只有那一项，
	// 打开「配置文件」看像根本没配置。以默认值为底补齐，已有值（含手写的）一律不覆盖。
	mergeCnfDefaults(entries, s.initialCnf())
	for _, d := range settingDefs {
		if !d.applies(s.Flavor, s.Series) {
			continue
		}
		raw, ok := normalized[d.key]
		if !ok {
			continue
		}
		if strings.TrimSpace(raw) == "" {
			delete(entries, d.key)
		} else {
			entries[d.key] = raw
		}
		if d.apply == "restart" {
			needRestart = true
		}
	}
	if err := s.checkCharsetPair(entries); err != nil {
		return false, err
	}
	if err := writeCnf(s.cnfPath(), entries); err != nil {
		return needRestart, err
	}
	// 在线可改的参数顺手生效；失败不影响已写好的配置，重启后依然会生效
	if err := s.applyLive(normalized); err != nil {
		return needRestart, nil
	}
	return needRestart, nil
}

// checkCharsetPair 挡住"字符集改了、排序规则没改"这种组合。
//
// MySQL 启动时如果 collation 不属于 character_set_server，会直接拒绝启动；
// 那种错误只在容器日志里能看，界面上表现为"服务起不来"，很难联想到这里。
func (s Spec) checkCharsetPair(entries map[string]string) error {
	charset := entries["character_set_server"]
	collation := entries["collation_server"]
	if charset == "" || collation == "" {
		return nil
	}
	if !strings.HasPrefix(collation, charset+"_") {
		return fmt.Errorf("字符集 %s 与排序规则 %s 不匹配：改字符集时请一并改排序规则", charset, collation)
	}
	// 前缀对了不代表这个版本认它：utf8mb4_0900_ai_ci 只在 MySQL 8.0+ 存在，
	// 在 5.7 或 MariaDB 上同样会让 mysqld 起不来。
	if d, ok := settingByKey["collation_server"]; ok {
		options := optionsFor(d, s.Flavor, s.Series)
		for _, opt := range options {
			if opt == collation {
				return nil
			}
		}
		return fmt.Errorf("排序规则 %s 在 %s %s 上不存在，可选：%s",
			collation, s.Flavor, s.Series, strings.Join(options, " / "))
	}
	return nil
}

// applyLive 对 reload 级参数执行 SET GLOBAL。
func (s Spec) applyLive(values map[string]string) error {
	db, err := s.open("")
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, d := range settingDefs {
		if d.apply != "reload" {
			continue
		}
		raw, ok := values[d.key]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		// 参数名来自白名单，值已经过校验，直接拼是安全的
		if _, err := db.ExecContext(ctx, "SET GLOBAL "+d.key+" = "+quoteLiveValue(d, raw)); err != nil {
			return err
		}
	}
	return nil
}

// quoteLiveValue 给 SET GLOBAL 生成合法的字面量。
func quoteLiveValue(d settingDef, raw string) string {
	switch d.kind {
	case "int", "number", "size":
		return strings.TrimSpace(raw)
	default:
		return quoteLiteral(raw)
	}
}

var sizePattern = regexp.MustCompile(`^[0-9]+\s*(K|M|G|T)?$`)

// validateSetting 校验并归一化一个值，避免把坏值写进配置让实例起不来。
func validateSetting(d settingDef, flavor, series, value string) (string, error) {
	switch d.kind {
	case "int":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return "", fmt.Errorf("%s 必须是整数", d.label)
		}
		return value, nil
	case "number":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return "", fmt.Errorf("%s 必须是数字", d.label)
		}
		return value, nil
	case "size":
		if !sizePattern.MatchString(strings.ToUpper(value)) {
			return "", fmt.Errorf("%s 的格式不对，应为数字加单位，例如 128M", d.label)
		}
		return strings.ToUpper(value), nil
	case "enum":
		// 选项按引擎与版本过滤后再校验：utf8mb4_0900_ai_ci 这类值在 MariaDB / 5.7 上
		// 根本不存在，放行就等于把一个必然启动失败的值写进 my.cnf。
		options := optionsFor(d, flavor, series)
		for _, opt := range options {
			if value == opt {
				return value, nil
			}
		}
		return "", fmt.Errorf("%s 只能是 %s", d.label, strings.Join(options, " / "))
	case "bool":
		switch strings.ToLower(value) {
		case "on", "true", "1", "yes":
			return "ON", nil
		case "off", "false", "0", "no":
			return "OFF", nil
		}
		return "", fmt.Errorf("%s 只能是 ON 或 OFF", d.label)
	default:
		if len(value) > 200 || strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("%s 的值不合法", d.label)
		}
		return value, nil
	}
}

// humanValue 把 SHOW VARIABLES 的原始值翻译成用户能直接改的写法。
//
// innodb_buffer_pool_size 返回的是字节数，直接摆给用户十有八九会被理解错，
// 这里换算成 128M 这样的可读值。
func humanValue(d settingDef, value string) string {
	if d.kind == "number" {
		// long_query_time 之类返回 10.000000，直接摆给用户看很怪
		if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return value
	}
	if d.kind != "size" {
		return value
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n <= 0 {
		return value
	}
	for _, step := range []struct {
		size int64
		unit string
	}{
		{1 << 40, "T"},
		{1 << 30, "G"},
		{1 << 20, "M"},
		{1 << 10, "K"},
	} {
		if n%step.size == 0 {
			return strconv.FormatInt(n/step.size, 10) + step.unit
		}
	}
	return value
}

// cnfPath 返回实例的配置文件路径：容器里挂成 /etc/mysql/conf.d，重启即生效。
func (s Spec) cnfPath() string {
	return filepath.Join(s.ConfDir, "niudbm.cnf")
}

// EnsureCnf 保证配置文件存在。
//
// 新实例直接写一份"带默认值的完整配置"：只留一个 [mysqld] 的空文件让人打开就懵，
// 根本不知道这里能改什么。默认值来自 settingDefs，显式写出来不改变行为。
//
// 调用点放在 Spec 构造之后（mysqlSpec），这样"服务已存在"就一定"配置文件已存在"，
// 而不是等到用户第一次点保存参数才凭空出现一个只有两三行的文件。
func (s Spec) EnsureCnf() error {
	cnf := s.cnfPath()
	if _, err := os.Stat(cnf); err == nil {
		// 文件已存在也要过一遍：老实例里可能还留着 "slow.log" 这种相对路径
		return fixRelativeCnfPaths(cnf)
	}
	// 目录与文件都要"其他用户可读"：这个目录是 bind mount 进容器的，
	// 而容器里的 mysqld 以 mysql 用户运行，uid 随基础镜像变化（Oracle Linux 9 是 27，
	// Debian 是 999），宿主上的属主永远对不上。权限一收紧，容器启动时就是
	// `Can't read dir of '/etc/mysql/conf.d/' (OS errno 13 - Permission denied)`。
	if err := os.MkdirAll(filepath.Dir(cnf), 0o755); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(cnf), 0o755)
	return writeCnf(cnf, s.initialCnf())
}

// containerDataDir 是容器内的数据目录，与 httpapi 的 containerDataPath 保持一致。
const containerDataDir = "/var/lib/mysql"

// cnfDirKeys 是官方 entrypoint 会取出来 dirname 后参与 chown 的配置项
// （见 docker_create_db_directories）。它们的值只要不是绝对路径，
// dirname 就会得到 "."，entrypoint 随即对容器根目录 find + chown：
// 日志被 /sys 的只读报错刷屏，实例永远等不到就绪。
var cnfDirKeys = []string{
	"slow_query_log_file",
	"general_log_file",
	"pid_file",
	"keyring_file_data",
	"secure_file_priv",
}

// fixRelativeCnfPaths 把这些配置项里的相对路径改写成容器内的绝对路径。
//
// 每次启动都跑一遍（幂等），因为配置文件是用户数据，不能靠"删掉重建"来修；
// 而旧版本生成的文件里就写着 slow_query_log_file = slow.log。
func fixRelativeCnfPaths(cnfPath string) error {
	raw, err := os.ReadFile(cnfPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	changed := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "[") {
			continue
		}
		eq := strings.Index(t, "=")
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(t[:eq]))
		value := strings.TrimSpace(t[eq+1:])
		if !isCnfDirKey(key) {
			continue
		}
		// 空值交给 mysqld 自己的默认；NULL 是 secure_file_priv 的显式禁用
		if value == "" || strings.EqualFold(value, "NULL") || strings.HasPrefix(value, "/") {
			continue
		}
		lines[i] = key + " = " + path.Clean(path.Join(containerDataDir, value))
		changed = true
	}
	if !changed {
		return nil
	}
	if err := os.WriteFile(cnfPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}
	return os.Chmod(cnfPath, 0o644)
}

func isCnfDirKey(key string) bool {
	for _, k := range cnfDirKeys {
		if k == key {
			return true
		}
	}
	return false
}

// initialCnf 是这台实例可用的参数及其默认值。
func (s Spec) initialCnf() map[string]string {
	out := make(map[string]string, len(settingDefs))
	for _, d := range settingDefs {
		if d.def == "" || !d.applies(s.Flavor, s.Series) {
			continue
		}
		out[d.key] = d.def
	}
	return out
}

// CnfTemplate 渲染一份"应有的默认配置"文本，不落盘。
// 老实例的配置文件可能只有一两行（早期版本就是这么建的），前端用它在编辑器里补全。
func (s Spec) CnfTemplate() string {
	return renderCnf(s.initialCnf())
}

// mergeCnfDefaults 只补缺失的键，不覆盖已有值。
//
// 已有值可能来自用户的参数页设置，也可能是他手写进文件的，覆盖哪一种都是错的。
func mergeCnfDefaults(entries, defaults map[string]string) {
	for key, value := range defaults {
		if _, ok := entries[key]; !ok {
			entries[key] = value
		}
	}
}

// CnfMissing 列出当前文件里缺了哪些本可配置的参数。
// 前端据此提示"缺 N 项，可一键补全"，比按行数猜准得多。
func (s Spec) CnfMissing(content string) []string {
	have := parseCnf(content)
	missing := make([]string, 0)
	for _, d := range settingDefs {
		if d.def == "" || !d.applies(s.Flavor, s.Series) {
			continue
		}
		if _, ok := have[d.key]; !ok {
			missing = append(missing, d.key)
		}
	}
	return missing
}

// cnfGroups 决定配置文件里的段落顺序与分组注释：二十多个 key 平铺在一起最难读。
var cnfGroups = []struct {
	Title string
	Keys  []string
}{
	{"连接与超时", []string{"max_connections", "wait_timeout", "thread_cache_size", "max_allowed_packet"}},
	{"InnoDB：内存与刷盘", []string{
		"innodb_buffer_pool_size", "innodb_redo_log_capacity", "innodb_log_buffer_size",
		"innodb_flush_log_at_trx_commit", "innodb_io_capacity",
	}},
	{"临时表与缓冲", []string{
		"tmp_table_size", "max_heap_table_size", "sort_buffer_size", "read_buffer_size",
		"read_rnd_buffer_size", "join_buffer_size", "binlog_cache_size",
		"table_open_cache", "key_buffer_size",
	}},
	{"字符集与表名", []string{"character_set_server", "collation_server", "lower_case_table_names"}},
	{"慢查询", []string{"slow_query_log", "long_query_time", "slow_query_log_file"}},
	{"认证", []string{"default_authentication_plugin"}},
}

// renderCnf 把参数渲染成 my.cnf 文本：固定分组 + 说明性注释，其余 key 落到「其它」。
func renderCnf(entries map[string]string) string {
	var b strings.Builder
	b.WriteString("[mysqld]\n")
	b.WriteString("# 由牛库管家生成，挂载到容器的 /etc/mysql/conf.d；改完需要重启服务才生效。\n")
	b.WriteString("# 在「配置」页保存会整份重写本文件（手写的注释与其它段会丢）；\n")
	b.WriteString("# 界面没暴露的参数，请到「配置文件」页直接写进来。\n")

	written := make(map[string]bool, len(entries))
	for _, g := range cnfGroups {
		keys := make([]string, 0, len(g.Keys))
		for _, k := range g.Keys {
			if _, ok := entries[k]; ok {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		b.WriteString("\n# ---- " + g.Title + " ----\n")
		for _, k := range keys {
			b.WriteString(k + " = " + entries[k] + "\n")
			written[k] = true
		}
	}

	rest := make([]string, 0, len(entries))
	for k := range entries {
		if !written[k] {
			rest = append(rest, k)
		}
	}
	if len(rest) > 0 {
		sort.Strings(rest)
		b.WriteString("\n# ---- 其它 ----\n")
		for _, k := range rest {
			b.WriteString(k + " = " + entries[k] + "\n")
		}
	}
	return b.String()
}

// readCnf 读文件并解析 [mysqld] 段。
func readCnf(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return parseCnf(string(raw)), nil
}

// parseCnf 只解析 [mysqld] 段的 key=value，其它段与注释忽略。
// 单独拆出来是为了让"编辑器里的文本"也能走同一套解析（CnfMissing）。
func parseCnf(raw string) map[string]string {
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.ToLower(strings.Trim(t, "[]"))
			continue
		}
		if section != "mysqld" {
			continue
		}
		if idx := strings.Index(t, "="); idx > 0 {
			out[strings.TrimSpace(t[:idx])] = strings.TrimSpace(t[idx+1:])
		}
	}
	return out
}

// writeCnf 把 [mysqld] 段完整重写一遍，键按顺序排列，行为可预期。
func writeCnf(cnfPath string, entries map[string]string) error {
	// 写盘前统一规范：参数页/配置文件页都可能被填入相对路径
	normalizeCnfDirValues(entries)
	// 0644 而不是 0640：容器里的 mysqld 是另一个 uid，必须让它读得到
	if err := os.WriteFile(cnfPath, []byte(renderCnf(entries)), 0o644); err != nil {
		return err
	}
	return os.Chmod(cnfPath, 0o644)
}

// normalizeCnfDirValues 把会参与 entrypoint chown 的值规范成容器内绝对路径。
//
// 这条路和 fixRelativeCnfPaths 一进一出：前者管"写进去的值"，后者管"已经写坏的旧文件"。
func normalizeCnfDirValues(entries map[string]string) {
	for _, key := range cnfDirKeys {
		v := strings.TrimSpace(entries[key])
		if v == "" || strings.EqualFold(v, "NULL") || strings.HasPrefix(v, "/") {
			continue
		}
		entries[key] = path.Clean(path.Join(containerDataDir, v))
	}
}
