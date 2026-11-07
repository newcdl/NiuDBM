package httpapi

import (
	"context"
	"net/http"
	"time"

	"niudbm/server/internal/store"
	"niudbm/server/internal/ulid"
)

// 部署一个常驻服务要几十秒到几分钟（拉镜像 + initdb + 初始化账号），
// 同步等只会让前端转圈。改成「立即返回 taskId + 后台执行 + 前端轮询阶段」，
// 用户才看得到现在卡在哪一步、失败是在哪一步失败的。
//
// 任务只存在内存里：进程重启后丢失，但那时部署本身也已经中断了，
// 不需要持久化——台账里有没有这个实例才是唯一事实。

const (
	deployRunning = "running"
	deployOK      = "ok"
	deployFailed  = "failed"
)

type deployTask struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Phase     string `json:"phase"`
	PhaseText string `json:"phaseText"`
	Status    string `json:"status"`
	// Error 是失败原因；成功后为空
	Error string `json:"error,omitempty"`
	// InstanceID 成功后才有，前端据此跳详情
	InstanceID string `json:"instanceId,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

// deployMaxKeep 限制内存里的任务条数，避免长期运行后无限增长。
const deployMaxKeep = 20

func (s *Server) startDeployTask(name, engine string) *deployTask {
	t := &deployTask{
		ID:        ulid.New(),
		Name:      name,
		Engine:    engine,
		Phase:     "prepare",
		PhaseText: "准备中：检查版本与端口",
		Status:    deployRunning,
		StartedAt: time.Now().UnixMilli(),
	}

	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	// 先清掉已结束的旧任务；仍在跑的必须留着，否则前端轮询会 404
	if len(s.deploys) >= deployMaxKeep {
		for id, old := range s.deploys {
			if old.Status == deployRunning {
				continue
			}
			delete(s.deploys, id)
			if len(s.deploys) < deployMaxKeep {
				break
			}
		}
	}
	s.deploys[t.ID] = t
	if s.st != nil {
		_ = s.st.CreateDeployTask(context.Background(), store.DeployTask{
			ID: t.ID, Name: t.Name, Engine: t.Engine, Phase: t.Phase,
			PhaseText: t.PhaseText, Status: t.Status, StartedAt: t.StartedAt,
		})
	}
	return t
}

func (s *Server) deployPhase(id, phase, text string) {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	if t, ok := s.deploys[id]; ok {
		t.Phase = phase
		t.PhaseText = text
		if s.st != nil {
			_ = s.st.UpdateDeployTask(context.Background(), store.DeployTask{
				ID: t.ID, Phase: t.Phase, PhaseText: t.PhaseText, Status: t.Status,
				Error: t.Error, InstanceID: t.InstanceID, FinishedAt: t.FinishedAt,
			})
		}
	}
}

func (s *Server) deployDone(id, instanceID string, err error) {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	t, ok := s.deploys[id]
	if !ok {
		return
	}
	t.FinishedAt = time.Now().UnixMilli()
	// 失败也把实例号带出去：台账里已经留着 failed 记录，前端据此才能
	// 点开会话看部署日志、删掉重来，而不是只能去操作记录里翻。
	t.InstanceID = instanceID
	if err != nil {
		t.Status = deployFailed
		t.Error = err.Error()
		t.Phase = "failed"
		t.PhaseText = "部署失败"
		if s.st != nil {
			_ = s.st.UpdateDeployTask(context.Background(), store.DeployTask{
				ID: t.ID, Phase: t.Phase, PhaseText: t.PhaseText, Status: t.Status,
				Error: t.Error, InstanceID: t.InstanceID, FinishedAt: t.FinishedAt,
			})
		}
		return
	}
	t.Status = deployOK
	t.Phase = "done"
	t.PhaseText = "部署完成"
	if s.st != nil {
		_ = s.st.UpdateDeployTask(context.Background(), store.DeployTask{
			ID: t.ID, Phase: t.Phase, PhaseText: t.PhaseText, Status: t.Status,
			InstanceID: t.InstanceID, FinishedAt: t.FinishedAt,
		})
	}
}

func (s *Server) deploySnapshot(id string) (deployTask, bool) {
	s.deployMu.Lock()
	t, ok := s.deploys[id]
	if ok {
		snapshot := *t
		s.deployMu.Unlock()
		return snapshot, true
	}
	s.deployMu.Unlock()
	if s.st == nil {
		return deployTask{}, false
	}
	saved, err := s.st.GetDeployTask(context.Background(), id)
	if err != nil {
		return deployTask{}, false
	}
	return deployTask{
		ID: saved.ID, Name: saved.Name, Engine: saved.Engine, Phase: saved.Phase,
		PhaseText: saved.PhaseText, Status: saved.Status, Error: saved.Error,
		InstanceID: saved.InstanceID, StartedAt: saved.StartedAt, FinishedAt: saved.FinishedAt,
	}, true
}

// handleGetDeployTask 供前端轮询部署进度。
func (s *Server) handleGetDeployTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	t, ok := s.deploySnapshot(r.PathValue("id"))
	if !ok {
		s.fail(w, r, errNotFound("部署任务不存在或已过期（服务重启后任务记录会清空）"))
		return
	}
	writeOK(w, r, t)
}
