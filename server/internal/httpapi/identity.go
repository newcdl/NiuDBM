package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

// identity 是一次请求的调用者身份。
//
// 两种来源，用途严格分开：
//   - 会话（Cookie）：登录之后所有业务接口只认它，见 auth.go；
//   - 网关注入的 Header：只在登录那一刻用来确认「你是哪个 NAS 账号、是不是管理员」。
//
// 早先版本把 Header 当成每个请求的权限来源，于是退出登录没有意义（下一个请求还带着头）。
// 现在 Header 只用于换会话，任何业务接口都不会因为带了 Header 就放行。
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

// gatewayIdentityOf 解析网关注入的调用者身份；第二个返回值是「为什么拿不到」。
//
// 取值按官方文档从严：X-Trim-Isadmin 只认字符串 true / false（"1"、"yes" 一律不认），
// 但官方文档同时说明网关只在会话校验通过后才转发，因此这里对大小写与空格的容忍
// 只影响解析，不影响安全边界。
//
// 端口通道（开发时的 --addr）上本来就不该有身份头：那条路径上的头是客户端自己塞的，
// 必须当不存在处理，否则任何能连上端口的人都能把自己写成管理员。
func (s *Server) gatewayIdentityOf(r *http.Request) (identity, string) {
	if !s.env.InPlatform {
		// 开发模式没有网关，Header 永远为空。这里给一个 dev 管理员，
		// 规则写在 README/校验指南里，避免"本地能跑、真机没身份"的错觉。
		return identity{UserID: "dev", Username: "dev", IsAdmin: true, Dev: true}, ""
	}
	if !s.socket {
		return identity{}, "当前不是从飞牛桌面进入的（直接访问端口时没有飞牛身份）"
	}

	h := r.Header
	id := identity{
		UserID:   strings.TrimSpace(h.Get("X-Trim-Userid")),
		Username: strings.TrimSpace(h.Get("X-Trim-Username")),
	}
	if id.UserID == "" && id.Username == "" {
		return identity{}, "这次请求没有飞牛身份（刷新过页面、或链接被复制到别处打开都会这样）"
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("X-Trim-Isadmin"))) {
	case "true":
		id.IsAdmin = true
	case "false", "":
		id.IsAdmin = false
	default:
		// 取值不在文档约定的两个之内，宁可当普通用户，也不猜成管理员
		id.IsAdmin = false
	}
	if id.UserID != "" {
		if n, err := strconv.ParseInt(id.UserID, 10, 64); err != nil || n <= 0 {
			// UID 只用来区分身份与写审计，格式不对就退回用户名，
			// 但绝不用它拼路径或当权限依据
			id.UserID = ""
		}
	}
	if id.UserID == "" {
		id.UserID = "name:" + id.Username
	}
	return id, ""
}

// isAdmin 判断本次请求是否具有管理员身份（登录后的会话身份）。
func (s *Server) isAdmin(r *http.Request) bool {
	id, ok := s.sessionUserOf(r)
	return ok && id.IsAdmin
}

// require 要求已登录；admin 为真表示写操作。
// 系统管理员可管理全部资源，普通成员按服务级角色判断。
func (s *Server) require(w http.ResponseWriter, r *http.Request, admin bool) (identity, bool) {
	id, ok := s.sessionUserOf(r)
	if !ok {
		writeErr(w, r, errUnauthorized("请先登录后再操作"))
		return identity{}, false
	}
	if admin {
		if !s.canWriteRequest(r, id, false) {
			writeErr(w, r, apiErr{http.StatusForbidden, codeForbidden, "当前账号没有该服务的写权限"})
			return identity{}, false
		}
		return id, true
	}
	if !s.canReadRequest(r, id) {
		writeErr(w, r, apiErr{http.StatusForbidden, codeForbidden, "当前账号没有访问该资源的权限"})
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
