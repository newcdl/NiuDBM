package mysqlctl

import (
	"strings"
	"testing"
)

// TestSeriesBase 守住版本归一的边界：配置项按大版本判断，
// 用户从镜像 tag 里抄来的 "8.4.3" 必须能和 "8.4" 一样命中。
func TestSeriesBase(t *testing.T) {
	cases := map[string]string{
		"8.4":   "8.4",
		"8.4.3": "8.4",
		"11.4":  "11.4",
		"5":     "5",
		" 8.0 ": "8.0",
	}
	for in, want := range cases {
		if got := seriesBase(in); got != want {
			t.Fatalf("seriesBase(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestTailText 守住错误详情：命令输出太长时要保留尾部（真正的原因在最后），
// 并且空输出不能变成空字符串，否则界面上什么都看不到。
func TestTailText(t *testing.T) {
	long := strings.Repeat("x", 1200) + "BOOM"
	got := tailText([]byte(long), nil)
	if len(got) > 700 {
		t.Fatalf("错误详情应被裁剪，实际 %d 字节", len(got))
	}
	if !strings.Contains(got, "BOOM") {
		t.Fatalf("裁剪应保留尾部：%q", got)
	}
	if got := tailText(nil, nil); got == "" {
		t.Fatal("没有输出时也要给一句可读的话")
	}
}
