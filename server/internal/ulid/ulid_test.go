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
