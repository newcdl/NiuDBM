package mysqlctl

import (
	"reflect"
	"testing"
)

// MySQL 的 Host 列只认点分掩码，CIDR 前缀写进去会被当成主机名、永远匹配不上。
// 这一层转换错了，界面上"填了网段"看着成功、实际连不进来，所以逐档钉住。
func TestNetmaskOf(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "172.16.10.0/24", want: "172.16.10.0/255.255.255.0"},
		{in: "10.0.0.0/8", want: "10.0.0.0/255.0.0.0"},
		{in: "192.168.1.0/16", want: "192.168.1.0/255.255.0.0"},
		{in: "1.2.3.4/32", want: "1.2.3.4/255.255.255.255"},
		{in: "0.0.0.0/0", want: "0.0.0.0/0.0.0.0"},
		{in: "172.16.10.0/255.255.255.0", want: "172.16.10.0/255.255.255.0"},
		{in: "172.16.10.0/33", wantErr: true},
		{in: "172.16.10.0/abc", wantErr: true},
	}
	for _, c := range cases {
		got, err := netmaskOf(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("netmaskOf(%q) 期望报错，实际返回 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("netmaskOf(%q) 意外报错：%v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("netmaskOf(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeHosts(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "空列表回落到所有人", in: nil, want: []string{"%"}},
		{name: "全是空白也回落", in: []string{"", "  "}, want: []string{"%"}},
		{name: "去空白", in: []string{" 172.16.10.111 "}, want: []string{"172.16.10.111"}},
		{name: "网段转掩码", in: []string{"172.16.10.0/24"}, want: []string{"172.16.10.0/255.255.255.0"}},
		{name: "去重", in: []string{"172.16.10.111", " 172.16.10.111 "}, want: []string{"172.16.10.111"}},
		{name: "去重看转换后", in: []string{"172.16.10.0/24", "172.16.10.0/255.255.255.0"},
			want: []string{"172.16.10.0/255.255.255.0"}},
		{name: "掩码非法时原样保留（等 ValidHost 拦）", in: []string{"172.16.10.0/99"},
			want: []string{"172.16.10.0/99"}},
	}
	for _, c := range cases {
		if got := NormalizeHosts(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: NormalizeHosts(%v) = %v，期望 %v", c.name, c.in, got, c.want)
		}
	}
}

func TestValidHost(t *testing.T) {
	ok := []string{"%", "localhost", "172.16.10.111", "172.16.10.0/24", "172.16.10.0/255.255.255.0"}
	for _, h := range ok {
		if err := ValidHost(h); err != nil {
			t.Errorf("ValidHost(%q) 期望通过，实际报错：%v", h, err)
		}
	}
	bad := []string{"", "  ", "db.internal", "172.16.10.0/33", "172.16.10"}
	for _, h := range bad {
		if err := ValidHost(h); err == nil {
			t.Errorf("ValidHost(%q) 期望报错，实际通过", h)
		}
	}
}
