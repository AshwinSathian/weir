package keys

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

var testCfg = Config{MaxPathBytes: 64, MaxQueryBytes: 32, MaxQueryParams: 3}

func okReq() *Request {
	return &Request{Method: "GET", Scheme: "https", Host: "example.com", Path: "/a", RawQuery: "x=1"}
}

func reasonOf(err error) string {
	if re, ok := errors.AsType[*RequestError](err); ok {
		return re.Reason
	}
	return ""
}

func TestValidateRejects(t *testing.T) {
	// FR-VAL-1, FR-VAL-4; T-6
	tests := []struct {
		name   string
		mod    func(r *Request)
		reason string
	}{
		{"uppercase scheme rejected", func(r *Request) { r.Scheme = "HTTPS" }, ReasonScheme},
		{"ftp scheme rejected", func(r *Request) { r.Scheme = "ftp" }, ReasonScheme},
		{"empty host rejected", func(r *Request) { r.Host = "" }, ReasonHost},
		{"host with userinfo rejected", func(r *Request) { r.Host = "u@example.com" }, ReasonHost},
		{"host over 255 bytes rejected", func(r *Request) { r.Host = strings.Repeat("a", 256) }, ReasonHost},
		{"empty method rejected", func(r *Request) { r.Method = "" }, ReasonMethod},
		{"method with space rejected", func(r *Request) { r.Method = "GE T" }, ReasonMethod},
		{"absolute-form path rejected", func(r *Request) { r.Path = "http://example.com/a" }, ReasonPath},
		{"asterisk without OPTIONS rejected", func(r *Request) { r.Path = "*" }, ReasonPath},
		{"empty path rejected", func(r *Request) { r.Path = "" }, ReasonPath},
		{"path over limit rejected", func(r *Request) { r.Path = "/" + strings.Repeat("a", 64) }, ReasonPath},
		{"path with space rejected", func(r *Request) { r.Path = "/a b" }, ReasonPath},
		{"path with control byte rejected", func(r *Request) { r.Path = "/a\nb" }, ReasonPath},
		{"path with non-ASCII rejected", func(r *Request) { r.Path = "/é" }, ReasonPath},
		{"non-hex escape rejected", func(r *Request) { r.Path = "/%zz" }, ReasonPathEscape},
		{"truncated escape rejected", func(r *Request) { r.Path = "/a%4" }, ReasonPathEscape},
		{"bare percent at end rejected", func(r *Request) { r.Path = "/a%" }, ReasonPathEscape},
		{"query over limit rejected", func(r *Request) { r.RawQuery = strings.Repeat("a", 33) }, ReasonQuery},
		{"query with control byte rejected", func(r *Request) { r.RawQuery = "a=\x7f" }, ReasonQuery},
		{"query with space rejected", func(r *Request) { r.RawQuery = "a= b" }, ReasonQuery},
		{"too many query segments rejected", func(r *Request) { r.RawQuery = "a&b&c&d" }, ReasonQueryParams},
		{"empty segments count", func(r *Request) { r.RawQuery = "&&&" }, ReasonQueryParams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := okReq()
			tt.mod(r)
			_, err := Validate(r, &testCfg)
			if got := reasonOf(err); got != tt.reason {
				t.Fatalf("reason = %q (err %v), want %q", got, err, tt.reason)
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	// FR-VAL-1: limits are inclusive; escapes and sub-delims pass through.
	tests := []struct {
		name string
		mod  func(r *Request)
	}{
		{"baseline", func(*Request) {}},
		{"nil header", func(r *Request) { r.Header = nil }},
		{"path at limit", func(r *Request) { r.Path = "/" + strings.Repeat("a", 63) }},
		{"query at limit", func(r *Request) { r.RawQuery = strings.Repeat("a", 32) }},
		{"segments at limit", func(r *Request) { r.RawQuery = "a&b&c" }},
		{"empty query", func(r *Request) { r.RawQuery = "" }},
		{"valid escapes and dot segments kept", func(r *Request) { r.Path = "/a/..%2Fb;x=%0a%Ff" }},
		{"lowercase method is a token", func(r *Request) { r.Method = "get" }},
		{"extension method", func(r *Request) { r.Method = "PURGE" }},
		{"upgrade without Connection token", func(r *Request) { r.Header = http.Header{"Upgrade": {"websocket"}} }},
		{"Connection upgrade without Upgrade field", func(r *Request) { r.Header = http.Header{"Connection": {"upgrade"}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := okReq()
			tt.mod(r)
			if _, err := Validate(r, &testCfg); err != nil {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
}

func TestOptionsAsterisk(t *testing.T) {
	// FR-VAL-1: "*" is valid only with OPTIONS, and only as the whole path.
	r := okReq()
	r.Method, r.Path, r.RawQuery = "OPTIONS", "*", ""
	if _, err := Validate(r, &testCfg); err != nil {
		t.Fatalf("OPTIONS * rejected: %v", err)
	}
	r.Path = "*x"
	if got := reasonOf(mustErr(t, r)); got != ReasonPath {
		t.Fatalf("OPTIONS *x reason = %q, want %q", got, ReasonPath)
	}
}

func mustErr(t *testing.T, r *Request) error {
	t.Helper()
	_, err := Validate(r, &testCfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func TestConnectRejected(t *testing.T) {
	// FR-UPG-1, T-44: CONNECT in any form and Connection-upgrade requests
	// return ErrUpgrade before path validation (CONNECT carries an
	// authority-form path, extended CONNECT carries no Upgrade field).
	tests := []struct {
		name string
		mod  func(r *Request)
	}{
		{"classic CONNECT with authority-form path", func(r *Request) { r.Method, r.Path = "CONNECT", "example.com:443" }},
		{"extended CONNECT websocket without Upgrade", func(r *Request) {
			r.Method, r.Header = "CONNECT", http.Header{":protocol": {"websocket"}}
		}},
		{"CONNECT with otherwise invalid request", func(r *Request) { r.Method, r.Scheme, r.Host, r.Path = "CONNECT", "", "", "" }},
		{"Connection upgrade with Upgrade field", func(r *Request) {
			r.Header = http.Header{"Connection": {"keep-alive, Upgrade"}, "Upgrade": {"websocket"}}
		}},
		{"upgrade token in a later Connection line", func(r *Request) {
			r.Header = http.Header{"Connection": {"keep-alive", " UPGRADE "}, "Upgrade": {"h2c"}}
		}},
		{"upgrade with invalid path still an upgrade", func(r *Request) {
			r.Path = "not-a-path"
			r.Header = http.Header{"Connection": {"upgrade"}, "Upgrade": {"websocket"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := okReq()
			tt.mod(r)
			if _, err := Validate(r, &testCfg); !errors.Is(err, ErrUpgrade) {
				t.Fatalf("err = %v, want ErrUpgrade", err)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	// FR-VAL-1, 04 §3.7: one spelling per authority, so key and forward agree.
	tests := []struct {
		scheme, in, want string
		ok               bool
	}{
		{"https", "EXAMPLE.com.", "example.com", true},
		{"https", "example.com:443", "example.com", true},
		{"http", "example.com:80", "example.com", true},
		{"http", "example.com:443", "example.com:443", true},
		{"https", "example.com:0443", "example.com", true},
		{"https", "example.com:8080", "example.com:8080", true},
		{"https", "example.com.:8080", "example.com:8080", true},
		{"http", "10.0.0.1:65535", "10.0.0.1:65535", true},
		{"http", "[::1]", "[::1]", true},
		{"http", "[::1]:80", "[::1]", true},
		{"http", "[2001:DB8::1]:81", "[2001:db8::1]:81", true},
		{"http", "[::ffff:1.2.3.4]", "[::ffff:1.2.3.4]", true},
		{"http", "[::ffff:102:304]", "[::ffff:1.2.3.4]", true},
		{"http", "[0000:0::1]:8080", "[::1]:8080", true},
		{"http", "a..b", "a..b", true},
		{"http", "a..", "", false},
		{"http", "[::1]:0", "", false},
		{"http", "example.com:65536", "", false},
		{"http", "example.com:", "", false},
		{"http", "example.com:8a", "", false},
		{"http", "example.com:1234567", "", false},
		{"http", "a:b:80", "", false},
		{"http", "::1", "", false},
		{"http", "[::1", "", false},
		{"http", "[::1]x", "", false},
		{"http", "[1.2.3.4]", "", false},
		{"http", "[fe80::1%25eth0]", "", false},
		{"http", "[]", "", false},
		{"http", "a]b", "", false},
		{"http", "exa%6Dple.com", "", false},
		{"http", "a/b", "", false},
		{"http", "a@b", "", false},
		{"http", "a_b", "", false},
		{"http", "exämple.com", "", false},
		{"http", ".", "", false},
		{"http", ".:80", "", false},
		{"http", "", "", false},
		{"http", strings.Repeat("a", 255), strings.Repeat("a", 255), true},
		{"http", strings.Repeat("a", 256), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.scheme+" "+tt.in, func(t *testing.T) {
			got, ok := normalizeHost(tt.scheme, tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("normalizeHost(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestValidateReturnsNormalizedHost(t *testing.T) {
	// FR-VAL-1, P2: the caller forwards and keys this value.
	r := okReq()
	r.Host = "Example.COM:443"
	host, err := Validate(r, &testCfg)
	if err != nil || host != "example.com" {
		t.Fatalf("Validate = %q, %v", host, err)
	}
}

func FuzzValidateRequest(f *testing.F) {
	// FR-VAL-1, FR-VAL-4, NFR-2; T-6: no panic, and no accepted path or
	// query with bytes outside 0x21-0x7E or a malformed escape.
	f.Add("GET", "http", "example.com", "/a/../b", "x=1", "keep-alive, Upgrade")
	f.Fuzz(func(t *testing.T, method, scheme, host, path, query, conn string) {
		r := &Request{Method: method, Scheme: scheme, Host: host, Path: path, RawQuery: query,
			Header: http.Header{"Connection": {conn}, "Upgrade": {"websocket"}}}
		h, err := Validate(r, &testCfg)
		// FR-UPG-1, T-44: an upgrade is never missed and never invented.
		wantUp := method == http.MethodConnect
		for opt := range strings.SplitSeq(conn, ",") {
			wantUp = wantUp || strings.EqualFold(strings.Trim(opt, " \t"), "upgrade")
		}
		if errors.Is(err, ErrUpgrade) != wantUp {
			t.Fatalf("method %q Connection %q: upgrade = %v, want %v", method, conn, !wantUp, wantUp)
		}
		if err != nil {
			if reasonOf(err) == "" && !errors.Is(err, ErrUpgrade) {
				t.Fatalf("error %v has no reason", err)
			}
			return
		}
		if path == "*" && method != http.MethodOptions {
			t.Fatalf("accepted * with method %q", method)
		}
		if !visibleASCII(path) || !visibleASCII(query) {
			t.Fatalf("accepted invisible byte: path %q query %q", path, query)
		}
		if path != "*" && !strings.HasPrefix(path, "/") {
			t.Fatalf("accepted path %q", path)
		}
		for i := 0; i < len(path); i++ {
			if path[i] == '%' && (i+2 >= len(path) || !isHex(path[i+1]) || !isHex(path[i+2])) {
				t.Fatalf("accepted malformed escape in %q", path)
			}
		}
		if h2, ok := normalizeHost(scheme, h); !ok || h2 != h {
			t.Fatalf("host %q normalized to %q, renormalizes to %q, %v", host, h, h2, ok)
		}
	})
}

func FuzzHost(f *testing.F) {
	// FR-VAL-1, NFR-2, 04 §3.7: no panic; an accepted host has no '/', '@',
	// '%', upper case or trailing dot, and normalization is idempotent.
	f.Add("http", "EXAMPLE.com.")
	f.Fuzz(func(t *testing.T, scheme, host string) {
		h, ok := normalizeHost(scheme, host)
		if !ok {
			return
		}
		if h == "" || strings.ContainsAny(h, "/@%?#") || h != strings.ToLower(h) || strings.HasSuffix(h, ".") {
			t.Fatalf("normalizeHost(%q) accepted %q", host, h)
		}
		if strings.HasPrefix(h, "[") {
			lit := h[1:strings.IndexByte(h, ']')]
			if a, err := netip.ParseAddr(lit); err != nil || a.String() != lit {
				t.Fatalf("normalizeHost(%q) = %q: IPv6 literal not canonical", host, h)
			}
		}
		if h2, ok := normalizeHost(scheme, h); !ok || h2 != h {
			t.Fatalf("not idempotent: %q -> %q -> %q, %v", host, h, h2, ok)
		}
	})
}
