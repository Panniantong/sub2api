package service

import (
	"context"
	"strconv"
	"time"
)

type CookieDashboard struct {
	PoolGroups              []CookiePoolDashboardGroup  `json:"pool_groups"`
	DegradedGroupID         int64                       `json:"degraded_group_id"`
	DegradedCurrentRequests *int                        `json:"degraded_current_requests"`
	RotationPools           *CookieRotationPoolSnapshot `json:"rotation_pools,omitempty"`
	Harvest                 map[string]int              `json:"harvest"`
	RotationAccounts        int                         `json:"rotation_accounts"`
	RotationRunning         int                         `json:"rotation_running"`
	DegradedAccounts        int                         `json:"degraded_accounts"`
	HealthyBoundAccounts    int                         `json:"healthy_bound_accounts"`
	HarvestEnabled          bool                        `json:"harvest_enabled"`
	RotationEnabled         bool                        `json:"rotation_enabled"`
	HarvestConcurrency      int                         `json:"harvest_concurrency"`
	UpdatedAt               time.Time                   `json:"updated_at"`
}

type CookiePoolDashboardGroup struct {
	Policy          CookieRotationPoolPolicy     `json:"policy"`
	Name            string                       `json:"name"`
	Enabled         bool                         `json:"enabled"`
	CurrentRequests *int                         `json:"current_requests"`
	WindowDemand    *int                         `json:"window_demand"`
	TargetCapacity  int                          `json:"target_capacity"`
	ReadyCapacity   int                          `json:"ready_capacity"`
	LowHost         CookieLowHostDashboard       `json:"low_host"`
	Active          int                          `json:"active"`
	Draining        int                          `json:"draining"`
	Preparing       int                          `json:"preparing"`
	Standby         int                          `json:"standby"`
	Exhausted       int                          `json:"exhausted"`
	Unavailable     int                          `json:"unavailable"`
	Degraded        int                          `json:"degraded"`
	UpdatedAt       *time.Time                   `json:"updated_at"`
	WaitReasons     map[string]int               `json:"wait_reasons"`
	Accounts        []CookiePoolDashboardAccount `json:"accounts,omitempty"`
	shortage        bool
}

func (s *OpenAIGatewayService) CookieDashboard(ctx context.Context, detailGroups ...int64) (*CookieDashboard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	group := int64(0)
	if len(detailGroups) > 0 && detailGroups[0] > 0 {
		group = detailGroups[0]
	}
	// Display-only coalescing: no TTL cache, no effect on dispatch freshness.
	// One canceled viewer must not cancel another viewer's shared read. The
	// shared operation has its own ten-second deadline and opens no transaction.
	result := s.cookieDashboardReads.DoChan(strconv.FormatInt(group, 10), func() (any, error) {
		return s.loadCookieDashboard(context.WithoutCancel(ctx), group)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*CookieDashboard), nil
	}
}

