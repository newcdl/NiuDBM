package pgctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Tables 列出一个库里可导入导出的表（只看 public）。
func (s Spec) Tables(dbName string) ([]string, error) {
	db, err := s.open(dbName)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := make([]string, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, translate(err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ExportCSV 用 COPY 把一张表导成 CSV。
//
// 走容器里的 psql 而不是驱动逐行读：COPY 是服务端生成，几十万行也不会把
// 内存堆在应用进程里；格式用 csv + HEADER，与 MySQL 那套导出的文件一致。
func (s Spec) ExportCSV(out io.Writer, dbName, table string, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	if err := ValidTableName(table); err != nil {
		return 0, err
	}
	rows, err := s.countRows(dbName, table)
	if err != nil {
		return 0, err
	}
	if rows > 0 && int64(limit) > rows {
		limit = int(rows)
	}
	counter := &countingWriter{w: out}
	copyStmt := fmt.Sprintf(`COPY (SELECT * FROM %s LIMIT %d) TO STDOUT WITH (FORMAT csv, HEADER true)`,
		quoteIdent(table), limit)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, counter, nil,
		map[string]string{"PGPASSWORD": s.Password},
		"psql", "-U", SuperUser, "-d", dbName, "-v", "ON_ERROR_STOP=1", "-c", copyStmt); err != nil {
		return int64(counter.dataRows()), fmt.Errorf("导出失败：%w", err)
	}
	return int64(counter.dataRows()), nil
}

// ImportCSV 把 CSV 导入到指定表。
//
// 与 MySQL 那边一致：先 DELETE 再 COPY 太危险，这里只做追加；
// HEADER true 让列名对上，列顺序不同也不会串。
func (s Spec) ImportCSV(dbName, table string, r io.Reader) (int64, error) {
	if err := ValidTableName(table); err != nil {
		return 0, err
	}
	before, _ := s.countRows(dbName, table)
	copyStmt := fmt.Sprintf(`COPY %s FROM STDIN WITH (FORMAT csv, HEADER true)`, quoteIdent(table))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, io.Discard, r,
		map[string]string{"PGPASSWORD": s.Password},
		"psql", "-U", SuperUser, "-d", dbName, "-v", "ON_ERROR_STOP=1", "-c", copyStmt); err != nil {
		return 0, fmt.Errorf("导入失败：%w", err)
	}
	after, err := s.countRows(dbName, table)
	if err != nil || after < before {
		return 0, nil
	}
	return after - before, nil
}

// ExecScript 执行一段 SQL 脚本，返回执行成功的语句数。
//
// psql 的 ON_ERROR_STOP=1 保证出错即停；语句数按分号粗算，
// 与 MySQL 那边一致——只是给用户一个"跑了多少条"的体感。
func (s Spec) ExecScript(dbName, script string) (int, error) {
	if strings.TrimSpace(script) == "" {
		return 0, errors.New("脚本内容为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.Docker.Client.ExecStream(ctx, s.Docker.Container, io.Discard, strings.NewReader(script),
		map[string]string{"PGPASSWORD": s.Password},
		"psql", "-U", SuperUser, "-d", dbName, "-v", "ON_ERROR_STOP=1", "-q", "-f", "-"); err != nil {
		return 0, fmt.Errorf("执行脚本失败：%w", err)
	}
	return countStatements(script), nil
}

// ValidTableName 校验表名：表名来自前端，拼进 COPY 之前必须挡住引号与分号。
func ValidTableName(name string) error {
	if name == "" {
		return errors.New("表名不能为空")
	}
	for _, r := range name {
		ok := r == '_' || r == '$' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !ok {
			return fmt.Errorf("表名 %s 含不支持的字符，请勿手工输入", name)
		}
	}
	return nil
}

func (s Spec) countRows(dbName, table string) (int64, error) {
	db, err := s.open(dbName)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdent(table)).Scan(&n); err != nil {
		return 0, translate(err)
	}
	return n, nil
}

// countingWriter 数导出了多少字节与多少行。
//
// 行数比字节数有用：界面上显示"导出 1234 行"，而字节数用户无法判断多少。
// 表头也算一行，所以最终返回时要减掉。
type countingWriter struct {
	w    io.Writer
	n    int
	rows int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	for _, b := range p {
		if b == '\n' {
			c.rows++
		}
	}
	return n, err
}

// dataRows 是去掉表头后的数据行数。
func (c *countingWriter) dataRows() int {
	if c.rows > 0 {
		return c.rows - 1
	}
	return 0
}

// countStatements 粗算脚本里的语句条数：去掉注释后按分号数。
func countStatements(script string) int {
	n := 0
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		for _, part := range strings.Split(trimmed, ";") {
			if strings.TrimSpace(part) != "" {
				n++
			}
		}
	}
	return n
}
