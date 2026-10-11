package weircaddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
)

const expKeyA = "0123456789abcdef0123456789abcdef-A"
const expKeyB = "fedcba9876543210fedcba9876543210-B"

const expBlock = `experiments {
	cookie       weir_v
	signing_keys {env.WEIR_EXP_KEY} {env.WEIR_EXP_KEY_PREV}
	consent      cookie cmp_consent analytics=yes
	header       Weir-Variant
	max_active   4
	downstream_keys_on_variant

	experiment checkout-copy {
		match    host example.com path /checkout*
		salt     2026-10-a
		variants control=50 b=50
		winner   ""
	}
	experiment banner {
		match    path /
		salt     s2
		variants x=1 y=3 z=1
		winner   y
	}
}`

// docs/10 §3 (E1), T-35: the experiments block adapts to the same module
// JSON as the hand-written equivalent, round-trips through JSON, keeps signing
// keys as placeholders, and a bad block fails with its Caddyfile line.
func TestCaddyfileExperiments(t *testing.T) {
	site := func(body string) string {
		return "example.com {\n\tweir {\n\t\tname site-a\n" + body + "\n\t}\n\treverse_proxy app:8080\n}\n"
	}
	t.Run("block equals the hand-written JSON", func(t *testing.T) {
		out, err := adapt(t, site(expBlock))
		if err != nil {
			t.Fatal(err)
		}
		got := weirHandlers(t, out)
		if len(got) != 1 {
			t.Fatalf("want one weir handler, got %d", len(got))
		}
		var want Handler
		const raw = `{"name": "site-a", "experiments": {
			"cookie": "weir_v", "signing_keys": ["{env.WEIR_EXP_KEY}", "{env.WEIR_EXP_KEY_PREV}"],
			"consent": {"cookie": "cmp_consent", "value": "analytics=yes"},
			"header": "Weir-Variant", "max_active": 4, "downstream_keys_on_variant": true,
			"experiments": [
				{"name": "checkout-copy", "match": {"host": "example.com", "path": "/checkout*"}, "salt": "2026-10-a",
					"variants": [{"name": "control", "weight": 50}, {"name": "b", "weight": 50}]},
				{"name": "banner", "match": {"path": "/"}, "salt": "s2",
					"variants": [{"name": "x", "weight": 1}, {"name": "y", "weight": 3}, {"name": "z", "weight": 1}], "winner": "y"}
			]}}`
		if err := decodeStrict([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, got[0], want)

		// and back: the marshalled form decodes to the same handler
		b, err := json.Marshal(got[0])
		if err != nil {
			t.Fatal(err)
		}
		var back Handler
		if err := decodeStrict(b, &back); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, back, got[0])
	})

	t.Run("signing keys stay placeholders in the adapted JSON", func(t *testing.T) {
		out, err := adapt(t, site(expBlock))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "{env.WEIR_EXP_KEY}") {
			t.Fatalf("placeholder missing from %s", out)
		}
	})

	t.Run("a repeated experiment name is left to the engine, a repeated key is not", func(t *testing.T) {
		if _, err := adapt(t, site("experiments {\n\tcookie a\n\tcookie b\n}")); err == nil || !strings.Contains(err.Error(), "set twice") {
			t.Fatalf("repeated key accepted: %v", err)
		}
	})

	t.Run("a signing key on its own line is never echoed", func(t *testing.T) {
		const secret = "SECRETSECRETSECRETSECRETSECRETSECRET"
		for name, body := range map[string]string{
			"own line":        "experiments {\n\tsigning_keys {env.A}\n\t" + secret + "\n}",
			"extra arg":       "experiments {\n\tcookie a " + secret + "\n}",
			"in experiment":   "experiments {\n\texperiment e {\n\t\tsalt a " + secret + "\n\t}\n}",
			"as match field":  "experiments {\n\texperiment e {\n\t\tmatch " + secret + " x\n\t}\n}",
			"as variant":      "experiments {\n\texperiment e {\n\t\tvariants " + secret + "\n\t}\n}",
			"as max_active":   "experiments {\n\tmax_active " + secret + "\n}",
			"as experiment":   "experiments {\n\texperiment " + secret + " x {\n\t\tsalt a\n\t}\n}",
			"as flag operand": "experiments {\n\tdownstream_keys_on_variant " + secret + "\n}",
		} {
			_, err := adapt(t, site(body))
			if err == nil {
				t.Errorf("%s: bad block accepted", name)
			} else if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error echoes the token: %v", name, err)
			}
		}
	})

	t.Run("a stray token is never echoed in any block", func(t *testing.T) {
		const secret = "Sup3rSecretValue-0123456789abcdefXYZ"
		for name, body := range map[string]string{
			"experiments with arg":  "experiments " + secret + " {\n\tcookie a\n}",
			"store with arg":        "store valkey " + secret + " {\n\taddrs h:1\n}",
			"store type":            "store " + secret + " {\n\taddrs h:1\n}",
			"store unknown key":     "store valkey {\n\taddrs h:1\n\t" + secret + "\n}",
			"key unknown key":       "key {\n\theaders a\n\t" + secret + "\n}",
			"limiter bad int":       "limiter {\n\tmax_concurrent " + secret + "\n}",
			"limiter bad duration":  "limiter {\n\tmax_queue_wait " + secret + "\n}",
			"top-level unknown key": secret,
			"name with extra arg":   "name a " + secret,
			"forward with arg":      "forward " + secret + " {\n\tallow a\n}",
			"optional bool junk":    "experiments {\n\tdownstream_keys_on_variant " + secret + "\n}",
			"consent short":         "experiments {\n\tconsent cookie " + secret + "\n}",
		} {
			_, err := adapt(t, site(body))
			if err == nil {
				t.Errorf("%s: bad block accepted", name)
			} else if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error echoes the token: %v", name, err)
			}
		}
	})

	t.Run("a mistyped directive name is still named", func(t *testing.T) {
		_, err := adapt(t, site("experiments {\n\tmax_activ 3\n}"))
		if err == nil || !strings.Contains(err.Error(), `"max_activ"`) {
			t.Fatalf("want the typo named, got %v", err)
		}
	})

	t.Run("downstream_keys_on_variant takes an optional boolean", func(t *testing.T) {
		for in, want := range map[string]bool{"": true, " true": true, " false": false} {
			out, err := adapt(t, site("experiments {\n\tsigning_keys {env.K}\n\tdownstream_keys_on_variant"+in+"\n}"))
			if err != nil {
				t.Fatalf("%q: %v", in, err)
			}
			if got := weirHandlers(t, out)[0].Experiments.DownstreamKeysOnVariant; got != want {
				t.Errorf("%q: got %v, want %v", in, got, want)
			}
		}
	})

	for name, tc := range map[string]struct{ body, want string }{
		"empty block":          {"experiments {\n}", "at least one key"},
		"unknown key":          {"experiments {\n\tcolour red\n}", "unknown key"},
		"variant without =":    {"experiments {\n\texperiment e {\n\t\tvariants a b\n\t}\n}", "name=weight"},
		"variant bad weight":   {"experiments {\n\texperiment e {\n\t\tvariants a=x b=1\n\t}\n}", "weight"},
		"match unknown field":  {"experiments {\n\texperiment e {\n\t\tmatch query x\n\t}\n}", "match"},
		"match without value":  {"experiments {\n\texperiment e {\n\t\tmatch path\n\t}\n}", ""},
		"consent not cookie":   {"experiments {\n\tconsent header X y\n}", "consent"},
		"consent short":        {"experiments {\n\tconsent cookie c\n}", ""},
		"experiment no name":   {"experiments {\n\texperiment {\n\t\tsalt x\n\t}\n}", ""},
		"signing keys missing": {"experiments {\n\tsigning_keys\n}", ""},
	} {
		t.Run(name+" fails with a line", func(t *testing.T) {
			_, err := adapt(t, site(tc.body))
			if err == nil {
				t.Fatal("bad block accepted")
			}
			if !strings.Contains(err.Error(), "Caddyfile:") {
				t.Errorf("error does not name the line: %v", err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func expHandler(keys ...string) *Handler {
	return &Handler{Name: "x", Experiments: &ExperimentsConfig{
		SigningKeys: keys,
		Experiments: []ExperimentConfig{{
			Name: "e", Salt: "s", Match: &ExperimentMatch{Path: "/a*"},
			Variants: []VariantConfig{{Name: "control", Weight: 1}, {Name: "b", Weight: 1}},
		}},
	}}
}

// T-35: placeholders resolve when the engine config is built; an unset one
// expands to an empty key and the load fails closed without printing keys.
func TestExperimentsSigningKeyPlaceholders(t *testing.T) {
	t.Setenv("WEIR_EXP_KEY", expKeyA)
	t.Setenv("WEIR_EXP_KEY_PREV", expKeyB)
	cfg := expHandler("{env.WEIR_EXP_KEY}", "{env.WEIR_EXP_KEY_PREV}").weirConfig()
	if len(cfg.Experiments.SigningKeys) != 2 || string(cfg.Experiments.SigningKeys[0]) != expKeyA ||
		string(cfg.Experiments.SigningKeys[1]) != expKeyB {
		t.Fatal("placeholders were not resolved in order")
	}
	if _, err := weir.New(cfg); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	t.Run("unset previous key fails the load", func(t *testing.T) {
		t.Setenv("WEIR_EXP_KEY_PREV", "")
		cfg := expHandler("{env.WEIR_EXP_KEY}", "{env.WEIR_EXP_KEY_PREV}").weirConfig()
		if _, err := weir.New(cfg); err == nil {
			t.Fatal("empty signing key accepted")
		}
	})
	t.Run("short literal key is refused without echoing it", func(t *testing.T) {
		const short = "tooshortsecretvalue"
		_, err := weir.New(expHandler(short).weirConfig())
		if err == nil || strings.Contains(err.Error(), short) {
			t.Fatalf("want an error without the key, got %v", err)
		}
	})
	t.Run("only a whole env placeholder is expanded", func(t *testing.T) {
		t.Setenv("WEIR_EXP_KEY_SET", expKeyA)
		os.Unsetenv("WEIR_EXP_KEY_UNSET") //nolint:errcheck // absent is the point
		for name, k := range map[string]string{
			"clock":            "{time.now.unix}" + expKeyA,
			"file":             "{file./etc/hostname}",
			"host name":        "{system.hostname}" + expKeyA,
			"brace pair":       "ab{cd}ef" + expKeyA,
			"env in the midst": "a{env.WEIR_EXP_KEY_SET}b",
			"unset env":        "{env.WEIR_EXP_KEY_UNSET}",
			"bad env name":     "{env.1X}",
		} {
			err := expHandler(k).Validate()
			if err == nil {
				t.Errorf("%s: accepted", name)
			} else if strings.Contains(err.Error(), expKeyA) {
				t.Errorf("%s: error echoes key material: %v", name, err)
			}
		}
		if err := expHandler("{env.WEIR_EXP_KEY_SET}").Validate(); err != nil {
			t.Errorf("whole env placeholder refused: %v", err)
		}
		if err := expHandler(expKeyA).Validate(); err != nil {
			t.Errorf("literal key refused: %v", err)
		}
		if err := (&Handler{Name: "x", Experiments: &ExperimentsConfig{Cookie: "c"}}).Validate(); err == nil {
			t.Error("a block that enables nothing was accepted")
		}
	})
	t.Run("no experiments block leaves the engine off", func(t *testing.T) {
		cfg := (&Handler{Name: "x"}).weirConfig()
		if len(cfg.Experiments.Experiments) != 0 || len(cfg.Experiments.SigningKeys) != 0 {
			t.Fatal("experiments configured without a block")
		}
	})
}

// R-3, T-35 (08 §3, docs/10 §3): experiments change which key a request maps to, so they
// stay out of the key-generation hash and a reload that edits them does not
// write a hard epoch.
func TestExperimentsExcludedFromKeyGenHash(t *testing.T) {
	t.Setenv("WEIR_EXP_KEY", expKeyA)
	base := (&Handler{Name: "x"}).weirConfig()
	want := keyGenHash(base)

	a := expHandler("{env.WEIR_EXP_KEY}")
	b := expHandler("{env.WEIR_EXP_KEY}")
	b.Experiments.Experiments[0].Variants[1].Weight = 9
	b.Experiments.DownstreamKeysOnVariant = true
	b.Experiments.Experiments = append(b.Experiments.Experiments, ExperimentConfig{
		Name: "f", Salt: "t", Variants: []VariantConfig{{Name: "control", Weight: 1}, {Name: "c", Weight: 1}},
	})
	for name, h := range map[string]*Handler{"one experiment": a, "edited and added": b} {
		if got := keyGenHash(h.weirConfig()); got != want {
			t.Errorf("%s changed the key-generation hash", name)
		}
	}
}

// T-35, 08 §3: a reload whose signing key is unset or short fails Provision
// and gives back the store reference it took, so the running handler keeps its
// store and the rollback claim in runbook 7.9 holds.
func TestExperimentsBadKeyReloadKeepsOldHandler(t *testing.T) {
	t.Setenv("WEIR_EXP_KEY", expKeyA)
	t.Setenv("WEIR_EXP_KEY_PREV", "")
	const exp = `"experiments": {"signing_keys": [%s], "experiments": [{"name": "e", "salt": "s",
		"variants": [{"name": "control", "weight": 1}, {"name": "b", "weight": 1}]}]}`
	cfg := func(keys string) string {
		return `{"name": "rollback", "max_bytes": "200MiB", ` + fmt.Sprintf(exp, keys) + `}`
	}
	old := mustLoad(t, newCtx(t), cfg(`"{env.WEIR_EXP_KEY}"`))
	for name, keys := range map[string]string{
		"unset previous key": `"{env.WEIR_EXP_KEY}", "{env.WEIR_EXP_KEY_PREV}"`,
		"short literal key":  `"tooshortsecretvalue"`,
	} {
		if _, err := loadIn(t, newCtx(t), cfg(keys)); err == nil {
			t.Fatalf("%s: reload accepted", name)
		}
		if n := refs(t, old); n != 1 {
			t.Fatalf("%s: store references = %d after the failed reload, want 1", name, n)
		}
		if _, err := old.pool.store.Get(context.Background(), store.Key{}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: old handler's store is not live: %v", name, err)
		}
	}
}

// docs/10 §2 (E1, E2), T-35, T-36: a new visitor in a split gets a variant,
// the cookie makes it sticky, other paths are untouched, and a reload that
// edits the experiment takes effect without purging other warm keys (R-3).
func TestE2EExperimentSplit(t *testing.T) {
	t.Setenv("WEIR_EXP_KEY", expKeyA)
	o := newE2EOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = io.WriteString(w, "variant="+r.Header.Get("Weir-Variant")+" path="+r.URL.Path)
	})
	tester := newTester(t)
	site := func(variants, winner string) string {
		return fmt.Sprintf(`	weir {
		name e2e-exp
		experiments {
			signing_keys {env.WEIR_EXP_KEY}
			experiment split {
				match  path /exp*
				salt   e2e
				variants %s
				winner   %q
			}
		}
	}
`, variants, winner)
	}
	tester.InitServer(o.caddyfile(site("control=50 b=50", "")), "caddyfile")
	nojar := &http.Client{Transport: tester.Client.Transport, CheckRedirect: tester.Client.CheckRedirect}

	seen := map[string]string{} // variant -> a cookie that carries it
	for range 60 {
		r := get(t, nojar, "/exp")
		var ck string
		for _, sc := range r.header.Values("Set-Cookie") {
			if strings.HasPrefix(sc, "weir_v=") {
				ck = strings.SplitN(sc, ";", 2)[0]
			}
		}
		if ck == "" {
			t.Fatalf("matched request without an assignment cookie: %v", r.header)
		}
		if cc := r.header.Get("Cache-Control"); strings.Contains(cc, "public") || !strings.Contains(cc, "private") {
			t.Fatalf("response that sets the cookie must be private (T-36), got %q", cc)
		}
		v := strings.TrimPrefix(strings.Fields(r.body)[0], "variant=split=")
		if v != "control" && v != "b" {
			t.Fatalf("origin saw %q", r.body)
		}
		seen[v] = ck
	}
	if len(seen) != 2 {
		t.Fatalf("60 new visitors landed in %d variants, want both (p of a false failure is about 1e-18)", len(seen))
	}
	for v, ck := range seen {
		for range 3 {
			r := get(t, nojar, "/exp", "Cookie", ck)
			if got := strings.TrimPrefix(strings.Fields(r.body)[0], "variant=split="); got != v {
				t.Fatalf("cookie for %s got variant %s", v, got)
			}
			if sc := r.header.Values("Set-Cookie"); len(sc) != 0 {
				t.Fatalf("a valid cookie was reissued: %v", sc)
			}
		}
	}
	if r := get(t, nojar, "/other"); len(r.header.Values("Set-Cookie")) != 0 || strings.HasPrefix(r.body, "variant=split=") {
		t.Fatalf("unmatched path was touched: %v %q", r.header, r.body)
	}

	// Reload with a winner: the edit must take effect (a control cookie now
	// gets b) while a warm key outside the experiment stays a hit (R-3).
	get(t, nojar, "/warm")
	tester.InitServer(o.caddyfile(site("control=50 b=50", "b")), "caddyfile")
	if r := get(t, nojar, "/exp", "Cookie", seen["control"]); !strings.HasPrefix(r.body, "variant=split=b ") {
		t.Fatalf("the reloaded winner did not apply: %q", r.body)
	}
	callsAfterExp := o.calls.Load()
	get(t, nojar, "/warm")
	if n := o.calls.Load() - callsAfterExp; n != 0 {
		t.Fatalf("an experiment edit purged the cache: /warm made %d origin calls", n)
	}
}
