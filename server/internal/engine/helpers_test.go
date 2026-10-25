package engine

import (
	"os"
	"strconv"
	"testing"
)

// 测试专用小工具，避免每个用例重复写样板。

func itoa(i int) string { return strconv.Itoa(i) }

func statFile(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func countOf(t *testing.T, path, table string) int {
	t.Helper()
	res, err := Execute(path, mustPrepare(t, "SELECT COUNT(*) FROM "+table), DefaultOptions())
	if err != nil {
		t.Fatalf("统计行数失败：%v", err)
	}
	if res.RowCount != 1 || len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("统计结果形状异常：%+v", res)
	}
	switch v := res.Rows[0][0].(type) {
	case int64:
		return int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("统计结果不是整数：%v", v)
		}
		return n
	default:
		t.Fatalf("统计结果类型未知：%T", v)
		return 0
	}
}
