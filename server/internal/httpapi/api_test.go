package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"niudbm/server/internal/engine"
	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
)

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TraceID string          `json:"traceId"`
}

type testEnv struct {
	srv   *httptest.Server
	paths Paths
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

	env := platform.Env{AppName: "niudbm", Version: "0.1.0-test", InPlatform: inPlatform, PkgVar: root}
	handler := New(env, "/app/niudbm", st, paths)
	handler.SetQueryOptions(engine.Options{Timeout: 5 * time.Second, MaxRows: 50})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, paths: paths}
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

func TestInstanceLifecycle(t *testing.T) {
	e := newTestEnv(t, false)

	// 1. 创建（带多语句初始化脚本）
	resp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{
		"name":    "博客库",
		"profile": "low",
		"seedSql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL);" +
			"INSERT INTO posts (title) VALUES ('第一篇');",
	})
	var created instanceView
	if err := json.Unmarshal(resp.Data, &created); err != nil {
		t.Fatalf("创建返回无法解析：%v", err)
	}
	if created.ID == "" || created.Name != "博客库" || created.SizeBytes <= 0 {
		t.Fatalf("创建结果字段不全：%+v", created)
	}
	if created.TableCount == nil || *created.TableCount != 1 {
		t.Fatalf("初始化脚本应建出 1 张表：%+v", created.TableCount)
	}
	id := created.ID

	// 2. 列表与详情
	list := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/instances", nil)
	var listData struct {
		Items []instanceView `json:"items"`
		Total int            `json:"total"`
	}
	_ = json.Unmarshal(list.Data, &listData)
	if listData.Total != 1 || listData.Items[0].ID != id {
		t.Fatalf("列表不对：%+v", listData)
	}

	// 3. SQL 控制台：写
	writeResp := e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+id+"/query", map[string]any{
		"sql": "INSERT INTO posts (title) VALUES ('第二篇')",
	})
	var writeRes engine.Result
	_ = json.Unmarshal(writeResp.Data, &writeRes)
	if writeRes.Kind != "exec" || writeRes.RowsAffected != 1 {
		t.Fatalf("写入结果不对：%+v", writeRes)
	}

	// 4. SQL 控制台：读
	readResp := e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+id+"/query", map[string]any{
		"sql": "SELECT title FROM posts ORDER BY id",
	})
	var readRes engine.Result
	_ = json.Unmarshal(readResp.Data, &readRes)
	if readRes.RowCount != 2 || readRes.Rows[0][0] != "第一篇" {
		t.Fatalf("读取结果不对：%+v", readRes)
	}

	// 5. 结构
	schResp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/instances/"+id+"/schema", nil)
	var sch struct {
		Tables []engine.Table `json:"tables"`
	}
	_ = json.Unmarshal(schResp.Data, &sch)
	if len(sch.Tables) != 1 || sch.Tables[0].Rows != 2 || len(sch.Tables[0].Columns) != 2 {
		t.Fatalf("结构信息不对：%+v", sch.Tables)
	}

	// 6. 备份
	bkResp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances/"+id+"/backups", map[string]any{
		"note": "第一次备份",
	})
	var bk store.Backup
	_ = json.Unmarshal(bkResp.Data, &bk)
	if bk.Status != "succeeded" || bk.SizeBytes <= 0 || bk.Checksum == "" {
		t.Fatalf("备份结果不对：%+v", bk)
	}
	if _, err := os.Stat(filepath.Join(e.paths.Backups, id, bk.FileName)); err != nil {
		t.Fatalf("备份文件不存在：%v", err)
	}

	// 7. 备份后再写一条，然后恢复
	e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+id+"/query", map[string]any{
		"sql": "INSERT INTO posts (title) VALUES ('第三篇')",
	})

	// 7.1 确认名不对 -> 拒绝
	e.must(t, http.StatusBadRequest, http.MethodPost, "/api/v1/instances/"+id+"/backups/"+bk.ID+"/restore",
		map[string]any{"confirmName": "写错了"})

	// 7.2 正确恢复
	e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+id+"/backups/"+bk.ID+"/restore",
		map[string]any{"confirmName": "博客库"})

	countResp := e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+id+"/query", map[string]any{
		"sql": "SELECT COUNT(*) FROM posts",
	})
	var countRes engine.Result
	_ = json.Unmarshal(countResp.Data, &countRes)
	if countRes.Rows[0][0].(float64) != 2 {
		t.Fatalf("恢复后应回到 2 行：%+v", countRes.Rows)
	}

	// 8. 备份列表应包含保护性备份
	bkListResp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/instances/"+id+"/backups", nil)
	var bkList struct {
		Items []store.Backup `json:"items"`
	}
	_ = json.Unmarshal(bkListResp.Data, &bkList)
	if len(bkList.Items) != 2 {
		t.Fatalf("应有 2 份备份（手动 + 恢复前保护），得到 %d", len(bkList.Items))
	}
	var hasProtective bool
	for _, b := range bkList.Items {
		if b.Type == "pre_restore" {
			hasProtective = true
		}
	}
	if !hasProtective {
		t.Fatalf("缺少 pre_restore 类型的保护性备份：%+v", bkList.Items)
	}

	// 9. 下载备份
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/instances/"+id+"/backups/"+bk.ID+"/download", nil)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载备份期望 200，得到 %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, bk.FileName) {
		t.Fatalf("下载文件名不对：%s", got)
	}
	body, _ := io.ReadAll(res.Body)
	if int64(len(body)) != bk.SizeBytes {
		t.Fatalf("下载体积不对：%d != %d", len(body), bk.SizeBytes)
	}

	// 10. 删除备份
	e.must(t, http.StatusOK, http.MethodDelete, "/api/v1/instances/"+id+"/backups/"+bk.ID, nil)
	if _, err := os.Stat(filepath.Join(e.paths.Backups, id, bk.FileName)); !os.IsNotExist(err) {
		t.Fatalf("备份文件应已删除：%v", err)
	}

	// 11. 删除实例：确认名不符 -> 拒绝
	e.must(t, http.StatusBadRequest, http.MethodDelete, "/api/v1/instances/"+id,
		map[string]any{"confirmName": "错的", "keepData": false})

	// 12. 删除实例：不保留数据 -> 进回收站
	e.must(t, http.StatusOK, http.MethodDelete, "/api/v1/instances/"+id,
		map[string]any{"confirmName": "博客库", "keepData": false})
	trash, err := os.ReadDir(e.paths.Trash)
	if err != nil || len(trash) != 1 {
		t.Fatalf("回收站应有 1 项：len=%d err=%v", len(trash), err)
	}

	// 13. 列表为空，再删返回 404
	list = e.must(t, http.StatusOK, http.MethodGet, "/api/v1/instances", nil)
	_ = json.Unmarshal(list.Data, &listData)
	if listData.Total != 0 {
		t.Fatalf("删除后列表应为空：%+v", listData)
	}
	e.must(t, http.StatusNotFound, http.MethodDelete, "/api/v1/instances/"+id,
		map[string]any{"confirmName": "博客库", "keepData": true})

	// 14. 审计里应留下关键动作
	auditResp := e.must(t, http.StatusOK, http.MethodGet, "/api/v1/audit?limit=100", nil)
	var audit struct {
		Items []store.Audit `json:"items"`
	}
	_ = json.Unmarshal(auditResp.Data, &audit)
	seen := map[string]bool{}
	for _, a := range audit.Items {
		seen[a.Action] = true
	}
	for _, want := range []string{"instance.create", "backup.create", "backup.restore", "instance.delete"} {
		if !seen[want] {
			t.Fatalf("审计缺少动作 %s，实际：%v", want, seen)
		}
	}
}

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
	}

	// 正常名字（含中文、空格、点）应通过
	e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "我的 数据库 v1.0"})
}

