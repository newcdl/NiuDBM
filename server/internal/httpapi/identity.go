package httpapi

import (
	"net/http"
	"strings"
)

// identity 是网关注入的调用者身份。
//
// 只有 Header 是可信来源：请求体、查询参数里的任何 user 标识一律忽略。
type identity struct {
	UserID   string
	Username string
	IsAdmin  bool
	// Dev 表示当前不在平台环境内，身份由本地放行得来
	Dev bool
}

func (i identity) name() string {
	if i.Username != "" {
		return i.Username
	}
	return i.UserID
}

// identity 解析调用者；第二个返回值表示身份是否可用。
func (s *Server) identityOf(r *http.Request) (identity, bool) {
	h := r.Header
	id := identity{
		UserID:   strings.TrimSpace(h.Get("X-Trim-Userid")),
		Username: strings.TrimSpace(h.Get("X-Trim-Username")),
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("X-Trim-Isadmin"))) {
	case "true", "1", "yes":
		id.IsAdmin = true
	}

	if !s.env.InPlatform {
		// 开发模式没有网关，Header 永远为空。这里放行并标记 Dev，
		// 规则写在 README/校验指南里，避免"本地能跑、上线全 401"的错觉。
		if id.UserID == "" {
			id.UserID = "dev"
		}
		if id.Username == "" {
			id.Username = "dev"
		}
		id.IsAdmin = true
		id.Dev = true
		return id, true
	}

	if id.UserID == "" && id.Username == "" {
		return identity{}, false
	}
	return id, true
}

func (s *Server) isAdmin(r *http.Request) bool {
	id, ok := s.identityOf(r)
	return ok && id.IsAdmin
}

// require 要求身份可用；v1 没有成员表，写操作一律要求管理员。
func (s *Server) require(w http.ResponseWriter, r *http.Request, admin bool) (identity, bool) {
	id, ok := s.identityOf(r)
	if !ok {
		writeErr(w, r, errUnauthorized("未收到网关注入的用户身份，请从应用中心打开"))
		return identity{}, false
	}
	if admin && !id.IsAdmin {
		writeErr(w, r, errUnauthorized("当前账号不是管理员，无法执行该操作"))
		return identity{}, false
	}
	return id, true
}

// fail 把任意错误统一转成响应包。
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if ae, ok := err.(apiErr); ok {
		writeErr(w, r, ae)
		return
	}
	writeErr(w, r, errInternal(err.Error()))
}
