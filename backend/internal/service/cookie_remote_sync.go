package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"time"
)

// runCookieRemoteSync periodically merges a remote library by host. Newer
// token timestamps win (capture time for legacy entries); never replace wholesale.
func (s *SettingService) runCookieRemoteSync(ctx context.Context) {
	var last time.Time
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		settings, err := s.GetOpenAICookieSettings(ctx)
		if err == nil && settings.RemoteSyncEnabled {
			interval := settings.RemoteSyncIntervalSeconds
			if interval < 10 {
				interval = 60
			}
			if time.Since(last) < time.Duration(interval)*time.Second {
				continue
			}
			_ = s.syncRemoteCookieLibrary(ctx, settings)
			last = time.Now()
		} else {
			last = time.Time{}
		}
	}
}

func (s *SettingService) syncRemoteCookieLibrary(ctx context.Context, settings *OpenAICookieSettings) (resultErr error) {
	added, updated, kept := 0, 0, 0
	defer func() {
		message := fmt.Sprintf("远程同步成功：新增 %d，更新 %d，保留 %d", added, updated, kept)
		if resultErr != nil {
			message = "远程同步失败：" + resultErr.Error()
		}
		_ = s.appendOpenAICookieLog(ctx, OpenAICookieAcquisitionLog{ID: uuid.NewString(), Kind: "remote_sync", CreatedAt: time.Now(), Success: resultErr == nil, Message: message})
	}()
	base := strings.TrimRight(strings.TrimSpace(settings.RemoteSyncURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/admin/settings/openai-codex-cookie-library", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", settings.RemoteSyncAdminKey)
	resp, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return fmt.Errorf("连接远程服务器失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote cookie sync returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Code int                             `json:"code"`
		Data []OpenAICodexCookieLibraryEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("远程响应格式无效")
	}
	if envelope.Code != 0 || envelope.Data == nil {
		return fmt.Errorf("远程响应不是有效 Cookie 库")
	}
	remote := normalizeOpenAICodexCookieLibrary(envelope.Data, time.Now())
	openAICodexCookieLibraryMu.Lock()
	defer openAICodexCookieLibraryMu.Unlock()
	local, err := s.GetOpenAICodexCookieLibrary(ctx)
	if err != nil {
		return err
	}
	byHost := map[string]OpenAICodexCookieLibraryEntry{}
	for _, e := range local {
		byHost[e.Host] = e
	}
	for _, e := range remote {
		if old, ok := byHost[e.Host]; !ok {
			added++
			byHost[e.Host] = e
		} else if openAICodexCookieIsNewer(e, old) {
			updated++
			byHost[e.Host] = e
		} else {
			kept++
		}
	}
	merged := make([]OpenAICodexCookieLibraryEntry, 0, len(byHost))
	for _, e := range byHost {
		merged = append(merged, e)
	}
	_, err = s.setOpenAICodexCookieLibraryLocked(ctx, merged)
	return err
}
