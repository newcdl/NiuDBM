package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"niudbm/server/internal/store"
)

func TestMemberOnlySeesGrantedServices(t *testing.T) {
	e := newTestEnv(t, true)
	cookie := e.loginCookie(t, map[string]string{
		"X-Trim-Userid":   "1001",
		"X-Trim-Username": "alice",
		"X-Trim-Isadmin":  "false",
	})
	headers := map[string]string{"Cookie": cookie}
	allowed := e.seedInstance(t, "博客库", "running")
	denied := e.seedInstance(t, "财务库", "running")
	if err := e.st.UpsertInstanceMember(t.Context(), store.InstanceMember{
		InstanceID: allowed.ID, UserID: "1001", Username: "alice", Role: store.MemberRoleViewer,
	}); err != nil {
		t.Fatal(err)
	}

	code, resp := e.call(t, http.MethodGet, "/api/v1/instances", headers, nil)
	if code != 200 || resp.Code != 0 {
		t.Fatalf("成员读取列表失败：HTTP %d %s", code, resp.Message)
	}
	var list struct {
		Items []instanceView `json:"items"`
	}
	if err := json.Unmarshal(resp.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != allowed.ID || list.Items[0].AccessRole != store.MemberRoleViewer {
		t.Fatalf("成员应只看到被授权服务：%+v", list.Items)
	}

	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances/"+allowed.ID, headers, nil); code != 200 {
		t.Fatalf("授权服务应可读，实际 %d", code)
	}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances/"+denied.ID, headers, nil); code != 403 {
		t.Fatalf("未授权服务应拒绝，实际 %d", code)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/v1/instances/"+allowed.ID+"/start", headers, nil); code != 403 {
		t.Fatalf("只读成员不能启动服务，实际 %d", code)
	}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/settings", headers, nil); code != 403 {
		t.Fatalf("成员不能读取全局设置，实际 %d", code)
	}

	if err := e.st.UpsertInstanceMember(t.Context(), store.InstanceMember{
		InstanceID: allowed.ID, UserID: "1001", Username: "alice", Role: store.MemberRoleOperator,
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/v1/instances/"+allowed.ID, headers,
		map[string]any{"confirmName": allowed.Name, "keepData": true}); code != 403 {
		t.Fatalf("运维者不能删除服务，实际 %d", code)
	}
}

func TestNonAdminCanLogin(t *testing.T) {
	e := newTestEnv(t, true)
	e.loginCookie(t, map[string]string{
		"X-Trim-Userid":   "2001",
		"X-Trim-Username": "member",
		"X-Trim-Isadmin":  "false",
	})
}
