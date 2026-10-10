package weircaddy

import (
	"testing"

	"github.com/AshwinSathian/weir"
)

// FR-FWD, T-45 (08 §3): the hash covers exactly the settings that change what
// an unchanged key means, so editing them cold-flushes and nothing else does.
func TestKeyGenHash(t *testing.T) {
	base := weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-A", "X-B"}}}
	want := keyGenHash(base)

	t.Run("ignores host, key rules, limiter and bypass settings", func(t *testing.T) {
		h := &Handler{
			Name: "a", MultiHost: true, MaxBytes: 1 << 20, SnapshotDir: "/x",
			Key:     KeyConfig{QueryDrop: []string{"utm_*"}, QuerySort: true, Headers: []string{"Accept-Language"}, Cookies: []string{"c"}},
			Bypass:  BypassConfig{Cookies: []string{"s"}},
			Limiter: LimiterConfig{MaxConcurrent: 3},
			Forward: ForwardConfig{Allow: []string{"x-b", "X-A", "x-a"}},
		}
		if got := keyGenHash(h.weirConfig()); got != want {
			t.Fatal("unrelated settings or allow-list order/case changed the hash")
		}
	})

	t.Run("forward mode, allow list and set-cookie stripping change it", func(t *testing.T) {
		for name, mut := range map[string]func(*weir.Config){
			"mode":             func(c *weir.Config) { c.Forward.Mode = weir.ForwardAll },
			"allow added":      func(c *weir.Config) { c.Forward.Allow = append(c.Forward.Allow, "X-C") },
			"allow removed":    func(c *weir.Config) { c.Forward.Allow = c.Forward.Allow[:1] },
			"strip set-cookie": func(c *weir.Config) { c.Storable.StripSetCookie = true },
		} {
			c := weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-A", "X-B"}}}
			mut(&c)
			if keyGenHash(c) == want {
				t.Errorf("%s did not change the hash", name)
			}
		}
	})

	t.Run("list boundaries are unambiguous", func(t *testing.T) {
		a := keyGenHash(weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-Ab"}}})
		b := keyGenHash(weir.Config{Forward: weir.ForwardConfig{Allow: []string{"X-A", "b"}}})
		if a == b {
			t.Fatal("different allow lists collide")
		}
	})
}
