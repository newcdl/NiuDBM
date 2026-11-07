package httpapi

import (
	"context"
	"testing"
	"time"

	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

func TestCancelJobStopsRegisteredExecution(t *testing.T) {
	e := newTestEnv(t, false)
	jobID := ulid.New()
	if err := e.st.CreateJob(context.Background(), store.Job{
		ID: jobID, Type: jobImagePull, TargetKind: "image", TargetID: "mysql:8.4",
		Status: store.JobRunning, Progress: 10, Attempts: 1, CreatedBy: "tester",
		CreatedAt: time.Now().UnixMilli(), StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.handler.registerJobCancel(jobID, cancel)

	e.must(t, 200, "POST", "/api/v1/jobs/"+jobID+"/cancel", nil)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("取消任务后执行 context 没有被取消")
	}
	job, err := e.st.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobCanceled {
		t.Fatalf("任务状态应为 canceled，实际 %q", job.Status)
	}
}
