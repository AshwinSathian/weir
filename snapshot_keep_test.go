package weir_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store/memory"
)

// FR-SNP-5 (v1.2), FR-SNP-2, CMP-F8: after a restart, a URL stored before it
// is a hit with no origin call when the store keeps freshness, and is
// revalidated at the origin on first use when it does not (the default).
func TestRestartKeepsCacheWarmWhenAsked(t *testing.T) {
	for name, keep := range map[string]bool{"kept": true, "default": false} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "weir.snap")
			o := testorigin.New()
			o.Default(cacheable("page"))
			start := func() (*weir.Engine, *memory.Store) {
				m, err := memory.New(memory.Config{SnapshotPath: path, SnapshotKeepFreshness: keep})
				if err != nil {
					t.Fatal(err)
				}
				cfg := cacheCfg
				cfg.Store = m
				return newEngine(t, cfg), m
			}
			e, m := start()
			serve(t, e, getReq("/a"), o)
			closeEngine(t, e)
			if err := m.Close(); err != nil { // the caller's store: its Close writes the snapshot
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("no snapshot written: %v", err)
			}

			e, m = start()
			defer m.Close() //nolint:errcheck // the test is over
			defer closeEngine(t, e)
			resp, body := serve(t, e, getReq("/a"), o)
			if body != "page" {
				t.Fatalf("body %q", body)
			}
			if keep && (!resp.Cache.Hit || o.Calls("/a") != 1) {
				t.Fatalf("hit %v, %d origin calls; want a hit and the one call from before the restart", resp.Cache.Hit, o.Calls("/a"))
			}
			if !keep && (resp.Cache.Hit || o.Calls("/a") != 2) {
				t.Fatalf("hit %v, %d origin calls; want the entry revalidated at the origin", resp.Cache.Hit, o.Calls("/a"))
			}
		})
	}
}
