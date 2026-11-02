package mysqlctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Database 是一个逻辑库。界面上把它叫作"实例"：一个 MySQL 服务里可以放多个。
type Database struct {
	Name       string `json:"name"`
	Charset    string `json:"charset"`
	TableCount int    `json:"tableCount"`
	SizeBytes  int64  `json:"sizeBytes"`
	// Users 是在这个库上有授权的业务账号名：列表里"授权用户"一列直接用，
	// 不用前端再拉一遍用户表自己拼。
	Users []string `json:"users"`
}

// 库名规则与 MySQL 自身一致：字母或下划线开头，最长 64。
var dbNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// systemSchemas 是引擎自带的库，既不展示也不允许删。
var systemSchemas = map[string]bool{
	"mysql":              true,
	"information_schema": true,
	"performance_schema": true,
	"sys":                true,
}

// Charset 是一个可用的库字符集。
type Charset struct {
	Value string `json:"value"`
	Label string `json:"label"`
	// Collation 是这个字符集上跨版本稳定、可直接用于 CREATE DATABASE 的排序规则
	Collation   string `json:"collation"`
	Recommended bool   `json:"recommended,omitempty"`
}

// charsetLabels 给常用字符集一句人话说明；没列到的直接显示字符集名。
var charsetLabels = map[string]string{
	"utf8mb4": "推荐，完整 Unicode，支持 emoji",
	"utf8mb3": "3 字节 UTF-8，旧程序兼容（原名 utf8）",
	"utf8":    "3 字节 UTF-8，旧程序兼容",
	"gb18030": "简体中文超集，含生僻字",
	"gbk":     "简体中文，旧系统常用",
	"gb2312":  "简体中文，更早的标准",
	"big5":    "繁体中文",
	"latin1":  "西欧语言",
	"latin2":  "中欧语言",
	"ascii":   "纯 ASCII",
	"binary":  "二进制，不做字符集转换",
	"utf16":   "UTF-16",
	"utf16le": "UTF-16 小端",
	"utf32":   "UTF-32",
	"ucs2":    "UCS-2，只能存 BMP 字符",
	"sjis":    "日文 Shift-JIS",
	"cp932":   "日文 Windows",
	"ujis":    "日文 EUC-JP",
	"euckr":   "韩文 EUC-KR",
	"tis620":  "泰文",
	"greek":   "希腊语",
	"hebrew":  "希伯来语",
}

// charsetOrder 决定下拉里的顺序：常用的排前面，其余按名字排。
var charsetOrder = []string{
	"utf8mb4", "utf8mb3", "utf8", "gb18030", "gbk", "gb2312", "big5",
	"latin1", "latin2", "ascii", "binary", "utf16", "utf16le", "utf32", "ucs2",
	"sjis", "cp932", "ujis", "euckr", "tis620", "greek", "hebrew",
}

// Charsets 列出这台实例真正支持的字符集。
//
// 不写死清单：不同实现/版本的字符集集合并不一样（gb18030 到 5.7.4 才有，
// utf8mb3 这个名字 8.0.28 才出现，MariaDB 的 uca1400 系 MySQL 又没有）。
// 直接读 information_schema，界面列出来的就是这台实例现在真能建出来的。
func (s Spec) Charsets() ([]Charset, error) {
	db, err := s.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT CHARACTER_SET_NAME, COLLATION_NAME, IS_DEFAULT FROM information_schema.COLLATIONS`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	grouped := map[string][]collationRow{}
	for rows.Next() {
		var cs, coll, isDefault string
		if err := rows.Scan(&cs, &coll, &isDefault); err != nil {
			return nil, translate(err)
		}
		grouped[cs] = append(grouped[cs], collationRow{name: coll, isDefault: strings.EqualFold(isDefault, "Yes")})
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	names := make([]string, 0, len(grouped))
	for cs := range grouped {
		names = append(names, cs)
	}
	sort.Slice(names, func(i, j int) bool {
		ri, rj := charsetRank(names[i]), charsetRank(names[j])
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})

	out := make([]Charset, 0, len(names))
	for _, cs := range names {
		pick := pickCollation(grouped[cs])
		if pick == "" {
			continue
		}
		label := cs
		if tip, ok := charsetLabels[cs]; ok {
			label = cs + " · " + tip
		}
		out = append(out, Charset{
			Value:       cs,
			Label:       label,
			Collation:   pick,
			Recommended: cs == "utf8mb4",
		})
	}
	return out, nil
}

type collationRow struct {
	name      string
	isDefault bool
}

func charsetRank(name string) int {
	for i, v := range charsetOrder {
		if v == name {
			return i
		}
	}
	return len(charsetOrder) + 1
}

// pickCollation 挑一个跨版本稳定的排序规则。
//
// 优先 <cs>_general_ci（5.x 就有），其次该字符集的默认值，最后 _bin。
// 不直接用默认值是因为它随版本变：utf8mb4 在 8.0 默认是 0900_ai_ci、在 5.7 是
// general_ci，同一套建库参数在两代实例上会得到不同的排序行为。
func pickCollation(list []collationRow) string {
	if len(list) == 0 {
		return ""
	}
	general, def, bin := "", "", ""
	for _, c := range list {
		// binary 字符集自己就叫 binary，没有 _general_ci / _bin 后缀
		if general == "" && (strings.HasSuffix(c.name, "_general_ci") || c.name == "binary") {
			general = c.name
		}
		if def == "" && c.isDefault {
			def = c.name
		}
		if bin == "" && strings.HasSuffix(c.name, "_bin") {
			bin = c.name
		}
	}
	switch {
	case general != "":
		return general
	case def != "":
		return def
	default:
		return bin
	}
}

// collationFor 查这个字符集在当前实例上的可用排序规则。
//
// 老版本这里是写死的四个字符集映射；引擎自己的字符集表才是唯一事实来源。
func (s Spec) collationFor(charset string) (string, error) {
	db, err := s.open("")
	if err != nil {
		return "", err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT COLLATION_NAME, IS_DEFAULT FROM information_schema.COLLATIONS WHERE CHARACTER_SET_NAME = ?`, charset)
	if err != nil {
		return "", translate(err)
	}
	defer rows.Close()

	list := make([]collationRow, 0, 8)
	for rows.Next() {
		var coll, isDefault string
		if err := rows.Scan(&coll, &isDefault); err != nil {
			return "", translate(err)
		}
		list = append(list, collationRow{name: coll, isDefault: strings.EqualFold(isDefault, "Yes")})
	}
	if err := rows.Err(); err != nil {
		return "", translate(err)
	}
	pick := pickCollation(list)
	if pick == "" {
		return "", fmt.Errorf("这台实例不支持字符集 %s，请从列表里选", charset)
	}
	return pick, nil
}

