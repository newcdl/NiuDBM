package httpapi

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
)

const (
	// 导入上限：NAS 上从浏览器传上来的是纯文本，20MB 已能覆盖大多数小库
	maxImportBytes = 20 << 20
	// 单次导出行数上限，防止误点导出把几十万行直接吐进浏览器
	defaultExportLimit = 500000
)

// errNotRunningInstance 把"实例没起"从引擎层一路带到 HTTP 层，再翻译成 409。
var errNotRunningInstance = errors.New("实例未运行")

type csvImportReq struct {
	Table string `json:"table"`
	CSV   string `json:"csv"`
	// Database 是目标库；不传时按"服务里只有一个库就用它"推断
	Database string `json:"database"`
}

// pickDatabase 解析这次操作针对哪个库。
//
// 服务里可能一个库都没有，也可能好几个：没有时直接说清楚，多于一个时要求指明，
// 只有一个就默认用它——每次导表都再选一次库没有必要。
func (s *Server) pickDatabase(r *http.Request, in store.Instance, want string) (string, error) {
	name := strings.TrimSpace(want)
	if name == "" {
		name = strings.TrimSpace(r.URL.Query().Get("database"))
	}
	if name != "" {
		if err := mysqlctl.ValidDBName(name); err != nil {
			return "", err
		}
		return name, nil
	}
	spec, err := s.mysqlSpec(in)
	if err != nil {
		return "", err
	}
	dbs, err := spec.ListDatabases()
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		return "", errNotRunningInstance
	}
	if err != nil {
		return "", err
	}
	switch len(dbs) {
	case 0:
		return "", errors.New("这个服务里还没有库，先在「数据库与账号」里建一个")
	case 1:
		return dbs[0].Name, nil
	}
	return "", errors.New("这个服务里有多个库，请先选择要操作的库")
}

// handleListTables 列出可导入导出的表。
func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	dbName, derr := s.pickDatabase(r, in, "")
	if derr != nil {
		if errors.Is(derr, errNotRunningInstance) {
			s.fail(w, r, errState("实例未运行，请先启动"))
			return
		}
		s.fail(w, r, errBadRequest(derr.Error()))
		return
	}
	tables, err := s.tableNames(in, dbName)
	if errors.Is(err, errNotRunningInstance) {
		s.fail(w, r, errState("实例未运行，请先启动"))
		return
	}
	if err != nil {
		s.fail(w, r, errInternal("读取表清单失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"items": tables, "total": len(tables), "database": dbName})
}

// tableNames 取实例里的表名，同时用作导入导出的目标校验。
func (s *Server) tableNames(in store.Instance, dbName string) ([]string, error) {
	spec, err := s.mysqlSpec(in)
	if err != nil {
		return nil, err
	}
	tables, err := spec.Tables(dbName)
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		return nil, errNotRunningInstance
	}
	return tables, err
}

// handleExportCSV 把一张表导出成 CSV 文件。
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	table := strings.TrimSpace(r.URL.Query().Get("table"))
	if table == "" {
		s.fail(w, r, errBadRequest("请选择要导出的表"))
		return
	}
	limit := defaultExportLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= defaultExportLimit {
			limit = v
		}
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	dbName, derr := s.pickDatabase(r, in, "")
	if derr != nil {
		if errors.Is(derr, errNotRunningInstance) {
			s.fail(w, r, errState("实例未运行，请先启动"))
			return
		}
		s.fail(w, r, errBadRequest(derr.Error()))
		return
	}

	// 先把表验一遍：一旦写了响应头就只能回半截 CSV，那时再报“表不存在”前端无法展示
	tables, err := s.tableNames(in, dbName)
	if err != nil {
		if errors.Is(err, errNotRunningInstance) {
			s.fail(w, r, errState("实例未运行，请先启动"))
			return
		}
		s.fail(w, r, errInternal("读取表清单失败："+err.Error()))
		return
	}
	if !hasName(tables, table) {
		s.fail(w, r, errBadRequest("表不存在："+table))
		return
	}

	// 再把执行器准备好，两处错误都能正常返回 JSON
	var export func(io.Writer) (int64, error)
	spec, serr := s.mysqlSpec(in)
	if serr != nil {
		s.fail(w, r, errBadRequest(serr.Error()))
		return
	}
	export = func(out io.Writer) (int64, error) { return spec.ExportCSV(out, dbName, table, limit) }

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", attachmentName(exportFileName(in.Name, table)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 带 BOM：Excel 直接双击打开中文才不会乱码；导入时会跳过它
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	n, err := export(w)
	if err != nil {
		// 响应头已经发出去了，这里只能把错误写进日志与审计
		s.audit(r, id, "instance.export", "instance", in.ID, "failed", table+"："+err.Error())
		return
	}
	s.audit(r, id, "instance.export", "instance", in.ID, "ok", fmt.Sprintf("%s，%d 行", table, n))
}

