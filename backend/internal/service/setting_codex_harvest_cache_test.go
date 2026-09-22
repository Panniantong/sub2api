package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexHarvestCachesRejectPreInvalidationRead(t *testing.T) {
	for _, kind := range []string{"enabled", "fail-closed", "models", "proxy", "scope"} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing=%t", kind, missing), func(t *testing.T) {
				old, next := "false", "true"
				switch kind {
				case "models":
					old, next = `["gpt-6-astra"]`, `["gpt-5.6-sol"]`
				case "proxy":
					old, next = "http://old.example:8080", "http://new.example:8080"
				case "scope":
					old, next = `{"mode":"all"}`, `{"mode":"selected","group_ids":[2]}`
				}
				started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var reads atomic.Int64
				s := NewSettingService(&codexTicketLifecycleSettings{get: func(ctx context.Context, _ string) (string, error) {
					if reads.Add(1) == 1 {
						close(started)
						select {
						case <-release:
							if missing {
								return "", ErrSettingNotFound
							}
							return old, nil
						case <-ctx.Done():
							return "", ctx.Err()
						}
					}
					return next, nil
				}}, &config.Config{})
				get := func() string {
					ctx := context.Background()
					switch kind {
					case "enabled":
						return fmt.Sprint(s.GetOpenAICodexTicketEnabled(ctx, false))
					case "fail-closed":
						return fmt.Sprint(s.GetOpenAICodexTicketFailClosed(ctx))
					case "models":
						return fmt.Sprint(s.GetOpenAICodexTicketModels(ctx, nil))
					case "proxy":
						return s.GetOpenAICodexTicketHarvestProxyURL(ctx)
					default:
						scope, err := s.GetCodexTicketHarvestScope(ctx)
						return fmt.Sprint(scope, err)
					}
				}
				go func() { defer close(done); get() }()
				<-started
				s.notifyCodexHarvestAfterSettingsWrite()
				current := get()
				close(release)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("old read did not complete")
				}
				require.Equal(t, current, get(), "old read must not repopulate the invalidated cache")
				require.Equal(t, int64(2), reads.Load())
			})
		}
	}
}
