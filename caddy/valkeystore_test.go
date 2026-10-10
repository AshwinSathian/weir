package weircaddy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
)

const secretPW = "s3cr3t-Pw-value"

var valkeySeq atomic.Int32

func valkeyName() string { return fmt.Sprintf("vk-%d", valkeySeq.Add(1)) }

// fullStore sets every field of the store block.
func fullStore() *StoreConfig {
	return &StoreConfig{
		Type: "valkey", Addrs: []string{"10.0.0.1:6379", "10.0.0.2:6379"}, Username: "weir", Password: secretPW,
		TLS: true, Cluster: true, Prefix: "site", HashTag: "tag", CoLocateEntries: true,
		MaxRetention: 0, MaxClockSkew: 0, NoClockSkew: true, MaxHardEpochs: 5, CallTimeout: caddy.Duration(3 * time.Second),
		HardEpochWait: caddy.Duration(100 * time.Millisecond), SkipPolicyCheck: true,
	}
}

// FR-STF-2 (docs/05 §7), 08 §2: every approved store/valkey field has a JSON
// key and reaches valkey.Config.
func TestStoreValkeyJSONMapsEveryField(t *testing.T) {
	raw := `{"name":"a","store":{"type":"valkey","addrs":["10.0.0.1:6379","10.0.0.2:6379"],"username":"weir",
		"password":"` + secretPW + `","tls":true,"cluster":true,"prefix":"site","hash_tag":"tag","co_locate_entries":true,
		"max_retention":"2h","max_clock_skew":"2s","max_hard_epochs":5,"call_timeout":"3s","hard_epoch_wait":"100ms",
		"skip_policy_check":true}}`
	var h Handler
	if err := decodeStrict([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	vc := h.Store.valkeyConfig()
	if len(vc.Addrs) != 2 || vc.Username != "weir" || vc.Password != secretPW || vc.TLS == nil || !vc.Cluster ||
		vc.Prefix != "site" || vc.HashTag != "tag" || !vc.CoLocateEntries || vc.MaxRetention != 2*time.Hour ||
		vc.MaxClockSkew != 2*time.Second || vc.MaxHardEpochs != 5 || vc.CallTimeout != 3*time.Second ||
		vc.HardEpochWait != 100*time.Millisecond || !vc.SkipPolicyCheck {
		t.Fatalf("not every field mapped: %v", vc)
	}
	if _, err := vc.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := decodeStrict([]byte(`{"name":"a","store":{"type":"valkey","addr":"x:1"}}`), &h); err == nil {
		t.Fatal("a misspelled store key must fail the load")
	}
}

// 08 §2: a bad store block fails Provision (and so `caddy validate`), and no
// error carries the password.
func TestStoreValkeyValidation(t *testing.T) {
	bad := map[string]string{
		"missing type":          `{"addrs":["h:1"]}`,
		"unknown type":          `{"type":"redis","addrs":["h:1"]}`,
		"no addresses":          `{"type":"valkey"}`,
		"address without port":  `{"type":"valkey","addrs":["h"]}`,
		"two addresses, single": `{"type":"valkey","addrs":["h:1","g:1"]}`,
		"bad prefix":            `{"type":"valkey","addrs":["h:1"],"prefix":"a:b"}`,
		"url address":           `{"type":"valkey","addrs":["valkey://u:` + secretPW + `@h:1"]}`,
		"userinfo address":      `{"type":"valkey","addrs":["u:` + secretPW + `@h:1"]}`,
		"skew conflict":         `{"type":"valkey","addrs":["h:1"],"no_clock_skew":true,"max_clock_skew":"1s"}`,
	}
	for name, st := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, fmt.Sprintf(`{"name":%q,"store":%s,"key":{"cookies":["%s"]}}`, valkeyName(), st, "c"))
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), secretPW) {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
	t.Run("memory-only settings are refused with a valkey store", func(t *testing.T) {
		for _, extra := range []string{`"max_bytes":"200MiB"`, `"snapshot_dir":"/tmp/x"`} {
			_, err := load(t, fmt.Sprintf(`{"name":%q,%s,"store":{"type":"valkey","addrs":["127.0.0.1:1"]}}`, valkeyName(), extra))
			if err == nil {
				t.Fatalf("%s accepted with a valkey store", extra)
			}
		}
	})
}

