package keys

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func classifyCfg() *Config {
	return &Config{
		MaxPathBytes: 2048, MaxQueryBytes: 1024, MaxQueryParams: 32, MaxKeyedHeaderBytes: 1024,
		QueryDrop: []string{"utm_*"}, Cookies: []string{"lang"}, AcceptEncoding: []string{"gzip"},
	}
}

func classifyReq(method string, h http.Header) *Request {
	if h == nil {
		h = http.Header{}
	}
	return &Request{Method: method, Scheme: "https", Host: "Example.COM:443", Path: "/p", RawQuery: "a=1&utm_source=x", Header: h}
}

func TestClassifyMethods(t *testing.T) {
	// FR-KEY-2, FR-FWD-3, FR-FWD-4; 04 §3.1 step 2
	tests := []struct {
		method       string
		class        Class
		unsafe, head bool
		fwdMethod    string
	}{
		{"GET", ClassCacheable, false, false, "GET"},
		{"HEAD", ClassCacheable, false, true, "GET"},
		{"OPTIONS", ClassPass, false, false, "OPTIONS"},
		{"TRACE", ClassPass, false, false, "TRACE"},
		{"POST", ClassPass, true, false, "POST"},
		{"get", ClassPass, true, false, "get"},
		{"PURGE", ClassPass, true, false, "PURGE"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" classifies", func(t *testing.T) {
			c, err := Classify(classifyReq(tt.method, nil), classifyCfg())
			if err != nil {
				t.Fatal(err)
			}
			if c.Class != tt.class || c.Unsafe != tt.unsafe || c.Head != tt.head || c.Forwarded.Method != tt.fwdMethod {
				t.Fatalf("got class=%v unsafe=%v head=%v fwd=%q", c.Class, c.Unsafe, c.Head, c.Forwarded.Method)
			}
			if tt.class == ClassPass && c.FwdReason != FwdMethod {
				t.Fatalf("FwdReason = %v, want FwdMethod", c.FwdReason)
			}
		})
	}
}

func TestClassifyRejects(t *testing.T) {
	// FR-VAL-1, FR-UPG-1; T-44
	r := classifyReq("GET", http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}})
	if _, err := Classify(r, classifyCfg()); !errors.Is(err, ErrUpgrade) {
		t.Fatalf("upgrade: err = %v", err)
	}
	r = classifyReq("GET", nil)
	r.Path = "a"
	if _, err := Classify(r, classifyCfg()); reasonOf(err) != ReasonPath {
		t.Fatalf("bad path: err = %v", err)
	}
}

