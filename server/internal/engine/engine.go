package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// 驱动名由 modernc.org/sqlite 注册，纯 Go 实现，无需 CGO 即可交叉编译。
const driverName = "sqlite"

// ErrNotFound 表示实例文件不存在。
var ErrNotFound = errors.New("实例文件不存在")

// Options 控制单次执行的边界。
type Options struct {
	Timeout time.Duration
	MaxRows int
}

// DefaultOptions 给控制台用的保守默认值：5 秒、最多 200 行。
func DefaultOptions() Options {
	return Options{Timeout: 5 * time.Second, MaxRows: 200}
}

// Column 是一列的结构信息。
type Column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"notNull"`
	PK      bool   `json:"pk"`
	Default string `json:"default,omitempty"`
}

// Table 是一张表或视图。
type Table struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // table / view
	Rows    int64    `json:"rows"` // -1 表示未取到（超时或视图）
	Columns []Column `json:"columns"`
}

// Result 是一次执行的结果，查询与写入共用同一结构，前端只需按 kind 分支。
type Result struct {
	Kind         string   `json:"kind"` // query / exec
	Statement    string   `json:"statement"`
	Columns      []string `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`
	RowCount     int      `json:"rowCount"`
	RowsAffected int64    `json:"rowsAffected"`
	Truncated    bool     `json:"truncated"`
	ElapsedMs    int64    `json:"elapsedMs"`
}

// mode 取值：ro（只读）/ rw（读写，文件必须已存在）/ rwc（读写，不存在则创建）。
//
// 只读模式绝不能带 journal_mode(pragma)：切 WAL 是一次写操作，
// 在 mode=ro 下会直接报 “attempt to write a readonly database”。
func dsn(path, mode string) string {
	base := "file:" + path +
		"?mode=" + mode +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)"
	if mode == "ro" {
		return base
	}
	return base + "&_pragma=journal_mode(WAL)"
}

func open(path string, readonly bool) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w：%s", ErrNotFound, path)
		}
		return nil, err
	}
	mode := "rw"
	if readonly {
		mode = "ro"
	}
	db, err := sql.Open(driverName, dsn(path, mode))
	if err != nil {
		return nil, err
	}
	// SQLite 同一文件只允许一个写者；连接数压到 1，避免自己跟自己抢锁
	db.SetMaxOpenConns(1)
	return db, nil
}

// Create 建一个空库并写入初始结构（seedSQL 可为空）。
//
// 名字与路径的关系在这里刻意解耦：文件路径由上层用 ULID 生成，
// 用户的"实例名"永远不参与拼路径，从根上避免路径穿越。
func Create(path, seedSQL string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("文件已存在：%s", path)
	}

	db, err := sql.Open(driverName, dsn(path, "rwc"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("初始化数据库失败：%w", err)
	}

	seed := strings.TrimSpace(seedSQL)
	if seed == "" {
		return nil
	}
	stmts, err := SplitStatements(seed)
	if err != nil {
		return fmt.Errorf("初始化 SQL 无法解析：%w", err)
	}
	for _, stmt := range stmts {
		p, err := PrepareSQL(stmt)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, p.SQL); err != nil {
			return fmt.Errorf("执行初始化 SQL 失败：%w", err)
		}
	}
	return nil
}

// Execute 执行一条已校验的语句。
func Execute(path string, p Prepared, opts Options) (Result, error) {
	start := time.Now()
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 200
	}

	db, err := open(path, p.Kind == KindQuery)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	res := Result{Statement: p.SQL, Kind: "query"}
	if p.Kind == KindExec {
		res.Kind = "exec"
	}

	if p.Kind == KindExec {
		out, err := db.ExecContext(ctx, p.SQL)
		if err != nil {
			return Result{}, err
		}
		res.RowsAffected, _ = out.RowsAffected()
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res, nil
	}

	rows, err := db.QueryContext(ctx, p.SQL)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	res.Columns = cols

	for rows.Next() {
		if res.RowCount >= opts.MaxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		for i, v := range vals {
			vals[i] = normalize(v)
		}
		res.Rows = append(res.Rows, vals)
		res.RowCount++
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// normalize 把驱动返回的字节切片转成字符串，其余保持原样交给 JSON 编码。
func normalize(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

// Inspect 读取库结构。表数量与单表行数都有上限，避免在大库上把页面拖死。
func Inspect(path string) ([]Table, error) {
	db, err := open(path, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT name, type FROM sqlite_master
		 WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%'
		 ORDER BY name LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Type); err != nil {
			return nil, err
		}
		t.Rows = -1
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range tables {
		cols, err := columnsOf(ctx, db, tables[i].Name)
		if err != nil {
			return nil, err
		}
		tables[i].Columns = cols
		if tables[i].Type == "table" {
			tables[i].Rows = countRows(ctx, db, tables[i].Name)
		}
	}
	return tables, nil
}

func columnsOf(ctx context.Context, db *sql.DB, table string) ([]Column, error) {
	q := `SELECT name, type, "notnull", pk, dflt_value FROM pragma_table_info(` + quoteLiteral(table) + `)`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := []Column{}
	for rows.Next() {
		var (
			c       Column
			notNull int
			pk      int
			dflt    sql.NullString
		)
		if err := rows.Scan(&c.Name, &c.Type, &notNull, &pk, &dflt); err != nil {
			return nil, err
		}
		c.NotNull = notNull != 0
		c.PK = pk != 0
		if dflt.Valid {
			c.Default = dflt.String
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func countRows(ctx context.Context, db *sql.DB, table string) int64 {
	sub, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var n int64
	if err := db.QueryRowContext(sub, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&n); err != nil {
		return -1
	}
	return n
}

// Backup 用 VACUUM INTO 生成一致性快照：拷贝过程中不会读到半写状态。
func Backup(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	_ = os.Remove(dst)

	db, err := open(src, false)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if _, err := db.ExecContext(ctx, `VACUUM INTO `+quoteLiteral(dst)); err != nil {
		return fmt.Errorf("生成备份失败：%w", err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		return fmt.Errorf("备份文件未生成或为空")
	}
	return nil
}

// Verify 对文件做一次完整性检查，用于备份后与恢复前的双重把关。
func Verify(path string) error {
	db, err := open(path, true)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var verdict string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&verdict); err != nil {
		return fmt.Errorf("完整性检查失败：%w", err)
	}
	if !strings.EqualFold(verdict, "ok") {
		return fmt.Errorf("完整性检查未通过：%s", verdict)
	}
	return nil
}

// Restore 用备份覆盖实例文件。
//
// 顺序刻意如此：先校验备份 → 复制到同目录临时文件 → 清掉目标库的 -wal/-shm →
// 原子 rename。任何一步失败，原库都还是原样。
func Restore(backupPath, dataPath string) error {
	if err := Verify(backupPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o750); err != nil {
		return err
	}

	tmp := dataPath + ".restore-" + fmt.Sprint(time.Now().UnixNano())
	if err := copyFile(backupPath, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := Verify(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(dataPath + "-wal")
	_ = os.Remove(dataPath + "-shm")
	if err := os.Rename(tmp, dataPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换实例文件失败：%w", err)
	}
	return nil
}

// Size 返回实例占用（主文件 + WAL + SHM）。
func Size(path string) int64 {
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
