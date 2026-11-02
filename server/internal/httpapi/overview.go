package httpapi

import (
	"net/http"
	"time"

	"niudbm/server/internal/images"
)

// overviewImage 是概览页的镜像块：这个版本本地有没有、当前架构能不能跑。
type overviewImage struct {
	Tag      string `json:"tag"`
	Name     string `json:"name"`
	Label    string `json:"label"`
	Family   string `json:"family"`
	Local    bool   `json:"local"`
	DockerOK bool   `json:"dockerOk"`
}

// overviewResult 是 GET /overview 的返回：概览页一次请求拿全，避免首屏打七八个接口。
type overviewResult struct {
	Version   string           `json:"version"`
	Instances overviewInstance `json:"instances"`
	Images    []overviewImage  `json:"images"`
	Backups   overviewBackup   `json:"backups"`
	Jobs      overviewJobs     `json:"jobs"`
	Audit     overviewAudit    `json:"audit"`
	Docker    overviewDocker   `json:"docker"`
}

type overviewInstance struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	// Creating / Failed 是部署过程的状态：不单列出来，用户会以为"少了几个服务"
	Creating int `json:"creating"`
	Failed   int `json:"failed"`
	Error    int `json:"error"`
	// SizeBytes 只统计算得出的目录；SizeUnknown 是数据在 docker 卷里、读不到体积的实例数
	SizeBytes   int64          `json:"sizeBytes"`
	SizeUnknown int            `json:"sizeUnknown"`
	Items       []instanceView `json:"items"`
}

type overviewBackup struct {
	Total    int                  `json:"total"`
	Failed   int                  `json:"failed"`
	LatestAt int64                `json:"latestAt"`
	Recent   []overviewBackupItem `json:"recent"`
}

// overviewBackupItem 是概览页要的最小字段：文件名、哪个库、成没成。
type overviewBackupItem struct {
	ID           string `json:"id"`
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	SizeBytes    int64  `json:"sizeBytes"`
	FileName     string `json:"fileName"`
	StartedAt    int64  `json:"startedAt"`
}

type overviewJobs struct {
	Active    int    `json:"active"`
	Failed24h int    `json:"failed24h"`
	Recent    []jobB `json:"recent"`
}

// jobB 是任务在概览页的展示字段。
type jobB struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	TargetID  string `json:"targetId"`
	CreatedAt int64  `json:"createdAt"`
}

type overviewAudit struct {
	Today  int          `json:"today"`
	Recent []auditBrief `json:"recent"`
}

// auditBrief 只带上列表页要显示的内容，详情交给「操作记录」页。
type auditBrief struct {
	ID       int64  `json:"id"`
	Ts       int64  `json:"ts"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Result   string `json:"result"`
	Detail   string `json:"detail"`
}

type overviewDocker struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
}

// handleOverview 汇总概览页需要的数字与最近动态。
//
// 数据量都很小（实例几十个、备份上千条），所以直接用现成的查询在内存里聚合，
// 不额外维护统计表——那种表一旦和真相不一致，排查成本远高于这点查询开销。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	ctx := r.Context()
	out := overviewResult{Version: s.env.Version}

	list, err := s.st.ListInstances(ctx, "", "")
	if err != nil {
		s.fail(w, r, errInternal("读取实例失败"))
		return
	}
	items := make([]instanceView, 0, len(list))
	for _, in := range list {
		v := s.view(in)
		out.Instances.Total++
		// 用容器真实状态计数：台账里可能还是 running，但容器早就退出了
		switch v.Status {
		case "running":
			out.Instances.Running++
		case "stopped":
			out.Instances.Stopped++
		case "creating":
			out.Instances.Creating++
		case "failed":
			out.Instances.Failed++
		case "error":
			out.Instances.Error++
		}
		if v.SizeUnknown {
			out.Instances.SizeUnknown++
		} else {
			out.Instances.SizeBytes += v.SizeBytes
		}
		items = append(items, v)
	}
	// 概览里的实例卡片同样显示备份情况，省得用户再点进详情确认
	s.attachBackupStats(ctx, items)
	// 只有"有东西可看"的才往概览上放，最近的排在前面
	sortInstancesByCreated(items)
	if len(items) > 8 {
		items = items[:8]
	}
	out.Instances.Items = items

	// 镜像：本地有没有、Docker 能不能用。引擎不再落盘，概览只需要这两件事。
	dockerVersion, dockerErr := s.dockerClient().Available(ctx)
	out.Docker.Available = dockerErr == nil
	out.Docker.Version = dockerVersion
	for _, img := range images.List() {
		item := overviewImage{
			Tag:      img.Tag,
			Name:     img.Name,
			Label:    img.Label,
			Family:   img.Family,
			DockerOK: img.Supports(s.arch()) && out.Docker.Available,
		}
		if out.Docker.Available {
			item.Local = s.dockerClient().ImageExists(ctx, img.Name)
		}
		out.Images = append(out.Images, item)
	}

	if stat, err := s.st.BackupStats(ctx, 0); err == nil {
		out.Backups.Total = stat.Total
		out.Backups.Failed = stat.Failed
		out.Backups.LatestAt = stat.LatestAt
	}
	if recent, err := s.st.ListRecentBackups(ctx, 5); err == nil {
		for _, b := range recent {
			out.Backups.Recent = append(out.Backups.Recent, overviewBackupItem{
				ID: b.ID, InstanceID: b.InstanceID, InstanceName: b.InstanceName,
				Type: b.Type, Status: b.Status, SizeBytes: b.SizeBytes,
				FileName: b.FileName, StartedAt: b.StartedAt,
			})
		}
	}

	if active, err := s.st.ActiveJobCount(ctx); err == nil {
		out.Jobs.Active = active
	}
	if failed, err := s.st.CountJobsSince(ctx, time.Now().Add(-24*time.Hour).UnixMilli(), "failed"); err == nil {
		out.Jobs.Failed24h = failed
	}
	if jobs, err := s.st.ListJobs(ctx, "", 5); err == nil {
		for _, j := range jobs {
			out.Jobs.Recent = append(out.Jobs.Recent, jobB{
				ID: j.ID, Type: j.Type, Status: j.Status, TargetID: j.TargetID, CreatedAt: j.CreatedAt,
			})
		}
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if n, err := s.st.CountAuditSince(ctx, startOfDay.UnixMilli()); err == nil {
		out.Audit.Today = n
	}
	if logs, err := s.st.ListAudit(ctx, 5); err == nil {
		for _, l := range logs {
			out.Audit.Recent = append(out.Audit.Recent, auditBrief{
				ID: l.ID, Ts: l.Ts, Username: l.Username, Action: l.Action,
				Result: l.Result, Detail: l.Detail,
			})
		}
	}

	writeOK(w, r, out)
}

// sortInstancesByCreated 按创建时间倒序，保证概览上看到的是最近建的库。
func sortInstancesByCreated(items []instanceView) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt > items[j-1].CreatedAt; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
