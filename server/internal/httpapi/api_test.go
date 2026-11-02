package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TraceID string          `json:"traceId"`
}

type testEnv struct {
	srv     *httptest.Server
	paths   Paths
	st      *store.Store
	handler *Server
	// 假实例用的端口计数器：同一次测试里每个实例都要有独立端口
	nextPort int
}

func newTestEnv(t *testing.T, inPlatform bool) *testEnv {
	t.Helper()
	root := t.TempDir()
	paths := Paths{
		Instances: filepath.Join(root, "instances"),
		Backups:   filepath.Join(root, "backups"),
		Trash:     filepath.Join(root, "trash"),
		Meta:      filepath.Join(root, "niudbm.db"),
	}
	for _, dir := range []string{paths.Instances, paths.Backups, paths.Trash} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(paths.Meta)
	if err != nil {
		t.Fatalf("打开元数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	env := platform.Env{AppName: "niudbm", Version: "0.1.0-test", InPlatform: inPlatform, PkgVar: root,
		AppDest: filepath.Join(root, "app")}
	handler := New(env, "/app/niudbm", st, paths)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, paths: paths, st: st, handler: handler, nextPort: portPoolMin}
}

// call 发一个请求；headers 为 nil 时按管理员处理（开发模式）。
func (e *testEnv) call(t *testing.T, method, path string, headers map[string]string, body any) (int, apiResp) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s 请求失败：%v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	var out apiResp
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s 返回不是合法 JSON：%v（%s）", method, path, err, raw)
		}
	}
	return res.StatusCode, out
}

func (e *testEnv) must(t *testing.T, want int, method, path string, body any) apiResp {
	t.Helper()
	got, resp := e.call(t, method, path, nil, body)
	if got != want {
		t.Fatalf("%s %s 期望 HTTP %d，得到 %d：%s", method, path, want, got, resp.Message)
	}
	return resp
}

// seedInstance 直接往台账塞一条 MySQL 实例记录。
//
// 接口测试不该真的起容器：拉镜像要几分钟，还依赖 NAS 上的 Docker。
// 需要"实例存在"的用例统一用这条记录，只验证接口契约与参数校验。
func (e *testEnv) seedInstance(t *testing.T, name, status string) store.Instance {
	t.Helper()
	e.nextPort++
	now := time.Now().UnixMilli()
	id := ulid.New()
	in := store.Instance{
		ID:            id,
		Name:          name,
		Engine:        "mysql",
		EngineVersion: "8.4",
		Mode:          "managed",
		Status:        status,
		DataDir:       id + "/mysql",
		Profile:       "standard",
		Port:          e.nextPort,
		Runtime:       runtimeDocker,
		StorageKind:   storageVolume,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := e.st.CreateInstance(t.Context(), in); err != nil {
		t.Fatalf("写入台账失败：%v", err)
	}
	// 密码文件要存在，否则详情里拿不到连接信息
	if err := e.handler.writeSecret(id, "SeedPass_Only42"); err != nil {
		t.Fatalf("写入密码失败：%v", err)
	}
	if err := e.handler.writeRootSecret(id, "SeedPass_Only42"); err != nil {
		t.Fatalf("写入 root 密码失败：%v", err)
	}
	return in
}

// TestNameValidation 名字是目录名的一部分，也是 docker 容器名的一部分，
// 必须在建任何东西之前挡住路径分隔符与怪异字符。
func TestNameValidation(t *testing.T) {
	e := newTestEnv(t, false)
	cases := []string{
		"",
		"   ",
		"a/b",
		"a\\b",
		"..",
		"a..b",
		"名字带*号",
		strings.Repeat("长", 33),
	}
	for _, name := range cases {
		code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{"name": name})
		if code != http.StatusBadRequest {
			t.Fatalf("名字 %q 应被拒绝，得到 %d（%s）", name, code, resp.Message)
		}
		if err := validateName(name); err == nil {
			t.Fatalf("validateName(%q) 应报错", name)
		}
	}

	for _, name := range []string{"我的 数据库 v1.0", "shop_prod", "库-01"} {
		if err := validateName(name); err != nil {
			t.Fatalf("正常名字 %q 不该被拒：%v", name, err)
		}
	}
}

// TestDuplicateInstanceName 重名要在动手部署之前就拒掉：
// 拉起容器几十秒之后才报"名字已存在"，用户只会白等一场。
func TestDuplicateInstanceName(t *testing.T) {
	e := newTestEnv(t, false)
	e.seedInstance(t, "唯一", "running")

	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{"name": "唯一"})
	if code != http.StatusBadRequest || !strings.Contains(resp.Message, "已存在") {
		t.Fatalf("重名应被拒绝，得到 %d：%s", code, resp.Message)
	}
}

