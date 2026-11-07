package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// loginCookie 走一遍登录并取出会话 Cookie。
//
// 刻意不复用 e.call：那个助手不返回响应头，而这里要的正是 Set-Cookie。
func (e *testEnv) loginCookie(t *testing.T, headers map[string]string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/auth/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("登录请求失败：%v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("登录应 200，得到 %d：%s", res.StatusCode, body)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly {
				t.Fatal("会话 Cookie 必须是 HttpOnly")
			}
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("登录成功但没有下发 %s：%v", sessionCookie, res.Cookies())
	return ""
}

func adminHeaders() map[string]string {
	return map[string]string{
		"X-Trim-Userid":   "1000",
		"X-Trim-Username": "admin",
		"X-Trim-Isadmin":  "true",
	}
}

// TestAuthLoginNeedsGatewayIdentity 没有飞牛身份时不能登录，且提示要能照做。
func TestAuthLoginNeedsGatewayIdentity(t *testing.T) {
	e := newTestEnv(t, true)

	code, resp := e.call(t, http.MethodPost, "/api/v1/auth/login", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("没有飞牛身份应 401，得到 %d", code)
	}
	if !strings.Contains(resp.Message, "飞牛桌面") {
		t.Fatalf("提示应告诉用户从飞牛桌面进入，实际：%s", resp.Message)
	}

	// 登录页拿到的状态：未登录、没有可用身份
	resp = e.must(t, http.StatusOK, http.MethodGet, "/api/v1/auth/state", nil)
	var st authState
	if err := json.Unmarshal(resp.Data, &st); err != nil {
		t.Fatal(err)
	}
	if st.Authenticated || st.Gateway.Available {
		t.Fatalf("未登录且无身份，状态不对：%+v", st)
	}
	if st.Gateway.Reason == "" {
		t.Fatal("拿不到身份时必须给出原因，否则登录页无法解释")
	}
}

// TestAuthStateReportsGatewayUser 登录页要能显示「以哪个飞牛账号登录」。
func TestAuthStateReportsGatewayUser(t *testing.T) {
	e := newTestEnv(t, true)

	resp := e.callRaw(t, http.MethodGet, "/api/v1/auth/state", adminHeaders())
	var st authState
	if err := json.Unmarshal(resp, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Gateway.Available || !st.Gateway.IsAdmin || st.Gateway.Username != "admin" {
		t.Fatalf("应报告管理员身份：%+v", st.Gateway)
	}
	if st.Authenticated {
		t.Fatal("还没点登录，不应处于已登录状态")
	}
}

// TestLogoutInvalidatesSession 退出登录必须是真退出：
// 网关身份头每个请求都会带上，若不砍会话，用户下一刻又会被带进来。
func TestLogoutInvalidatesSession(t *testing.T) {
	e := newTestEnv(t, true)
	h := adminHeaders()
	cookie := e.loginCookie(t, h)

	withCookie := map[string]string{"Cookie": cookie}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", withCookie, nil); code != http.StatusOK {
		t.Fatalf("登录后应可读，得到 %d", code)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/v1/auth/logout", withCookie, nil); code != http.StatusOK {
		t.Fatalf("退出登录应 200，得到 %d", code)
	}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", withCookie, nil); code != http.StatusUnauthorized {
		t.Fatalf("退出后旧会话应立即失效，得到 %d", code)
	}
	// 带着身份头再访问也进不去——必须重新点登录
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", h, nil); code != http.StatusUnauthorized {
		t.Fatalf("退出后仅凭身份头不应放行，得到 %d", code)
	}
}

// TestSessionCookieIsNotForgeable 随便编一个令牌不能进。
func TestSessionCookieIsNotForgeable(t *testing.T) {
	e := newTestEnv(t, true)
	code, _ := e.call(t, http.MethodGet, "/api/v1/instances",
		map[string]string{"Cookie": sessionCookie + "=made-up-token"}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("伪造会话应 401，得到 %d", code)
	}
}

// TestPortChannelIgnoresIdentityHeaders 端口通道上的身份头是客户端自己塞的，
// 不能因为写了 X-Trim-Isadmin: true 就变成管理员。
func TestPortChannelIgnoresIdentityHeaders(t *testing.T) {
	e := newTestEnv(t, true)
	e.handler.SetListen("tcp:127.0.0.1:8780")

	code, resp := e.call(t, http.MethodPost, "/api/v1/auth/login", adminHeaders(), nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("端口通道上带身份头应 401，得到 %d：%s", code, resp.Message)
	}

	// 反过来：从 socket 进来的身份头可用（真机就是这条路径）
	e2 := newTestEnv(t, true)
	e2.handler.SetListen("unix:/var/apps/niudbm/target/app.sock")
	if code, resp := e2.call(t, http.MethodPost, "/api/v1/auth/login", adminHeaders(), nil); code != http.StatusOK {
		t.Fatalf("socket 通道上的管理员身份应可登录，得到 %d：%s", code, resp.Message)
	}
}

// TestDevModeNeedsNoLogin 开发模式（直接 go run）没有网关也没有 Cookie，放行成 dev。
func TestDevModeNeedsNoLogin(t *testing.T) {
	e := newTestEnv(t, false)
	e.handler.SetListen("tcp:127.0.0.1:8780")

	resp := e.callRaw(t, http.MethodGet, "/api/v1/auth/state", nil)
	var st authState
	if err := json.Unmarshal(resp, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Authenticated || !st.Dev {
		t.Fatalf("开发模式应直接可用：%+v", st)
	}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", nil, nil); code != http.StatusOK {
		t.Fatalf("开发模式业务接口应可读，得到 %d", code)
	}
}

// callRaw 返回 data 段原文，供直接反序列化成具体结构。
func (e *testEnv) callRaw(t *testing.T, method, path string, headers map[string]string) json.RawMessage {
	t.Helper()
	code, resp := e.call(t, method, path, headers, nil)
	if code != http.StatusOK {
		t.Fatalf("%s %s 期望 200，得到 %d：%s", method, path, code, resp.Message)
	}
	return resp.Data
}
