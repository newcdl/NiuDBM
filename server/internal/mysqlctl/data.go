package mysqlctl

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Tables 列出指定库里的表。
func (s Spec) Tables(dbName string) ([]string, error) {
	db, err := s.open(dbName)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]string, 0, 16)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, translate(err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ExportCSV 把指定库的一张表导出为 CSV。
func (s Spec) ExportCSV(out io.Writer, dbName, table string, limit int) (int64, error) {
	db, err := s.open(dbName)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		"SELECT * FROM "+quoteIdent(table)+" LIMIT "+strconv.Itoa(limit))
	if err != nil {
		return 0, translate(err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, translate(err)
	}

	w := csv.NewWriter(out)
	if err := w.Write(cols); err != nil {
		return 0, err
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var n int64
	record := make([]string, len(cols))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, translate(err)
		}
		for i, v := range vals {
			record[i] = cellText(v)
		}
		if err := w.Write(record); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, translate(err)
	}
	w.Flush()
	return n, w.Error()
}

// ImportCSV 从 CSV 导入数据。第一行是列名，其余逐行插入；整体一个事务，出错全回滚。
func (s Spec) ImportCSV(dbName, table string, r io.Reader) (int64, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return 0, fmt.Errorf("读取 CSV 表头失败：%w", err)
	}
	if len(header) == 0 {
		return 0, fmt.Errorf("CSV 表头为空")
	}

	db, err := s.open(dbName)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, translate(err)
	}
	defer tx.Rollback()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(header)), ",")
	stmt := "INSERT INTO " + quoteIdent(table) + " (" + quoteCols(header) + ") VALUES (" + placeholders + ")"

	var n int64
	for {
		record, rerr := reader.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return n, fmt.Errorf("第 %d 行解析失败：%w", n+2, rerr)
		}
		args := make([]any, len(record))
		for i := range record {
			args[i] = record[i]
		}
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return n, fmt.Errorf("第 %d 行写入失败：%w", n+2, translate(err))
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return n, translate(err)
	}
	return n, nil
}

// ExecScript 在指定库里执行一段 SQL 脚本，返回语句条数（用于审计与界面提示）。
func (s Spec) ExecScript(dbName, script string) (int, error) {
	db, err := s.open(dbName)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if _, err := db.ExecContext(ctx, script); err != nil {
		return 0, translate(err)
	}
	return countStatements(script), nil
}

// cellText 把驱动返回值统一成 CSV 里的文本。
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(x)
	}
}

func quoteCols(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = quoteIdent(c)
	}
	return strings.Join(parts, ", ")
}

// countStatements 粗略统计语句条数：跳过引号与注释内的分号。
func countStatements(script string) int {
	count := 0
	inSingle, inDouble, inBacktick := false, false, false
	inLineComment, inBlockComment := false, false
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
			}
			continue
		case inBlockComment:
			if c == '*' && i+1 < len(script) && script[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		case inSingle:
			if c == '\\' {
				i++
				continue
			}
			if c == '\'' {
				if i+1 < len(script) && script[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		case inDouble:
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		case inBacktick:
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		case '-':
			if i+2 < len(script) && script[i+1] == '-' && script[i+2] == ' ' {
				inLineComment = true
				i += 2
			}
		case '#':
			inLineComment = true
		case '/':
			if i+1 < len(script) && script[i+1] == '*' {
				inBlockComment = true
				i++
			}
		case ';':
			count++
		}
	}
	return count
}
