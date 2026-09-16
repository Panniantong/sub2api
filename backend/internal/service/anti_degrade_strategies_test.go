package service

import "testing"

func TestAntiDegradeStrategyProfilesAreStableAndDistinct(t *testing.T) {
	profiles := ListAntiDegradeStrategyProfiles()
	if len(profiles) < 8 {
		t.Fatalf("expected at least 8 strategy profiles, got %d", len(profiles))
	}
	seen := make(map[AntiDegradeMode]bool, len(profiles))
	for _, profile := range profiles {
		if profile.ID == "" || profile.Name == "" || profile.Description == "" {
			t.Fatalf("incomplete profile: %#v", profile)
		}
		if seen[profile.ID] {
			t.Fatalf("duplicate strategy id %q", profile.ID)
		}
		seen[profile.ID] = true
	}
	if antiDegradeStrategyProfile(AntiDegradeModeNative).ApplySupported {
		t.Fatal("native baseline must require explicit revert confirmation")
	}
	if DefaultAntiDegradeMode != AntiDegradeModeLegacy || antiDegradeStrategyProfile(DefaultAntiDegradeMode).DiagnosticOnly {
		t.Fatal("legacy must be the default regular strategy")
	}
	if fp, tls, cap := antiDegradeModeSettings(AntiDegradeModeMinimal); fp != codexFingerprintDevice || tls != "standard" || cap != 8 {
		t.Fatalf("minimal profile mismatch: fp=%s tls=%s cap=%d", fp, tls, cap)
	}
	if fp, tls, cap := antiDegradeModeSettings(AntiDegradeModeLowConcurrency); fp != codexFingerprintSession || tls != "standard" || cap != 4 {
		t.Fatalf("low-concurrency profile mismatch: fp=%s tls=%s cap=%d", fp, tls, cap)
	}
}
