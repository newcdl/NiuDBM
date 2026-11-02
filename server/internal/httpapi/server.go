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
	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
	"niudbm/server/internal/webui"
)

// Paths 是服务端会用到的全部落盘位置，启动时算好，避免各处重复拼接。
type Paths struct {
	Instances string // 实例文件根目录
	Backups   string // 备份额根目录（优先共享目录）
	Trash     string // 删除但未清理的实例
	Secrets   string // 实例凭据目录，权限 0700
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
	// 同一实例上的写操作、备份、恢复必须串行：恢复要删 -wal 并 rename 文件，
	// 如果此时另一请求正在写同一个库，两边都会出错。键是实例 ID。
	locks sync.Map
	// 部署串行：并行拉镜像会把 NAS 的 IO 与带宽打满
	createMySQLMu sync.Mutex

	// 定时备份调度：间隔可注入，测试里调成毫秒级
	schedEvery time.Duration
	// policyRunning 记录正在执行策略的实例，避免同一策略被重复触发
	policyRunning sync.Map
	// downNotified 记录已经推过"实例掉线"的实例，恢复后才允许再次推送
	downNotified sync.Map
	// 历史清理节流：每 6 小时最多清一次
	cleanupMu   sync.Mutex
	lastCleanup time.Time

	// 部署任务（内存态）：创建常驻服务要几十秒到几分钟（拉镜像 + 初始化），
	// 同步等会让前端只能转圈，改异步后由前端轮询阶段。
	deployMu sync.Mutex
	deploys  map[string]*deployTask
}

