package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const SettingKeyOpenAICodexTicketHarvestScope = "openai_codex_ticket_harvest_scope"

const (
	CodexHarvestSchedulableOnly       = "schedulable_only"
	CodexHarvestPrioritizeSchedulable = "prioritize_schedulable"
)

// CodexTicketHarvestScope selects which accounts the background harvester
// probes. Fail-closed ticket-gated routing also refuses leftover tickets on
// accounts outside this scope. Selected with no groups harvests nothing and
// admits no leftover tickets on gated models. Per-account skip_harvest is
// not this gate: those accounts are still schedulable.
type CodexTicketHarvestScope struct {
	Mode          string  `json:"mode"`
	GroupIDs      []int64 `json:"group_ids"`
	AccountPolicy string  `json:"account_policy"`
}

func NormalizeCodexTicketHarvestScope(scope CodexTicketHarvestScope) (CodexTicketHarvestScope, error) {
	if scope.Mode == "" {
		scope.Mode = "all"
	}
	if scope.Mode != "all" && scope.Mode != "selected" {
		return scope, fmt.Errorf("harvest scope mode must be all or selected")
	}
	if scope.AccountPolicy == "" {
		scope.AccountPolicy = CodexHarvestSchedulableOnly
	}
	if scope.AccountPolicy != CodexHarvestSchedulableOnly && scope.AccountPolicy != CodexHarvestPrioritizeSchedulable {
		return scope, fmt.Errorf("harvest account policy must be schedulable_only or prioritize_schedulable")
	}
	if len(scope.GroupIDs) > 1000 {
		return scope, fmt.Errorf("at most 1000 harvest groups are allowed")
	}
	ids := append([]int64{}, scope.GroupIDs...)
	for _, id := range ids {
		if id <= 0 {
			return scope, fmt.Errorf("harvest group IDs must be positive")
		}
	}
	slices.Sort(ids)
	scope.GroupIDs = slices.Compact(ids)
	return scope, nil
}

func parseCodexTicketHarvestScope(raw string) (CodexTicketHarvestScope, error) {
	if strings.TrimSpace(raw) == "" {
		return NormalizeCodexTicketHarvestScope(CodexTicketHarvestScope{})
	}
	var scope CodexTicketHarvestScope
	if err := json.Unmarshal([]byte(raw), &scope); err != nil {
		return scope, fmt.Errorf("invalid harvest scope JSON")
	}
	// Only a missing legacy setting defaults to all. Corrupt persisted settings
	// must never accidentally widen a previously selected scope.
	if scope.Mode == "" {
		return scope, fmt.Errorf("harvest scope mode is required")
	}
	return NormalizeCodexTicketHarvestScope(scope)
}

type cachedOpenAICodexTicketHarvestScope struct {
	scope     CodexTicketHarvestScope
	expiresAt int64
}

const openAICodexTicketHarvestScopeCacheTTL = 5 * time.Second

func cloneCodexTicketHarvestScope(scope CodexTicketHarvestScope) CodexTicketHarvestScope {
	out := scope
	if scope.GroupIDs != nil {
		out.GroupIDs = append([]int64{}, scope.GroupIDs...)
	}
	return out
}

var errCodexHarvestScopeChanged = errors.New("harvest scope changed during read")

// Read the complete scope atomically. Harvest rounds fail closed on storage
// errors instead of widening to all accounts. Ticket-gated selection reuses
// this helper, so a 5s cache plus singleflight keeps the hot path off DB.
func (s *SettingService) GetCodexTicketHarvestScope(ctx context.Context) (CodexTicketHarvestScope, error) {
	if s == nil || s.settingRepo == nil {
		return parseCodexTicketHarvestScope("")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return CodexTicketHarvestScope{}, err
		}
		if cached, ok := s.openAICodexTicketHarvestScopeCache.Load().(*cachedOpenAICodexTicketHarvestScope); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cloneCodexTicketHarvestScope(cached.scope), nil
		}
		resultCh := s.openAICodexTicketHarvestScopeSF.DoChan(SettingKeyOpenAICodexTicketHarvestScope, func() (any, error) {
			return s.loadCodexTicketHarvestScope(ctx)
		})
		select {
		case <-ctx.Done():
			return CodexTicketHarvestScope{}, ctx.Err()
		case result := <-resultCh:
			if errors.Is(result.Err, errCodexHarvestScopeChanged) {
				continue
			}
			if result.Err != nil {
				return CodexTicketHarvestScope{}, result.Err
			}
			cached, ok := result.Val.(*cachedOpenAICodexTicketHarvestScope)
			if !ok || cached == nil {
				return CodexTicketHarvestScope{}, fmt.Errorf("invalid harvest scope cache payload")
			}
			if s.openAICodexTicketHarvestScopeCache.Load() == cached {
				return cloneCodexTicketHarvestScope(cached.scope), nil
			}
		}
	}
}

