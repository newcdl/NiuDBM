package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"niudbm/server/internal/engine"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

const (
	maxNameRunes = 32
	// engineSQLite 是 v1 唯一支持的引擎：装完即用，不需要下载任何东西。
	engineSQLite = "sqlite"
)

type instanceView struct {
	store.Instance
	SizeBytes  int64  `json:"sizeBytes"`
	FilePath   string `json:"filePath"`
	TableCount *int   `json:"tableCount,omitempty"`
}

type createInstanceReq struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
	SeedSQL string `json:"seedSql"`
	Note    string `json:"note"`
}

type updateInstanceReq struct {
	Name string `json:"name"`
}

type deleteInstanceReq struct {
	ConfirmName string `json:"confirmName"`
	KeepData    *bool  `json:"keepData"`
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, false)
	if !ok {
		return
	}
	_ = id

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	list, err := s.st.ListInstances(r.Context(), q, status)
	if err != nil {
		s.fail(w, r, errInternal("读取实例列表失败"))
		return
	}

	items := make([]instanceView, 0, len(list))
	for _, in := range list {
		items = append(items, s.view(in))
	}
	writeOK(w, r, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}

	var req createInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validateName(name); err != nil {
		s.fail(w, r, err)
		return
	}
	profile := req.Profile
	if profile == "" {
		profile = "low"
	}
	if !validProfile(profile) {
		s.fail(w, r, errBadRequest("资源档位只能是 low / standard / performance"))
		return
	}
	if len(req.SeedSQL) > 64*1024 {
		s.fail(w, r, errBadRequest("初始化 SQL 过长"))
		return
	}

	instID := ulid.New()
	relDir := filepath.ToSlash(filepath.Join(instID, "data.db"))
	in := store.Instance{
		ID:            instID,
		Name:          name,
		Engine:        engineSQLite,
		EngineVersion: "",
		Mode:          "managed",
		Status:        "running",
		DataDir:       relDir,
		Profile:       profile,
		CreatedAt:     time.Now().UnixMilli(),
		UpdatedAt:     time.Now().UnixMilli(),
	}

	dataFile := s.dataFile(in)
	if err := engine.Create(dataFile, req.SeedSQL); err != nil {
		// 初始化脚本出错时库里可能已经落了空文件，不清掉就会变成看不见的磁盘占用
		_ = os.RemoveAll(filepath.Dir(dataFile))
		s.audit(r, id, "instance.create", "instance", instID, "failed", err.Error())
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}
	if err := s.st.CreateInstance(r.Context(), in); err != nil {
		// 台账写不进去就把文件撤掉，避免留下"看不见但占空间"的孤儿目录
		_ = os.RemoveAll(filepath.Dir(dataFile))
		if errors.Is(err, store.ErrNameTaken) {
			s.fail(w, r, errBadRequest("实例名已存在，请换一个"))
			return
		}
		s.fail(w, r, errInternal("写入台账失败"))
		return
	}

	detail := "名称 " + name + "，档位 " + profile
	if req.Note != "" {
		detail += "，备注 " + truncate(req.Note, 200)
	}
	s.audit(r, id, "instance.create", "instance", instID, "ok", detail)

	v := s.view(in)
	writeCreated(w, r, v)
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	v := s.view(in)
	writeOK(w, r, v)
}

