package weircaddy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir/store"
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

// docs/05 §7, 08 §2: every approved store/valkey field has a JSON
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
	vc := h.Store.valkeyConfig(h.Name)
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

// docs/05 §7, 08 §2: a bad store block fails Provision (and so `caddy validate`),
// and no error carries the password, whatever shape a pasted secret takes.
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
		"user:pass address":     `{"type":"valkey","addrs":["u:` + secretPW + `"]}`,
		"bare secret address":   `{"type":"valkey","addrs":["` + secretPW + `"]}`,
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

// docs/05 §7: String, GoString and slog redact the credentials.
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

// Real-clock exception (CLAUDE.md rule 6): the client dials loopback port 1,
// which refuses at once, and the breaker's open window outlasts the 20
// requests; a synctest bubble cannot hold a real socket.
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
		"password missing":    {"store valkey {\n addrs a:1\n password\n}", "takes exactly one argument"},
		"bad duration":        {"store valkey {\n addrs a:1\n call_timeout soon\n}", "invalid duration"},
		"negative epochs":     {"store valkey {\n addrs a:1\n max_hard_epochs -1\n}", "non-negative integer"},
		"repeated store":      {"store valkey {\n addrs a:1\n}\n store valkey {\n addrs b:1\n}", `"store" is set twice`},
		"max_bytes conflicts": {"max_bytes 200MiB\n store valkey {\n addrs a:1\n}", "max_bytes"},
		"url address":         {"store valkey {\n addrs valkey://u:" + secretPW + "@h:1\n}", "host:port"},
		"flag with argument":  {"store valkey {\n addrs a:1\n tls yes\n}", "takes no arguments"},
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

// R-3, 08 §3 (review of PR 94): changing a store setting and tightening
// forward in one reload builds a new pool entry on the same server, and must
// still write the hard epoch. A store-only change must not.
func TestStoreChangeWithForwardChangePurges(t *testing.T) {
	name := valkeyName()
	raw := func(timeout, fwd string) string {
		return fmt.Sprintf(`{"name":%q,"store":{"type":"valkey","addrs":["127.0.0.1:1"],"call_timeout":%q},"forward":{"allow":[%s]}}`,
			name, timeout, fwd)
	}
	mustLoad(t, newCtx(t), raw("1s", `"X-A"`))
	// Only the store setting changes: no purge, so the dead server is fine.
	mustLoad(t, newCtx(t), raw("2s", `"X-A"`))
	// Store setting and forward both change: the purge needs the server.
	_, err := loadIn(t, newCtx(t), raw("3s", `"X-A","X-B"`))
	if err == nil || !strings.Contains(err.Error(), "key-generation change") {
		t.Fatalf("want a key-generation purge error, got %v", err)
	}
}

// Review of PR 94: explicit defaults are the same store as unset ones, and the
// prefix defaults to the site name so two sites on one server do not share
// entries or epochs.
func TestStoreDefaultsNormalizeBeforeDigest(t *testing.T) {
	unset := &Handler{Name: "n1", Store: &StoreConfig{Type: "valkey", Addrs: []string{"h:1"}}}
	explicit := &Handler{Name: "n1", Store: &StoreConfig{
		Type: "valkey", Addrs: []string{"h:1"}, Prefix: "n1", HashTag: "e", CallTimeout: caddy.Duration(5 * time.Second),
		MaxRetention: caddy.Duration(24 * time.Hour), MaxClockSkew: caddy.Duration(time.Second), MaxHardEpochs: 10000,
	}}
	if unset.storeSpec() != explicit.storeSpec() {
		t.Fatal("explicit defaults build a different store than unset ones")
	}
	other := &Handler{Name: "n2", Store: unset.Store}
	if other.storeSpec().valkey == unset.storeSpec().valkey {
		t.Fatal("two site names default to one prefix")
	}
	if got := unset.Store.valkeyConfig(unset.Name).Prefix; got != "n1" {
		t.Fatalf("default prefix = %q, want the site name", got)
	}
	if got := (&StoreConfig{Prefix: "p"}).valkeyConfig("n1").Prefix; got != "p" {
		t.Fatalf("explicit prefix = %q", got)
	}
}