// FR-STF-2: Config.String and slog redact the credentials.
func TestStoreConfigRedacts(t *testing.T) {
	s := fullStore()
	for _, got := range []string{s.String(), s.GoString(), fmt.Sprintf("%v %+v %#v", s, s, s), s.LogValue().String()} {
		if strings.Contains(got, secretPW) {
			t.Fatalf("password printed: %s", got)
		}
	}
}

// 08 §3: the pool key and the key-generation hash cover every store setting,
// and the pool key never holds the password in the clear.
func TestPoolKeyIncludesStoreSettings(t *testing.T) {
	mk := func(mut func(*StoreConfig)) *Handler {
		s := fullStore()
		if mut != nil {
			mut(s)
		}
		return &Handler{Name: "pk", Store: s}
	}
	base := mk(nil)
	want, wantKG := base.storeSpec(), keyGenHashFor(base.weirConfig(), base.storeSpec().valkey)
	if want == (&Handler{Name: "pk"}).storeSpec() {
		t.Fatal("a valkey store and the default memory store share a pool key")
	}
	for name, mut := range map[string]func(*StoreConfig){
		"addrs":             func(s *StoreConfig) { s.Addrs = []string{"10.0.0.1:6379", "10.0.0.3:6379"} },
		"addrs order":       func(s *StoreConfig) { s.Addrs = []string{"10.0.0.2:6379", "10.0.0.1:6379"} },
		"username":          func(s *StoreConfig) { s.Username = "other" },
		"password":          func(s *StoreConfig) { s.Password = "other" },
		"tls":               func(s *StoreConfig) { s.TLS = false },
		"cluster":           func(s *StoreConfig) { s.Cluster = false },
		"prefix":            func(s *StoreConfig) { s.Prefix = "other" },
		"hash tag":          func(s *StoreConfig) { s.HashTag = "other" },
		"co-locate":         func(s *StoreConfig) { s.CoLocateEntries = false },
		"max retention":     func(s *StoreConfig) { s.MaxRetention = caddy.Duration(time.Hour) },
		"max clock skew":    func(s *StoreConfig) { s.NoClockSkew, s.MaxClockSkew = false, caddy.Duration(time.Second) },
		"no clock skew":     func(s *StoreConfig) { s.NoClockSkew = false },
		"max hard epochs":   func(s *StoreConfig) { s.MaxHardEpochs = 6 },
		"call timeout":      func(s *StoreConfig) { s.CallTimeout = caddy.Duration(4 * time.Second) },
		"hard epoch wait":   func(s *StoreConfig) { s.HardEpochWait = caddy.Duration(200 * time.Millisecond) },
		"skip policy check": func(s *StoreConfig) { s.SkipPolicyCheck = false },
	} {
		h := mk(mut)
		if h.storeSpec() == want {
			t.Errorf("%s does not change the pool key", name)
		}
		if keyGenHashFor(h.weirConfig(), h.storeSpec().valkey) == wantKG {
			t.Errorf("%s does not change the key-generation hash", name)
		}
	}
	if got := fmt.Sprintf("%v %+v", want, want); strings.Contains(got, secretPW) {
		t.Fatalf("pool key holds the password: %s", got)
	}
	// Equal settings, built separately, are one store (a reload shares it).
	if mk(nil).storeSpec() != want {
		t.Fatal("equal settings give different pool keys")
	}
	// A memory site keeps the hash it had before this card, so an upgrade
	// does not drop snapshots.
	cfg := (&Handler{Name: "m"}).weirConfig()
	if keyGenHashFor(cfg, [sha256.Size]byte{}) != keyGenHash(cfg) {
		t.Fatal("a memory store changed the key-generation hash")
	}
}

