package repository

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

func (r *settingRepository) CookieEmailCooldownsBatch(ctx context.Context, emails []string) (map[string]map[string]time.Time, error) {
	result := map[string]map[string]time.Time{}
	if len(emails) == 0 {
		return result, nil
	}
	rows, err := r.client.QueryContext(ctx, `SELECT email,host,cooldown_until FROM cookie_email_host_cooldowns WHERE email=ANY($1) AND cooldown_until>now()`, pq.Array(emails))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email, host string
		var until time.Time
		if err := rows.Scan(&email, &host, &until); err != nil {
			return nil, err
		}
		if result[email] == nil {
			result[email] = map[string]time.Time{}
		}
		result[email][host] = until
	}
	return result, rows.Err()
}

func (r *settingRepository) loadCookieEmailCooldowns(ctx context.Context, email string) (map[string]time.Time, error) {
	rows, err := r.client.QueryContext(ctx, `SELECT host,cooldown_until FROM cookie_email_host_cooldowns WHERE email=$1 AND cooldown_until>now()`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]time.Time{}
	for rows.Next() {
		var host string
		var until time.Time
		if err := rows.Scan(&host, &until); err != nil {
			return nil, err
		}
		result[host] = until
	}
	return result, rows.Err()
}

func (r *settingRepository) SaveCookieEmailCooldown(ctx context.Context, email, host string, until time.Time) error {
	defer func() {
		r.cooldownMu.Lock()
		delete(r.cooldownCache, email)
		r.cooldownGeneration++
		r.cooldownMu.Unlock()
		r.cooldownFlight.Forget(email)
	}()
	_, err := r.client.ExecContext(ctx, `INSERT INTO cookie_email_host_cooldowns(email,host,cooldown_until) VALUES($1,$2,$3)
	ON CONFLICT(email,host) DO UPDATE SET cooldown_until=GREATEST(cookie_email_host_cooldowns.cooldown_until,EXCLUDED.cooldown_until)`, email, host, until)
	return err
}

// Cache only short-lived, successful reads. Updates invalidate in-flight loads
// using a generation fence, so an older read cannot reinstall stale state.
type cookieCooldownCacheEntry struct {
	values  map[string]time.Time
	expires time.Time
}

// Warm cold candidate mailboxes in one query. Final dispatch deliberately
// bypasses this short candidate cache via CookieDispatchState.
func (r *settingRepository) PrimeCookieEmailCooldowns(ctx context.Context, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	emails = append([]string(nil), emails...)
	sort.Strings(emails)
	// Share cold loads for the same pool. Waiters remain cancellable; the shared
	// query is bounded independently so one disconnect cannot cancel its peers.
	for attempt := 0; attempt < 3; attempt++ {
		missing, _ := r.missingCookieCooldownEmails(emails)
		if len(missing) == 0 {
			return nil
		}
		ch := r.cooldownFlight.DoChan("candidate-pool:"+strings.Join(emails, "\x00"), func() (any, error) {
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			return nil, r.primeCookieEmailCooldowns(readCtx, emails)
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-ch:
			if !errors.Is(result.Err, errCookieCooldownChanged) {
				return result.Err
			}
		}
	}
	return errCookieCooldownChanged
}

func (r *settingRepository) missingCookieCooldownEmails(emails []string) ([]string, uint64) {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()
	missing := make([]string, 0, len(emails))
	now := time.Now()
	previous := ""
	for _, email := range emails {
		if email == "" || email == previous {
			continue
		}
		previous = email
		if entry, ok := r.cooldownCache[email]; !ok || !now.Before(entry.expires) {
			missing = append(missing, email)
		}
	}
	return missing, r.cooldownGeneration
}

func (r *settingRepository) primeCookieEmailCooldowns(ctx context.Context, emails []string) error {
	missing, generation := r.missingCookieCooldownEmails(emails)
	if len(missing) == 0 {
		return nil
	}
	values, err := r.CookieEmailCooldownsBatch(ctx, missing)
	if err != nil {
		return err
	}
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()
	if generation != r.cooldownGeneration {
		return errCookieCooldownChanged
	}
	if r.cooldownCache == nil {
		r.cooldownCache = make(map[string]cookieCooldownCacheEntry)
	}
	if len(r.cooldownCache)+len(missing) > 4096 {
		clear(r.cooldownCache)
	}
	for _, email := range missing {
		if len(r.cooldownCache) >= 4096 {
			break
		}
		r.cooldownCache[email] = cookieCooldownCacheEntry{values: values[email], expires: time.Now().Add(5 * time.Second)}
	}
	return nil
}

func cloneCookieCooldowns(values map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(values))
	for h, t := range values {
		out[h] = t
	}
	return out
}
func (r *settingRepository) CookieEmailCooldowns(ctx context.Context, email string) (map[string]time.Time, error) {
	// A write racing a shared read must invalidate its result for waiters too,
	// not just prevent the result from being stored in the cache.
	for attempt := 0; attempt < 3; attempt++ {
		values, err := r.cookieEmailCooldownsCached(ctx, email)
		if !errors.Is(err, errCookieCooldownChanged) {
			return values, err
		}
	}
	return nil, errCookieCooldownChanged
}

var errCookieCooldownChanged = errors.New("cookie cooldown changed during read")

func (r *settingRepository) cookieEmailCooldownsCached(ctx context.Context, email string) (map[string]time.Time, error) {
	r.cooldownMu.Lock()
	cached, ok := r.cooldownCache[email]
	r.cooldownMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cloneCookieCooldowns(cached.values), nil
	}
	ch := r.cooldownFlight.DoChan(email, func() (any, error) {
		r.cooldownMu.Lock()
		cached, ok := r.cooldownCache[email]
		generation := r.cooldownGeneration
		r.cooldownMu.Unlock()
		if ok && time.Now().Before(cached.expires) {
			return cached.values, nil
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		values, err := r.loadCookieEmailCooldowns(readCtx, email)
		if err != nil {
			return nil, err
		}
		r.cooldownMu.Lock()
		if generation != r.cooldownGeneration {
			r.cooldownMu.Unlock()
			return nil, errCookieCooldownChanged
		}
		if generation == r.cooldownGeneration {
			if r.cooldownCache == nil {
				r.cooldownCache = make(map[string]cookieCooldownCacheEntry)
			}
			if len(r.cooldownCache) >= 4096 {
				clear(r.cooldownCache)
			}
			r.cooldownCache[email] = cookieCooldownCacheEntry{values: values, expires: time.Now().Add(5 * time.Second)}
		}
		r.cooldownMu.Unlock()
		return values, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		return cloneCookieCooldowns(result.Val.(map[string]time.Time)), nil
	}
}