// handleImportCSV 从 CSV 导入数据（JSON 里带 CSV 文本）。
func (s *Server) handleImportCSV(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	var req csvImportReq
	if err := decodeJSONLimit(r, &req, maxImportBytes); err != nil {
		s.fail(w, r, err)
		return
	}
	table := strings.TrimSpace(req.Table)
	if table == "" {
		s.fail(w, r, errBadRequest("请选择目标表"))
		return
	}
	if strings.TrimSpace(req.CSV) == "" {
		s.fail(w, r, errBadRequest("CSV 内容为空"))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	dbName, derr := s.pickDatabase(r, in, req.Database)
	if derr != nil {
		if errors.Is(derr, errNotRunningInstance) {
			s.fail(w, r, errState("实例未运行，请先启动"))
			return
		}
		s.fail(w, r, errBadRequest(derr.Error()))
		return
	}

	reader := stripBOM(strings.NewReader(req.CSV))
	var (
		n   int64
		err error
	)
	spec, serr := s.mysqlSpec(in)
	if serr != nil {
		s.fail(w, r, errBadRequest(serr.Error()))
		return
	}
	n, err = spec.ImportCSV(dbName, table, reader)
	if errors.Is(err, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，请先启动"))
		return
	}
	if err != nil {
		s.audit(r, id, "instance.import", "instance", in.ID, "failed", "CSV → "+table+"；导入失败，已回滚本次 CSV 写入")
		s.recordJob(id, jobDBImport, "instance", in.ID, "csv table="+table, time.Now(), "", err)
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	s.audit(r, id, "instance.import", "instance", in.ID, "ok", fmt.Sprintf("CSV → %s，%d 行", table, n))
	s.recordJob(id, jobDBImport, "instance", in.ID, "csv table="+table, time.Now(),
		fmt.Sprintf("%d 行", n), nil)
	writeOK(w, r, map[string]any{"table": table, "rows": n})
}

// handleImportSQL 导入 .sql 脚本（请求体就是纯文本）。
//
// 不用 JSON 包一层：脚本里全是引号与换行，让前端做转义既浪费带宽又容易出错。
func (s *Server) handleImportSQL(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.fail(w, r, errBadRequest("读取脚本失败，或文件超过 20MB"))
		return
	}
	script := strings.TrimPrefix(string(raw), "\uFEFF")
	if strings.TrimSpace(script) == "" {
		s.fail(w, r, errBadRequest("脚本内容为空"))
		return
	}

	dbName, derr := s.pickDatabase(r, in, "")
	if derr != nil {
		if errors.Is(derr, errNotRunningInstance) {
			s.fail(w, r, errState("实例未运行，请先启动"))
			return
		}
		s.fail(w, r, errBadRequest(derr.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	start := time.Now()
	var (
		executed int
		execErr  error
	)
	spec, serr := s.mysqlSpec(in)
	if serr != nil {
		s.fail(w, r, errBadRequest(serr.Error()))
		return
	}
	executed, execErr = spec.ExecScript(dbName, script)
	if errors.Is(execErr, mysqlctl.ErrNotRunning) {
		s.fail(w, r, errState("实例未运行，请先启动"))
		return
	}
	elapsed := time.Since(start).Milliseconds()
	if execErr != nil {
		s.audit(r, id, "instance.import", "instance", in.ID, "failed",
			fmt.Sprintf("SQL 脚本第 %d 条失败（不记录脚本或数据库错误中的敏感内容）", executed+1))
		// 任务参数只记条数与耗时，脚本内容绝不进台账
		s.recordJob(id, jobDBImport, "instance", in.ID,
			fmt.Sprintf("sql statements=%d", executed), start, "", execErr)
		s.fail(w, r, errBadRequest(execErr.Error()))
		return
	}
	s.audit(r, id, "instance.import", "instance", in.ID, "ok", fmt.Sprintf("SQL 脚本 %d 条语句", executed))
	s.recordJob(id, jobDBImport, "instance", in.ID, fmt.Sprintf("sql statements=%d", executed), start,
		fmt.Sprintf("%d 条语句，耗时 %dms", executed, elapsed), nil)
	writeOK(w, r, map[string]any{"statements": executed, "elapsedMs": elapsed})
}

// hasName 在表名清单里查找。
func hasName(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// stripBOM 跳过 UTF-8 BOM：我们导出的 CSV 带 BOM，再导回来时不能把它算进第一个列名。
func stripBOM(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	if head, err := br.Peek(3); err == nil && bytes.Equal(head, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}
	return br
}

// exportFileName 生成下载文件名，把实例名与表名里的路径分隔符换掉。
func exportFileName(instance, table string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	stamp := time.Now().Format("20060102-150405")
	return replacer.Replace(instance) + "-" + replacer.Replace(table) + "-" + stamp + ".csv"
}

// attachmentName 同时给出 ASCII 兜底名和 RFC 5987 的 UTF-8 名，中文名在各浏览器都能落盘。
func attachmentName(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' {
			return '_'
		}
		return r
	}, name)
	return `attachment; filename="` + ascii + `"; filename*=UTF-8''` + urlEscape(name)
}

// urlEscape 只做 RFC 5987 要求的最小转义：非 unreserved 字符一律百分号编码。
func urlEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		unreserved := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~'
		if unreserved {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}
