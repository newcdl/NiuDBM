package httpapi

import (
	"strings"
	"testing"
)

// TestDockerErrHint 守住"报错要能照着修"：
// 权限不足和守护进程没起是两种修法，混成一句"检测不到 Docker"用户没法排查。
func TestDockerErrHint(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "没有 socket 权限",
			raw:  "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock",
			want: "没有权限访问",
		},
		{
			name: "守护进程没起",
			raw:  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			want: "Docker 服务正在运行",
		},
		{
			name: "其他错误只取首行",
			raw:  "some docker failure\nsecond line",
			want: "some docker failure",
		},
		{
			name: "空输出",
			raw:  "",
			want: "执行失败",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dockerErrHint(c.raw)
			if !strings.Contains(got, c.want) {
				t.Fatalf("提示应包含 %q，实际 %q", c.want, got)
			}
		})
	}
}
