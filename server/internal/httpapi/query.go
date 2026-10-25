package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"niudbm/server/internal/engine"
)

type queryReq struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req queryReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		s.fail(w, r, errBadRequest("SQL 不能为空"))
		return
	}
	if len(req.SQL) > 128*1024 {
		s.fail(w, r, errBadRequest("SQL 过长（上限 128 KB）"))
		return
	}

	prepared, err := engine.PrepareSQL(req.SQL)
	if err != nil {
		switch {
		case errors.Is(err, engine.ErrForbidden), errors.Is(err, engine.ErrMultiStatement),
			errors.Is(err, engine.ErrEmptySQL):
			s.fail(w, r, errBadRequest(err.Error()))
		default:
			s.fail(w, r, errBadRequest("SQL 校验失败："+err.Error()))
		}
		return
	}

	opts := s.opts
	if req.Limit > 0 {
		if req.Limit > 500 {
			req.Limit = 500
		}
		opts.MaxRows = req.Limit
	}

	// 同一实例串行执行：SQLite 的写入与备份/恢复不能交叠
	unlock := s.lockInstance(in.ID)
	defer unlock()

	res, err := engine.Execute(s.dataFile(in), prepared, opts)
	if err != nil {
		if errors.Is(err, engine.ErrNotFound) {
			s.fail(w, r, errState("实例文件不存在，请检查磁盘是否被移动"))
			return
		}
		s.fail(w, r, errBadRequest("执行失败："+err.Error()))
		return
	}

	// 查询语句量大且无副作用，只审计写操作，避免把审计表写成执行流水
	if prepared.Kind == engine.KindExec {
		s.audit(r, id, "instance.query.exec", "instance", in.ID, "ok",
			prepared.Keyword+"："+truncate(prepared.SQL, 300))
	}
	writeOK(w, r, res)
}
