package httpapi

import "testing"

// MySQL 26.10 用的是"年份.月份"的新版本号，字典序里比 "8.0" 小；
// 一旦按字符串比较，新版会被当成 5.7 处理（少传 --mysqlx=0 之类的参数）。
func TestSeriesAtLeast(t *testing.T) {
	cases := []struct {
		tag  string
		min  string
		want bool
	}{
		{tag: "8.4", min: "8.0", want: true},
		{tag: "8.4.13", min: "8.0", want: true},
		{tag: "9.7", min: "8.0", want: true},
		{tag: "26.10", min: "8.0", want: true},
		{tag: "5.7", min: "8.0", want: false},
		{tag: "8.0", min: "8.0", want: true},
		{tag: "10.11", min: "10.3", want: true},
		{tag: "10.2", min: "10.3", want: false},
		{tag: "11.8", min: "10.3", want: true},
	}
	for _, c := range cases {
		if got := seriesAtLeast(c.tag, c.min); got != c.want {
			t.Errorf("seriesAtLeast(%q, %q) = %v，期望 %v", c.tag, c.min, got, c.want)
		}
	}
}