// Review of PR 94: every StoreConfig field except Type reaches the digest, so a
// field added later and forgotten there fails here.
func TestStoreDigestCoversEveryField(t *testing.T) {
	base := fullStore()
	base.NoClockSkew = false
	want := base.digestFor("d")
	typ := reflect.TypeFor[StoreConfig]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Name == "Type" {
			continue
		}
		c := *base
		v := reflect.ValueOf(&c).Elem().Field(i)
		switch v.Kind() {
		case reflect.String:
			v.SetString(v.String() + "x")
		case reflect.Bool:
			v.SetBool(!v.Bool())
		case reflect.Int, reflect.Int64:
			v.SetInt(v.Int() + int64(time.Second)/10)
		case reflect.Slice:
			v.Set(reflect.Append(v, reflect.ValueOf("10.0.0.9:6379")))
		default:
			t.Fatalf("field %s has an unhandled kind %s", f.Name, v.Kind())
		}
		if c.digestFor("d") == want {
			t.Errorf("field %s does not reach the digest", f.Name)
		}
	}
}

// Review of PR 94: {env.VAR} in username and password is resolved at Provision,
// so the stored config can hold the placeholder and not the secret; rotating the
// variable builds a new store.
func TestStoreSecretsResolveEnvPlaceholders(t *testing.T) {
	t.Setenv("WEIR_TEST_VK_PW2", secretPW)
	s := &StoreConfig{Type: "valkey", Addrs: []string{"h:1"}, Username: "{env.WEIR_TEST_VK_PW2}", Password: "{env.WEIR_TEST_VK_PW2}"}
	vc := s.valkeyConfig("n")
	if vc.Password != secretPW || vc.Username != secretPW {
		t.Fatal("placeholders not resolved")
	}
	before := s.digestFor("n")
	t.Setenv("WEIR_TEST_VK_PW2", "rotated")
	if s.digestFor("n") == before {
		t.Fatal("a rotated secret keeps the same pool key")
	}
}

// Review of PR 94: a Caddyfile error never echoes a token that may be a secret
// (an unquoted password with spaces, a flag followed by a value).
func TestCaddyfileStoreErrorsDoNotEchoSecrets(t *testing.T) {
	for name, line := range map[string]string{
		"password with spaces": "password my " + secretPW + " pw",
		"flag with a value":    "tls " + secretPW,
		"username with spaces": "username a " + secretPW,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := adapt(t, "example.com {\n weir {\n name s\n store valkey {\n addrs h:1\n "+line+"\n }\n }\n}\n")
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), secretPW) {
				t.Fatalf("error echoes the secret: %v", err)
			}
		})
	}
}

// Review of PR 94: the pool shares one Valkey store across equal configs and
// Cleanup closes it when the last user leaves.
func TestValkeyStorePoolLifecycle(t *testing.T) {
	raw := fmt.Sprintf(`{"name":%q,"store":{"type":"valkey","addrs":["127.0.0.1:1"]}}`, valkeyName())
	h1, h2 := mustLoad(t, newCtx(t), raw), mustLoad(t, newCtx(t), raw)
	if h1.pool != h2.pool {
		t.Fatal("equal configs do not share a store")
	}
	st := h1.pool.store
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(context.Background(), store.Key{}); strings.Contains(fmt.Sprint(err), "closed") {
		t.Fatal("store closed while another site still holds it")
	}
	if err := h2.Cleanup(); err != nil {
		t.Fatal(err)
	}
	// Closed: the Valkey store refuses with ErrUnavailable and "closed".
	_, err := st.Get(context.Background(), store.Key{})
	if !errors.Is(err, store.ErrUnavailable) || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("store not closed after the last release: %v", err)
	}
}

// Review of PR 94: closing a Valkey store never blocks past the caller's
// deadline, so a reload cannot hang on a blackholed server.
func TestCloseStoreHonorsContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	p := &pooledStore{store: blockingStore{block: block}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := p.closeStore(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("closeStore = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("closeStore ignored the deadline")
	}
}

// blockingStore embeds a nil store.Store: only Close is ever called.
type blockingStore struct {
	store.Store
	block chan struct{}
}

func (b blockingStore) Close() error { <-b.block; return nil }

// Review of PR 94: multi_host loses the per-owner store cap on Valkey, so the
// operator is told once.
func TestValkeyMultiHostWarns(t *testing.T) {
	h := &Handler{Name: "w", MultiHost: true, Store: &StoreConfig{Type: "valkey", Addrs: []string{"h:1"}}}
	if w := h.storeWarnings(); len(w) != 1 || !strings.Contains(w[0], "multi_host") {
		t.Fatalf("warnings = %v", w)
	}
	if w := (&Handler{Name: "w", Store: h.Store}).storeWarnings(); len(w) != 0 {
		t.Fatalf("unexpected warnings %v", w)
	}
}
