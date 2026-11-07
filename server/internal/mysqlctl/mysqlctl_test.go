package mysqlctl

import "testing"

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

func TestExternalConnectionFailure(t *testing.T) {
	spec := Spec{
		Host: "127.0.0.1", Port: 1, User: "root", Password: "test",
		External: true, TLSMode: "disable",
	}
	if err := spec.CheckConnection(); err == nil {
		t.Fatal("连接不存在的外部 MySQL 应返回错误")
	}
}