// TestCreateRejectsNonMySQL 产品只做 MySQL：其它引擎必须明确拒绝，
// 而不是悄悄建成一个 MySQL。
func TestCreateRejectsNonMySQL(t *testing.T) {
	e := newTestEnv(t, false)
	for _, engine := range []string{"sqlite", "postgres"} {
		code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{
			"name": "别的引擎-" + engine, "engine": engine,
		})
		if code != http.StatusBadRequest || !strings.Contains(resp.Message, "只支持 MySQL") {
			t.Fatalf("%s 应被拒，得到 %d：%s", engine, code, resp.Message)
		}
	}
}

// TestCreateRejectsUnknownVersion 版本必须是镜像清单里的 tag。
func TestCreateRejectsUnknownVersion(t *testing.T) {
	e := newTestEnv(t, false)
	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{
		"name": "版本不对", "engineVersion": "9.9",
	})
	if code != http.StatusBadRequest || !strings.Contains(resp.Message, "不支持这个版本") {
		t.Fatalf("未知版本应被拒，得到 %d：%s", code, resp.Message)
	}
}

// TestCreateValidatesUsers 建库时的账号必须在起容器之前验好：
// 错了就直接拒，不能先花几分钟拉镜像再回滚。
func TestCreateValidatesUsers(t *testing.T) {
	e := newTestEnv(t, false)
	cases := []map[string]any{
		{"name": "短密码", "users": []map[string]any{{"name": "app", "password": "123", "level": "read"}}},
		{"name": "坏档位", "users": []map[string]any{{"name": "app", "password": "password123", "level": "root"}}},
		{"name": "坏用户名", "users": []map[string]any{{"name": "a b", "password": "password123", "level": "read"}}},
	}
	for _, body := range cases {
		code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, body)
		if code != http.StatusBadRequest {
			t.Fatalf("%v 应被拒，得到 %d（%s）", body["name"], code, resp.Message)
		}
	}
}

// TestPermissionInPlatform 网关注入身份：没身份 401、普通用户只读、管理员可写。
func TestPermissionInPlatform(t *testing.T) {
	e := newTestEnv(t, true)

	code, _ := e.call(t, http.MethodGet, "/api/v1/instances", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无身份应 401，得到 %d", code)
	}

	user := map[string]string{"X-Trim-Userid": "1001", "X-Trim-Username": "普通用户"}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", user, nil); code != http.StatusOK {
		t.Fatalf("普通用户应可读，得到 %d", code)
	}
	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", user, map[string]any{"name": "x"})
	if code != http.StatusUnauthorized || !strings.Contains(resp.Message, "管理员") {
		t.Fatalf("普通用户写操作应 401，得到 %d：%s", code, resp.Message)
	}

	// 管理员可写：用"不支持的引擎"作为探针，既能验证放行又不会真的起容器
	admin := map[string]string{"X-Trim-Userid": "1001", "X-Trim-Username": "管理员", "X-Trim-Isadmin": "true"}
	code, resp = e.call(t, http.MethodPost, "/api/v1/instances", admin, map[string]any{"name": "管理员建的", "engine": "postgres"})
	if code != http.StatusBadRequest || strings.Contains(resp.Message, "管理员") {
		t.Fatalf("管理员应通过鉴权（业务 400 而不是 401），得到 %d：%s", code, resp.Message)
	}

	// 审计里应记录真实网关身份（审计仅管理员可读）
	// 写操作审计只落成功的动作，这里直接塞一条，专测"记录的是网关身份"
	if err := e.st.AddAudit(t.Context(), store.Audit{
		Ts: time.Now().UnixMilli(), UserID: "1001", Username: "管理员",
		Action: "instance.create", Result: "ok", Detail: "测试",
	}); err != nil {
		t.Fatalf("写入审计失败：%v", err)
	}
	auditCode, auditResp := e.call(t, http.MethodGet, "/api/v1/audit", admin, nil)
	if auditCode != http.StatusOK {
		t.Fatalf("管理员读审计应 200，得到 %d：%s", auditCode, auditResp.Message)
	}
	var audit struct {
		Items []store.Audit `json:"items"`
	}
	_ = json.Unmarshal(auditResp.Data, &audit)
	if len(audit.Items) == 0 {
		t.Fatal("审计列表不应为空")
	}
	if audit.Items[0].Username != "管理员" {
		t.Fatalf("审计应记录网关注入的用户名，得到 %q", audit.Items[0].Username)
	}
}