// ValidDBName 校验新建库名。
func ValidDBName(name string) error {
	if !dbNamePattern.MatchString(name) {
		return errors.New("库名只能包含字母、数字、下划线，且以字母或下划线开头（最长 64）")
	}
	if systemSchemas[strings.ToLower(name)] {
		return errors.New(name + " 是引擎系统库，不能使用")
	}
	return nil
}

// ListDatabases 读 information_schema，把真实存在的库列出来。
//
// 不维护自己的库表：库可能在 SQL 控制台、脚本、别的客户端里被建出来，
// 以引擎为准才不会出现"界面里没有但实际存在"的鬼库。
func (s Spec) ListDatabases() ([]Database, error) {
	db, err := s.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT SCHEMA_NAME, DEFAULT_CHARACTER_SET_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]Database, 0, 4)
	for rows.Next() {
		var name, charset string
		if err := rows.Scan(&name, &charset); err != nil {
			return nil, translate(err)
		}
		if systemSchemas[strings.ToLower(name)] {
			continue
		}
		out = append(out, Database{Name: name, Charset: charset})
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}
	if len(out) == 0 {
		return out, nil
	}

	// 体积与表数单独查一次：information_schema.TABLES 在 5.x 上是临时表，
	// 用相关子查询会退化成每个库扫一遍全表。
	stats, err := s.databaseStats(ctx, db)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if st, ok := stats[out[i].Name]; ok {
			out[i].TableCount = st.tables
			out[i].SizeBytes = st.bytes
		}
	}

	// 授权用户聚合成"库 → 账号名列表"：列表一列能直接显示出来，点开再看明细。
	// 取不到就留空，不影响库列表本身。
	if roles, rerr := s.Roles(); rerr == nil {
		byDB := make(map[string][]string, len(out))
		for _, r := range roles {
			for _, g := range r.Grants {
				key := strings.ToLower(g.Database)
				byDB[key] = append(byDB[key], r.Name)
			}
		}
		for i := range out {
			names := byDB[strings.ToLower(out[i].Name)]
			sort.Strings(names)
			out[i].Users = names
		}
	}
	return out, nil
}

type dbStat struct {
	tables int
	bytes  int64
}

func (s Spec) databaseStats(ctx context.Context, db *sql.DB) (map[string]dbStat, error) {
	rows, err := db.QueryContext(ctx, `SELECT TABLE_SCHEMA, COUNT(*),
			COALESCE(SUM(IFNULL(DATA_LENGTH, 0) + IFNULL(INDEX_LENGTH, 0)), 0)
		FROM information_schema.TABLES GROUP BY TABLE_SCHEMA`)
	if err != nil {
		// 统计失败不该让整个列表空掉：库名与字符集已经拿到了，够界面用
		return map[string]dbStat{}, nil
	}
	defer rows.Close()

	out := map[string]dbStat{}
	for rows.Next() {
		var name string
		var st dbStat
		if err := rows.Scan(&name, &st.tables, &st.bytes); err != nil {
			return out, nil
		}
		out[name] = st
	}
	return out, nil
}

// CreateDatabase 建一个逻辑库。
func (s Spec) CreateDatabase(name, charset string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	if charset == "" {
		charset = "utf8mb4"
	}
	collation, err := s.collationFor(charset)
	if err != nil {
		return err
	}
	stmt := "CREATE DATABASE " + quoteIdent(name) +
		" CHARACTER SET " + charset + " COLLATE " + collation
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("创建库失败：%w", translate(err))
		}
		return nil
	})
}

// DropDatabase 删库。
//
// 不再有"默认库"这种删不掉的东西：服务建出来是空的，用户自己建的每个库都能删。
// 系统库仍然拒绝——它们属于引擎本身，删了 information_schema 会坏掉。
func (s Spec) DropDatabase(name string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	if systemSchemas[strings.ToLower(name)] {
		return errors.New("引擎自带的库不能删除")
	}
	stmt := "DROP DATABASE " + quoteIdent(name)
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("删除库失败：%w", translate(err))
		}
		return nil
	})
}