func TestClassifyCacheableForward(t *testing.T) {
	// FR-FWD-1, FR-FWD-5, FR-KEY-6, INV-1; T-1, T-5
	h := http.Header{
		"X-Forwarded-Host": {"evil.example"},
		"User-Agent":       {"x"},
		"Authorization":    {"Bearer t"},
		"Cache-Control":    {"max-age=0"},
		"Pragma":           {"no-cache"},
		"Cookie":           {"sid=1; lang=en"},
		"Accept-Encoding":  {"gzip, br"},
		"If-None-Match":    {`"a"`},
		"If-Match":         {`"a"`},
		"Range":            {"bytes=0-1"},
		"Connection":       {"close, Authorization, X-Tenant, Cookie, Accept-Encoding"},
		"X-Tenant":         {"t"},
		"Te":               {"trailers"},
	}
	r := classifyReq("GET", h)
	r.Body = io.NopCloser(strings.NewReader("fat"))
	cfg := classifyCfg()
	cfg.Allow = []string{"X-Tenant"}
	c, err := Classify(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// RFC 9110 §7.6.1: Connection options go, except the keyed fields,
	// which the key already covers.
	want := http.Header{
		"Cache-Control":   {"max-age=0"},
		"Pragma":          {"no-cache"},
		"Cookie":          {"lang=en"},
		"Accept-Encoding": {"gzip"},
	}
	f := c.Forwarded
	if !equalHeader(f.Header, want) {
		t.Fatalf("forwarded header = %v, want %v", f.Header, want)
	}
	if f.Body != nil || c.HasBody {
		t.Fatal("fat GET body forwarded")
	}
	if f.Host != "example.com" || f.Path != "/p" || f.RawQuery != "a=1" {
		t.Fatalf("forwarded target = %q %q %q", f.Host, f.Path, f.RawQuery)
	}
	if !c.Authorized || !c.Range {
		t.Fatalf("Authorized=%v Range=%v", c.Authorized, c.Range)
	}
	if c.Origin != "https://example.com" || c.Partition != "https://example.com/p" {
		t.Fatalf("origin %q partition %q", c.Origin, c.Partition)
	}
	if c.URITag != TagURI(c.Origin, "/p", "a=1") || c.OriginTag != TagOrigin(c.Origin) {
		t.Fatal("tags do not match the forwarded target")
	}
	if len(r.Header) != len(h) || r.Header.Get("X-Forwarded-Host") == "" {
		t.Fatal("client header mutated")
	}
}

func TestAllowCannotForwardRawCookie(t *testing.T) {
	// INV-1, FR-KEY-6; T-1
	cfg := classifyCfg()
	cfg.Allow = []string{"Cookie"}
	c, err := Classify(classifyReq("GET", http.Header{"Cookie": {"sid=1"}}), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Forwarded.Header["Cookie"]; ok {
		t.Fatalf("unkeyed Cookie forwarded: %q", v)
	}
}

func TestBodylessForwardHasNoBodyFields(t *testing.T) {
	// FR-FWD-1, INV-1, P2; T-5: a cacheable fetch has no body, so no field
	// may describe one, and the host is Forwarded.Host alone.
	h := http.Header{
		"Content-Length": {"5"}, "Expect": {"100-continue"}, "Trailer": {"X"},
		"Host": {"evil.example"}, "Content-Type": {"text/plain"},
	}
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("ForwardAll=%v", all), func(t *testing.T) {
			cfg := classifyCfg()
			cfg.ForwardAll = all
			cfg.Allow = []string{"Content-Length", "Expect", "Trailer", "Host"}
			r := classifyReq("GET", h.Clone())
			r.Body = io.NopCloser(strings.NewReader("hello"))
			c, err := Classify(r, cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Content-Length", "Expect", "Trailer", "Host"} {
				if v, ok := c.Forwarded.Header[name]; ok {
					t.Fatalf("%s forwarded: %q", name, v)
				}
			}
		})
	}
	c, _ := Classify(classifyReq("POST", h.Clone()), classifyCfg())
	if _, ok := c.Forwarded.Header["Host"]; ok || c.Forwarded.Header.Get("Content-Length") != "5" {
		t.Fatalf("pass forward: %v", c.Forwarded.Header)
	}
}

func TestClassifyNormalizePath(t *testing.T) {
	// FR-KEY-4, FR-FWD-5
	cfg := classifyCfg()
	cfg.NormalizePath = true
	r := classifyReq("GET", nil)
	r.Path = "/%7euser/%2f"
	c, err := Classify(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Forwarded.Path != "/~user/%2F" {
		t.Fatalf("forwarded path %q", c.Forwarded.Path)
	}
}

func TestClassifyConditionalsParsed(t *testing.T) {
	// FR-SRV-2, FR-FWD-1
	ims := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		inm  []string
		want []string
	}{
		{"strong and weak tags kept", []string{`"a", W/"b"`, `"c,d"`}, []string{`"a"`, `W/"b"`, `"c,d"`}},
		{"star alone kept", []string{"*"}, []string{"*"}},
		{"malformed field ignored", []string{`"a", b`}, nil},
		{"unclosed quote ignored", []string{`"a`}, nil},
		{"star in list ignored", []string{`"a", *`}, nil},
		{"absent is nil", nil, nil},
		{"empty value is no tags", []string{""}, nil},
		{"separators only is no tags", []string{" , ,"}, nil},
		{"W/ without quote ignored", []string{`W/a`}, nil},
		{"over MaxKeyedHeaderBytes ignored", []string{strings.Repeat(`"",`, 400)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{"If-Modified-Since": {ims.Format(http.TimeFormat)}}
			if tt.inm != nil {
				h["If-None-Match"] = tt.inm
			}
			c, err := Classify(classifyReq("GET", h), classifyCfg())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.ClientCond.IfNoneMatch, tt.want) {
				t.Fatalf("IfNoneMatch = %q, want %q", c.ClientCond.IfNoneMatch, tt.want)
			}
			// RFC 9110 §13.1.3: any If-None-Match, parsed or not, hides If-Modified-Since.
			if got := c.ClientCond.IfModifiedSince; got.Equal(ims) != (tt.inm == nil) {
				t.Fatalf("IfModifiedSince = %v with If-None-Match %q", got, tt.inm)
			}
			if c.Forwarded.Header["If-None-Match"] != nil || c.Forwarded.Header["If-Modified-Since"] != nil {
				t.Fatal("client conditionals forwarded")
			}
		})
	}
}

