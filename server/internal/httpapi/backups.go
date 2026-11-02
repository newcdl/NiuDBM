package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

type backupReq struct {
	Note string `json:"note"`
	// Database 是这次要备份的库；留空表示服务里的默认库。
	// 一个服务能有多个库，备份按库走，导出的 .sql 只含这一个库。
	Database string `json:"database"`
}

type restoreReq struct {
	ConfirmName string `json:"confirmName"`
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	list, err := s.st.ListBackups(r.Context(), in.ID)
	if err != nil {
		s.fail(w, r, errInternal("读取备份列表失败"))
		return
	}
	writeOK(w, r, map[string]any{"items": list, "total": len(list), "backupRoot": s.paths.Backups})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req backupReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}

	// 备份按库走：没有默认库可以兜底，必须点名要备份哪个库
	dbName := strings.TrimSpace(req.Database)
	if dbName == "" {
		s.fail(w, r, errBadRequest("请选择要备份的库"))
		return
	}
	if err := mysqlctl.ValidDBName(dbName); err != nil {
		s.fail(w, r, errBadRequest(err.Error()))
		return
	}

	unlock := s.lockInstance(in.ID)
	defer unlock()

	started := time.Now()
	rec, err := s.makeBackup(r.Context(), in, "manual", req.Note, dbName)
	if err != nil {
		s.audit(r, id, "backup.create", "instance", in.ID, "failed", err.Error())
		s.recordJob(id, jobBackupCreate, "instance", in.ID, "note="+truncate(req.Note, 100), started, "", err)
		s.fail(w, r, err)
		return
	}
	s.audit(r, id, "backup.create", "backup", rec.ID, "ok",
		fmt.Sprintf("库 %s，%s，%d 字节，%s", rec.Database, rec.FileName, rec.SizeBytes, rec.StoragePath))
	s.recordJob(id, jobBackupCreate, "instance", in.ID, "库="+rec.Database+"，note="+truncate(req.Note, 100), started,
		rec.FileName+"（"+humanSize(rec.SizeBytes)+"）", nil)
	// 手动备份同样受保留策略约束：不然用户"点几次备份"就把共享目录塞满了
	s.pruneBackups(r.Context(), in.ID)
	writeCreated(w, r, rec)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	var req restoreReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ConfirmName != in.Name {
		s.fail(w, r, errRejected("请输入实例名以确认恢复"))
		return
	}

	bid := r.PathValue("bid")
	rec, err := s.st.GetBackup(r.Context(), in.ID, bid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, errNotFound("备份记录不存在"))
			return
		}
		s.fail(w, r, errInternal("读取备份记录失败"))
		return
	}
	if rec.Status != "succeeded" {
		s.fail(w, r, errRejected("该备份未成功完成，不能用于恢复"))
		return
	}
	if _, err := os.Stat(rec.StoragePath); err != nil {
		s.fail(w, r, errRejected("备份文件已丢失："+rec.FileName))
		return
	}

	// 整段恢复过程（含保护性备份）要占住实例锁，避免恢复与写入交叠
	unlock := s.lockInstance(in.ID)
	defer unlock()

	started := time.Now()
	// 恢复前自动留一份保护性备份，这样"点错了"也还能回去。
	// 备份哪个库就恢复哪个库，保护性备份也只备这一个库，不做整实例备份。
	protective, perr := s.makeBackup(r.Context(), in, "pre_restore", "恢复前自动备份", rec.Database)
	if perr != nil {
		s.audit(r, id, "backup.restore", "backup", rec.ID, "failed", "保护性备份失败："+perr.Error())
		s.recordJob(id, jobBackupRestore, "instance", in.ID, "backup="+rec.FileName, started, "",
			errBackupVerify("保护性备份失败："+perr.Error()))
		s.fail(w, r, errBackupVerify("恢复前自动备份失败，已中止："+perr.Error()))
		return
	}

	if err := s.restoreInto(r, in, rec.StoragePath); err != nil {
		s.audit(r, id, "backup.restore", "backup", rec.ID, "failed", err.Error())
		s.recordJob(id, jobBackupRestore, "instance", in.ID, "backup="+rec.FileName, started, "", err)
		s.fail(w, r, errBackupVerify("恢复失败："+err.Error()))
		return
	}

	_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
	s.audit(r, id, "backup.restore", "backup", rec.ID, "ok",
		"来自 "+rec.FileName+"，保护性备份 "+protective.FileName)
	s.recordJob(id, jobBackupRestore, "instance", in.ID, "backup="+rec.FileName, started,
		"已恢复到 "+rec.FileName, nil)

	writeOK(w, r, map[string]any{
		"restored":         true,
		"backupId":         rec.ID,
		"protectiveBackup": protective,
		"sizeBytes":        s.instanceSize(in),
	})
}