func TestDuplicateInstanceName(t *testing.T) {
	e := newTestEnv(t, false)
	e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "唯一"})
	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", nil, map[string]any{"name": "唯一"})
	if code != http.StatusBadRequest || !strings.Contains(resp.Message, "已存在") {
		t.Fatalf("重名应被拒绝，得到 %d：%s", code, resp.Message)
	}
}

func TestQueryGuards(t *testing.T) {
	e := newTestEnv(t, false)
	resp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "护栏"})
	var in instanceView
	_ = json.Unmarshal(resp.Data, &in)

	cases := []struct {
		sql  string
		want int
	}{
		{"ATTACH DATABASE '/tmp/x.db' AS x", http.StatusBadRequest},
		{"SELECT 1; SELECT 2", http.StatusBadRequest},
		{"PRAGMA writable_schema = 1", http.StatusBadRequest},
		{"SELECT * FROM 不存在的表", http.StatusBadRequest},
		{"SELECT 1", http.StatusOK},
		{"CREATE TABLE ok (id INTEGER)", http.StatusOK},
	}
	for _, c := range cases {
		code, resp := e.call(t, http.MethodPost, "/api/v1/instances/"+in.ID+"/query", nil, map[string]any{"sql": c.sql})
		if code != c.want {
			t.Fatalf("%q 期望 %d，得到 %d：%s", c.sql, c.want, code, resp.Message)
		}
	}
}

