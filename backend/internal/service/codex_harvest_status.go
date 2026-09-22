package service

import (
	"context"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
)

func (s *CodexHarvestService) Snapshot(ctx context.Context, proxy string) CodexHarvestControlSnapshot {
	v, configured, err := s.Controls(ctx)
	out := CodexHarvestControlSnapshot{Settings: v, Configured: configured, Defaults: s.defaults,
		Presets: CodexHarvestSpeedPresets(), Bounds: CodexHarvestSpeedBounds(), Runtime: s.Runtime()}
	if err != nil {
		out.SettingsError = "harvest settings unavailable; retaining last valid values"
	}
	sidecar, err := mihomo.LoadDirectedSidecar(os.Getenv("DATA_DIR"), proxy)
	if err != nil {
		out.AvailabilityReason = err.Error()
		return out
	}
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := sidecar.Directory(query); err != nil {
		out.AvailabilityReason = err.Error()
		return out
	}
	out.Available = true
	return out
}
