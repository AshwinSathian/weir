package weir_test

import (
	"testing"

	"github.com/AshwinSathian/weir"
)

// FR-SNP-4, R-3: the digest changes with every setting that changes what an
// unchanged key means, and with nothing else.
func TestKeyGenerationChangesWithForwarding(t *testing.T) {
	base := weir.Config{}.KeyGeneration()
	if len(base) != 64 {
		t.Fatalf("digest %q, want 64 hex characters", base)
	}
	for name, c := range map[string]weir.Config{
		"Forward.Mode":            {Forward: weir.ForwardConfig{Mode: weir.ForwardAll}},
		"Forward.Allow":           {Forward: weir.ForwardConfig{Allow: []string{"X-Tenant"}}},
		"Storable.StripSetCookie": {Storable: weir.StorableConfig{StripSetCookie: true}},
	} {
		if c.KeyGeneration() == base {
			t.Errorf("%s does not change the digest", name)
		}
	}
	a := weir.Config{Forward: weir.ForwardConfig{Allow: []string{"x-b", "X-A", "X-B"}}}.KeyGeneration()
	b := weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-A", "X-B"}}}.KeyGeneration()
	if a != b {
		t.Error("the order, case and repeats of Forward.Allow change the digest")
	}
	if c := (weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-Ab"}}}).KeyGeneration(); c == (weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-A", "b"}}}).KeyGeneration() {
		t.Error("two different Allow lists share a digest")
	}
}

// FR-SNP-4: settings that only move a request to another key leave the
// digest alone, so adding a keyed header or a host does not empty the cache.
func TestKeyGenerationStableAcrossKeyOnlySettings(t *testing.T) {
	base := weir.Config{}.KeyGeneration()
	for name, c := range map[string]weir.Config{
		"Key.Headers":    {Key: weir.KeyConfig{Headers: []string{"Accept-Language"}}},
		"Key.QueryDrop":  {Key: weir.KeyConfig{QueryDrop: weir.TrackingParams()}},
		"Bypass.Cookies": {Bypass: weir.BypassConfig{Cookies: []string{"session"}}},
		"Limiter":        {Limiter: weir.LimiterConfig{MaxConcurrent: 8}},
	} {
		if c.KeyGeneration() != base {
			t.Errorf("%s changes the digest", name)
		}
	}
	// Pinned: a release that changed this value would empty every cache
	// whose name carries it.
	if want := "a271aa0d"; base[:8] != want {
		t.Errorf("zero Config digest starts %s, want %s", base[:8], want)
	}
}