func TestPermissionInPlatform(t *testing.T) {
	e := newTestEnv(t, true)

	// 没有网关注入的身份：一律 401
	code, _ := e.call(t, http.MethodGet, "/api/v1/instances", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无身份应 401，得到 %d", code)
	}

	// 普通用户：可读，不可写
	user := map[string]string{"X-Trim-Userid": "1001", "X-Trim-Username": "普通用户"}
	if code, _ := e.call(t, http.MethodGet, "/api/v1/instances", user, nil); code != http.StatusOK {
		t.Fatalf("普通用户应可读，得到 %d", code)
	}
	code, resp := e.call(t, http.MethodPost, "/api/v1/instances", user, map[string]any{"name": "x"})
	if code != http.StatusUnauthorized || !strings.Contains(resp.Message, "管理员") {
		t.Fatalf("普通用户写操作应 401，得到 %d：%s", code, resp.Message)
	}

	// 管理员：可写
	admin := map[string]string{"X-Trim-Userid": "1001", "X-Trim-Username": "管理员", "X-Trim-Isadmin": "true"}
	if code, resp := e.call(t, http.MethodPost, "/api/v1/instances", admin, map[string]any{"name": "管理员建的"}); code != http.StatusCreated {
		t.Fatalf("管理员应可创建，得到 %d：%s", code, resp.Message)
	}

	// 审计里应记录真实网关身份（审计仅管理员可读）
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
	}
	_ = json.Unmarshal(resp.Data, &health)
	if health.Status != "ok" || health.Engine != "sqlite" {
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

func TestBackupOfDeletedInstance(t *testing.T) {
	e := newTestEnv(t, false)
	resp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "待删"})
	var in instanceView
	_ = json.Unmarshal(resp.Data, &in)

	e.must(t, http.StatusOK, http.MethodDelete, "/api/v1/instances/"+in.ID,
		map[string]any{"confirmName": "待删", "keepData": true})

	code, _ := e.call(t, http.MethodPost, "/api/v1/instances/"+in.ID+"/backups", nil, map[string]any{})
	if code != http.StatusNotFound {
		t.Fatalf("已删除实例不应还能备份，得到 %d", code)
	}
}

func TestRestoreRejectsMissingBackup(t *testing.T) {
	e := newTestEnv(t, false)
	resp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "无备份"})
	var in instanceView
	_ = json.Unmarshal(resp.Data, &in)

	code, _ := e.call(t, http.MethodPost, "/api/v1/instances/"+in.ID+"/backups/NOPE/restore",
		nil, map[string]any{"confirmName": "无备份"})
	if code != http.StatusNotFound {
		t.Fatalf("不存在的备份应 404，得到 %d", code)
	}
}

func TestQueryLimitParam(t *testing.T) {
	e := newTestEnv(t, false)
	resp := e.must(t, http.StatusCreated, http.MethodPost, "/api/v1/instances", map[string]any{"name": "限量"})
	var in instanceView
	_ = json.Unmarshal(resp.Data, &in)

	e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+in.ID+"/query", map[string]any{
		"sql": "CREATE TABLE t (id INTEGER)",
	})
	for i := 0; i < 5; i++ {
		e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+in.ID+"/query", map[string]any{
			"sql": "INSERT INTO t (id) VALUES (" + strconv.Itoa(i) + ")",
		})
	}

	resp = e.must(t, http.StatusOK, http.MethodPost, "/api/v1/instances/"+in.ID+"/query", map[string]any{
		"sql": "SELECT id FROM t", "limit": 2,
	})
	var res engine.Result
	_ = json.Unmarshal(resp.Data, &res)
	if res.RowCount != 2 || !res.Truncated {
		t.Fatalf("limit=2 应截断：%+v", res)
	}
}
