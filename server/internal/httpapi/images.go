package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"niudbm/server/internal/images"
	"niudbm/server/internal/store"
)

// imageUsage 按台账统计每个版本被多少个服务使用。
//
// 镜像是"按版本共享"的资源：一个 mysql:8.4 可能被三个服务同时用着，
// 删之前必须能说清是谁在用，不能只给一句"镜像正在使用"。
func (s *Server) imageUsage(ctx context.Context) map[string][]string {
	usage := make(map[string][]string)
	list, err := s.st.ListInstances(ctx, "", "")
	if err != nil {
		return usage
	}
	for _, in := range list {
		usage[in.EngineVersion] = append(usage[in.EngineVersion], in.Name)
	}
	return usage
}

// handleListImages 返回可部署版本与本地镜像状态。
//
// 版本清单是编译期常量（images 包），本地有没有则实时问 docker：
// 这样界面上"未拉取/已就绪"永远是真的，不需要维护第二份状态。
func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	client := s.dockerClient()
	dockerOK := false
	if _, err := client.Available(r.Context()); err == nil {
		dockerOK = true
	}
	usage := s.imageUsage(r.Context())

	type imageItem struct {
		Tag      string   `json:"tag"`
		Name     string   `json:"name"`
		Family   string   `json:"family"`
		Label    string   `json:"label"`
		Note     string   `json:"note,omitempty"`
		Archs    []string `json:"archs"`
		Supports bool     `json:"supports"`
		Local    bool     `json:"local"`
		Default  bool     `json:"default"`
		// UsedBy 是正在用这个版本的服务名；空表示可以安全删除
		UsedBy []string `json:"usedBy"`
	}
	items := make([]imageItem, 0, len(images.List()))
	for _, img := range images.List() {
		item := imageItem{
			Tag: img.Tag, Name: img.Name, Family: img.Family, Label: img.Label,
			Note: img.Note, Archs: img.Archs, Supports: img.Supports(s.arch()),
			Default: img.Default,
			UsedBy:  usage[img.Tag],
		}
		if item.UsedBy == nil {
			item.UsedBy = []string{}
		}
		if dockerOK {
			// 加速站拉下来的标签带前缀，两个都查：只要有一个在本地就算就绪
			for _, cand := range s.imageCandidates(img.Name) {
				if client.ImageExists(r.Context(), cand) {
					item.Local = true
					break
				}
			}
		}
		items = append(items, item)
	}
	writeOK(w, r, map[string]any{
		"items":    items,
		"total":    len(items),
		"dockerOk": dockerOK,
		"arch":     s.arch(),
	})
}

type pullImageReq struct {
	Tag string `json:"tag"`
}

// handlePullImage 现在就把镜像拉到本机，不用等到部署时再等。
//
// 拉取可能几分钟，同步等会顶到网关超时，所以走任务中心：
// 立即回 jobId，前端轮询 /jobs/{id} 看进度与结果。
func (s *Server) handlePullImage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	var req pullImageReq
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	img, found := images.Find(req.Tag)
	if !found {
		s.fail(w, r, errBadRequest("没有这个版本："+req.Tag))
		return
	}
	if !img.Supports(s.arch()) {
		s.fail(w, r, errBadRequest(img.Name+" 官方没有 "+s.arch()+" 架构的镜像，换一个版本或换机器"))
		return
	}
	client := s.dockerClient()
	if _, err := client.Available(r.Context()); err != nil {
		s.fail(w, r, errState("Docker 不可用："+err.Error()))
		return
	}
	for _, cand := range s.imageCandidates(img.Name) {
		if client.ImageExists(r.Context(), cand) {
			writeOK(w, r, map[string]any{"image": img.Name, "cached": true, "message": "本地已有这个镜像"})
			return
		}
	}

	jobID := s.beginJob(id, jobImagePull, "image", img.Tag, img.Name)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.setJobProgress(jobID, 10)
		name, err := s.ensureImage(ctx, client, img.Name)
		if err != nil {
			s.finishJob(jobID, store.JobFailed, "", "拉取 "+img.Name+" 失败："+err.Error())
			return
		}
		s.setJobProgress(jobID, 100)
		s.finishJob(jobID, store.JobSucceeded, "已就绪："+name, "")
	}()
	s.audit(r, id, "image.pull", "image", img.Tag, "ok", "开始拉取 "+img.Name)
	writeAccepted(w, r, map[string]any{"async": true, "jobId": jobID, "image": img.Name})
}

// handleDeleteImage 删除本地镜像。
//
// 先按台账查占用：有服务用着就直接拒绝并点名，不进入 docker 那一步。
// 删镜像不会丢数据（数据在卷或宿主目录里），但会让下次部署重新拉取，所以
// 这属于"清理磁盘"的动作，界面上要说明白。
func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	img, found := images.Find(tag)
	if !found {
		s.fail(w, r, errNotFound("没有这个版本："+tag))
		return
	}
	if users := s.imageUsage(r.Context())[img.Tag]; len(users) > 0 {
		s.fail(w, r, errState(fmt.Sprintf(
			"还有 %d 个服务在用 %s（%s）：先删掉这些服务，或把它们换到别的版本",
			len(users), img.Name, strings.Join(users, "、"))))
		return
	}
	client := s.dockerClient()
	if _, err := client.Available(r.Context()); err != nil {
		s.fail(w, r, errState("Docker 不可用："+err.Error()))
		return
	}

	removed := 0
	var lastErr error
	for _, cand := range s.imageCandidates(img.Name) {
		if !client.ImageExists(r.Context(), cand) {
			continue
		}
		if err := client.ImageRemove(r.Context(), cand); err != nil {
			lastErr = err
			continue
		}
		removed++
	}
	if removed == 0 {
		if lastErr != nil {
			s.fail(w, r, errBadRequest("删除镜像失败："+lastErr.Error()))
			return
		}
		s.fail(w, r, errNotFound("本地没有 "+img.Name))
		return
	}
	s.audit(r, id, "image.delete", "image", img.Tag, "ok", "删除本地镜像 "+img.Name)
	writeOK(w, r, map[string]any{"message": "已从本机删除 " + img.Name})
}