// FR-STF-2, FR-STF-3: an unreachable server does not fail the load; requests
// fall through to the origin and the store breaker opens (docs/05 §7).
func TestValkeyStoreOutageOpensBreaker(t *testing.T) {
	ctx, name := newCtx(t), valkeyName()
	// Port 1 on loopback refuses at once.
	h := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"store":{"type":"valkey","addrs":["127.0.0.1:1"],"call_timeout":"500ms"}}`, name))
	var calls atomic.Int32
	const n = 20
	for i := range n {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a", nil)
		if err := h.ServeHTTP(w, r, respond(&calls, "body")); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if w.Code != http.StatusOK || w.Body.String() != "body" {
			t.Fatalf("request %d: %d %q", i, w.Code, w.Body.String())
		}
	}
	if got := calls.Load(); got != n {
		t.Fatalf("origin calls = %d, want %d (nothing can be cached with the store down)", got, n)
	}
	errs := value(t, ctx, "weir_store_errors_total", name, "", "")
	if errs < 5 || errs > 10 {
		t.Fatalf("store errors = %v, want 5-10 (the breaker opens after 5 and stops further calls)", errs)
	}
}

// 08 §2: `store valkey { ... }` adapts to the same module JSON as the
// hand-written store object, and the password comes from {$VAR}.
func TestCaddyfileStoreValkey(t *testing.T) {
	t.Setenv("WEIR_TEST_VK_PW", secretPW)
	const src = `example.com {
	weir {
		name site-a
		store valkey {
			addrs 10.0.0.1:6379 10.0.0.2:6379
			username weir
			password {$WEIR_TEST_VK_PW}
			tls
			cluster
			prefix site
			hash_tag tag
			co_locate_entries
			max_retention 2h
			max_clock_skew 2s
			max_hard_epochs 5
			call_timeout 3s
			hard_epoch_wait 100ms
			skip_policy_check
		}
	}
	reverse_proxy app:8080
}
`
	out, err := adapt(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := weirHandlers(t, out)
	if len(got) != 1 {
		t.Fatalf("handlers = %d", len(got))
	}
	want := Handler{Name: "site-a", Store: &StoreConfig{
		Type: "valkey", Addrs: []string{"10.0.0.1:6379", "10.0.0.2:6379"}, Username: "weir", Password: secretPW,
		TLS: true, Cluster: true, Prefix: "site", HashTag: "tag", CoLocateEntries: true,
		MaxRetention: caddy.Duration(2 * time.Hour), MaxClockSkew: caddy.Duration(2 * time.Second), MaxHardEpochs: 5,
		CallTimeout: caddy.Duration(3 * time.Second), HardEpochWait: caddy.Duration(100 * time.Millisecond), SkipPolicyCheck: true,
	}}
	sameJSON(t, got[0], want)

	errCases := map[string]struct{ src, want string }{
		"unknown store type":  {"store memory {\n addrs a:1\n}", `unknown store type "memory"`},
		"no type":             {"store", "wrong argument count"},
		"no block":            {"store valkey", "requires a block"},
		"empty block":         {"store valkey {\n}", "requires a block"},
		"no addresses":        {"store valkey {\n prefix p\n}", "no addresses"},
		"unknown key":         {"store valkey {\n addrs a:1\n adrs b:1\n}", `unknown key "adrs" in store block`},
		"repeated key":        {"store valkey {\n addrs a:1\n addrs b:1\n}", `"addrs" is set twice in store block`},
		"password missing":    {"store valkey {\n addrs a:1\n password\n}", "wrong argument count"},
		"bad duration":        {"store valkey {\n addrs a:1\n call_timeout soon\n}", "invalid duration"},
		"negative epochs":     {"store valkey {\n addrs a:1\n max_hard_epochs -1\n}", "non-negative integer"},
		"repeated store":      {"store valkey {\n addrs a:1\n}\n store valkey {\n addrs b:1\n}", `"store" is set twice`},
		"max_bytes conflicts": {"max_bytes 200MiB\n store valkey {\n addrs a:1\n}", "max_bytes"},
		"url address":         {"store valkey {\n addrs valkey://u:" + secretPW + "@h:1\n}", "host:port"},
		"flag with argument":  {"store valkey {\n addrs a:1\n tls yes\n}", "wrong argument count"},
	}
	for name, c := range errCases {
		t.Run(name, func(t *testing.T) {
			_, err := adapt(t, "example.com {\n weir {\n name s\n "+c.src+"\n }\n}\n")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q lacks %q", err, c.want)
			}
			if strings.Contains(err.Error(), secretPW) {
				t.Fatalf("error leaks the password: %v", err)
			}
			if !strings.Contains(err.Error(), "Caddyfile:") {
				t.Fatalf("error does not name the line: %v", err)
			}
		})
	}
}
