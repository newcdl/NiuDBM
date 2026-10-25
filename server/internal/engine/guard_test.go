package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareAllowsCommonStatements(t *testing.T) {
	cases := []struct {
		sql  string
		kind Kind
	}{
		{"SELECT 1", KindQuery},
		{"select * from t where name = 'a;b'", KindQuery},
		{"SELECT 1;", KindQuery},
		{"  /* 注释 */ SELECT 1", KindQuery},
		{"-- 行注释\nSELECT 1", KindQuery},
		{"WITH x AS (SELECT 1) SELECT * FROM x", KindQuery},
		{"PRAGMA table_info(t)", KindQuery},
		{"VALUES (1), (2)", KindQuery},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)", KindExec},
		{"INSERT INTO t (name) VALUES ('x')", KindExec},
		{"UPDATE t SET name = 'y' WHERE id = 1", KindExec},
		{"DELETE FROM t", KindExec},
		{"SELECT 'it''s ok'", KindQuery},
	}

	for _, c := range cases {
		got, err := PrepareSQL(c.sql)
		if err != nil {
			t.Fatalf("%q 应通过校验，却报错：%v", c.sql, err)
		}
		if got.Kind != c.kind {
			t.Fatalf("%q 期望 kind=%v，得到 %v", c.sql, c.kind, got.Kind)
		}
	}
}

func TestPrepareRejectsMultiStatement(t *testing.T) {
	_, err := PrepareSQL("SELECT 1; SELECT 2")
	if !errors.Is(err, ErrMultiStatement) {
		t.Fatalf("应拒绝多语句，得到：%v", err)
	}

	_, err = PrepareSQL("DROP TABLE a; DROP TABLE b;")
	if !errors.Is(err, ErrMultiStatement) {
		t.Fatalf("应拒绝多语句，得到：%v", err)
	}
}

func TestPrepareRejectsForbidden(t *testing.T) {
	for _, sql := range []string{
		"ATTACH DATABASE '/tmp/x.db' AS x",
		"attach database 'x' as y",
		"DETACH DATABASE x",
		"SELECT load_extension('/tmp/evil.so')",
		"PRAGMA writable_schema = 1",
		"VACUUM INTO '/tmp/x.db'",
		"VACUUM\nINTO '/tmp/x.db'",
	} {
		_, err := PrepareSQL(sql)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%q 应被拦截，得到：%v", sql, err)
		}
	}
}

func TestPrepareRejectsEmptyAndBroken(t *testing.T) {
	for _, sql := range []string{"", "   ", "-- 只有注释", "/* 只有注释 */"} {
		_, err := PrepareSQL(sql)
		if !errors.Is(err, ErrEmptySQL) {
			t.Fatalf("%q 应被判为空语句，得到：%v", sql, err)
		}
	}

	for _, sql := range []string{"SELECT 'x", "SELECT \"x", "SELECT /* 未闭合"} {
		if _, err := PrepareSQL(sql); err == nil {
			t.Fatalf("%q 应报错", sql)
		}
	}
}

func TestPrepareKeepsOriginalText(t *testing.T) {
	sql := "SELECT  'a;b'  FROM t"
	got, err := PrepareSQL(sql)
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if !strings.Contains(got.SQL, "'a;b'") {
		t.Fatalf("语句内容被破坏：%s", got.SQL)
	}
}

func TestPrepareDoesNotMatchKeywordsInStrings(t *testing.T) {
	// 字符串里出现 ATTACH 不该触发拦截
	if _, err := PrepareSQL(`SELECT 'ATTACH DATABASE x'`); err != nil {
		t.Fatalf("字符串内容不应触发拦截：%v", err)
	}
}
