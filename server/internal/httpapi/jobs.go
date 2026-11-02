package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

// 任务类型。命名沿用"对象.动作"，前端按前缀分组显示。
const (
	jobInstanceCreate  = "instance.create"
	jobInstanceStart   = "instance.start"
	jobInstanceStop    = "instance.stop"
	jobInstanceRestart = "instance.restart"
	jobInstanceDelete  = "instance.delete"
	jobBackupCreate    = "backup.create"
	jobBackupSchedule  = "backup.scheduled"
	jobBackupRestore   = "backup.restore"
	jobBackupDelete    = "backup.delete"
	jobDBImport        = "db.import"
	jobImagePull       = "image.pull"
	jobEngineDownload  = "engine.download"
	jobEngineImport    = "engine.import"
)

// recordJob 记录一次已经结束的操作。
//
// 本应用的写操作都是同步完成的（请求返回即出结果），因此不引入任务队列，
// 直接落一条终态记录：任务中心要的是"操作履历"，而不是"必须异步才有意义"。
func (s *Server) recordJob(id identity, typ, kind, target, params string, started time.Time, result string, opErr error) {
	job := store.Job{
		ID:         ulid.New(),
		Type:       typ,
		TargetKind: kind,
		TargetID:   target,
		Params:     truncate(params, 500),
		Result:     truncate(result, 500),
		Attempts:   1,
		CreatedBy:  id.name(),
		CreatedAt:  started.UnixMilli(),
		StartedAt:  started.UnixMilli(),
		FinishedAt: time.Now().UnixMilli(),
		Status:     store.JobSucceeded,
		Progress:   100,
	}
	if opErr != nil {
		job.Status = store.JobFailed
		job.Progress = 0
		job.Result = ""
		job.ErrorHint = truncate(opErr.Error(), 500)
		job.ErrorCode = errorCodeString(opErr)
	}
	// 记录失败不能影响业务结果：操作已经做完了，这里只是留痕
	_ = s.st.CreateJob(context.WithoutCancel(context.Background()), job)
}

// beginJob 为异步操作（引擎下载/导入、定时备份）开一条 running 记录。
func (s *Server) beginJob(id identity, typ, kind, target, params string) string {
	jobID := ulid.New()
	now := time.Now().UnixMilli()
	_ = s.st.CreateJob(context.Background(), store.Job{
		ID:         jobID,
		Type:       typ,
		TargetKind: kind,
		TargetID:   target,
		Params:     truncate(params, 500),
		Status:     store.JobRunning,
		Progress:   1,
		Attempts:   1,
		CreatedBy:  id.name(),
		CreatedAt:  now,
		StartedAt:  now,
	})
	return jobID
}

// finishJob 把异步任务落到终态。已经是终态（例如用户取消）时保持原样。
func (s *Server) finishJob(jobID, status, result, hint string) {
	if jobID == "" {
		return
	}
	ctx := context.Background()
	job, err := s.st.GetJob(ctx, jobID)
	if err != nil || !job.Active() {
		return
	}
	job.Status = status
	job.Result = truncate(result, 500)
	job.ErrorHint = truncate(hint, 500)
	if status == store.JobFailed {
		job.ErrorCode = "30001"
	}
	job.FinishedAt = time.Now().UnixMilli()
	_ = s.st.FinishJob(ctx, job)
}

// setJobProgress 只更新进度，失败不影响主流程。
func (s *Server) setJobProgress(jobID string, pct int) {
	if jobID == "" {
		return
	}
	_ = s.st.SetJobProgress(context.Background(), jobID, pct)
}

// errorCodeString 把接口错误里的业务码转成字符串，便于任务中心展示。
func errorCodeString(err error) string {
	var ae apiErr
	if errors.As(err, &ae) {
		return strconv.Itoa(ae.Code)
	}
	return ""
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	items, err := s.st.ListJobs(r.Context(), status, limit)
	if err != nil {
		s.fail(w, r, errInternal("读取任务失败"))
		return
	}
	active, err := s.st.ActiveJobCount(r.Context())
	if err != nil {
		active = 0
	}
	writeOK(w, r, map[string]any{"items": items, "total": len(items), "active": active})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	job, err := s.st.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, errNotFound("任务不存在"))
			return
		}
		s.fail(w, r, errInternal("读取任务失败"))
		return
	}
	writeOK(w, r, job)
}

// handleCancelJob 取消一个还没结束的任务。
//
// 引擎下载/导入是唯一真正能"立刻停下"的操作（有上下文）；同步操作在写入任务时
// 已经是终态，走到这里会得到 409，前端据此提示"任务已结束"。
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	job, err := s.st.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, errNotFound("任务不存在"))
			return
		}
		s.fail(w, r, errInternal("读取任务失败"))
		return
	}
	if !job.Active() {
		s.fail(w, r, errState("该任务已结束，不能取消"))
		return
	}

	// 顺序很重要：先在台账里落"已取消"，再撤执行上下文。反过来的话，
	// 执行侧的收尾回调会抢先把状态写成 failed，用户看到的是"取消失败"。
	canceled, _ := s.st.CancelJob(r.Context(), jobID)

	s.audit(r, id, "job.cancel", "job", jobID, "ok", job.Type+" "+job.TargetID)
	writeOK(w, r, map[string]any{"id": canceled.ID, "status": canceled.Status})
}

func jobParams(kv map[string]any) string {
	b, err := json.Marshal(kv)
	if err != nil {
		return ""
	}
	return string(b)
}
