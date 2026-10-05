package httpcc

import (
	"net/http"
	"testing"
)

func header(k string, vs ...string) http.Header {
	h := http.Header{}
	for _, v := range vs {
		h.Add(k, v)
	}
	return h
}

func TestParseResponseDirectives(t *testing.T) {
	// FR-FRS-2; RFC 9111 §5.2.2 and §5.2.3
	set := func(v int64) Seconds { return Seconds{V: v, Set: true} }
	invalid := Seconds{Set: true, Invalid: true}
	tests := []struct {
		name  string
		lines []string
		want  ResponseDirectives
	}{
		{"absent header parses to zero value", nil, ResponseDirectives{}},
		{"token arguments", []string{"max-age=60, s-maxage=30, stale-while-revalidate=10, stale-if-error=5"},
			ResponseDirectives{MaxAge: set(60), SMaxAge: set(30), SWR: set(10), SIE: set(5)}},
		{"quoted delta-seconds accepted", []string{`max-age="5"`}, ResponseDirectives{MaxAge: set(5)}},
		{"names are case-insensitive", []string{"Max-Age=7, NO-STORE, Public"},
			ResponseDirectives{MaxAge: set(7), NoStore: true, Public: true}},
		{"non-ASCII look-alike names are unknown", []string{"no-ſtore, ſ-maxage=99999, ſtale-if-error=60, max-age=5"},
			ResponseDirectives{MaxAge: set(5)}},
		{"boolean directives", []string{"no-store, no-cache, private, public, must-revalidate, proxy-revalidate, must-understand"},
			ResponseDirectives{NoStore: true, NoCache: true, Private: true, Public: true,
				MustRevalidate: true, ProxyRevalidate: true, MustUnderstand: true}},
		// FR-STO-5, T-8: public only ever widens what may be stored, so a
		// form RFC 9111 §5.2.2.9 does not define grants nothing.
		{"public with an argument is ignored", []string{`public=no, PUBLIC="x", max-age=5`},
			ResponseDirectives{MaxAge: set(5), Malformed: true}},
		{"public with and without an argument conflict", []string{"public, public=no", "max-age=5"},
			ResponseDirectives{MaxAge: set(5), Malformed: true}},
		{"public after an unclosed quote is not public", []string{`foo="bar, public, max-age=5`},
			ResponseDirectives{MaxAge: set(5), Malformed: true}},
		{"must-revalidate with an argument still restricts", []string{"must-revalidate=x"},
			ResponseDirectives{MustRevalidate: true, Malformed: true}},
		{"closed quote holding public is well-formed", []string{`foo="bar, public", max-age=5`},
			ResponseDirectives{MaxAge: set(5)}},
		{"qualified no-cache treated as unqualified", []string{`no-cache="Set-Cookie, X-Foo", max-age=5`},
			ResponseDirectives{NoCache: true, MaxAge: set(5)}},
		{"qualified private treated as unqualified", []string{`private="Set-Cookie", max-age=5`},
			ResponseDirectives{Private: true, MaxAge: set(5)}},
		{"unknown directives ignored", []string{`foo, bar="baz, qux", max-age=3, community="UCI"`},
			ResponseDirectives{MaxAge: set(3)}},
		{"directives spread over several lines", []string{"max-age=3", "no-store"},
			ResponseDirectives{MaxAge: set(3), NoStore: true}},
		{"whitespace and empty elements tolerated", []string{" , max-age=3 ,, public ,"},
			ResponseDirectives{MaxAge: set(3), Public: true}},
		{"duplicate with same value is not a conflict", []string{"max-age=5, max-age=5"},
			ResponseDirectives{MaxAge: set(5)}},
		{"duplicate with different value sets Duplicates", []string{"max-age=5, max-age=6"},
			ResponseDirectives{MaxAge: set(5), Duplicates: true}},
		{"duplicate across lines sets Duplicates", []string{"s-maxage=5", "s-maxage=50"},
			ResponseDirectives{SMaxAge: set(5), Duplicates: true}},
		{"duplicate stale directive sets Duplicates", []string{"stale-if-error=5, stale-if-error=6"},
			ResponseDirectives{SIE: set(5), Duplicates: true}},
		{"negative argument is invalid", []string{"max-age=-1"}, ResponseDirectives{MaxAge: invalid}},
		{"signed argument is invalid", []string{"max-age=+1"}, ResponseDirectives{MaxAge: invalid}},
		{"non-digit argument is invalid", []string{"max-age=5s"}, ResponseDirectives{MaxAge: invalid}},
		{"decimal argument is invalid", []string{"max-age=1.5"}, ResponseDirectives{MaxAge: invalid}},
		{"missing argument is invalid", []string{"max-age"}, ResponseDirectives{MaxAge: invalid}},
		{"empty argument is invalid", []string{"max-age="}, ResponseDirectives{MaxAge: invalid}},
		{"escaped quoted argument is invalid", []string{`max-age="\5"`}, ResponseDirectives{MaxAge: invalid}},
		// A malformed element must not hide a later private or no-store.
		{"unterminated quote is invalid and later directives still count", []string{`max-age="5, no-store, private`},
			ResponseDirectives{MaxAge: invalid, NoStore: true, Private: true, Malformed: true}},
		{"escaped closing quote left open does not hide private", []string{`ext="C:\", private, no-store`},
			ResponseDirectives{Private: true, NoStore: true, Malformed: true}},
		{"quote inside a token argument does not open a string", []string{`ext=a"b, no-store, private`},
			ResponseDirectives{NoStore: true, Private: true}},
		{"quote inside a name does not open a string", []string{`a"b, private`},
			ResponseDirectives{Private: true}},
		{"delta-seconds at the limit is kept", []string{"max-age=2147483648"}, ResponseDirectives{MaxAge: set(maxDelta)}},
		{"delta-seconds above the limit clamps", []string{"max-age=2147483649"}, ResponseDirectives{MaxAge: set(maxDelta)}},
		{"huge delta-seconds clamps without overflow", []string{"max-age=99999999999999999999999999"},
			ResponseDirectives{MaxAge: set(maxDelta)}},
		{"whitespace around '=' is tolerated", []string{"max-age = 5"}, ResponseDirectives{MaxAge: set(5)}},
		{"leading zeros parse", []string{"max-age=0007"}, ResponseDirectives{MaxAge: set(7)}},
		{"comma inside quotes does not split", []string{`private="a,no-store", public`},
			ResponseDirectives{Private: true, Public: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResponse(header("Cache-Control", tt.lines...))
			if got != tt.want {
				t.Errorf("ParseResponse(%q)\n got %+v\nwant %+v", tt.lines, got, tt.want)
			}
		})
	}
}

