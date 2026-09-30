package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const responseCookieQueueSize = 128

type responseCookieEvent struct {
	accountID   int64
	accountName string
	statusCode  int
	receivedAt  time.Time
	cookies     []string
	oversized   bool
}

type responseCookieSyncRuntime struct {
	once    sync.Once
	queue   chan responseCookieEvent
	running atomic.Bool
	dropped atomic.Uint64
}

// The request path only copies bounded header data and attempts a nonblocking
// enqueue. It never reads settings from storage, parses JWTs, or waits for I/O.
// Only real upstream Set-Cookie headers are used, before downstream rewriting.
func (s *OpenAIGatewayService) enqueueOpenAIResponseCookie(account *Account, response *http.Response) {
	if s == nil || s.settingService == nil || account == nil || account.Platform != PlatformOpenAI || response == nil {
		return
	}
	if cached, ok := s.settingService.openAICookieCache.Load().(*cachedOpenAICookieSettings); ok &&
		time.Now().Before(cached.until) && !cached.value.ResponseCookieSyncEnabled {
		return
	}
	event := responseCookieEvent{accountID: account.ID, accountName: account.Name, statusCode: response.StatusCode, receivedAt: time.Now()}
	total := 0
	for key, values := range response.Header {
		if !strings.EqualFold(key, "Set-Cookie") {
			continue
		}
		for _, value := range values {
			total += len(value)
			if total > 32768 || len(event.cookies) >= 64 {
				event.oversized = true
				break
			}
			event.cookies = append(event.cookies, value)
		}
	}
	if len(event.cookies) == 0 && !event.oversized {
		return
	}
	runtime := &s.responseCookieSync
	runtime.once.Do(func() { runtime.queue = make(chan responseCookieEvent, responseCookieQueueSize) })
	select {
	case runtime.queue <- event:
	default:
		runtime.dropped.Add(1)
	}
	if runtime.running.CompareAndSwap(false, true) {
		go s.drainOpenAIResponseCookies()
	}
}

// One worker bounds storage pressure. It exits when idle; the CAS recheck
// closes the race between an enqueue and the worker going idle.
func (s *OpenAIGatewayService) drainOpenAIResponseCookies() {
	runtime := &s.responseCookieSync
	for {
		if dropped := runtime.dropped.Swap(0); dropped > 0 {
			s.writeResponseCookieLog(OpenAICookieAcquisitionLog{
				Stage: "queue_full", Message: fmt.Sprintf("后台同步队列已满，跳过 %d 条响应 Cookie；请求正常继续", dropped),
			})
		}
		select {
		case event := <-runtime.queue:
			s.processOpenAIResponseCookie(event)
		default:
			runtime.running.Store(false)
			if (len(runtime.queue) > 0 || runtime.dropped.Load() > 0) && runtime.running.CompareAndSwap(false, true) {
				continue
			}
			return
		}
	}
}

func (s *OpenAIGatewayService) processOpenAIResponseCookie(event responseCookieEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	item := OpenAICookieAcquisitionLog{
		AccountID: event.accountID, AccountName: event.accountName,
		StatusCode: event.statusCode, CreatedAt: event.receivedAt,
	}
	settings, err := s.responseCookieSettings(ctx)
	if err != nil {
		item.Stage, item.Message = "failed", "读取响应 Cookie 同步配置失败"
		s.writeResponseCookieLog(item)
		return
	}
	if !settings.ResponseCookieSyncEnabled {
		return
	}
	defer func() { s.writeResponseCookieLog(item) }()
	if event.oversized {
		item.Stage, item.Message = "ignored_invalid", "响应 Cookie 超出大小限制，保留库中 Cookie"
		return
	}
	item.Cookie = openAICodexResponseCookieHeader(&http.Response{Header: http.Header{"Set-Cookie": event.cookies}})
	item.Host, item.Payload = openAICodexCookieJWTInfo(item.Cookie)
	if event.statusCode < 200 || event.statusCode >= 300 {
		item.Stage, item.Message = "ignored_status", "上游 HTTP 响应未成功，保留库中 Cookie"
		return
	}
	exp, ok := item.Payload["exp"].(float64)
	if item.Host == "" || !ok || exp <= float64(time.Now().Unix()) {
		item.Stage, item.Message = "ignored_invalid", "响应未包含有效且未过期的 Host Cookie，保留库中 Cookie"
		return
	}
	if !openAICodexCookieHostAllowed(item.Cookie, settings.HostWhitelist) {
		item.Stage, item.Message = "ignored_host", "响应 Cookie 的 Host 不在白名单中，未同步"
		return
	}
	updated, err := s.settingService.UpsertOpenAICodexCookieIfNewer(ctx, OpenAICodexCookieLibraryEntry{
		Cookie: item.Cookie, CapturedAt: event.receivedAt,
	})
	if err != nil {
		item.Stage, item.Message = "failed", "响应 Cookie 入库失败，保留原有 Cookie"
		slog.Warn("response cookie sync failed", "account_id", event.accountID, "error", err)
		return
	}
	item.Success = true
	if updated {
		item.Stage, item.Message = "updated", "已异步保存更新的上游响应 Cookie"
	} else {
		item.Stage, item.Message = "ignored_older", "响应 Cookie 相同或时间不更新，保留库中 Cookie"
	}
}

// This worker needs only the source switch and whitelist. A read-only snapshot
// avoids holding cookieSettingsMu across database I/O and blocking forwarding.
// Never publish this snapshot into the shared cache: a concurrent admin save
// may already have installed a newer one there.
func (s *OpenAIGatewayService) responseCookieSettings(ctx context.Context) (OpenAICookieSettings, error) {
	if cached, ok := s.settingService.openAICookieCache.Load().(*cachedOpenAICookieSettings); ok && time.Now().Before(cached.until) {
		return cached.value, nil
	}
	var settings OpenAICookieSettings
	raw, err := s.settingService.settingRepo.GetValue(ctx, cookieSettingsKey)
	if errors.Is(err, ErrSettingNotFound) {
		return settings, nil // Older configurations leave this source disabled.
	}
	if err != nil {
		return settings, err
	}
	if strings.TrimSpace(raw) == "" {
		return settings, nil
	}
	err = json.Unmarshal([]byte(raw), &settings)
	return settings, err
}

func (s *OpenAIGatewayService) writeResponseCookieLog(item OpenAICookieAcquisitionLog) {
	// A separate timeout lets failures be recorded even when the update times out.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	item.ID, item.Kind, item.Task = uuid.NewString(), "response_sync", "response_sync"
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if err := s.settingService.appendOpenAICookieLog(ctx, item); err != nil {
		slog.Warn("response cookie acquisition log failed", "account_id", item.AccountID, "error", err)
	}
}
