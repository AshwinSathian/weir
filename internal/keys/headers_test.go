package keys

import (
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeHeader(t *testing.T) {
	// FR-VAL-3, 01 §5.2.3; T-13: malformed values collapse to absent.
	const limit = 16
	tests := []struct {
		name  string
		lines []string
		want  string
		ok    bool
	}{
		{"absent", nil, "", false},
		{"plain value kept", []string{"en"}, "en", true},
		{"outer whitespace trimmed", []string{" \ten \t"}, "en", true},
		{"lines combined with comma space", []string{"a", "b"}, "a, b", true},
		{"whitespace around commas collapsed", []string{"a ,\tb,c"}, "a, b, c", true},
		{"whitespace away from commas kept", []string{"a \t b"}, "a \t b", true},
		{"case preserved", []string{"En-GB"}, "En-GB", true},
		{"trailing comma", []string{"a , "}, "a,", true},
		{"leading comma", []string{",a"}, ", a", true},
		{"empty value is present", []string{""}, "", true},
		{"empty second line", []string{"a", ""}, "a,", true},
		{"NUL is absent", []string{"e\x00n"}, "", false},
		{"non-ASCII is absent", []string{"\xff"}, "", false},
		{"CR LF is absent", []string{"a\r\nb"}, "", false},
		{"malformed second line makes all absent", []string{"a", "\x7f"}, "", false},
		{"at the limit", []string{strings.Repeat("x", limit)}, strings.Repeat("x", limit), true},
		{"over the limit is absent", []string{strings.Repeat("x", limit+1)}, "", false},
		{"limit counts the normalized form", []string{"aaaa,bbbb,cccc,d"}, "", false},
		{"limit counts the received lines", []string{strings.Repeat(" ", limit) + "a"}, "", false},
		{"limit counts the line separators", []string{"aaaaaaa", "bbbbbbbb"}, "", false},
		{"two lines at the limit", []string{"aaaaaaa", "bbbbbbb"}, "aaaaaaa, bbbbbbb", true},
		{"separators only", []string{" , ,, "}, ",,,", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeHeader(tt.lines, limit)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("normalizeHeader(%q) = %q, %v; want %q, %v", tt.lines, got, ok, tt.want, tt.ok)
			}
			// INV-1: the origin's view normalizes to itself, so the key of
			// the forwarded request is the key of the client's.
			if again, ok2 := normalizeHeader([]string{got}, limit); ok && (again != got || !ok2) {
				t.Fatalf("not idempotent: %q then %q, %v", got, again, ok2)
			}
		})
	}
}