func TestClassifyRequestDirectives(t *testing.T) {
	// FR-SRV-8; D5
	h := http.Header{"Cache-Control": {"no-cache, no-store, max-age=5"}}
	cfg := classifyCfg()
	c, _ := Classify(classifyReq("GET", h), cfg)
	if !c.ReqCC.NoStore || c.ReqCC.NoCache || c.ReqCC.MaxAge.Set {
		t.Fatalf("not honored: %+v", c.ReqCC)
	}
	cfg.HonorRevalidation = true
	c, _ = Classify(classifyReq("GET", h), cfg)
	if !c.ReqCC.NoCache || !c.ReqCC.MaxAge.Set {
		t.Fatalf("honored: %+v", c.ReqCC)
	}
}

func TestClassifyPassForward(t *testing.T) {
	// FR-FWD-3, FR-LIM-7; 04 §3.1
	h := http.Header{
		"X-Custom":          {"1"},
		"Cookie":            {"sid=1"},
		"Accept-Encoding":   {"br"},
		"Connection":        {"keep-alive, X-Hop"},
		"X-Hop":             {"1"},
		"Keep-Alive":        {"timeout=5"},
		"Transfer-Encoding": {"chunked"},
	}
	r := classifyReq("POST", h)
	r.Body = io.NopCloser(strings.NewReader("x"))
	c, err := Classify(r, classifyCfg())
	if err != nil {
		t.Fatal(err)
	}
	want := http.Header{"X-Custom": {"1"}, "Cookie": {"sid=1"}, "Accept-Encoding": {"br"}}
	if !equalHeader(c.Forwarded.Header, want) {
		t.Fatalf("forwarded header = %v, want %v", c.Forwarded.Header, want)
	}
	if c.Forwarded.Body != r.Body || !c.HasBody || c.Forwarded.RawQuery != r.RawQuery {
		t.Fatal("pass request lost its body or query")
	}
	r.Body = http.NoBody
	if c, _ = Classify(r, classifyCfg()); c.HasBody {
		t.Fatal("http.NoBody counted as a body")
	}
	r.Body = io.NopCloser(strings.NewReader(""))
	h["Content-Length"] = []string{"0"}
	if c, _ = Classify(r, classifyCfg()); c.HasBody {
		t.Fatal("Content-Length: 0 counted as a body")
	}
}

