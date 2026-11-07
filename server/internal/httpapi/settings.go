package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// settingsKey 是全局设置在 k-v 表里的键。
const settingsKey = "app"

// 保留策略的默认值：与前端展示的默认一致，避免"页面显示 7、实际按 30 清"。
const (
	defaultBackupRetention = 7
	defaultAuditRetention  = 180
)

// engineSummary 是设置页展示的引擎概况（只读）。
type engineSummary struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Version string `json:"version"`
}

// appSettings 是 GET/PATCH /settings 的契约结构。
type appSettings struct {
	Version               string          `json:"version"`
	BackupRetentionCount  int             `json:"backupRetentionCount"`
	BackupDir             string          `json:"backupDir"`
	AuditRetentionDays    int             `json:"auditRetentionDays"`
	NotifyWebhook         string          `json:"notifyWebhook"`
	NotifyBackupSucceeded bool            `json:"notifyBackupSucceeded"`
	NotifyBackupFailed    bool            `json:"notifyBackupFailed"`
	NotifyInstanceDown    bool            `json:"notifyInstanceDown"`
	Engines               []engineSummary `json:"engines"`
}

// settingsPatch 是 PATCH 的入参：指针区分"没传"与"传了零值"。
//
// 只读字段（版本、目录、引擎概况）也要接住：界面是把整份设置回传的，
// 解析时开了 DisallowUnknownFields，少一个字段就 400，用户看到的是"保存失败"。
type settingsPatch struct {
	BackupRetentionCount  *int            `json:"backupRetentionCount"`
	AuditRetentionDays    *int            `json:"auditRetentionDays"`
	NotifyWebhook         *string         `json:"notifyWebhook"`
	NotifyBackupSucceeded *bool           `json:"notifyBackupSucceeded"`
	NotifyBackupFailed    *bool           `json:"notifyBackupFailed"`
	NotifyInstanceDown    *bool           `json:"notifyInstanceDown"`
	Version               *string         `json:"version"`
	BackupDir             *string         `json:"backupDir"`
	Engines               json.RawMessage `json:"engines"`
}

// storedSettings 是真正落库的那部分：只读字段（版本、目录、引擎）不存。
type storedSettings struct {
	BackupRetentionCount  int    `json:"backupRetentionCount"`
	AuditRetentionDays    int    `json:"auditRetentionDays"`
	NotifyWebhook         string `json:"notifyWebhook"`
	NotifyBackupSucceeded bool   `json:"notifyBackupSucceeded"`
	NotifyBackupFailed    bool   `json:"notifyBackupFailed"`
	NotifyInstanceDown    bool   `json:"notifyInstanceDown"`
}

func defaultStoredSettings() storedSettings {
	return storedSettings{
		BackupRetentionCount: defaultBackupRetention,
		AuditRetentionDays:   defaultAuditRetention,
		// 备份失败与实例掉线默认推送：这两件事用户不主动看就不会发现
		NotifyBackupFailed: true,
		NotifyInstanceDown: true,
	}
}

