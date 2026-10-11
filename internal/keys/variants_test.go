package keys

import (
	"net/http"
	"strings"
	"testing"
)

// expCfg returns classifyCfg with experiments wired to a fixed assignment.
func expCfg(forwardAll bool, dims ...Dim) *Config {
	cfg := classifyCfg()
	cfg.ForwardAll = forwardAll
	cfg.ExpHeader, cfg.ExpCookie = "Weir-Variant", "weir_v"
	cfg.Assign = func(string, string, []string) ([]Dim, any) { return dims, "decision" }
	return cfg
}

// T-35: the variant is a key dimension; the encoding is tagged and appended.
func TestVariantDimensionEncoding(t *testing.T) {
	in := baseInput()
	plain := string(appendKey(nil, in))
	in.Variants = []Dim{{"exp", "b"}, {"e2", "c"}}
	got := string(appendKey(nil, in))
	if want := plain + "\x30\x03exp\x31\x01b" + "\x30\x02e2\x31\x01c"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if PrimaryKey(in) == PrimaryKey(baseInput()) {
		t.Fatal("a variant did not change the key")
	}
	// Shifting bytes between experiment and variant, or between dimensions, changes the key.
	a, b := baseInput(), baseInput()
	a.Variants = []Dim{{"ab", "c"}}
	b.Variants = []Dim{{"a", "bc"}}
	if PrimaryKey(a) == PrimaryKey(b) {
		t.Fatal("dimension fields ran together")
	}
}

// T-35, INV-1: two variants key apart, and the key tracks the forwarded header.
func TestClassifyVariantKeysAndForwards(t *testing.T) {
	ca, err := Classify(classifyReq("GET", nil), expCfg(false, Dim{"exp", "a"}))
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := Classify(classifyReq("GET", nil), expCfg(false, Dim{"exp", "b"}))
	cn, _ := Classify(classifyReq("GET", nil), classifyCfg())
	if ca.Primary == cb.Primary || ca.Primary == cn.Primary {
		t.Fatal("variants share a key")
	}
	if got := ca.Forwarded.Header.Get("Weir-Variant"); got != "exp=a" {
		t.Fatalf("forwarded header %q", got)
	}
	if ca.Decision != "decision" || len(ca.Variants) != 1 {
		t.Fatalf("decision not carried: %+v", ca)
	}
	if cn.Forwarded.Header.Get("Weir-Variant") != "" {
		t.Fatal("header set without a dimension")
	}
}

