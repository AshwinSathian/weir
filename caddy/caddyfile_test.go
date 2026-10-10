package weircaddy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/encode"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/standard"
)

// adapt runs the Caddyfile through Caddy's own adapter, the path `caddy
// adapt` and `caddy run` take.
func adapt(t *testing.T, src string) (string, error) {
	t.Helper()
	out, _, err := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}.Adapt([]byte(src), nil)
	return string(out), err
}

// weirHandlers returns the weir handler objects of every route in adapted
// JSON, in document order, with the "handler" discriminator removed so they
// decode into Handler under the strict decoder.
func weirHandlers(t *testing.T, adapted string) []Handler {
	t.Helper()
	var doc struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []json.RawMessage `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(adapted), &doc); err != nil {
		t.Fatal(err)
	}
	var out []Handler
	for _, srv := range doc.Apps.HTTP.Servers {
		for _, r := range srv.Routes {
			collectWeir(t, r, &out)
		}
	}
	return out
}

// sameJSON compares the canonical marshalled form, so a divergence in how
// Handler marshals is caught, not only in its decoded fields.
func sameJSON(t *testing.T, got, want Handler) {
	t.Helper()
	g, err1 := json.Marshal(got)
	w, err2 := json.Marshal(want)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !bytes.Equal(g, w) {
		t.Fatalf("adapted != hand-written\n got: %s\nwant: %s", g, w)
	}
}

func collectWeir(t *testing.T, raw json.RawMessage, out *[]Handler) {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["handler"] == "weir" {
				delete(x, "handler")
				b, _ := json.Marshal(x)
				var h Handler
				if err := decodeStrict(b, &h); err != nil {
					t.Fatalf("adapted weir object does not decode: %v", err)
				}
				*out = append(*out, h)
				return
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
}

// No docs/01 or docs/06 ID covers Caddyfile syntax; the contract is 08 §1 and
// §2: the example adapts to the same module JSON as the hand-written
// equivalent, and bad input fails with the Caddyfile line.
func TestCaddyfileParse(t *testing.T) {
	const full = `example.com {
	weir {
		name       site-a          # required; store identity across reloads (§3)
		max_bytes  512MiB          # memory store size
		snapshot_dir /var/lib/weir
		key {
			query_drop utm_* fbclid gclid
			query_keep id
			query_sort
			normalize_path
			headers    Accept-Language
			cookies    currency
			accept_encoding br gzip
		}
		forward {
			allow X-Request-Id
		}
		bypass {
			cookies session_id
			headers X-Admin
		}
		limiter {
			max_concurrent    128
			max_queue         256
			max_queue_wait    2s
			max_per_partition 16
		}
		stale {
			while_revalidate 30s
			if_error 5m        # operator default, off unless set (D6)
		}
	}
	reverse_proxy app:8080
}
`
	t.Run("08 section 2 example equals the hand-written JSON", func(t *testing.T) {
		out, err := adapt(t, full)
		if err != nil {
			t.Fatal(err)
		}
		got := weirHandlers(t, out)
		if len(got) != 1 {
			t.Fatalf("want one weir handler, got %d in %s", len(got), out)
		}
		var want Handler
		const raw = `{
			"name": "site-a", "max_bytes": "512MiB", "snapshot_dir": "/var/lib/weir",
			"key": {"query_drop": ["utm_*", "fbclid", "gclid"], "query_keep": ["id"], "query_sort": true,
				"normalize_path": true, "headers": ["Accept-Language"], "cookies": ["currency"],
				"accept_encoding": ["br", "gzip"]},
			"forward": {"allow": ["X-Request-Id"]},
			"bypass": {"cookies": ["session_id"], "headers": ["X-Admin"]},
			"limiter": {"max_concurrent": 128, "max_queue": 256, "max_queue_wait": "2s", "max_per_partition": 16},
			"stale": {"while_revalidate": "30s", "if_error": "5m"}
		}`
		if err := decodeStrict([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, got[0], want)
	})

	t.Run("literal 08 section 2 example", func(t *testing.T) {
		const lit = `example.com {
	weir {
		name       site-a
		max_bytes  512MiB
		key {
			query_drop utm_* fbclid gclid
			query_sort
			headers    Accept-Language
			cookies    currency
			accept_encoding br gzip
		}
		forward {
			allow X-Request-Id
		}
		bypass {
			cookies session_id
		}
		limiter {
			max_concurrent    128
			max_per_partition 16
			max_queue_wait    2s
		}
		stale {
			if_error 5m
		}
	}
	reverse_proxy app:8080
}
`
		out, err := adapt(t, lit)
		if err != nil {
			t.Fatal(err)
		}
		got := weirHandlers(t, out)
		var want Handler
		const raw = `{"name": "site-a", "max_bytes": "512MiB",
			"key": {"query_drop": ["utm_*", "fbclid", "gclid"], "query_sort": true,
				"headers": ["Accept-Language"], "cookies": ["currency"], "accept_encoding": ["br", "gzip"]},
			"forward": {"allow": ["X-Request-Id"]}, "bypass": {"cookies": ["session_id"]},
			"limiter": {"max_concurrent": 128, "max_queue_wait": "2s", "max_per_partition": 16},
			"stale": {"if_error": "5m"}}`
		if err := decodeStrict([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("want one weir handler, got %d", len(got))
		}
		sameJSON(t, got[0], want)
	})

	t.Run("name alone is a valid block", func(t *testing.T) {
		out, err := adapt(t, "example.com {\n\tweir {\n\t\tname a\n\t}\n\treverse_proxy app:8080\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := weirHandlers(t, out); len(got) != 1 || got[0].Name != "a" {
			t.Fatalf("got %+v", got)
		}
	})

	// 08 §2: a matcher token after the directive limits which requests go
	// through the cache; the handler config is unchanged.
	t.Run("matcher token is accepted", func(t *testing.T) {
		out, err := adapt(t, "example.com {\n\tweir /api/* {\n\t\tname a\n\t}\n\treverse_proxy app:8080\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := weirHandlers(t, out); len(got) != 1 || got[0].Name != "a" || !strings.Contains(out, "/api/*") {
			t.Fatalf("got %+v in %s", got, out)
		}
	})

	t.Run("byte sizes and durations", func(t *testing.T) {
		out, err := adapt(t, "example.com {\n\tweir {\n\t\tname a\n\t\tmax_bytes 1500000\n\t\tlimiter {\n\t\t\tmax_queue_wait 1d\n\t\t}\n\t}\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		h := weirHandlers(t, out)[0]
		if int64(h.MaxBytes) != 1500000 || int64(h.Limiter.MaxQueueWait) != 24*60*60*1e9 {
			t.Fatalf("max_bytes %d, max_queue_wait %d", h.MaxBytes, h.Limiter.MaxQueueWait)
		}
	})

	errs := []struct {
		name, body, want, line string
	}{
		{"repeated key in a sub-block", "\tweir {\n\t\tname a\n\t\tforward {\n\t\t\tallow A\n\t\t\tallow B\n\t\t}\n\t}\n", `"allow" is set twice in forward block`, "Caddyfile:6"},
		{"negative duration", "\tweir {\n\t\tname a\n\t\tstale {\n\t\t\tif_error -5m\n\t\t}\n\t}\n", "must not be negative", "Caddyfile:5"},
		{"argument on the directive line", "\tweir foo {\n\t\tname a\n\t}\n", "wrong argument count", "Caddyfile:2"},
		{"sub-block key without a block", "\tweir {\n\t\tname a\n\t\tkey\n\t\tmax_bytes 1MiB\n\t}\n", "key requires a block with at least one key", "Caddyfile:4"},
		{"sub-block key with empty braces", "\tweir {\n\t\tname a\n\t\tlimiter {\n\t\t}\n\t}\n", "limiter requires a block with at least one key", "Caddyfile:5"},
		{"bad name reports the name line", "\tweir {\n\t\tmax_bytes 1MiB\n\t\tname a/b\n\t}\n", "outside [A-Za-z0-9._-]", "Caddyfile:4"},
		{"missing name", "\tweir {\n\t\tmax_bytes 1MiB\n\t}\n", "name is required", "Caddyfile:2"},
		{"empty block", "\tweir\n", "name is required", "Caddyfile:2"},
		{"bad name charset", "\tweir {\n\t\tname a/b\n\t}\n", "outside [A-Za-z0-9._-]", ""},
		{"unknown key", "\tweir {\n\t\tname a\n\t\tbogus 1\n\t}\n", `unknown key "bogus"`, "Caddyfile:4"},
		{"unknown key in nested block", "\tweir {\n\t\tname a\n\t\tkey {\n\t\t\tquery_dorp x\n\t\t}\n\t}\n", `unknown key "query_dorp"`, "Caddyfile:5"},
		{"duplicate name", "\tweir {\n\t\tname a\n\t\tname b\n\t}\n", `"name" is set twice`, ""},
		{"name without value", "\tweir {\n\t\tname\n\t}\n", "argument", ""},
		{"name with two values", "\tweir {\n\t\tname a b\n\t}\n", "argument", ""},
		{"bad byte size", "\tweir {\n\t\tname a\n\t\tmax_bytes lots\n\t}\n", "invalid byte size", ""},
		{"byte size above cap", "\tweir {\n\t\tname a\n\t\tmax_bytes 2PiB\n\t}\n", "above 1 PiB", ""},
		{"bad duration", "\tweir {\n\t\tname a\n\t\tstale {\n\t\t\tif_error soon\n\t\t}\n\t}\n", "duration", ""},
		{"bad integer", "\tweir {\n\t\tname a\n\t\tlimiter {\n\t\t\tmax_concurrent many\n\t\t}\n\t}\n", "max_concurrent", ""},
		{"negative integer", "\tweir {\n\t\tname a\n\t\tlimiter {\n\t\t\tmax_queue -1\n\t\t}\n\t}\n", "max_queue", ""},
		{"list key without values", "\tweir {\n\t\tname a\n\t\tkey {\n\t\t\theaders\n\t\t}\n\t}\n", "argument", ""},
		{"flag key with a value", "\tweir {\n\t\tname a\n\t\tkey {\n\t\t\tquery_sort yes\n\t\t}\n\t}\n", "argument", ""},
	}
	for _, tc := range errs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adapt(t, "example.com {\n"+tc.body+"\treverse_proxy app:8080\n}\n")
			if err == nil {
				t.Fatal("want an error")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("error %q does not contain %q", msg, tc.want)
			}
			// 08 §2: errors name the Caddyfile line.
			line := tc.line
			if line == "" {
				line = "Caddyfile:"
			}
			if !strings.Contains(msg, line) {
				t.Fatalf("error %q does not name %s", msg, line)
			}
		})
	}
}

// No docs/01 or docs/06 ID covers directive order; the contract is 08 §1 and
// §5: the directive sorts before reverse_proxy without a global order
// option, also inside handle blocks, and route keeps the written order.
func TestDirectiveOrder(t *testing.T) {
	const w = "weir {\n name a\n }\n"
	cases := []struct {
		name, src string
	}{
		{"site block, weir written after reverse_proxy", "example.com {\n reverse_proxy app:8080\n " + w + "}\n"},
		{"handle block", "example.com {\n handle /x/* {\n reverse_proxy app:8080\n " + w + " }\n}\n"},
		// route keeps the written order (Caddy docs), so weir goes first here.
		{"route block, weir written first", "example.com {\n route {\n " + w + " reverse_proxy app:8080\n }\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := adapt(t, tc.src)
			if err != nil {
				t.Fatal(err)
			}
			wi := bytes.Index([]byte(out), []byte(`"handler":"weir"`))
			ri := bytes.Index([]byte(out), []byte(`"handler":"reverse_proxy"`))
			if wi < 0 || ri < 0 {
				t.Fatalf("handlers missing in %s", out)
			}
			if wi > ri {
				t.Fatalf("weir at %d runs after reverse_proxy at %d", wi, ri)
			}
		})
	}

	// route keeps the written order, so reverse_proxy first stays first. This
	// pins the behaviour 08 §5 relies on: weir must be written in the block.
	t.Run("route does not reorder", func(t *testing.T) {
		out, err := adapt(t, "example.com {\n route {\n reverse_proxy app:8080\n "+w+" }\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Index(out, `"handler":"reverse_proxy"`) > strings.Index(out, `"handler":"weir"`) {
			t.Fatalf("route reordered handlers: %s", out)
		}
	})

	// 08 §5: encode stays outer by default, weir sits between encode and
	// reverse_proxy.
	t.Run("encode is outer of weir by default", func(t *testing.T) {
		out, err := adapt(t, "example.com {\n reverse_proxy app:8080\n "+w+" encode gzip\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		e := strings.Index(out, `"handler":"encode"`)
		wi := strings.Index(out, `"handler":"weir"`)
		r := strings.Index(out, `"handler":"reverse_proxy"`)
		if e < 0 || wi < 0 || r < 0 || e >= wi || wi >= r {
			t.Fatalf("order encode=%d weir=%d reverse_proxy=%d", e, wi, r)
		}
	})
}