func TestClassifyForwardAll(t *testing.T) {
	// FR-FWD-2; G4
	cfg := classifyCfg()
	cfg.ForwardAll = true
	h := http.Header{
		"User-Agent":                {"x"},
		"Cookie":                    {"sid=1; lang=en"},
		"Accept-Encoding":           {"br"},
		"If-None-Match":             {`"a"`},
		"Range":                     {"bytes=0-1"},
		"Connection":                {"X-Hop, Accept-Encoding"},
		"X-Hop":                     {"1"},
		"Upgrade-Insecure-Requests": {"1"},
	}
	c, err := Classify(classifyReq("GET", h), cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := http.Header{
		"User-Agent": {"x"}, "Cookie": {"sid=1; lang=en"}, "Accept-Encoding": {"identity"},
		"Upgrade-Insecure-Requests": {"1"},
	}
	if !equalHeader(c.Forwarded.Header, want) {
		t.Fatalf("forwarded header = %v, want %v", c.Forwarded.Header, want)
	}
}

func TestPassURITagUsesRewrittenQuery(t *testing.T) {
	// FR-INV-1; 04 §3.1: POST /p?utm_source=x must invalidate the URI a GET is stored under
	cfg := classifyCfg()
	cfg.NormalizePath = true
	cfg.QuerySort = true
	r := classifyReq("POST", nil)
	r.Path = "/%7ep"
	r.RawQuery = "b=2&utm_source=x&a=1"
	c, err := Classify(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	get := classifyReq("GET", nil)
	get.Path, get.RawQuery = "/~p", "a=1&b=2"
	g, err := Classify(get, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.URITag != g.URITag {
		t.Fatal("pass URI tag differs from the cacheable URI tag")
	}
	if c.Forwarded.Path != "/%7ep" || c.Forwarded.RawQuery != "b=2&utm_source=x&a=1" {
		t.Fatalf("pass target rewritten: %q %q", c.Forwarded.Path, c.Forwarded.RawQuery)
	}
}

func TestTraceparentValidated(t *testing.T) {
	// FR-FWD-6; T-40
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tests := []struct {
		name    string
		noTrace bool
		in      http.Header
		want    http.Header
	}{
		{"valid headers forwarded", false,
			http.Header{"Traceparent": {tp}, "Tracestate": {"a=1", "b=2"}, "X-Request-Id": {"r1"}},
			http.Header{"Traceparent": {tp}, "Tracestate": {"a=1", "b=2"}, "X-Request-Id": {"r1"}}},
		{"all-zero trace id drops both trace headers", false,
			http.Header{"Traceparent": {"00-00000000000000000000000000000000-00f067aa0ba902b7-01"}, "Tracestate": {"a=1"}},
			http.Header{}},
		{"all-zero parent id drops both", false,
			http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"}, "Tracestate": {"a=1"}},
			http.Header{}},
		{"uppercase hex drops both", false,
			http.Header{"Traceparent": {strings.ToUpper(tp)}, "Tracestate": {"a=1"}},
			http.Header{}},
		{"other version drops both", false,
			http.Header{"Traceparent": {"01" + tp[2:]}, "Tracestate": {"a=1"}},
			http.Header{}},
		{"trailing bytes drop both", false,
			http.Header{"Traceparent": {tp + "-x"}},
			http.Header{}},
		{"two traceparent lines drop both", false,
			http.Header{"Traceparent": {tp, tp}, "Tracestate": {"a=1"}},
			http.Header{}},
		{"tracestate alone dropped", false,
			http.Header{"Tracestate": {"a=1"}},
			http.Header{}},
		{"128-byte request id kept", false,
			http.Header{"X-Request-Id": {strings.Repeat("r", 128)}},
			http.Header{"X-Request-Id": {strings.Repeat("r", 128)}}},
		{"129-byte request id dropped", false,
			http.Header{"X-Request-Id": {strings.Repeat("r", 129)}},
			http.Header{}},
		{"request id with space dropped", false,
			http.Header{"X-Request-Id": {"a b"}},
			http.Header{}},
		{"NoTraceHeaders forwards none", true,
			http.Header{"Traceparent": {tp}, "Tracestate": {"a=1"}, "X-Request-Id": {"r1"}},
			http.Header{}},
	}
	for _, tt := range tests {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ForwardAll=%v", tt.name, all), func(t *testing.T) {
				cfg := classifyCfg()
				cfg.NoTraceHeaders, cfg.ForwardAll = tt.noTrace, all
				c, err := Classify(classifyReq("GET", tt.in.Clone()), cfg)
				if err != nil {
					t.Fatal(err)
				}
				got := c.Forwarded.Header.Clone()
				delete(got, "Accept-Encoding")
				if !equalHeader(got, tt.want) {
					t.Fatalf("ForwardAll=%v: forwarded %v, want %v", all, got, tt.want)
				}
			})
		}
	}
}

func equalHeader(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// FR-SRV-5, FR-FWD-4, T-7: the Range pass-through adds the client's Range and
// If-Range lines to a copy; the cacheable forward keeps neither.
func TestAsRangePass(t *testing.T) {
	h := http.Header{"Range": {"bytes=0-1", "bytes=5-6"}, "If-Range": {`"v1"`}}
	c, err := Classify(classifyReq(http.MethodHead, h), classifyCfg())
	if err != nil {
		t.Fatal(err)
	}
	p := c.AsRangePass()
	if p.Class != ClassPass || p.Forwarded.Method != http.MethodGet {
		t.Fatalf("class %v method %s, want ClassPass GET", p.Class, p.Forwarded.Method)
	}
	if got := p.Forwarded.Header["Range"]; !slices.Equal(got, h["Range"]) || p.Forwarded.Header.Get("If-Range") != `"v1"` {
		t.Fatalf("pass forward header %v", p.Forwarded.Header)
	}
	if c.Forwarded.Header["Range"] != nil || c.Forwarded.Header["If-Range"] != nil {
		t.Fatalf("cacheable forward changed: %v", c.Forwarded.Header)
	}
	p.Forwarded.Header["Range"][0] = "x"
	if h["Range"][0] != "bytes=0-1" {
		t.Fatal("pass forward aliases the client's Range lines")
	}
}
