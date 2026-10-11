package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Values are copied once at HTTP admission; saving settings cannot change an
// in-flight request's deadlines or account limit. Negative values inherit config.
type OpenAIRequestSchedulingPolicy struct {
	Enabled          bool
	Mode             string
	QueueSeconds     int
	PreOutputSeconds int
	MaxSwitches      int
	LegacyRetries    bool
	GroupPolicies    []OpenAIGroupSchedulingPolicy
}

type OpenAIGroupSchedulingPolicy struct {
	GroupID          int64  `json:"group_id"`
	Mode             string `json:"mode"`
	PreOutputSeconds *int   `json:"pre_output_seconds,omitempty"`
	MaxSwitches      *int   `json:"max_switches,omitempty"`
}

func parseOpenAIGroupPolicies(raw string) ([]OpenAIGroupSchedulingPolicy, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var rules []OpenAIGroupSchedulingPolicy
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, r := range rules {
		if r.GroupID <= 0 || seen[r.GroupID] || (r.Mode != "inherit" && r.Mode != "legacy" && r.Mode != "balanced" && r.Mode != "low_load") {
			return nil, fmt.Errorf("invalid or duplicate group scheduling rule")
		}
		if r.PreOutputSeconds != nil && (*r.PreOutputSeconds < 0 || *r.PreOutputSeconds > 3600) {
			return nil, fmt.Errorf("pre-output seconds must be 0–3600")
		}
		if r.MaxSwitches != nil && (*r.MaxSwitches < 0 || *r.MaxSwitches > 20) {
			return nil, fmt.Errorf("switches must be 0–20")
		}
		seen[r.GroupID] = true
	}
	return rules, nil
}

type openAISchedulingSnapshotKey struct{}
type openAIAffinityDecisionKey struct{}

func parseOpenAIRequestSchedulingPolicy(values map[string]string) OpenAIRequestSchedulingPolicy {
	p := OpenAIRequestSchedulingPolicy{Mode: "balanced", QueueSeconds: 2, PreOutputSeconds: -1, MaxSwitches: -1}
	p.GroupPolicies, _ = parseOpenAIGroupPolicies(values[SettingKeyOpenAIAdvancedSchedulerGroupPolicies])
	if values[SettingKeyOpenAIAdvancedSchedulerMode] == "low_load" {
		p.Mode = "low_load"
	}
	for _, field := range []struct {
		key      string
		target   *int
		min, max int
	}{
		{SettingKeyOpenAIAdvancedSchedulerQueueSeconds, &p.QueueSeconds, 1, 10},
		{SettingKeyOpenAIAdvancedSchedulerPreOutputSeconds, &p.PreOutputSeconds, 1, 3600},
		{SettingKeyOpenAIAdvancedSchedulerMaxSwitches, &p.MaxSwitches, 0, 20},
	} {
		if n, err := strconv.Atoi(strings.TrimSpace(values[field.key])); err == nil && n >= field.min && n <= field.max {
			*field.target = n
		}
	}
	return p
}

func normalizeOpenAIRequestSchedulingSettings(s *SystemSettings) error {
	rules, err := parseOpenAIGroupPolicies(s.OpenAIAdvancedSchedulerGroupPolicies)
	if err != nil {
		return infraerrors.BadRequest("INVALID_GROUP_SCHEDULING", err.Error())
	}
	if len(rules) == 0 {
		s.OpenAIAdvancedSchedulerGroupPolicies = ""
	} else {
		raw, _ := json.Marshal(rules)
		s.OpenAIAdvancedSchedulerGroupPolicies = string(raw)
	}
	s.OpenAIAdvancedSchedulerMode = strings.TrimSpace(s.OpenAIAdvancedSchedulerMode)
	if mode := s.OpenAIAdvancedSchedulerMode; mode != "" && mode != "balanced" && mode != "low_load" {
		return infraerrors.BadRequest("INVALID_OPENAI_SCHEDULING_MODE", "scheduling mode must be balanced or low_load")
	}
	for _, f := range []struct {
		value    *string
		min, max int
		name     string
	}{
		{&s.OpenAIAdvancedSchedulerQueueSeconds, 1, 10, "queue seconds"},
		{&s.OpenAIAdvancedSchedulerPreOutputSeconds, 1, 3600, "pre-output seconds"},
		{&s.OpenAIAdvancedSchedulerMaxSwitches, 0, 20, "account switches"},
	} {
		*f.value = strings.TrimSpace(*f.value)
		if *f.value == "" {
			continue
		}
		n, err := strconv.Atoi(*f.value)
		if err != nil || n < f.min || n > f.max {
			return infraerrors.BadRequest("INVALID_OPENAI_REQUEST_SCHEDULING", f.name+" is outside its allowed range")
		}
		*f.value = strconv.Itoa(n)
	}
	return nil
}

func (s *OpenAIGatewayService) WithOpenAISchedulingSnapshot(ctx context.Context, groups ...*int64) context.Context {
	if _, ok := ctx.Value(openAISchedulingSnapshotKey{}).(openAIAdvancedSchedulerRuntimeSettings); ok {
		return ctx
	}
	settings := s.openAIAdvancedSchedulerRuntimeSettings(ctx)
	if len(groups) > 0 && groups[0] != nil {
		for _, rule := range settings.requestPolicy.GroupPolicies {
			if rule.GroupID != *groups[0] {
				continue
			}
			p := &settings.requestPolicy
			if rule.Mode == "legacy" {
				settings.enabled = false
				p.PreOutputSeconds, p.MaxSwitches, p.LegacyRetries = 0, -1, true
			} else {
				if rule.Mode != "inherit" {
					settings.enabled = true
					p.Mode = rule.Mode
				}
				if rule.PreOutputSeconds != nil {
					p.PreOutputSeconds = *rule.PreOutputSeconds
				}
				if rule.MaxSwitches != nil {
					p.MaxSwitches = *rule.MaxSwitches
				}
			}
			break
		}
	}
	ctx = context.WithValue(ctx, openAIAffinityDecisionKey{}, rand.Float64() < 0.70)
	return context.WithValue(ctx, openAISchedulingSnapshotKey{}, settings)
}

func OpenAIRequestSchedulingFromContext(ctx context.Context) OpenAIRequestSchedulingPolicy {
	if settings, ok := ctx.Value(openAISchedulingSnapshotKey{}).(openAIAdvancedSchedulerRuntimeSettings); ok {
		p := settings.requestPolicy
		p.Enabled = settings.enabled
		return p
	}
	return parseOpenAIRequestSchedulingPolicy(nil)
}