func TestParseRequestDirectives(t *testing.T) {
	// RFC 9111 §5.2.1 and §5.4; the engine tests cover how Serve uses these fields.
	set := func(v int64) Seconds { return Seconds{V: v, Set: true} }
	tests := []struct {
		name string
		h    http.Header
		want RequestDirectives
	}{
		{"absent headers parse to zero value", http.Header{}, RequestDirectives{}},
		{"all request directives", header("Cache-Control", "no-store, no-cache, only-if-cached, max-age=0, min-fresh=10, max-stale=20"),
			RequestDirectives{NoStore: true, NoCache: true, OnlyIfCached: true, MaxAge: set(0), MinFresh: set(10), MaxStale: set(20)}},
		{"max-stale without argument accepts any staleness", header("Cache-Control", "max-stale"),
			RequestDirectives{MaxStale: set(maxDelta)}},
		{"invalid max-age marked invalid", header("Cache-Control", "max-age=abc"),
			RequestDirectives{MaxAge: Seconds{Set: true, Invalid: true}}},
		{"Pragma no-cache sets NoCache", header("Pragma", "no-cache"), RequestDirectives{NoCache: true}},
		{"Pragma no-cache among other tokens", header("Pragma", "foo, No-Cache"), RequestDirectives{NoCache: true}},
		{"other Pragma values ignored", header("Pragma", "foo=no-cache"), RequestDirectives{}},
		{"repeated max-age keeps the first value", header("Cache-Control", "max-age=0, max-age=60"),
			RequestDirectives{MaxAge: set(0)}},
		{"max-stale with argument then bare keeps the argument", header("Cache-Control", "max-stale=5, max-stale"),
			RequestDirectives{MaxStale: set(5)}},
		{"bare max-stale then argument keeps any staleness", header("Cache-Control", "max-stale, max-stale=5"),
			RequestDirectives{MaxStale: set(maxDelta)}},
		{"empty max-stale argument is invalid", header("Cache-Control", "max-stale="),
			RequestDirectives{MaxStale: Seconds{Set: true, Invalid: true}}},
		{"non-digit max-stale argument is invalid", header("Cache-Control", "max-stale=abc"),
			RequestDirectives{MaxStale: Seconds{Set: true, Invalid: true}}},
		{"unterminated quote does not hide a request no-store", header("Cache-Control", `x="a, no-store`),
			RequestDirectives{NoStore: true}},
		{"Pragma ignored alongside Cache-Control", http.Header{"Cache-Control": {"max-age=60"}, "Pragma": {"no-cache"}},
			RequestDirectives{MaxAge: set(60)}},
		{"Pragma ignored alongside an empty Cache-Control line", http.Header{"Cache-Control": {""}, "Pragma": {"no-cache"}},
			RequestDirectives{}},
		{"non-ASCII look-alike no-store is unknown", header("Cache-Control", "no-ſtore"), RequestDirectives{}},
		{"response-only directives ignored", header("Cache-Control", "public, private, s-maxage=5"), RequestDirectives{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseRequest(tt.h); got != tt.want {
				t.Errorf("ParseRequest(%v)\n got %+v\nwant %+v", tt.h, got, tt.want)
			}
		})
	}
}

func FuzzCacheControl(f *testing.F) {
	// FR-FRS-2, NFR-2: no panic and no negative or unclamped lifetime on any input.
	f.Fuzz(func(t *testing.T, v string) {
		h := header("Cache-Control", v)
		h.Set("Pragma", v)
		r := ParseResponse(h)
		q := ParseRequest(h)
		// A malformed prefix must never hide a later private or no-store.
		if r := ParseResponse(header("Cache-Control", v+", no-store, private")); !r.NoStore || !r.Private {
			t.Fatalf("%q: trailing no-store or private lost: %+v", v, r)
		}
		if q := ParseRequest(header("Cache-Control", v+", no-store")); !q.NoStore {
			t.Fatalf("%q: trailing request no-store lost", v)
		}
		for _, s := range []Seconds{r.MaxAge, r.SMaxAge, r.SWR, r.SIE, q.MaxAge, q.MinFresh, q.MaxStale} {
			if s.V < 0 || s.V > maxDelta {
				t.Fatalf("%q: out of range %+v", v, s)
			}
			if s.V != 0 && (!s.Set || s.Invalid) {
				t.Fatalf("%q: value on unset or invalid %+v", v, s)
			}
		}
	})
}
