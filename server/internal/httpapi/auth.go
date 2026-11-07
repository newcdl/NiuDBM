package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"niudbm/server/internal/store"
)

// 控制台登录会话：
//
// 身份本身仍由飞牛统一网关给出（X-Trim-Userid / Username / Isadmin），应用不自建账号；
// 但「每个请求都带着身份头」不等于「用户点过登录」——退出登录之后下一次请求还是会带着头，
// 退出就成了摆设。所以这里把一次显式登录换成会话 Cookie，之后只认会话。
//
// 网关注入身份只负责证明“你是谁”；登录后能看到哪些数据库服务，由应用内的服务级授权决定。
const (
	sessionCookie = "niudbm_session"
	sessionTTL    = 7 * 24 * time.Hour
)

// ctxAuthUserKey 存放本次请求的登录用户，由 RequireSession 写入。
type ctxAuthUserKey struct{}

// sessionUserOf 取出当前登录用户。
func (s *Server) sessionUserOf(r *http.Request) (identity, bool) {
	if id, ok := r.Context().Value(ctxAuthUserKey{}).(identity); ok {
		return id, true
	}
	// 静态资源、登录接口等没有走中间件的路径，这里再取一次（幂等，无副作用）
	return s.resolveSession(r)
}

// resolveSession 只看会话 Cookie，不看网关身份头。
//
// 开发模式（直接 go run / vite dev，没有网关也没有 Cookie）放行成 dev 管理员：
// 这一分支只在不处于平台环境时成立，真机上不可能走到。
func (s *Server) resolveSession(r *http.Request) (identity, bool) {
	if !s.env.InPlatform {
		return identity{UserID: "dev", Username: "dev", IsAdmin: true, Dev: true}, true
	}
	token := sessionTokenOf(r)
	if token == "" {
		return identity{}, false
	}
	sess, err := s.st.GetSession(r.Context(), hashSessionToken(token))
	if err != nil {
		return identity{}, false
	}
	return identity{
		UserID:   sess.UserID,
		Username: sess.Username,
		IsAdmin:  sess.IsAdmin,
	}, true
}

// sessionTokenOf 从 Cookie 里取明文令牌。
func sessionTokenOf(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// hashSessionToken 是令牌落库形式：只存 SHA-256。
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newSessionToken 生成 32 字节随机令牌（base64url，无填充）。
func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// cookiePath 让 Cookie 只在本应用的前缀下发送；开发模式（无前缀）落到根。
func (s *Server) cookiePath() string {
	if s.prefix == "" {
		return "/"
	}
	return s.prefix + "/"
}

// secureCookie 判断要不要加 Secure：网关可能在外面终结 TLS，
// 这种情况下 r.TLS 为空，只能看转发头。
func secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// publicAPIPath 是登录前必须能访问的接口：
//   - /auth/state：登录页要先问「这次请求带没带飞牛身份、是不是管理员」；
//   - /auth/login：登录本身；
//   - /health：只报服务存活与 Docker 是否可用，不含台账内容。
func publicAPIPath(path string) bool {
	switch path {
	case "/api/v1/auth/state", "/api/v1/auth/login", "/api/v1/health":
		return true
	}
	return false
}

// requireSession 是全部业务接口的统一入口：没有会话一律 401。
//
// 放在 ServeHTTP 里而不是每个 handler 开头，是为了让「新加的接口忘了鉴权」这件事
// 在结构上不可能发生——漏掉的是白名单，需要显式添加。
func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) bool {
	id, ok := s.resolveSession(r)
	if !ok {
		writeErr(w, r, errUnauthorized("请先登录后再操作"))
		return false
	}
	*r = *r.WithContext(context.WithValue(r.Context(), ctxAuthUserKey{}, id))
	return true
}

// authState 是登录页需要的全部信息；刻意不含实例、台账等任何业务数据。
type authState struct {
	Authenticated bool        `json:"authenticated"`
	User          *authUser   `json:"user,omitempty"`
	Gateway       authGateway `json:"gateway"`
	Platform      bool        `json:"platform"`
	Dev           bool        `json:"dev"`
}

type authUser struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

// authGateway 描述「这次请求自带的飞牛身份」。
//
// Reason 只在拿不到身份或不是管理员时给出，用来让登录页说清原因：
// 「用 IP:端口 打开」与「用普通账号打开」的解决办法完全不同，不能都只说一句没权限。
type authGateway struct {
	Available bool   `json:"available"`
	UID       string `json:"uid,omitempty"`
	Username  string `json:"username,omitempty"`
	IsAdmin   bool   `json:"isAdmin"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	out := authState{Platform: s.env.InPlatform}

	if id, ok := s.resolveSession(r); ok {
		out.Authenticated = true
		out.Dev = id.Dev
		out.User = &authUser{UID: id.UserID, Username: id.Username, IsAdmin: id.IsAdmin}
	}

	gw, reason := s.gatewayIdentityOf(r)
	out.Gateway = authGateway{
		Available: gw.UserID != "",
		UID:       gw.UserID,
		Username:  gw.Username,
		IsAdmin:   gw.IsAdmin,
		Reason:    reason,
	}
	writeOK(w, r, out)
}

// handleAuthLogin 用一次显式的「点登录」换发会话。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	id, reason := s.gatewayIdentityOf(r)
	if id.UserID == "" {
		if reason == "" {
			reason = "这次请求没有飞牛身份"
		}
		writeErr(w, r, errUnauthorized(reason+"。请从飞牛桌面点开牛库管家再登录"))
		return
	}
	token, err := newSessionToken()
	if err != nil {
		writeErr(w, r, errInternal("生成登录令牌失败"))
		return
	}
	sess := store.Session{
		TokenHash: hashSessionToken(token),
		UserID:    id.UserID,
		Username:  id.Username,
		IsAdmin:   id.IsAdmin,
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
		CreatedAt: time.Now().UnixMilli(),
		ExpiresAt: time.Now().Add(sessionTTL).UnixMilli(),
	}
	if err := s.st.CreateSession(r.Context(), sess); err != nil {
		writeErr(w, r, errInternal("登录失败：无法写入会话"))
		return
	}
	s.setSessionCookie(w, r, token)
	role := "成员"
	if id.IsAdmin {
		role = "管理员"
	}
	s.audit(r, id, "auth.login", "user", id.UserID, "ok", "以 NAS "+role+"身份登录")
	writeOK(w, r, map[string]any{
		"user":      authUser{UID: id.UserID, Username: id.Username, IsAdmin: id.IsAdmin},
		"expiresAt": sess.ExpiresAt,
	})
}

// handleAuthLogout 注销会话。幂等：没登录也返回成功，前端不必判断状态。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	id, _ := s.resolveSession(r)
	if token := sessionTokenOf(r); token != "" {
		_ = s.st.DeleteSession(r.Context(), hashSessionToken(token))
	}
	s.clearSessionCookie(w, r)
	if id.UserID != "" && !id.Dev {
		s.audit(r, id, "auth.logout", "user", id.UserID, "ok", "退出登录")
	}
	writeOK(w, r, map[string]any{"ok": true})
}

// handleAuthMe 返回当前登录用户，供前端刷新页面后复用会话。
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.resolveSession(r)
	if !ok {
		writeErr(w, r, errUnauthorized("登录状态已失效，请重新登录"))
		return
	}
	writeOK(w, r, authUser{UID: id.UserID, Username: id.Username, IsAdmin: id.IsAdmin})
}

// clientIP 取调用方 IP：网关模式下 RemoteAddr 是 unix socket，只能看转发头。
func clientIP(r *http.Request) string {
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