// loadSettings 读设置并把只读字段补齐；读到坏数据时退回默认值而不是报错，
// 否则一个手改坏的 JSON 会让整个设置页打不开。
func (s *Server) loadSettings(ctx context.Context) appSettings {
	stored := defaultStoredSettings()
	if kv, err := s.st.SettingsKV(ctx); err == nil {
		if raw, ok := kv[settingsKey]; ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &stored)
		}
	}
	return appSettings{
		Version:               s.env.Version,
		BackupRetentionCount:  stored.BackupRetentionCount,
		BackupDir:             s.paths.Backups,
		AuditRetentionDays:    stored.AuditRetentionDays,
		NotifyWebhook:         stored.NotifyWebhook,
		NotifyBackupSucceeded: stored.NotifyBackupSucceeded,
		NotifyBackupFailed:    stored.NotifyBackupFailed,
		NotifyInstanceDown:    stored.NotifyInstanceDown,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	writeOK(w, r, s.loadSettings(r.Context()))
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	var patch settingsPatch
	if err := decodeJSON(r, &patch); err != nil {
		s.fail(w, r, err)
		return
	}

	current := s.loadSettings(r.Context())
	stored := storedSettings{
		BackupRetentionCount:  current.BackupRetentionCount,
		AuditRetentionDays:    current.AuditRetentionDays,
		NotifyWebhook:         current.NotifyWebhook,
		NotifyBackupSucceeded: current.NotifyBackupSucceeded,
		NotifyBackupFailed:    current.NotifyBackupFailed,
		NotifyInstanceDown:    current.NotifyInstanceDown,
	}
	changes := make([]string, 0, 4)

	if patch.BackupRetentionCount != nil {
		v := *patch.BackupRetentionCount
		if v < 0 || v > 100 {
			s.fail(w, r, errBadRequest("备份保留份数只能是 0–100（0 表示不自动清理）"))
			return
		}
		stored.BackupRetentionCount = v
		changes = append(changes, fmt.Sprintf("备份保留 %d 份", v))
	}
	if patch.AuditRetentionDays != nil {
		v := *patch.AuditRetentionDays
		if v < 0 || v > 3650 {
			s.fail(w, r, errBadRequest("审计保留天数只能是 0–3650（0 表示不清理）"))
			return
		}
		stored.AuditRetentionDays = v
		changes = append(changes, fmt.Sprintf("审计保留 %d 天", v))
	}
	if patch.NotifyWebhook != nil {
		hook := strings.TrimSpace(*patch.NotifyWebhook)
		if hook != "" {
			u, err := url.Parse(hook)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				s.fail(w, r, errBadRequest("Webhook 必须是 http:// 或 https:// 开头的完整地址"))
				return
			}
		}
		stored.NotifyWebhook = hook
		changes = append(changes, "通知地址已更新")
	}
	for _, f := range []struct {
		in   *bool
		dst  *bool
		name string
	}{
		{patch.NotifyBackupSucceeded, &stored.NotifyBackupSucceeded, "备份成功通知"},
		{patch.NotifyBackupFailed, &stored.NotifyBackupFailed, "备份失败通知"},
		{patch.NotifyInstanceDown, &stored.NotifyInstanceDown, "实例异常通知"},
	} {
		if f.in == nil {
			continue
		}
		*f.dst = *f.in
		state := "关闭"
		if *f.in {
			state = "开启"
		}
		changes = append(changes, f.name+state)
	}

	raw, err := json.Marshal(stored)
	if err != nil {
		s.fail(w, r, errInternal("序列化设置失败"))
		return
	}
	if err := s.st.PutSettingsKV(r.Context(), map[string]string{settingsKey: string(raw)}); err != nil {
		s.fail(w, r, errInternal("保存设置失败"))
		return
	}
	detail := "无变化"
	if len(changes) > 0 {
		detail = strings.Join(changes, "，")
	}
	s.audit(r, id, "settings.update", "settings", settingsKey, "ok", detail)
	writeOK(w, r, s.loadSettings(r.Context()))
}

// notifyEvent 是推送给 Webhook 的报文。字段刻意少而稳定，方便用户自己接机器人。
type notifyEvent struct {
	App          string `json:"app"`
	Version      string `json:"version"`
	Event        string `json:"event"`
	Title        string `json:"title"`
	Message      string `json:"message"`
	InstanceID   string `json:"instanceId,omitempty"`
	InstanceName string `json:"instanceName,omitempty"`
	At           int64  `json:"at"`
}

// notify 按事件类型决定要不要推送；没配 Webhook 时直接返回，
// 推送失败只记日志，绝不因为通知发不出去影响主业务。
func (s *Server) notify(ctx context.Context, cfg appSettings, ev notifyEvent) error {
	if cfg.NotifyWebhook == "" {
		return nil
	}
	ev.App = "niudbm"
	ev.Version = s.env.Version
	if ev.At == 0 {
		ev.At = time.Now().UnixMilli()
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.NotifyWebhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("接收端返回 %s", resp.Status)
	}
	return nil
}

// handleTestNotify 发一条测试消息，让用户当场确认 Webhook 通不通。
func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	id, ok := s.require(w, r, true)
	if !ok {
		return
	}
	cfg := s.loadSettings(r.Context())
	if cfg.NotifyWebhook == "" {
		s.fail(w, r, errBadRequest("请先填写 Webhook 地址再发送"))
		return
	}
	err := s.notify(r.Context(), cfg, notifyEvent{
		Event:   "test",
		Title:   "牛库管家测试通知",
		Message: "如果你看到这条消息，说明 Webhook 配置可用。",
	})
	if err != nil {
		s.audit(r, id, "settings.notify_test", "settings", settingsKey, "failed", err.Error())
		s.fail(w, r, errBadRequest("测试通知发送失败："+err.Error()))
		return
	}
	s.audit(r, id, "settings.notify_test", "settings", settingsKey, "ok", "测试通知发送成功")
	writeOK(w, r, map[string]any{"ok": true, "message": "测试通知已发送，请到接收端确认。"})
}

// auditRetentionDays 供调度器读取，避免把整个 settings 传下去。
func (s *Server) auditRetentionDays(ctx context.Context) int {
	return s.loadSettings(ctx).AuditRetentionDays
}

// backupRetentionCount 取默认保留份数（策略没写时用它）。
func (s *Server) backupRetentionCount(ctx context.Context) int {
	return s.loadSettings(ctx).BackupRetentionCount
}
