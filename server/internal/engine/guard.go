// Package engine 负责实例文件的真实读写：建库、执行 SQL、探测结构、备份与恢复。
//
// 这一层不碰台账（那是 store 的事），只对"一个 SQLite 文件"负责，
// 因此可以独立测试：给一个临时目录就能跑完整生命周期。
package engine

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrEmptySQL 表示没有可执行内容。
	ErrEmptySQL = errors.New("SQL 为空")
	// ErrMultiStatement 表示一次提交里有多条语句。
	ErrMultiStatement = errors.New("一次只能执行一条语句，请拆开提交")
	// ErrForbidden 表示命中控制台黑名单。
	ErrForbidden = errors.New("该语句在控制台中不允许执行")
)

// Kind 决定用 Query 还是 Exec。
type Kind int

const (
	// KindQuery 返回结果集。
	KindQuery Kind = iota
	// KindExec 只返回影响行数。
	KindExec
)

// Prepared 是校验过的单条语句。
type Prepared struct {
	SQL     string
	Kind    Kind
	Keyword string
}

// 控制台里明确禁止的写法。进程内执行任意 SQL 意味着能碰宿主文件系统，
// 所以 ATTACH/DETACH 与扩展加载必须挡住；这些能力属于后续"导入导出"专用通道。
var forbiddenKeywords = []string{"ATTACH", "DETACH"}

var queryKeywords = map[string]bool{
	"SELECT":  true,
	"WITH":    true,
	"EXPLAIN": true,
	"PRAGMA":  true,
	"VALUES":  true,
}

// SplitStatements 按分号切分语句，忽略字符串与注释里的分号。
// 控制台仍然是单条执行，但初始化脚本允许多条（贴整段 schema 是很自然的用法）。
func SplitStatements(input string) ([]string, error) {
	_, stmts, err := scan(input)
	return stmts, err
}

// PrepareSQL 校验并归一化一条控制台语句。
func PrepareSQL(input string) (Prepared, error) {
	statements, err := SplitStatements(input)
	if err != nil {
		return Prepared{}, err
	}
	code, _, err := scan(input)
	if err != nil {
		return Prepared{}, err
	}
	if len(statements) == 0 {
		return Prepared{}, ErrEmptySQL
	}
	if len(statements) > 1 {
		return Prepared{}, ErrMultiStatement
	}

	stmt := statements[0]
	keyword := firstKeyword(code)
	if keyword == "" {
		return Prepared{}, ErrEmptySQL
	}

	upperCode := strings.ToUpper(code)
	for _, bad := range forbiddenKeywords {
		if keyword == bad || strings.Contains(upperCode, " "+bad+" ") || strings.HasPrefix(upperCode, bad+" ") {
			return Prepared{}, fmt.Errorf("%w：%s", ErrForbidden, bad)
		}
	}
	if strings.Contains(upperCode, "LOAD_EXTENSION") {
		return Prepared{}, fmt.Errorf("%w：load_extension", ErrForbidden)
	}
	if strings.Contains(upperCode, "WRITABLE_SCHEMA") {
		return Prepared{}, fmt.Errorf("%w：writable_schema", ErrForbidden)
	}
	if strings.Contains(upperCode, "VACUUM INTO") || strings.Contains(upperCode, "VACUUM\nINTO") {
		return Prepared{}, fmt.Errorf("%w：VACUUM INTO（请用备份功能）", ErrForbidden)
	}

	kind := KindExec
	if queryKeywords[keyword] {
		kind = KindQuery
	}
	return Prepared{SQL: stmt, Kind: kind, Keyword: keyword}, nil
}

// scan 遍历 SQL，返回"去掉字符串与注释后的代码文本"和按分号切出的非空语句。
//
// 这样做而不是简单 strings.Split：字符串常量里出现分号（如 'a;b'）是常见写法，
// 直接切会把一条合法语句误判成多条。
func scan(input string) (code string, statements []string, err error) {
	var (
		codeBuf strings.Builder
		cur     strings.Builder
		trimmed = strings.TrimSpace(input)
	)

	flush := func() {
		if stmt := strings.TrimSpace(cur.String()); stmt != "" {
			statements = append(statements, stmt)
		}
		cur.Reset()
	}

	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j, err := skipQuoted(trimmed, i, c)
			if err != nil {
				return "", nil, err
			}
			cur.WriteString(trimmed[i:j])
			codeBuf.WriteString(strings.Repeat(" ", j-i))
			i = j - 1
		case c == '[':
			j := strings.IndexByte(trimmed[i:], ']')
			if j < 0 {
				return "", nil, errors.New("方括号未闭合")
			}
			j += i + 1
			cur.WriteString(trimmed[i:j])
			codeBuf.WriteString(strings.Repeat(" ", j-i))
			i = j - 1
		case c == '-' && i+1 < len(trimmed) && trimmed[i+1] == '-':
			j := strings.IndexByte(trimmed[i:], '\n')
			if j < 0 {
				cur.WriteString(trimmed[i:])
				codeBuf.WriteString(strings.Repeat(" ", len(trimmed)-i))
				i = len(trimmed)
			} else {
				j += i
				cur.WriteString(trimmed[i:j])
				codeBuf.WriteString(strings.Repeat(" ", j-i))
				i = j - 1
			}
		case c == '/' && i+1 < len(trimmed) && trimmed[i+1] == '*':
			j := strings.Index(trimmed[i+2:], "*/")
			if j < 0 {
				return "", nil, errors.New("块注释未闭合")
			}
			j += i + 4
			cur.WriteString(trimmed[i:j])
			codeBuf.WriteString(strings.Repeat(" ", j-i))
			i = j - 1
		case c == ';':
			flush()
			codeBuf.WriteByte(';')
		default:
			cur.WriteByte(c)
			codeBuf.WriteByte(c)
		}
	}
	flush()
	return codeBuf.String(), statements, nil
}

// skipQuoted 返回闭合引号之后的位置。
func skipQuoted(s string, start int, quote byte) (int, error) {
	for i := start + 1; i < len(s); i++ {
		if s[i] != quote {
			continue
		}
		// SQL 里两个连续引号表示转义
		if i+1 < len(s) && s[i+1] == quote {
			i++
			continue
		}
		return i + 1, nil
	}
	return 0, fmt.Errorf("引号 %q 未闭合", string(quote))
}

func firstKeyword(code string) string {
	s := strings.TrimLeftFunc(code, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
	})
	end := strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
	})
	if end < 0 {
		end = len(s)
	}
	return strings.ToUpper(s[:end])
}
