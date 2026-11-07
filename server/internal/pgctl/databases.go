package pgctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"niudbm/server/internal/mysqlctl"
)

// 一个"服务"（PostgreSQL 容器）下的多个"数据库"。
//
// 返回类型直接复用 mysqlctl.Database：库名 + 字符集 + 表数 + 体积 + 授权用户，
// 两个引擎的界面结构本来就一样，前端不该为此写两套渲染。

// 库名规则按 PG 的实际约束放宽：允许大写与短横线（PG 建库时会原样保留，
// 只是查询时要加双引号），但挡掉引号、空白与反斜杠这些会破坏语句的字符。
var dbNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$-]{0,62}$`)

// systemDatabases 是引擎自带的库，既不展示也不允许删。
var systemDatabases = map[string]bool{
	"postgres":  true,
	"template0": true,
	"template1": true,
}

// ValidDBName 校验新建库名。
func ValidDBName(name string) error {
	if !dbNamePattern.MatchString(name) {
		return errors.New("库名只能包含字母、数字、下划线、短横线，且以字母或下划线开头（最长 63）")
	}
	if systemDatabases[strings.ToLower(name)] {
		return errors.New(name + " 是引擎自带或保留的库名，不能使用")
	}
	return nil
}

// encodingLabels 给常用编码一句人话说明；没列到的直接显示编码名。
var encodingLabels = map[string]string{
	"UTF8":      "推荐，完整 Unicode，支持 emoji",
	"EUC_CN":    "简体中文（GB2312）",
	"EUC_TW":    "繁体中文",
	"EUC_JP":    "日文 EUC-JP",
	"EUC_KR":    "韩文 EUC-KR",
	"LATIN1":    "西欧语言",
	"WIN1252":   "西欧语言（Windows）",
	"SQL_ASCII": "不做编码转换，按字节存（不推荐）",
}

// Charsets 列出这个实例支持的库编码。
//
// 不写死清单：PG 的编码集合由编译选项决定，问引擎自己最准
// （pg_encoding_to_char 对不存在的编号返回空串）。
func (s Spec) Charsets() ([]mysqlctl.Charset, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 只列"能当服务端编码"的那些：pg_encoding_to_char 认识 0..41，
	// 但 PG 文档里 35 之后（SJIS/BIG5/GBK/UHC/GB18030/JOHAB/…）只作为
	// 客户端编码支持，拿来 CREATE DATABASE 会直接报 "not a valid encoding name"。
	// MULE_INTERNAL(7) 是内部编码，也排除。
	rows, err := db.QueryContext(ctx,
		`SELECT pg_encoding_to_char(i) FROM generate_series(0, 34) AS i
		 WHERE pg_encoding_to_char(i) <> '' AND i <> 7 ORDER BY 1`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]mysqlctl.Charset, 0, 12)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, translate(err)
		}
		label := name
		if tip, ok := encodingLabels[name]; ok {
			label = name + " · " + tip
		}
		out = append(out, mysqlctl.Charset{
			Value: name,
			Label: label,
			// PG 的排序规则属于库级 locale，建库时按服务器默认继承；
			// 这里不编造一个 collation，前端只展示编码。
			Recommended: name == "UTF8",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}
	// 常用编码排前面，其余按名字
	rank := func(name string) int {
		order := []string{"UTF8", "EUC_CN", "EUC_TW", "EUC_JP", "EUC_KR", "LATIN1", "WIN1252"}
		for i, v := range order {
			if v == name {
				return i
			}
		}
		return len(order) + 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Value), rank(out[j].Value)
		if ri != rj {
			return ri < rj
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// ListDatabases 读 pg_database，把真实存在的库列出来。
//
// 库可能在 psql、脚本、别的客户端里被建出来，以引擎为准才不会出现
// "界面里没有但实际存在"的鬼库。
func (s Spec) ListDatabases() ([]mysqlctl.Database, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT datname, pg_encoding_to_char(encoding), pg_database_size(datname)
		FROM pg_database
		WHERE datallowconn AND NOT datistemplate
		ORDER BY datname`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]mysqlctl.Database, 0, 4)
	for rows.Next() {
		var name, encoding string
		var size int64
		if err := rows.Scan(&name, &encoding, &size); err != nil {
			return nil, translate(err)
		}
		if systemDatabases[strings.ToLower(name)] {
			continue
		}
		out = append(out, mysqlctl.Database{Name: name, Charset: encoding, SizeBytes: size})
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	// 表数只能连到库里数（PG 不能跨库查）。连不上就留 0，不影响库列表本身。
	for i := range out {
		out[i].TableCount = s.countTablesIn(out[i].Name)
	}
	// 授权账号聚合：列表里"授权用户"一列直接用
	if roles, rerr := s.Roles(); rerr == nil {
		for i := range out {
			names := make([]string, 0, 4)
			for _, r := range roles {
				for _, g := range r.Grants {
					if strings.EqualFold(g.Database, out[i].Name) {
						names = append(names, r.Name)
						break
					}
				}
			}
			sort.Strings(names)
			out[i].Users = names
		}
	}
	return out, nil
}

// countTablesIn 连到指定库数表；失败返回 0。
func (s Spec) countTablesIn(dbName string) int {
	db, err := s.open(dbName)
	if err != nil {
		return 0
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND table_type = 'BASE TABLE'`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// CreateDatabase 建一个库。
//
// encoding 为空时按服务器默认（模板库的编码）。指定非 UTF8 编码时必须显式给
// LC_COLLATE/LC_CTYPE='C' 且用 template0：否则会报
// "encoding GBK does not match locale"，看起来像"这个编码不支持"。
func (s Spec) CreateDatabase(name, encoding string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	encoding = strings.ToUpper(strings.TrimSpace(encoding))
	stmt := "CREATE DATABASE " + quoteIdent(name)
	if encoding == "" || encoding == "UTF8" {
		if encoding != "" {
			stmt += " ENCODING 'UTF8'"
		}
		stmt += " TEMPLATE template0"
	} else {
		stmt += " ENCODING " + quoteLiteral(encoding) +
			" LC_COLLATE 'C' LC_CTYPE 'C' TEMPLATE template0"
	}
	return s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			msg := translate(err).Error()
			if strings.Contains(msg, "not a valid encoding name") {
				return fmt.Errorf("这个实例不支持把 %s 作为库编码（PostgreSQL 里 GBK / BIG5 / GB18030 只能作客户端编码）；中文内容用 UTF8 即可", encoding)
			}
			return fmt.Errorf("创建库失败：%s", msg)
		}
		return nil
	})
}

// DropDatabase 删库。引擎自带的库拒绝删除。
func (s Spec) DropDatabase(name string) error {
	if err := ValidDBName(name); err != nil {
		return err
	}
	if systemDatabases[strings.ToLower(name)] {
		return errors.New("引擎自带的库不能删除")
	}
	return s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		// 先断开其余连接：有人连着时 DROP DATABASE 会一直报"正在被访问"
		_, _ = db.ExecContext(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			 WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := db.ExecContext(ctx, "DROP DATABASE "+quoteIdent(name)); err != nil {
			return fmt.Errorf("删除库失败：%w", translate(err))
		}
		return nil
	})
}

// withConn 连到指定维护库执行一段操作。
func (s Spec) withConn(dbName string, fn func(ctx context.Context, db *sql.DB) error) error {
	db, err := s.open(dbName)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return fn(ctx, db)
}
