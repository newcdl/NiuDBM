// Package httpapi 提供网关下的 HTTP 接口与前端静态资源。
package httpapi

import (
	"bytes"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"niudbm/server/internal/diag"
	"niudbm/server/internal/engine"
	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
	"niudbm/server/internal/webui"
)

// Paths 是服务端会用到的全部落盘位置，启动时算好，避免各处重复拼接。
type Paths struct {
	Instances string // 实例文件根目录
	Backups   string // 备份额根目录（优先共享目录）
	Trash     string // 删除但未清理的实例
	Meta      string // 元数据库文件
}

// Server 是应用唯一的 HTTP 入口。
type Server struct {
	env    platform.Env
	prefix string
	dist   fs.FS
	mux    *http.ServeMux
	listen string
	st     *store.Store
	paths  Paths
	opts   engine.Options
	// 同一实例上的写操作、备份、恢复必须串行：恢复要删 -wal 并 rename 文件，
	// 如果此时另一请求正在写同一个库，两边都会出错。键是实例 ID。
	locks sync.Map
}

// New 构造服务。prefix 是网关对外前缀，例如 /app/niudbm。
func New(env platform.Env, prefix string, st *store.Store, paths Paths) *Server {
	s := &Server{
		env:    env,
		prefix: strings.TrimSuffix(prefix, "/"),
		dist:   webui.Dist(),
		st:     st,
		paths:  paths,
		opts:   engine.DefaultOptions(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/diag", s.handleDiag)

	mux.HandleFunc("GET /api/v1/instances", s.handleListInstances)
	mux.HandleFunc("POST /api/v1/instances", s.handleCreateInstance)
	mux.HandleFunc("GET /api/v1/instances/{id}", s.handleGetInstance)
	mux.HandleFunc("PATCH /api/v1/instances/{id}", s.handleUpdateInstance)
	mux.HandleFunc("DELETE /api/v1/instances/{id}", s.handleDeleteInstance)
	mux.HandleFunc("GET /api/v1/instances/{id}/schema", s.handleSchema)
	mux.HandleFunc("POST /api/v1/instances/{id}/query", s.handleQuery)

	mux.HandleFunc("GET /api/v1/instances/{id}/backups", s.handleListBackups)
	mux.HandleFunc("POST /api/v1/instances/{id}/backups", s.handleCreateBackup)
	mux.HandleFunc("POST /api/v1/instances/{id}/backups/{bid}/restore", s.handleRestoreBackup)
	mux.HandleFunc("DELETE /api/v1/instances/{id}/backups/{bid}", s.handleDeleteBackup)
	mux.HandleFunc("GET /api/v1/instances/{id}/backups/{bid}/download", s.handleDownloadBackup)

	mux.HandleFunc("GET /api/v1/audit", s.handleListAudit)
	mux.HandleFunc("/", s.handleStatic)
	s.mux = mux
	return s
}

// SetListen 记录实际监听方式，仅用于展示。
func (s *Server) SetListen(info string) { s.listen = info }

// lockInstance 取实例级互斥锁，返回的函数用于解锁。
func (s *Server) lockInstance(id string) func() {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// SetQueryOptions 允许入口调整执行边界（测试里会缩短超时）。
func (s *Server) SetQueryOptions(opts engine.Options) { s.opts = opts }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	origPath := r.URL.Path
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	// 页面会被网关嵌进 iframe，这里不设 X-Frame-Options
	rec.Header().Set("X-Content-Type-Options", "nosniff")
	rec.Header().Set("Referrer-Policy", "same-origin")

	// 网关是否剥离 gatewayPrefix 尚未真机确认（P-08），这里统一归一化：
	// 带前缀就剔掉、不带前缀就原样，接口与静态资源都能命中。
	r.URL.Path = s.stripPrefix(origPath)

	s.mux.ServeHTTP(rec, r)
	log.Printf("%s %s %d %s", r.Method, origPath, rec.status, time.Since(start).Round(time.Millisecond))
}

// stripPrefix 返回剥离网关前缀后的路径，不匹配时原样返回。
func (s *Server) stripPrefix(p string) string {
	if s.prefix == "" {
		return p
	}
	rest, ok := strings.CutPrefix(p, s.prefix)
	if !ok {
		return p
	}
	if rest == "" {
		return "/"
	}
	if !strings.HasPrefix(rest, "/") {
		// 例如 /app/niudbmXXX 这种同名前缀的其他路径，不属于本应用
		return p
	}
	return rest
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	total, err := s.st.CountInstances(ctx)
	if err != nil {
		writeErr(w, r, errInternal("读取台账失败"))
		return
	}
	writeOK(w, r, map[string]any{
		"status":    "ok",
		"version":   s.env.Version,
		"engine":    "sqlite",
		"instances": total,
		"platform":  s.env.InPlatform,
	})
}

func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	report := diag.Run(s.env)
	report.Env.Listen = s.listen
	report.Env.IsAdmin = s.isAdmin(r)
	writeOK(w, r, report)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqPath := strings.TrimPrefix(r.URL.Path, "/")

	if reqPath == "" {
		s.serveIndex(w, r)
		return
	}

	data, err := fs.ReadFile(s.dist, reqPath)
	if err != nil {
		// 未知的接口路径不能回退成前端页面，否则前端会把 HTML 当成 JSON 解析
		if strings.HasPrefix(reqPath, "api/") {
			writeErr(w, r, errNotFoundAPI())
			return
		}
		// 带扩展名 = 缺失的静态资源；否则视为前端路由，交回 index
		if strings.Contains(path.Base(reqPath), ".") {
			http.NotFound(w, r)
			return
		}
		s.serveIndex(w, r)
		return
	}

	if strings.HasPrefix(reqPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, reqPath, time.Time{}, bytes.NewReader(data))
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	html, err := fs.ReadFile(s.dist, "index.html")
	if err != nil {
		http.Error(w, "前端资源缺失，请先构建前端", http.StatusInternalServerError)
		return
	}

	// base 必须在服务端注入：网关下页面可能被访问成 /app/niudbm（无尾斜杠），
	// 只靠相对路径会让静态资源解析到错误位置。
	page := string(html)
	if base := s.assetBase(); base != "" {
		page = strings.Replace(page, "<head>", `<head>`+"\n    <base href=\""+base+"\">", 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(page))
}

func (s *Server) assetBase() string {
	if s.prefix == "" {
		return "/"
	}
	return s.prefix + "/"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
