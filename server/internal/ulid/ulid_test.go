package ulid

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestNewShape(t *testing.T) {
	id := New()
	if len(id) != 26 {
		t.Fatalf("长度应为 26，得到 %d（%s）", len(id), id)
	}
	for _, c := range id {
		if !strings.ContainsRune(alphabet, c) {
			t.Fatalf("含非法字符 %q：%s", c, id)
		}
	}
}

func TestMonotonicWithinSameMillisecond(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000)
	prev := NewAt(at)
	for i := 0; i < 1000; i++ {
		cur := NewAt(at)
		if cur <= prev {
			t.Fatalf("同一毫秒内未保持递增：%s 之后是 %s", prev, cur)
		}
		prev = cur
	}
}

func TestUnique(t *testing.T) {
	seen := make(map[string]struct{}, 5000)
	for i := 0; i < 5000; i++ {
		id := New()
		if _, dup := seen[id]; dup {
			t.Fatalf("重复生成：%s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestSortableByTime(t *testing.T) {
	base := time.UnixMilli(1_700_000_000_000)
	early := NewAt(base)
	later := NewAt(base.Add(time.Second))
	if !sort.StringsAreSorted([]string{early, later}) {
		t.Fatalf("不同时间生成的 ULID 未按时间排序：%s, %s", early, later)
	}
}

func TestParseTimeRoundTrip(t *testing.T) {
	// 用未来时间：同一进程内已生成过 ULID 时，NewAt 会把时间钳到 lastMs 以保持单调，
	// 拿当前或过去的时刻做断言会受其他用例影响。
	at := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	got, err := ParseTime(NewAt(at))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.UnixMilli() != at.UnixMilli() {
		t.Fatalf("时间戳不一致：期望 %d，得到 %d", at.UnixMilli(), got.UnixMilli())
	}
}

func TestParseTimeRejectsBadInput(t *testing.T) {
	if _, err := ParseTime("TOO-SHORT"); err == nil {
		t.Fatal("长度非法时应报错")
	}
	if _, err := ParseTime(strings.Repeat("I", 26)); err == nil {
		t.Fatal("含非法字符时应报错")
	}
}