func TestClassifyKeyedHeaders(t *testing.T) {
	// FR-KEY-2, FR-FWD-1, FR-VAL-3, INV-1; T-1, T-13
	cfg := classifyCfg()
	cfg.Headers = []string{"X-Tenant", "X-Region"}
	classify := func(t *testing.T, cfg *Config, lines ...string) Classified {
		t.Helper()
		h := http.Header{"X-Unkeyed": {"u"}}
		if lines != nil {
			h["X-Tenant"] = lines
		}
		c, err := Classify(classifyReq("GET", h), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := c.Forwarded.Header["X-Unkeyed"]; ok && !cfg.ForwardAll {
			t.Fatal("unkeyed header forwarded")
		}
		return c
	}
	absent := classify(t, cfg)
	a := classify(t, cfg, "a")
	t.Run("value enters the key and the forward", func(t *testing.T) {
		if a.Primary == absent.Primary || a.Primary == classify(t, cfg, "b").Primary {
			t.Fatal("keyed header value does not change the key")
		}
		if got := a.Forwarded.Header["X-Tenant"]; len(got) != 1 || got[0] != "a" {
			t.Fatalf("forwarded X-Tenant = %q", got)
		}
		if a.Unkeyed {
			t.Fatal("keyed header marked the request Unkeyed")
		}
	})
	t.Run("equal after normalization share key and forward", func(t *testing.T) {
		one, two := classify(t, cfg, " a ,b"), classify(t, cfg, "a", "b")
		if one.Primary != two.Primary {
			t.Fatal("normal forms differ in key")
		}
		for _, c := range []Classified{one, two} {
			if got := c.Forwarded.Header["X-Tenant"]; len(got) != 1 || got[0] != "a, b" {
				t.Fatalf("forwarded X-Tenant = %q, want the normal form", got)
			}
		}
	})
	t.Run("malformed and oversized are absent", func(t *testing.T) {
		for _, v := range []string{"a\x00", "\xc3\xa9", strings.Repeat("x", cfg.MaxKeyedHeaderBytes+1)} {
			c := classify(t, cfg, v)
			if c.Primary != absent.Primary {
				t.Fatalf("%.8q minted a key", v)
			}
			if _, ok := c.Forwarded.Header["X-Tenant"]; ok {
				t.Fatalf("%.8q forwarded", v)
			}
		}
	})
	t.Run("empty differs from absent", func(t *testing.T) {
		if classify(t, cfg, "").Primary == absent.Primary {
			t.Fatal("an empty header the origin can see shares the absent key")
		}
	})
	t.Run("same value under another name differs", func(t *testing.T) {
		h := http.Header{"X-Region": {"a"}}
		c, err := Classify(classifyReq("GET", h), cfg)
		if err != nil || c.Primary == a.Primary {
			t.Fatalf("err %v; X-Region: a shares the key of X-Tenant: a", err)
		}
	})
	t.Run("ForwardAll forwards the normal form", func(t *testing.T) {
		all := *cfg
		all.ForwardAll = true
		c := classify(t, &all, " a ,b")
		if got := c.Forwarded.Header["X-Tenant"]; len(got) != 1 || got[0] != "a, b" {
			t.Fatalf("forwarded X-Tenant = %q", got)
		}
		if c.Primary != classify(t, cfg, "a, b").Primary {
			t.Fatal("ForwardAll keys a different value")
		}
		if bad := classify(t, &all, "a\x00"); len(bad.Forwarded.Header["X-Tenant"]) != 0 {
			t.Fatal("ForwardAll forwards a malformed keyed header the key calls absent")
		}
	})
	t.Run("keyed names the forward rewrites or drops are keyed as forwarded", func(t *testing.T) {
		odd := *cfg
		odd.Headers = []string{"Accept-Encoding", "Cookie", "Range", "Te", "X-Tenant"}
		odd.Allow = []string{"X-Tenant"} // New rejects this; the keyed form still wins
		h := http.Header{"Accept-Encoding": {"br, gzip;q=0.1"}, "Cookie": {"lang=en; other=1"}, "Range": {"bytes=0-1"},
			"Te": {"trailers"}, "X-Tenant": {"a ,b"}}
		c, err := Classify(classifyReq("GET", h), &odd)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Classify(&c.Forwarded, &odd)
		if err != nil || again.Primary != c.Primary {
			t.Fatalf("err %v; key of the forwarded request differs: %v", err, c.Forwarded.Header)
		}
		f := c.Forwarded.Header
		if f.Get("Accept-Encoding") != "gzip" || f.Get("Cookie") != "lang=en" || f.Get("X-Tenant") != "a, b" || len(f["Range"])+len(f["Te"]) != 0 {
			t.Fatalf("forward = %v", f)
		}
	})
}

func TestClassifyBypass(t *testing.T) {
	// FR-BYP-1, FR-FWD-3; 04 §3.1 step 3
	cfg := classifyCfg()
	cfg.BypassHeaders = []string{"X-Preview"}
	cfg.BypassCookies = []string{"session"}
	tests := []struct {
		name   string
		method string
		h      http.Header
		reason FwdReason
		class  Class
	}{
		{"no rule matches", "GET", http.Header{"Cookie": {"lang=en"}}, FwdNone, ClassCacheable},
		{"header present", "GET", http.Header{"X-Preview": {"1"}}, FwdBypass, ClassPass},
		{"empty header present", "HEAD", http.Header{"X-Preview": {""}}, FwdBypass, ClassPass},
		{"cookie present", "GET", http.Header{"Cookie": {"lang=en; session=abc"}}, FwdBypass, ClassPass},
		{"cookie in a later line", "GET", http.Header{"Cookie": {"lang=en", " session=abc"}}, FwdBypass, ClassPass},
		{"cookie without a value", "GET", http.Header{"Cookie": {"session"}}, FwdBypass, ClassPass},
		{"cookie name is case-sensitive", "GET", http.Header{"Cookie": {"Session=abc"}}, FwdNone, ClassCacheable},
		{"longer cookie name", "GET", http.Header{"Cookie": {"session2=abc; xsession=1"}}, FwdNone, ClassCacheable},
		{"name inside a value", "GET", http.Header{"Cookie": {"lang=session"}}, FwdNone, ClassCacheable},
		{"whitespace before the equals sign", "GET", http.Header{"Cookie": {"lang=en;\tsession =abc"}}, FwdBypass, ClassPass},
		{"unsafe method keeps its reason", "POST", http.Header{"X-Preview": {"1"}}, FwdMethod, ClassPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.h["User-Agent"] = []string{"ua"}
			tt.h["Connection"] = []string{"close"}
			c, err := Classify(classifyReq(tt.method, tt.h), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if c.Class != tt.class || c.FwdReason != tt.reason {
				t.Fatalf("class %v reason %v, want %v %v", c.Class, c.FwdReason, tt.class, tt.reason)
			}
			if tt.reason != FwdBypass {
				return
			}
			f := c.Forwarded
			// FR-FWD-3: as received, minus hop-by-hop fields.
			if f.Method != tt.method || f.RawQuery != "a=1&utm_source=x" || f.Header.Get("User-Agent") != "ua" ||
				len(f.Header["Connection"]) != 0 || len(f.Header["Cookie"]) != len(tt.h["Cookie"]) {
				t.Fatalf("forwarded %+v", f)
			}
			if c.Unsafe || c.HasBody || c.Primary != [32]byte{} {
				t.Fatalf("unsafe %v, body %v, key %x", c.Unsafe, c.HasBody, c.Primary)
			}
		})
	}
}

func TestIsHopByHop(t *testing.T) {
	// FR-FWD-2: the names Forward.Allow can never bring back.
	for _, n := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Transfer-Encoding", "Upgrade", "Http2-Settings"} {
		if !IsHopByHop(n) {
			t.Errorf("%s not hop-by-hop", n)
		}
	}
	if IsHopByHop("X-Tenant") || IsHopByHop("Cookie") {
		t.Error("end-to-end field reported hop-by-hop")
	}
}

func FuzzBypassed(f *testing.F) {
	// FR-BYP-1, NFR-2: the bypass cookie scan never panics, misses no
	// well-formed occurrence and matches nothing that lacks the name.
	f.Add("lang=en; session=abc", "")
	f.Add("session", "x=1")
	f.Add(" ;; =; session =", "\xff=session")
	f.Add("xsession=1; session2=2", "lang=session")
	f.Fuzz(func(t *testing.T, line1, line2 string) {
		cfg := &Config{BypassCookies: []string{"session"}}
		got := bypassed(http.Header{"Cookie": {line1, line2}}, cfg)
		if got && !strings.Contains(line1+";"+line2, "session") {
			t.Fatalf("bypass without the cookie name: %q %q", line1, line2)
		}
		if !bypassed(http.Header{"Cookie": {line1, line2, "session=1"}}, cfg) {
			t.Fatalf("missed a session cookie in its own line after %q %q", line1, line2)
		}
	})
}