func (s *OpenAIGatewayService) loadCookieDashboard(ctx context.Context, detailGroups ...int64) (*CookieDashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	ctx = s.settingService.WithCookieAdminSnapshot(ctx, accounts)
	var settings *OpenAICookieSettings
	if s.settingService.cookieAdminSnapshot(ctx) != nil {
		settings, err = s.settingService.GetOpenAICookieSettings(ctx)
	} else {
		// An empty/OAuth-free dashboard still needs a cancellable settings read.
		settings, err = s.settingService.cookieAdminSettings(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Use one consistent library snapshot for the whole dashboard. Failure is
	// reported instead of turning unavailable data into a misleading zero.
	var entries []OpenAICodexCookieLibraryEntry
	if s.settingService.cookieAdminSnapshot(ctx) != nil {
		entries, err = s.settingService.GetOpenAICodexCookieLibrary(ctx)
	} else {
		entries, err = s.settingService.readOpenAICodexCookieLibrary(ctx, true)
	}
	if err != nil {
		return nil, err
	}
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return entries, nil }
	byHost := make(map[string]OpenAICodexCookieLibraryEntry, len(entries))
	for _, entry := range entries {
		byHost[normalizeOpenAICookieHost(entry.Host)] = entry
	}
	result := &CookieDashboard{
		Harvest: s.CookieHarvestRunning(), HarvestEnabled: settings.Enabled && !settings.LocalHarvestDisabled && (len(settings.AccountIDs) > 0 || settings.AccountID > 0 || len(settings.GroupIDs) > 0),
		RotationEnabled:    settings.Enabled && settings.CookieRotationEnabled,
		HarvestConcurrency: settings.CookieHarvestConcurrency, UpdatedAt: time.Now(),
	}
	seen := make(map[int64]bool, len(accounts))
	if settings.rotationPool != nil && len(settings.RotationPools) > 0 {
		result.RotationPools, err = settings.rotationPool()
		if err != nil {
			return nil, err
		}
	}
	result.PoolGroups = []CookiePoolDashboardGroup{}
	poolRows := map[int64]int{}
	for _, p := range cookiePoolPolicies(settings) {
		row := CookiePoolDashboardGroup{Policy: p, Enabled: result.RotationEnabled && p.Enabled, WaitReasons: map[string]int{}, shortage: true}
		if s.settingService.defaultSubGroupReader != nil {
			if group, e := s.settingService.defaultSubGroupReader.GetByID(ctx, p.GroupID); e == nil && group != nil {
				row.Name = group.Name
			}
		}
		if result.RotationPools != nil {
			if runtime := result.RotationPools.Groups[p.GroupID]; runtime != nil {
				active := 0
				for _, m := range runtime.Members {
					if m.State == "active" && !m.LowHostForced {
						active++
					}
				}
				row.shortage = runtime.ReadyCapacity-runtime.LowHostReadyCapacity < runtime.TargetCapacity || active < p.MinAccounts
				row.TargetCapacity = runtime.TargetCapacity
				at := runtime.UpdatedAt
				row.UpdatedAt = &at
			}
		}
		if p.DemandMode != "dynamic" {
			row.TargetCapacity = cookiePoolTargetCapacity(p, p.TargetConcurrency)
		}
		if demand, e := s.concurrencyService.cookieGroupDemand(ctx, p.GroupID, cookiePoolScaleDownSeconds(p)); e == nil {
			row.CurrentRequests, row.WindowDemand = &demand.Current, &demand.Peak
		}
		poolRows[p.GroupID] = len(result.PoolGroups)
		result.PoolGroups = append(result.PoolGroups, row)
	}
	result.DegradedGroupID = settings.DegradedGroupID
	if settings.DegradedGroupID > 0 {
		if demand, e := s.concurrencyService.cookieGroupDemand(ctx, settings.DegradedGroupID, 60); e == nil {
			result.DegradedCurrentRequests = &demand.Current
		}
	}
	// Each account is hydrated once below. All nested display calculations use
	// that immutable value, avoiding repeated mailbox merges and repository calls.
	settings.emailAccount = nil
	for i := range accounts {
		a := &accounts[i]
		if seen[a.ID] || !isOpenAICodexTicketAccount(a) {
			continue
		}
		a, err = s.settingService.cookieEmailAccount(ctx, a)
		if err != nil {
			return nil, err
		}
		seen[a.ID] = true
		healthy := cookieDashboardHealthyBinding(a, settings, byHost, result.UpdatedAt)
		if healthy {
			result.HealthyBoundAccounts++
		}
		if isOpenAICookieRotationAccount(a, settings) {
			result.RotationAccounts++
		}
		// Only exhausted, enabled accounts can enter the degraded group.
		degraded := cookieDegradedTarget(a, settings, result.UpdatedAt) > 0
		if degraded {
			result.DegradedAccounts++
		}
		if p := cookiePoolPolicy(a, settings); p != nil {
			row := &result.PoolGroups[poolRows[p.GroupID]]
			member, _ := cookiePoolMember(a, settings)
			state := "standby"
			if degraded {
				row.Degraded++
			}
			switch {
			case !row.Enabled || !a.IsSchedulable():
				state = "unavailable"
				row.Unavailable++
			case (member.State == "active" || member.State == "draining") && healthy:
				state = member.State
				row.Active++
				if member.State == "draining" {
					row.Draining++
				}
				row.ReadyCapacity += a.Concurrency
			case member.State == "preparing":
				state = "preparing"
				row.Preparing++
			case member.State == "exhausted":
				state = "exhausted"
				row.Exhausted++
			case member.State == "waiting" || member.State == "active" || member.State == "draining":
				state = "unavailable"
				row.Unavailable++
			default:
				row.Standby++
			}
			reason, until := cookiePoolAccountWait(a, member, row, result.UpdatedAt)
			low := cookieDashboardLowHostAccount(a, member, row, settings, healthy, degraded, result.UpdatedAt)
			row.LowHost.add(low, a.Concurrency)
			if state == "unavailable" {
				row.WaitReasons[reason]++
			}
			if len(detailGroups) > 0 && detailGroups[0] == p.GroupID {
				detail := s.cookiePoolDashboardAccount(a, member, state, reason, until, settings, byHost, result.UpdatedAt)
				detail.LowHost = low
				row.Accounts = append(row.Accounts, detail)
			}
		}
	}
	// In-flight work can still finish after an administrator disables rotation.
	s.openaiCookieRotationRunning.Range(func(_, _ any) bool {
		result.RotationRunning++
		return true
	})
	return result, nil
}

func cookieDashboardHealthyBinding(a *Account, settings *OpenAICookieSettings, entries map[string]OpenAICodexCookieLibraryEntry, now time.Time) bool {
	if _, reason := cookiePoolMember(a, settings); reason != "" {
		return false
	}
	if !a.IsSchedulable() || cookieHostMonitorOwns(a, settings) {
		return false
	}
	host := normalizeOpenAICookieHost(openAICodexCookieHostFromAccount(a))
	if !cookieHostInScope(host, settings.HostWhitelist) {
		return false
	}
	entry, ok := entries[host]
	if !ok || entry.Cookie == "" || !entry.ExpiresAt.After(now) || openAICodexCookieHostCooldownUntil(a, host).After(now) {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, a.GetExtraString("codex_cookie_host_binding_expires_at"))
	return err == nil && expires.After(now)
}