// T-35: every client spelling of the header is removed in every mode, and
// the assignment replaces it.
func TestWeirVariantAlwaysReplaced(t *testing.T) {
	spellings := []string{"Weir-Variant", "Weir_Variant", "WEIR_VARIANT", "weir-variant"}
	for _, forwardAll := range []bool{false, true} {
		for _, dims := range [][]Dim{nil, {{"exp", "a"}, {"e2", "b"}}} {
			cfg := expCfg(forwardAll, dims...)
			cfg.Allow = []string{"Weir-Variant"} // an Allow entry must not let the client's value through
			h := http.Header{}
			for _, s := range spellings {
				h[s] = []string{"exp=evil"}
			}
			c, err := Classify(classifyReq("GET", h), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for k := range c.Forwarded.Header {
				if sameHeaderName(k, "Weir-Variant") && k != "Weir-Variant" {
					t.Fatalf("forwardAll=%v: client spelling %q reached the origin", forwardAll, k)
				}
			}
			want := ""
			if len(dims) > 0 {
				want = "exp=a;e2=b"
			}
			if got := strings.Join(c.Forwarded.Header["Weir-Variant"], ","); got != want {
				t.Fatalf("forwardAll=%v dims=%v: header %q, want %q", forwardAll, dims, got, want)
			}
			// The client's value never enters the key.
			ref, _ := Classify(classifyReq("GET", nil), cfg)
			if c.Primary != ref.Primary {
				t.Fatalf("forwardAll=%v: client header changed the key", forwardAll)
			}
		}
	}
}

// T-35: pass-through requests never carry a client variant header either.
func TestWeirVariantRemovedOnPass(t *testing.T) {
	h := http.Header{"Weir_Variant": {"exp=evil"}, "Weir-Variant": {"exp=evil"}, "Cookie": {"weir_v=v1.x.y.z; a=1"}}
	c, err := Classify(classifyReq("POST", h), expCfg(false, Dim{"exp", "a"}))
	if err != nil {
		t.Fatal(err)
	}
	for k := range c.Forwarded.Header {
		if sameHeaderName(k, "Weir-Variant") {
			t.Fatalf("pass-through forwarded %v", c.Forwarded.Header)
		}
	}
	if h["Weir-Variant"] == nil || h["Cookie"][0] != "weir_v=v1.x.y.z; a=1" {
		t.Fatal("the client's header map was modified")
	}
	if got := c.Forwarded.Header["Cookie"]; len(got) != 1 || got[0] != "a=1" {
		t.Fatalf("pass-through Cookie = %q, want the visitor cookie removed", got)
	}
}

// T-35, INV-1: the experiment cookie is removed from the forwarded Cookie
// header in every mode and the other cookies stay.
func TestExperimentCookieNeverForwarded(t *testing.T) {
	tests := []struct {
		name       string
		forwardAll bool
		cookie     []string
		want       []string
	}{
		{"strict keeps keyed cookies only", false, []string{"weir_v=v1.x.y.z; lang=en; sid=1"}, []string{"lang=en"}},
		{"forward all keeps the rest", true, []string{"weir_v=v1.x.y.z; lang=en; sid=1"}, []string{"lang=en; sid=1"}},
		{"forward all, cookie alone", true, []string{"weir_v=v1.x.y.z"}, nil},
		{"forward all, several lines and a repeat", true, []string{"a=1; weir_v=1", "weir_v=2; b=2", "c=3"}, []string{"a=1", "b=2", "c=3"}},
		{"forward all, other case and no value", true, []string{"WEIR_V=1; weir_v; keep=1"}, []string{"keep=1"}},
		{"forward all, space before the equals sign", true, []string{"weir_v =1; a=1", "weir_v\t=2; b=2"}, []string{"a=1", "b=2"}},
		{"forward all, similar name stays", true, []string{"weir_v2=1; xweir_v=2"}, []string{"weir_v2=1; xweir_v=2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := expCfg(tt.forwardAll, Dim{"exp", "a"})
			c, err := Classify(classifyReq("GET", http.Header{"Cookie": tt.cookie}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Forwarded.Header["Cookie"]; strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("Cookie = %q, want %q", got, tt.want)
			}
		})
	}
}

// T-35: a Config that keys the experiment cookie still never keys or forwards it.
func TestExperimentCookieNotKeyedEvenIfListed(t *testing.T) {
	cfg := expCfg(false, Dim{"exp", "a"})
	cfg.Cookies = []string{"lang", "weir_v"}
	a, _ := Classify(classifyReq("GET", http.Header{"Cookie": {"weir_v=one; lang=en"}}), cfg)
	b, _ := Classify(classifyReq("GET", http.Header{"Cookie": {"weir_v=two; lang=en"}}), cfg)
	if a.Primary != b.Primary {
		t.Fatal("the visitor id split the key")
	}
	if got := a.Forwarded.Header["Cookie"]; len(got) != 1 || got[0] != "lang=en" {
		t.Fatalf("Cookie = %q", got)
	}
}

// E8: Assign sees the normalized host, the rewritten path and the Cookie lines.
func TestAssignReceivesNormalizedRequest(t *testing.T) {
	cfg := expCfg(false)
	cfg.NormalizePath = true
	var host, path string
	var lines []string
	cfg.Assign = func(h, p string, l []string) ([]Dim, any) { host, path, lines = h, p, l; return nil, nil }
	r := classifyReq("GET", http.Header{"Cookie": {"a=1"}})
	r.Path = "/%7euser/%2f"
	if _, err := Classify(r, cfg); err != nil {
		t.Fatal(err)
	}
	if host != "example.com" || path != "/~user/%2F" || len(lines) != 1 {
		t.Fatalf("Assign(%q, %q, %q)", host, path, lines)
	}
}

// T-35, INV-1: the key equals the forwarded request with experiments on.
// Two requests that differ only in an unkeyed client header pick one key.
func TestForwardedEqualsKeyedWithExperiments(t *testing.T) {
	for _, forwardAll := range []bool{false, true} {
		cfg := expCfg(forwardAll, Dim{"exp", "a"})
		a, _ := Classify(classifyReq("GET", http.Header{"Cookie": {"weir_v=1; lang=en"}}), cfg)
		b, _ := Classify(classifyReq("GET", http.Header{"Cookie": {"weir_v=2; lang=en"}, "Weir_Variant": {"x"}}), cfg)
		if a.Primary != b.Primary {
			t.Fatalf("forwardAll=%v: unkeyed bytes split the key", forwardAll)
		}
		if a.Forwarded.Header.Get("Cookie") != b.Forwarded.Header.Get("Cookie") {
			t.Fatalf("forwardAll=%v: forwarded requests differ", forwardAll)
		}
	}
}

func FuzzExperimentForward(f *testing.F) {
	// T-35, INV-1: no input makes the visitor cookie or a client variant
	// header reach the origin, or lets the forwarded request key differently.
	f.Add(false, "weir_v=v1.a.b.c; lang=en", "Weir_Variant", "exp=evil")
	f.Add(true, "a=1; WEIR_V=x; weir_v", "weir-variant", "exp=a")
	f.Add(true, ";;weir_v=;", "WEIR_VARIANT", "")
	f.Add(true, "weir_v =1; \tweir_v\t=2", "weir_variant", "x")
	f.Add(true, "weir_v\t=1; a=1", "Weir-Variant", "x")
	f.Add(true, "\xffweir_v=1", "Weir-Variant", "\x00")
	f.Fuzz(func(t *testing.T, forwardAll bool, cookie, name, value string) {
		cfg := expCfg(forwardAll, Dim{"exp", "a"})
		h := http.Header{"Cookie": {cookie}}
		if isToken(name) {
			h[http.CanonicalHeaderKey(name)] = []string{value}
		}
		before := h.Clone()
		c, err := Classify(classifyReq("GET", h), cfg)
		if !equalHeader(h, before) {
			t.Fatalf("client header mutated: %v, was %v", h, before)
		}
		if err != nil || c.Class != ClassCacheable {
			return
		}
		for k, v := range c.Forwarded.Header {
			if sameHeaderName(k, "Weir-Variant") && (k != "Weir-Variant" || len(v) != 1 || v[0] != "exp=a") {
				t.Fatalf("forwarded %q: %q", k, v)
			}
		}
		// An oracle independent of cookiePairNamed: how net/http's lenient
		// parser, and so an origin built on it, reads the forwarded lines.
		for _, ck := range (&http.Request{Header: http.Header{"Cookie": c.Forwarded.Header["Cookie"]}}).Cookies() {
			if strings.EqualFold(ck.Name, "weir_v") {
				t.Fatalf("visitor cookie forwarded: %q", c.Forwarded.Header["Cookie"])
			}
		}
		again, err := Classify(&c.Forwarded, cfg)
		if err != nil || again.Primary != c.Primary {
			t.Fatalf("key of the forwarded request differs: %v", err)
		}
	})
}
