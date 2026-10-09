package weircaddy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/keys"
)

// load provisions the module from JSON the way Caddy does: strict decode,
// then Provision. Callers clean up through the returned Handler.
func load(t *testing.T, raw string) (*Handler, error) {
	t.Helper()
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	t.Cleanup(cancel)
	v, err := ctx.LoadModuleByID("http.handlers.weir", []byte(raw))
	if err != nil {
		return nil, err
	}
	h := v.(*Handler)
	t.Cleanup(func() { _ = h.Cleanup() })
	return h, nil
}

// FR-LCY-2: the adapter config maps onto weir.Config; every key in 08 §2.
func TestConfigFromJSON(t *testing.T) {
	t.Run("every key maps to weir.Config", func(t *testing.T) {
		h := &Handler{}
		raw := `{
			"name": "site-a",
			"max_bytes": "512MiB",
			"snapshot_dir": "/var/lib/weir",
			"key": {"query_drop": ["utm_*", "fbclid"], "query_keep": ["id"], "query_sort": true,
				"normalize_path": true, "headers": ["accept-language"], "cookies": ["currency"],
				"accept_encoding": ["br", "gzip"]},
			"forward": {"allow": ["x-request-id"]},
			"bypass": {"cookies": ["session_id"], "headers": ["x-admin"]},
			"limiter": {"max_concurrent": 128, "max_queue": 256, "max_queue_wait": "2s", "max_per_partition": 16},
			"stale": {"while_revalidate": "30s", "if_error": "5m"}
		}`
		if err := decodeStrict([]byte(raw), h); err != nil {
			t.Fatal(err)
		}
		if h.Name != "site-a" || h.SnapshotDir != "/var/lib/weir" {
			t.Fatalf("name/snapshot_dir: %q %q", h.Name, h.SnapshotDir)
		}
		if got := int64(h.MaxBytes); got != 512<<20 {
			t.Fatalf("max_bytes = %d", got)
		}
		cfg := h.weirConfig()
		if len(cfg.Key.QueryDrop) != 2 || cfg.Key.QueryDrop[0] != "utm_*" || !cfg.Key.QuerySort || !cfg.Key.NormalizePath ||
			len(cfg.Key.QueryKeep) != 1 || cfg.Key.Headers[0] != "accept-language" || cfg.Key.Cookies[0] != "currency" ||
			len(cfg.Key.AcceptEncoding) != 2 {
			t.Fatalf("key = %+v", cfg.Key)
		}
		if cfg.Forward.Allow[0] != "x-request-id" || cfg.Bypass.Cookies[0] != "session_id" || cfg.Bypass.Headers[0] != "x-admin" {
			t.Fatalf("forward/bypass = %+v %+v", cfg.Forward, cfg.Bypass)
		}
		l := cfg.Limiter
		if l.MaxConcurrent != 128 || l.MaxQueue != 256 || l.MaxQueueWait != 2*time.Second || l.MaxPerPartition != 16 {
			t.Fatalf("limiter = %+v", l)
		}
		f := cfg.Freshness
		if f.DefaultStaleWhileRevalidate != 30*time.Second || f.DefaultStaleIfError != 5*time.Minute {
			t.Fatalf("freshness = %+v", f)
		}
	})

	t.Run("omitted keys keep the engine defaults", func(t *testing.T) {
		h := &Handler{}
		if err := decodeStrict([]byte(`{"name":"a"}`), h); err != nil {
			t.Fatal(err)
		}
		if got := h.weirConfig(); got.Key.QuerySort || got.Limiter.MaxConcurrent != 0 || got.Freshness.DefaultStaleIfError != 0 {
			t.Fatalf("defaults changed: %+v", got)
		}
	})

	t.Run("max_bytes forms", func(t *testing.T) {
		for in, want := range map[string]int64{
			`1048576`: 1 << 20, `"1048576"`: 1 << 20, `"64KiB"`: 64 << 10, `"2GiB"`: 2 << 30,
			`"10MB"`: 10_000_000, `"1.5MiB"`: 3 << 19,
		} {
			var b ByteSize
			if err := b.UnmarshalJSON([]byte(in)); err != nil || int64(b) != want {
				t.Errorf("%s = %d, %v; want %d", in, int64(b), err, want)
			}
		}
		for _, in := range []string{`"-1MiB"`, `-5`, `"abc"`, `"1XiB"`, `""`, `true`, `"99999999999GiB"`, `1.5`} {
			var b ByteSize
			if err := b.UnmarshalJSON([]byte(in)); err == nil {
				t.Errorf("%s accepted as %d", in, int64(b))
			}
		}
	})

	t.Run("unknown key is rejected", func(t *testing.T) {
		if _, err := load(t, `{"name":"a","max_byte":"1MiB"}`); err == nil {
			t.Fatal("unknown key accepted")
		}
		if _, err := load(t, `{"name":"a","limiter":{"max_concurent":1}}`); err == nil {
			t.Fatal("unknown nested key accepted")
		}
	})

	t.Run("name is required and restricted", func(t *testing.T) {
		long := strings.Repeat("a", 65)
		for _, name := range []string{``, long, `a b`, `a/b`, `é`, `a%2f`, `a:b`, `a\n`} {
			h := &Handler{Name: name}
			if err := h.Validate(); err == nil {
				t.Errorf("name %q accepted", name)
			}
		}
		for _, name := range []string{`a`, `site-a`, `Site_A.1`, strings.Repeat("a", 64), `..`} {
			// ".." passes the charset; the snapshot path is built with filepath.Join on a
			// file name that ends in ".weir", so it is not a traversal (T-45 note in 08 §2).
			h := &Handler{Name: name}
			if err := h.Validate(); err != nil {
				t.Errorf("name %q rejected: %v", name, err)
			}
		}
		if _, err := load(t, `{}`); err == nil {
			t.Fatal("missing name accepted")
		}
	})
}

// FR-LCY-2: a bad engine config fails Provision, which caddy validate runs.
func TestProvisionReportsBadConfig(t *testing.T) {
	for name, raw := range map[string]string{
		"negative limiter":  `{"name":"a","limiter":{"max_concurrent":-1}}`,
		"bad duration":      `{"name":"a","stale":{"if_error":"soon"}}`,
		"negative duration": `{"name":"a","stale":{"if_error":"-5m"}}`,
		"negative queue":    `{"name":"a","limiter":{"max_queue":-1}}`,
		"bad max_bytes":     `{"name":"a","max_bytes":"lots"}`,
		"bad name":          `{"name":"a b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, raw); err == nil {
				t.Fatal("bad config accepted")
			}
		})
	}

	t.Run("good config provisions an engine and cleanup closes it", func(t *testing.T) {
		h, err := load(t, `{"name":"ok","max_bytes":"8MiB"}`)
		if err != nil {
			t.Fatal(err)
		}
		if h.engine == nil {
			t.Fatal("no engine after Provision")
		}
		if err := h.Cleanup(); err != nil {
			t.Fatal(err)
		}
		if err := h.Cleanup(); err != nil {
			t.Fatalf("second Cleanup: %v", err)
		}
	})
}

// FR-UPG-1: the adapter imports the upgrade detector across modules.
func TestInternalKeysImport(t *testing.T) {
	if !keys.IsUpgrade("CONNECT", nil) {
		t.Fatal("CONNECT is not an upgrade")
	}
	var _ = weir.Config{}
}
