package engine

import (
	"errors"
	"path/filepath"
	"testing"
)

func mustPrepare(t *testing.T, sql string) Prepared {
	t.Helper()
	p, err := PrepareSQL(sql)
	if err != nil {
		t.Fatalf("准备语句失败 %q：%v", sql, err)
	}
	return p
}

func TestCreateExecuteInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")

	seed := `CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		email TEXT
	);
	INSERT INTO users (name, email) VALUES ('甲', 'a@example.com');
	INSERT INTO users (name, email) VALUES ('乙', NULL);`

	if err := Create(path, seed); err != nil {
		t.Fatalf("建库失败：%v", err)
	}

	// 写入
	res, err := Execute(path, mustPrepare(t, "INSERT INTO users (name) VALUES ('丙')"), DefaultOptions())
	if err != nil {
		t.Fatalf("插入失败：%v", err)
	}
	if res.Kind != "exec" || res.RowsAffected != 1 {
		t.Fatalf("插入结果不符合预期：kind=%s affected=%d", res.Kind, res.RowsAffected)
	}

	// 查询
	res, err = Execute(path, mustPrepare(t, "SELECT id, name FROM users ORDER BY id"), DefaultOptions())
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if res.RowCount != 3 || len(res.Columns) != 2 {
		t.Fatalf("查询结果不符合预期：rows=%d cols=%d", res.RowCount, len(res.Columns))
	}
	if res.Rows[0][1] != "甲" {
		t.Fatalf("首行数据不对：%v", res.Rows[0])
	}

	// 结构
	tables, err := Inspect(path)
	if err != nil {
		t.Fatalf("读取结构失败：%v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("应只有 1 张表，得到 %d", len(tables))
	}
	tb := tables[0]
	if tb.Name != "users" || tb.Rows != 3 || len(tb.Columns) != 3 {
		t.Fatalf("结构信息不对：%+v", tb)
	}
	if !tb.Columns[0].PK {
		t.Fatalf("id 应被识别为主键：%+v", tb.Columns[0])
	}
	if !tb.Columns[1].NotNull {
		t.Fatalf("name 应为 NOT NULL：%+v", tb.Columns[1])
	}
}

func TestQueryLimitTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := Create(path, "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := Execute(path, mustPrepare(t, "INSERT INTO t (id) VALUES ("+itoa(i)+")"), DefaultOptions()); err != nil {
			t.Fatal(err)
		}
	}

	opts := DefaultOptions()
	opts.MaxRows = 10
	res, err := Execute(path, mustPrepare(t, "SELECT id FROM t"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 10 || !res.Truncated {
		t.Fatalf("应截断到 10 行并标记：rows=%d truncated=%v", res.RowCount, res.Truncated)
	}
}

func TestCreateRejectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := Create(path, ""); err != nil {
		t.Fatal(err)
	}
	if err := Create(path, ""); err == nil {
		t.Fatal("重复创建应报错")
	}
}

func TestExecuteOnMissingFile(t *testing.T) {
	_, err := Execute(filepath.Join(t.TempDir(), "nope.db"), mustPrepare(t, "SELECT 1"), DefaultOptions())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，得到：%v", err)
	}
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "instances", "data.db")
	backupPath := filepath.Join(dir, "backups", "nested", "snap.db")

	if err := Create(dataPath, "CREATE TABLE t (id INTEGER); INSERT INTO t (id) VALUES (1);"); err != nil {
		t.Fatal(err)
	}

	if err := Backup(dataPath, backupPath); err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	if fi, err := statFile(backupPath); err != nil || fi == 0 {
		t.Fatalf("备份文件异常：size=%d err=%v", fi, err)
	}
	if err := Verify(backupPath); err != nil {
		t.Fatalf("备份校验失败：%v", err)
	}

	// 备份之后写入新数据，恢复应该把它抹掉
	if _, err := Execute(dataPath, mustPrepare(t, "INSERT INTO t (id) VALUES (2)"), DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if got := countOf(t, dataPath, "t"); got != 2 {
		t.Fatalf("恢复前应有 2 行，得到 %d", got)
	}

	if err := Restore(backupPath, dataPath); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if got := countOf(t, dataPath, "t"); got != 1 {
		t.Fatalf("恢复后应回到 1 行，得到 %d", got)
	}
	if err := Verify(dataPath); err != nil {
		t.Fatalf("恢复后完整性检查失败：%v", err)
	}
}

func TestRestoreRejectsBrokenBackup(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	if err := Create(dataPath, "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}

	broken := filepath.Join(dir, "broken.db")
	if err := writeFile(broken, "这不是一个 sqlite 文件"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(broken, dataPath); err == nil {
		t.Fatal("损坏的备份不应被接受")
	}
	// 原库必须还能用
	if _, err := Execute(dataPath, mustPrepare(t, "SELECT 1"), DefaultOptions()); err != nil {
		t.Fatalf("恢复失败后原库被破坏：%v", err)
	}
}

func TestSizeCountsWal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := Create(path, "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(path, mustPrepare(t, "INSERT INTO t (id) VALUES (1)"), DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if Size(path) <= 0 {
		t.Fatal("占用应大于 0")
	}
}

func TestInspectSkipsInternalTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := Create(path, "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	tables, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Name == "sqlite_sequence" || tb.Name == "sqlite_master" {
			t.Fatalf("不应把内部表列出来：%s", tb.Name)
		}
	}
}
