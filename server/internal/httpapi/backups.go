package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"niudbm/server/internal/engine"
	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

type backupReq struct {
	Note string `json:"note"`
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

	unlock := s.lockInstance(in.ID)
	defer unlock()

	rec, err := s.makeBackup(r, in, "manual", req.Note)
	if err != nil {
		s.audit(r, id, "backup.create", "instance", in.ID, "failed", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, id, "backup.create", "backup", rec.ID, "ok",
		fmt.Sprintf("%s，%d 字节，%s", rec.FileName, rec.SizeBytes, rec.StoragePath))
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

	// 恢复前自动留一份保护性备份，这样"点错了"也还能回去
	protective, perr := s.makeBackup(r, in, "pre_restore", "恢复前自动备份")
	if perr != nil {
		s.audit(r, id, "backup.restore", "backup", rec.ID, "failed", "保护性备份失败："+perr.Error())
		s.fail(w, r, errBackupVerify("恢复前自动备份失败，已中止："+perr.Error()))
		return
	}

	if err := engine.Restore(rec.StoragePath, s.dataFile(in)); err != nil {
		s.audit(r, id, "backup.restore", "backup", rec.ID, "failed", err.Error())
		s.fail(w, r, errBackupVerify("恢复失败："+err.Error()))
		return
	}

	_ = s.st.SetStatus(r.Context(), in.ID, "running", "")
	s.audit(r, id, "backup.restore", "backup", rec.ID, "ok",
		"来自 "+rec.FileName+"，保护性备份 "+protective.FileName)

	writeOK(w, r, map[string]any{
		"restored":         true,
		"backupId":         rec.ID,
		"protectiveBackup": protective,
		"sizeBytes":        engine.Size(s.dataFile(in)),
	})
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
func (s *Server) makeBackup(r *http.Request, in store.Instance, kind, note string) (store.Backup, error) {
	bid := ulid.New()
	// 文件名用完整 ULID：只取前几位会落在同一秒内重复（ULID 前段就是时间戳），
	// 导致两份备份互相覆盖——手动备份与保护性备份很容易在同一秒发生。
	fileName := fmt.Sprintf("%s-%s.db", time.Now().Format("20060102-150405"), bid)
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
		Note:        truncate(note, 200),
		StartedAt:   time.Now().UnixMilli(),
	}
	if err := s.st.CreateBackup(r.Context(), rec); err != nil {
		return store.Backup{}, errInternal("写入备份记录失败")
	}

	finish := func(e error) (store.Backup, error) {
		rec.FinishedAt = time.Now().UnixMilli()
		if e != nil {
			rec.Status = "failed"
			rec.ErrorCode = "50001"
			rec.ErrorHint = e.Error()
			_ = s.st.FinishBackup(r.Context(), rec)
			return rec, errBackupTarget("备份失败：" + e.Error())
		}
		rec.Status = "succeeded"
		if fi, err := os.Stat(dst); err == nil {
			rec.SizeBytes = fi.Size()
		}
		rec.Checksum = sha256Of(dst)
		_ = s.st.FinishBackup(r.Context(), rec)
		return rec, nil
	}

	if err := engine.Backup(s.dataFile(in), dst); err != nil {
		return finish(err)
	}
	if err := engine.Verify(dst); err != nil {
		return finish(fmt.Errorf("备份文件校验失败：%w", err))
	}
	return finish(nil)
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