// view 把台账记录补上体积与表数量；列表、详情、创建共用同一套算法，避免各处算法不一致。
func (s *Server) view(in store.Instance) instanceView {
	dataFile := s.dataFile(in)
	v := instanceView{Instance: in, SizeBytes: engine.Size(dataFile), FilePath: dataFile}
	if tables, err := engine.Inspect(dataFile); err == nil {
		n := len(tables)
		v.TableCount = &n
	}
	return v
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req updateInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		s.fail(w, r, errBadRequest("没有需要修改的字段"))
		return
	}
	if err := validateName(name); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.st.RenameInstance(r.Context(), in.ID, name); err != nil {
		if errors.Is(err, store.ErrNameTaken) {
			s.fail(w, r, errBadRequest("实例名已存在，请换一个"))
			return
		}
		s.fail(w, r, errInternal("改名失败"))
		return
	}
	s.audit(r, id, "instance.rename", "instance", in.ID, "ok", in.Name+" → "+name)

	in.Name = name
	writeOK(w, r, instanceView{Instance: in, SizeBytes: engine.Size(s.dataFile(in)), FilePath: s.dataFile(in)})
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req deleteInstanceReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ConfirmName == "" {
		req.ConfirmName = r.URL.Query().Get("confirmName")
	}
	if req.KeepData == nil {
		if raw := r.URL.Query().Get("keepData"); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				s.fail(w, r, errBadRequest("keepData 只能是 true 或 false"))
				return
			}
			req.KeepData = &v
		}
	}
	if req.ConfirmName != in.Name {
		s.fail(w, r, errRejected("请输入实例名以确认删除"))
		return
	}
	if req.KeepData == nil {
		s.fail(w, r, errBadRequest("必须明确 keepData（是否保留数据文件）"))
		return
	}

	var moved string
	if !*req.KeepData {
		unlock := s.lockInstance(in.ID)
		defer unlock()
		src := filepath.Dir(s.dataFile(in))
		dst := filepath.Join(s.paths.Trash, in.ID+"-"+strconv.FormatInt(time.Now().Unix(), 10))
		if err := os.MkdirAll(s.paths.Trash, 0o750); err != nil {
			s.fail(w, r, errInternal("无法创建回收目录"))
			return
		}
		if err := os.Rename(src, dst); err != nil {
			// 跨分区等情况下不静默丢数据：宁可失败让用户选择保留数据
			s.fail(w, r, errInternal("移动数据文件失败，请改用“保留数据”后手动清理："+err.Error()))
			return
		}
		moved = dst
	}

	if err := s.st.SoftDelete(r.Context(), in); err != nil {
		s.fail(w, r, errInternal("更新台账失败"))
		return
	}

	detail := "keepData=" + strconv.FormatBool(*req.KeepData)
	if moved != "" {
		detail += "，已移入回收站 " + moved
	}
	s.audit(r, id, "instance.delete", "instance", in.ID, "ok", detail)

	writeOK(w, r, map[string]any{"id": in.ID, "keepData": *req.KeepData, "trashPath": moved})
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	tables, err := engine.Inspect(s.dataFile(in))
	if err != nil {
		s.fail(w, r, errInternal("读取表结构失败："+err.Error()))
		return
	}
	writeOK(w, r, map[string]any{"tables": tables, "total": len(tables)})
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, true); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListAudit(r.Context(), limit)
	if err != nil {
		s.fail(w, r, errInternal("读取审计失败"))
		return
	}
	writeOK(w, r, map[string]any{"items": list, "total": len(list)})
}

// ---------- 公共小工具 ----------

func (s *Server) dataFile(in store.Instance) string {
	return filepath.Join(s.paths.Instances, filepath.FromSlash(in.DataDir))
}

// load 取路径参数里的实例，不存在时已写好响应。
func (s *Server) load(w http.ResponseWriter, r *http.Request) (store.Instance, bool) {
	instID := r.PathValue("id")
	if instID == "" {
		writeErr(w, r, errInstanceNotFound())
		return store.Instance{}, false
	}
	in, err := s.st.GetInstance(r.Context(), instID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, r, errInstanceNotFound())
			return store.Instance{}, false
		}
		writeErr(w, r, errInternal("读取实例失败"))
		return store.Instance{}, false
	}
	return in, true
}

func (s *Server) audit(r *http.Request, id identity, action, kind, target, result, detail string) {
	ctx := r.Context()
	_ = s.st.AddAudit(ctx, store.Audit{
		Ts:         time.Now().UnixMilli(),
		UserID:     id.UserID,
		Username:   id.Username,
		Action:     action,
		TargetKind: kind,
		TargetID:   target,
		Result:     result,
		Detail:     detail,
	})
}

// validateName 校验实例名。名字只用于展示，从不参与拼路径，
// 但仍要挡住控制字符与路径分隔符，避免日志注入与误导。
func validateName(name string) error {
	if name == "" {
		return errBadRequest("实例名不能为空")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return errBadRequest("实例名最多 32 个字符")
	}
	if strings.Contains(name, "..") {
		return errBadRequest("实例名不能包含 ..")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errBadRequest("实例名不能包含控制字符")
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return errBadRequest(`实例名不能包含 / \ : * ? " < > |`)
		}
	}
	if strings.TrimSpace(name) != name {
		return errBadRequest("实例名首尾不能有空格")
	}
	return nil
}

func validProfile(p string) bool {
	switch p {
	case "low", "standard", "performance":
		return true
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