func (s *SettingService) loadCodexTicketHarvestScope(ctx context.Context) (*cachedOpenAICodexTicketHarvestScope, error) {
	previous := s.openAICodexTicketHarvestScopeCache.Load()
	if cached, ok := previous.(*cachedOpenAICodexTicketHarvestScope); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached, nil
	}
	// A shared read must survive cancellation of its first waiter.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexTicketHarvestScope)
	if errors.Is(err, ErrSettingNotFound) {
		raw, err = "", nil
	}
	if err != nil {
		return nil, err
	}
	scope, err := parseCodexTicketHarvestScope(raw)
	if err != nil {
		return nil, err
	}
	cached := &cachedOpenAICodexTicketHarvestScope{
		scope:     cloneCodexTicketHarvestScope(scope),
		expiresAt: time.Now().Add(openAICodexTicketHarvestScopeCacheTTL).UnixNano(),
	}
	// Invalidation replaces the pointer. An older read may neither overwrite
	// that boundary nor return its stale scope to an existing waiter.
	if !s.openAICodexTicketHarvestScopeCache.CompareAndSwap(previous, cached) {
		return nil, errCodexHarvestScopeChanged
	}
	return cached, nil
}

func (s *SettingService) InvalidateOpenAICodexTicketHarvestScopeCache() {
	if s == nil {
		return
	}
	s.openAICodexTicketHarvestScopeCache.Store(&cachedOpenAICodexTicketHarvestScope{expiresAt: 0})
	s.openAICodexTicketHarvestScopeSF.Forget(SettingKeyOpenAICodexTicketHarvestScope)
}

func (scope CodexTicketHarvestScope) includes(account *Account) bool {
	if scope.Mode == "all" {
		return true
	}
	if len(account.Groups) > 0 {
		for _, group := range account.Groups {
			if group != nil && group.IsActive() && slices.Contains(scope.GroupIDs, group.ID) {
				return true
			}
		}
		return false
	}
	// Retain compatibility with legacy/test snapshots that only carry IDs.
	for _, id := range account.GroupIDs {
		if slices.Contains(scope.GroupIDs, id) {
			return true
		}
	}
	return false
}

// allowsAccount applies the full operational scheduling state. The optional
// compatibility policy may defer a manually disabled account, but never probes
// accounts that are rate-limited, overloaded, temporarily paused, expired, or
// quota exhausted.
func (scope CodexTicketHarvestScope) allowsAccount(account *Account) bool {
	if account == nil {
		return false
	}
	candidate := *account
	candidate.Schedulable = true
	if !candidate.IsSchedulable() {
		return false
	}
	return scope.AccountPolicy == CodexHarvestPrioritizeSchedulable || account.Schedulable
}

// A shared account is harvested once, at its best priority among selected
// memberships. Unselected memberships must not boost its harvest priority.
// Legacy snapshots without membership priorities fall back to account priority.
func (scope CodexTicketHarvestScope) priority(account *Account) int {
	best, found := account.Priority, false
	for _, membership := range account.AccountGroups {
		if scope.Mode == "selected" && !slices.Contains(scope.GroupIDs, membership.GroupID) {
			continue
		}
		if membership.Group != nil && !membership.Group.IsActive() {
			continue
		}
		if !found || membership.Priority < best {
			best, found = membership.Priority, true
		}
	}
	return best
}

type codexHarvestTier struct {
	Schedulable     bool
	Priority        int
	AccountPriority int
}

func (scope CodexTicketHarvestScope) tier(account *Account) codexHarvestTier {
	return codexHarvestTier{Schedulable: account.Schedulable, Priority: scope.priority(account), AccountPriority: account.Priority}
}