// New 构造服务。prefix 是网关对外前缀，例如 /app/niudbm。
func New(env platform.Env, prefix string, st *store.Store, paths Paths) *Server {
	s := &Server{
		env:     env,
		prefix:  strings.TrimSuffix(prefix, "/"),
		dist:    webui.Dist(),
		st:      st,
		paths:   paths,
		deploys: map[string]*deployTask{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/diag", s.handleDiag)

	mux.HandleFunc("GET /api/v1/instances", s.handleListInstances)
	mux.HandleFunc("POST /api/v1/instances", s.handleCreateInstance)
	// 部署进度：创建常驻服务是异步的，前端按 taskId 轮询阶段
	mux.HandleFunc("GET /api/v1/deploy-tasks/{id}", s.handleGetDeployTask)

	mux.HandleFunc("GET /api/v1/instances/{id}", s.handleGetInstance)
	mux.HandleFunc("PATCH /api/v1/instances/{id}", s.handleUpdateInstance)
	mux.HandleFunc("DELETE /api/v1/instances/{id}", s.handleDeleteInstance)
	// 部署现场：失败时容器日志会落盘，界面用这个接口把它拿出来
	mux.HandleFunc("GET /api/v1/instances/{id}/deploy-log", s.handleDeployLog)

	// 运维四件套：配置、用户、导入导出（表结构浏览与 SQL 控制台不属于本应用）
	mux.HandleFunc("GET /api/v1/instances/{id}/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/instances/{id}/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/v1/instances/{id}/config/presets", s.handleConfigPresets)
	mux.HandleFunc("GET /api/v1/instances/{id}/status", s.handleInstanceStatus)
	// 连接信息里的 root 远程访问开关与密码重设
	mux.HandleFunc("GET /api/v1/instances/{id}/root-access", s.handleGetRootAccess)
	mux.HandleFunc("PUT /api/v1/instances/{id}/root-access", s.handleSetRootAccess)
	mux.HandleFunc("PUT /api/v1/instances/{id}/root-password", s.handleSetRootPassword)
	// 直接编辑配置文件（白名单之外的逃生口）
	mux.HandleFunc("GET /api/v1/instances/{id}/config/file", s.handleGetConfigFile)
	mux.HandleFunc("PUT /api/v1/instances/{id}/config/file", s.handlePutConfigFile)
	mux.HandleFunc("POST /api/v1/instances/{id}/config/file/restore", s.handleRestoreConfigFile)

	mux.HandleFunc("GET /api/v1/instances/{id}/users", s.handleListUsers)
	mux.HandleFunc("POST /api/v1/instances/{id}/users", s.handleCreateUser)
	mux.HandleFunc("PATCH /api/v1/instances/{id}/users/{name}", s.handleUpdateUser)
	mux.HandleFunc("DELETE /api/v1/instances/{id}/users/{name}", s.handleDeleteUser)
	// 密码回显：读本机保存的那一份
	mux.HandleFunc("GET /api/v1/instances/{id}/users/{name}/password", s.handleRevealUserPassword)

	mux.HandleFunc("GET /api/v1/instances/{id}/tables", s.handleListTables)
	mux.HandleFunc("GET /api/v1/instances/{id}/export", s.handleExportCSV)
	mux.HandleFunc("POST /api/v1/instances/{id}/import/csv", s.handleImportCSV)
	mux.HandleFunc("POST /api/v1/instances/{id}/import/sql", s.handleImportSQL)

	// 服务启停与连接信息
	mux.HandleFunc("POST /api/v1/instances/{id}/start", s.handleStart)
	mux.HandleFunc("POST /api/v1/instances/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /api/v1/instances/{id}/restart", s.handleRestart)
	mux.HandleFunc("GET /api/v1/instances/{id}/connection", s.handleConnection)

	// 一个数据库服务下的多个实例（逻辑库）：列表、新建、删除
	mux.HandleFunc("GET /api/v1/instances/{id}/databases", s.handleListDatabases)
	mux.HandleFunc("POST /api/v1/instances/{id}/databases", s.handleCreateDatabase)
	// 新建库时的字符集候选：以引擎自己支持的为准，不写死清单
	mux.HandleFunc("GET /api/v1/instances/{id}/databases/charsets", s.handleListCharsets)
	mux.HandleFunc("DELETE /api/v1/instances/{id}/databases/{name}", s.handleDropDatabase)
	// 给已经建好的库补一个只对它授权的账号
	mux.HandleFunc("POST /api/v1/instances/{id}/databases/{name}/users", s.handleCreateDatabaseUser)

	mux.HandleFunc("GET /api/v1/instances/{id}/backups", s.handleListBackups)
	mux.HandleFunc("POST /api/v1/instances/{id}/backups", s.handleCreateBackup)
	mux.HandleFunc("POST /api/v1/instances/{id}/backups/{bid}/restore", s.handleRestoreBackup)
	mux.HandleFunc("DELETE /api/v1/instances/{id}/backups/{bid}", s.handleDeleteBackup)
	mux.HandleFunc("GET /api/v1/instances/{id}/backups/{bid}/download", s.handleDownloadBackup)

	mux.HandleFunc("GET /api/v1/audit", s.handleListAudit)

	// 任务中心：操作履历（含定时备份、引擎下载这类后台任务）
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.handleCancelJob)

	// 概览页：一次请求拿全首屏数字与最近动态
	mux.HandleFunc("GET /api/v1/overview", s.handleOverview)

	// 全局设置与通知
	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("PATCH /api/v1/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/v1/settings/notify/test", s.handleTestNotify)

	// 定时备份策略：一实例一条
	mux.HandleFunc("GET /api/v1/instances/{id}/backup-policy", s.handleGetPolicy)
	mux.HandleFunc("PUT /api/v1/instances/{id}/backup-policy", s.handleUpdatePolicy)

	// 镜像清单：版本就是镜像 tag，本地有没有实时问 docker
	mux.HandleFunc("GET /api/v1/images", s.handleListImages)
	// 镜像页的写操作：立即拉取（异步任务）与删除（先查占用）
	mux.HandleFunc("POST /api/v1/images/pull", s.handlePullImage)
	mux.HandleFunc("DELETE /api/v1/images/{tag}", s.handleDeleteImage)
	// 安装前体检：端口占用、Docker 状态、机器上已有的数据库
	mux.HandleFunc("GET /api/v1/preflight", s.handlePreflight)

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
		"engine":    "mysql",
		"instances": total,
		"platform":  s.env.InPlatform,
		// 前端据此决定是否允许部署：没有 Docker 时按钮要能说明原因，而不是点了才报错
		"docker": s.dockerReady(r.Context()) == nil,
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