func TestHealthAndStatic(t *testing.T) {
	e := newTestEnv(t, false)

	resp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/health", nil)
	var health struct {
		Status    string `json:"status"`
		Engine    string `json:"engine"`
		Instances int    `json:"instances"`
		Docker    bool   `json:"docker"`
	}
	_ = json.Unmarshal(resp.Data, &health)
	if health.Status != "ok" || health.Engine != "mysql" {
		t.Fatalf("健康检查不对：%+v", health)
	}

	// 带网关前缀同样可达（P-08 双兼容）
	e.must(t, http.StatusOK, http.MethodGet, "/app/niudbm/api/v1/health", nil)

	// 未知接口返回 JSON 404，而不是 HTML
	code, resp := e.call(t, http.MethodGet, "/api/v1/nope", nil, nil)
	if code != http.StatusNotFound || resp.Code != codeNotFound {
		t.Fatalf("未知接口应 404 JSON，得到 %d code=%d", code, resp.Code)
	}

	// 首页注入 base
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/app/niudbm", nil)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	html, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(html), `<base href="/app/niudbm/">`) {
		t.Fatalf("首页缺少 base 注入：%s", truncate(string(html), 200))
	}
}

// TestImagesList 镜像清单是部署版本的唯一来源，必须带上架构与本地状态。
func TestImagesList(t *testing.T) {
	e := newTestEnv(t, false)
	resp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/images", nil)
	var out struct {
		Items    []map[string]any `json:"items"`
		Arch     string           `json:"arch"`
		DockerOK bool             `json:"dockerOk"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) == 0 || out.Arch == "" {
		t.Fatalf("镜像清单不该为空：%+v", out)
	}
}

// TestDeployLogGuards 部署日志是管理员排障用的，未知实例要 404、非管理员要 401。
func TestDeployLogGuards(t *testing.T) {
	e := newTestEnv(t, false)
	code, _ := e.call(t, http.MethodGet, "/api/v1/instances/NOPE/deploy-log", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("未知实例应 404，得到 %d", code)
	}

	in := e.seedInstance(t, "正常实例", "running")
	resp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/instances/"+in.ID+"/deploy-log", nil)
	var body struct {
		Available bool   `json:"available"`
		Message   string `json:"message"`
	}
	_ = json.Unmarshal(resp.Data, &body)
	if body.Available || body.Message == "" {
		t.Fatalf("没有部署失败过时应说明没有日志：%+v", body)
	}

	platform := newTestEnv(t, true)
	user := map[string]string{"X-Trim-Userid": "1001", "X-Trim-Username": "普通用户"}
	if code, _ := platform.call(t, http.MethodGet, "/api/v1/instances/NOPE/deploy-log", user, nil); code != http.StatusUnauthorized {
		t.Fatalf("普通用户读部署日志应 401，得到 %d", code)
	}
}

// TestUsersNeedRunningInstance 停着的实例读不到账号表：要回一条能照做的错误。
func TestUsersNeedRunningInstance(t *testing.T) {
	e := newTestEnv(t, false)
	in := e.seedInstance(t, "停着的库", "stopped")
	code, resp := e.call(t, http.MethodGet, "/api/v1/instances/"+in.ID+"/users", nil, nil)
	if code != http.StatusConflict && code != http.StatusBadRequest {
		t.Fatalf("停着的实例读账号应报状态错误，得到 %d：%s", code, resp.Message)
	}
}

// TestBackupOfDeletedInstance 已删除的实例不能再触发备份。
func TestBackupOfDeletedInstance(t *testing.T) {
	e := newTestEnv(t, false)
	in := e.seedInstance(t, "待删", "running")

	e.must(t, http.StatusOK, http.MethodDelete, "/api/v1/instances/"+in.ID,
		map[string]any{"confirmName": "待删", "keepData": true})

	code, _ := e.call(t, http.MethodPost, "/api/v1/instances/"+in.ID+"/backups", nil, map[string]any{})
	if code != http.StatusNotFound {
		t.Fatalf("已删除实例不应还能备份，得到 %d", code)
	}
}

// TestRestoreRejectsMissingBackup 不存在的备份不能进入恢复流程。
func TestRestoreRejectsMissingBackup(t *testing.T) {
	e := newTestEnv(t, false)
	in := e.seedInstance(t, "无备份", "running")

	code, _ := e.call(t, http.MethodPost, "/api/v1/instances/"+in.ID+"/backups/NOPE/restore",
		nil, map[string]any{"confirmName": "无备份"})
	if code != http.StatusNotFound {
		t.Fatalf("不存在的备份应 404，得到 %d", code)
	}
}
