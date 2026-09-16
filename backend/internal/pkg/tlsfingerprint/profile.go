package tlsfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Clone returns an independent profile. A pooled transport must never retain
// mutable slices owned by an account configuration or the profile cache.
func (p *Profile) Clone() *Profile {
	if p == nil {
		return nil
	}
	c := *p
	c.CipherSuites = append([]uint16(nil), p.CipherSuites...)
	c.Curves = append([]uint16(nil), p.Curves...)
	c.PointFormats = append([]uint16(nil), p.PointFormats...)
	c.SignatureAlgorithms = append([]uint16(nil), p.SignatureAlgorithms...)
	c.ALPNProtocols = append([]string(nil), p.ALPNProtocols...)
	c.SupportedVersions = append([]uint16(nil), p.SupportedVersions...)
	c.KeyShareGroups = append([]uint16(nil), p.KeyShareGroups...)
	c.PSKModes = append([]uint16(nil), p.PSKModes...)
	c.Extensions = append([]uint16(nil), p.Extensions...)
	return &c
}

// CacheKey identifies the complete configured template, including slice order.
// In particular, editing a template without renaming it creates a new pool.
// Account and proxy isolation must be added by the caller.
func (p *Profile) CacheKey() string {
	if p == nil {
		return "none"
	}
	b, _ := json.Marshal(p) // Profile only contains JSON-supported scalar/slice fields.
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
