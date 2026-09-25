package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyPelicanShowcaseConfig = "pelican_showcase_config"

const (
	PelicanShowcaseDefaultMaxItems      = 20
	PelicanShowcaseMaxItemsLimit        = 100
	PelicanShowcaseDefaultRetentionDays = 7
	PelicanShowcaseRetentionDaysLimit   = 90
	pelicanShowcaseMaxGroups            = 50
)

// PelicanShowcaseConfig selects the groups whose scheduled Pelican HTML results are
// shown to users, and bounds the gallery: MaxItems per group, plus an optional
// age limit (AutoCleanup + RetentionDays). The gallery keeps its own copies, so these
// limits are independent of the admin test history (per plan 1–200, 7 days).
type PelicanShowcaseConfig struct {
	GroupIDs      []int64 `json:"group_ids"`
	MaxItems      int     `json:"max_items"`
	AutoCleanup   bool    `json:"auto_cleanup"`
	RetentionDays int     `json:"retention_days"`
}

func DefaultPelicanShowcaseConfig() PelicanShowcaseConfig {
	return PelicanShowcaseConfig{
		GroupIDs:      []int64{},
		MaxItems:      PelicanShowcaseDefaultMaxItems,
		AutoCleanup:   true,
		RetentionDays: PelicanShowcaseDefaultRetentionDays,
	}
}

// NormalizePelicanShowcaseConfig fills zero limits with defaults, rejects out-of-range
// values, and sorts/deduplicates group IDs (display order follows the groups' sort order).
func NormalizePelicanShowcaseConfig(cfg PelicanShowcaseConfig) (PelicanShowcaseConfig, error) {
	if cfg.MaxItems == 0 {
		cfg.MaxItems = PelicanShowcaseDefaultMaxItems
	}
	if cfg.MaxItems < 1 || cfg.MaxItems > PelicanShowcaseMaxItemsLimit {
		return cfg, fmt.Errorf("showcase max items must be 1–%d", PelicanShowcaseMaxItemsLimit)
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = PelicanShowcaseDefaultRetentionDays
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > PelicanShowcaseRetentionDaysLimit {
		return cfg, fmt.Errorf("showcase retention must be 1–%d days", PelicanShowcaseRetentionDaysLimit)
	}
	ids := append([]int64{}, cfg.GroupIDs...)
	for _, id := range ids {
		if id <= 0 {
			return cfg, fmt.Errorf("showcase group IDs must be positive")
		}
	}
	slices.Sort(ids)
	cfg.GroupIDs = slices.Compact(ids)
	if len(cfg.GroupIDs) > pelicanShowcaseMaxGroups {
		return cfg, fmt.Errorf("at most %d showcase groups are allowed", pelicanShowcaseMaxGroups)
	}
	return cfg, nil
}

// A missing setting means "never configured" and yields the defaults (no groups).
// Corrupt JSON is an error, so callers fail closed instead of deleting snapshots.
func parsePelicanShowcaseConfig(raw string) (PelicanShowcaseConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultPelicanShowcaseConfig(), nil
	}
	var cfg PelicanShowcaseConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("invalid pelican showcase config JSON")
	}
	return NormalizePelicanShowcaseConfig(cfg)
}

// retentionCutoff returns the oldest generation time still shown, or zero when
// auto cleanup is off (only the per-group count limit applies).
func (cfg PelicanShowcaseConfig) retentionCutoff(now time.Time) time.Time {
	if !cfg.AutoCleanup {
		return time.Time{}
	}
	return now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
}

// PelicanShowcaseRuntime is the gallery switch plus its limits, read on every use.
type PelicanShowcaseRuntime struct {
	Enabled bool
	Config  PelicanShowcaseConfig
}

var errPelicanShowcaseSettingsUnavailable = errors.New("pelican showcase settings unavailable")

// GetPelicanShowcaseRuntime reads the switch and config straight from the settings store.
// Errors are returned rather than defaulted: publishing, the user gallery and cleanup all
// skip on error, so an unreadable or corrupt config never widens exposure or deletes data.
func (s *SettingService) GetPelicanShowcaseRuntime(ctx context.Context) (PelicanShowcaseRuntime, error) {
	if s == nil || s.settingRepo == nil {
		return PelicanShowcaseRuntime{}, errPelicanShowcaseSettingsUnavailable
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyPelicanShowcaseEnabled, SettingKeyPelicanShowcaseConfig})
	if err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	cfg, err := parsePelicanShowcaseConfig(vals[SettingKeyPelicanShowcaseConfig])
	if err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	return PelicanShowcaseRuntime{Enabled: vals[SettingKeyPelicanShowcaseEnabled] == "true", Config: cfg}, nil
}

// validateAddedPelicanShowcaseGroups checks only groups that were not selected before,
// so a group deleted after selection never blocks saving unrelated settings.
func (s *SettingService) validateAddedPelicanShowcaseGroups(ctx context.Context, groupIDs []int64) error {
	if s.defaultSubGroupReader == nil || len(groupIDs) == 0 {
		return nil
	}
	selected := make(map[int64]bool)
	if previous, err := s.GetPelicanShowcaseRuntime(ctx); err == nil {
		for _, id := range previous.Config.GroupIDs {
			selected[id] = true
		}
	}
	for _, id := range groupIDs {
		if selected[id] {
			continue
		}
		group, err := s.defaultSubGroupReader.GetByID(ctx, id)
		if err != nil && !errors.Is(err, ErrGroupNotFound) {
			return err
		}
		if err != nil || group == nil {
			return infraerrors.BadRequest("INVALID_PELICAN_SHOWCASE_GROUP", fmt.Sprintf("showcase group %d does not exist", id))
		}
	}
	return nil
}