// restoreInto 只有逻辑恢复一种：dump 文件经容器内客户端导入。
//
// 恢复要求实例在运行，Restore 内部会保证这一点。
func (s *Server) restoreInto(r *http.Request, in store.Instance, src string) error {
	spec, err := s.mysqlSpec(in)
	if err != nil {
		return err
	}
	return spec.Restore(src)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}

	bid := r.PathValue("bid")
	rec, err := s.st.GetBackup(r.Context(), in.ID, bid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, errNotFound("备份记录不存在"))
			return
		}
		s.fail(w, r, errInternal("读取备份记录失败"))
		return
	}

	if err := os.Remove(rec.StoragePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(w, r, errInternal("删除备份文件失败："+err.Error()))
		return
	}
	if err := s.st.DeleteBackup(r.Context(), rec.ID); err != nil {
		s.fail(w, r, errInternal("删除备份记录失败"))
		return
	}
	s.audit(r, id, "backup.delete", "backup", rec.ID, "ok", rec.FileName)
	s.recordJob(id, jobBackupDelete, "instance", in.ID, "backup="+rec.FileName, time.Now(), "已删除", nil)
	writeOK(w, r, map[string]any{"id": rec.ID})
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	rec, err := s.st.GetBackup(r.Context(), in.ID, r.PathValue("bid"))
	if err != nil {
		s.fail(w, r, errNotFound("备份记录不存在"))
		return
	}
	f, err := os.Open(rec.StoragePath)
	if err != nil {
		s.fail(w, r, errNotFound("备份文件已丢失"))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+rec.FileName+`"`)
	http.ServeContent(w, r, rec.FileName, time.UnixMilli(rec.StartedAt), f)
}

// makeBackup 是备份与恢复共用的落盘流程：先落 running 记录，再执行，最后回填。
//
// 入参是 ctx 而不是 request：定时备份没有 HTTP 请求，同一套流程要能被调度器直接调用。
func (s *Server) makeBackup(ctx context.Context, in store.Instance, kind, note, database string) (store.Backup, error) {
	bid := ulid.New()
	// 只做逻辑备份：dump 出来是 .sql，跨版本、跨机器都能直接导入，
	// 也不需要停容器，日常备份不会打断用户连接。
	ext := ".sql"
	spec, err := s.mysqlSpec(in)
	if err != nil {
		return store.Backup{}, errBadRequest(err.Error())
	}
	// 库名必须明确：服务建出来是空的，没有"默认库"可以兜底。
	// 记录里写清真实库名，列表能看出这份备份属于哪个库，恢复时也回到这个库。
	database = strings.TrimSpace(database)
	if database == "" {
		return store.Backup{}, errBadRequest("请选择要备份的库")
	}
	// 文件名带上库名：同一个服务下多个库的备份放在同一目录，光看时间戳分不清是谁的。
	fileName := fmt.Sprintf("%s-%s-%s%s", time.Now().Format("20060102-150405"), sanitizeFileName(database), bid, ext)
	dst := filepath.Join(s.paths.Backups, in.ID, fileName)
	if _, err := os.Stat(dst); err == nil {
		return store.Backup{}, errInternal("备份文件名冲突，请重试")
	}

	rec := store.Backup{
		ID:          bid,
		InstanceID:  in.ID,
		Type:        kind,
		Status:      "running",
		StoragePath: dst,
		FileName:    fileName,
		Database:    database,
		Note:        truncate(note, 200),
		StartedAt:   time.Now().UnixMilli(),
	}
	if err := s.st.CreateBackup(ctx, rec); err != nil {
		return store.Backup{}, errInternal("写入备份记录失败")
	}

	finish := func(e error) (store.Backup, error) {
		rec.FinishedAt = time.Now().UnixMilli()
		if e != nil {
			rec.Status = "failed"
			rec.ErrorCode = "50001"
			rec.ErrorHint = e.Error()
			_ = s.st.FinishBackup(ctx, rec)
			return rec, errBackupTarget("备份失败：" + e.Error())
		}
		rec.Status = "succeeded"
		if fi, err := os.Stat(dst); err == nil {
			rec.SizeBytes = fi.Size()
		}
		rec.Checksum = sha256Of(dst)
		_ = s.st.FinishBackup(ctx, rec)
		return rec, nil
	}

	if err := spec.Backup(dst, database); err != nil {
		return finish(err)
	}
	if err := mysqlctl.Verify(dst); err != nil {
		return finish(fmt.Errorf("备份文件校验失败：%w", err))
	}
	return finish(nil)
}

// sanitizeFileName 把库名压成文件名安全的形式：
// 库名受 ValidDBName 约束，但老数据与手工导入的库名可能带别的字符，
// 拼进路径前统一兜一层，避免把备份写到别的目录去。
func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "db"
	}
	return out
}

func sha256Of(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
