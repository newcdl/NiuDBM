package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestImageTag(t *testing.T) {
	cases := map[string]string{
		"postgres:17":                                "17",
		"docker.m.daocloud.io/library/mysql:8.4":     "8.4",
		"registry.local:5000/team/db:16":             "16",
		"postgres":                                   "",
		"registry.local:5000/team/db":                "",
		"ghcr.io/a/b@sha256:abc":                     "",
		"docker.m.daocloud.io/library/postgres:17.2": "17.2",
	}
	for ref, want := range cases {
		if got := imageTag(ref); got != want {
			t.Fatalf("imageTag(%q) = %q，期望 %q", ref, got, want)
		}
	}
}

func TestNormalizeImageRef(t *testing.T) {
	ok := []string{
		"postgres:17",
		"docker.m.daocloud.io/library/postgres:17",
		"registry.local:5000/team/db:16",
		"docker.1panel.live/library/mysql:8.4",
	}
	for _, ref := range ok {
		got, err := normalizeImageRef("  " + ref + " ")
		if err != nil {
			t.Fatalf("%q 应通过校验，却报错：%v", ref, err)
		}
		if got != ref {
			t.Fatalf("%q 应被规整成 %q，得到 %q", ref, ref, got)
		}
	}

	bad := map[string]string{
		"":                               "不能为空",
		"postgres":                       "带版本标签",
		"docker.io/library/postgres":     "带版本标签",
		"postgres 17":                    "只能包含",
		"postgres:17\n":                  "", // 首尾空白会被 trim 掉，属于合法
		"mysql:8.4;rm -rf /":             "只能包含",
		"docker.io//library/postgres:17": "格式不对",
	}
	for ref, want := range bad {
		_, err := normalizeImageRef(ref)
		if ref == "postgres:17\n" {
			if err != nil {
				t.Fatalf("%q 应被 trim 成合法地址，却报错：%v", ref, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%q 应被拒绝", ref)
		}
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Fatalf("%q 的错误信息应包含 %q，实际：%v", ref, want, err)
		}
	}
}

// TestCreateRejectsBadImageRef 地址写错要在动手之前挡住，并且说清怎么写。
func TestCreateRejectsBadImageRef(t *testing.T) {
	e := newTestEnv(t, false)
	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{
		"name":          "pg-img",
		"engine":        "postgres",
		"engineVersion": "17",
		"image":         "postgres",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("没有版本标签的地址应 400，得到 %d：%s", code, resp.Message)
	}
	if !strings.Contains(resp.Message, "版本标签") {
		t.Fatalf("提示应说明要带版本标签，实际：%s", resp.Message)
	}
}
