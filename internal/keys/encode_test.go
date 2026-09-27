package keys

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"

	"github.com/AshwinSathian/weir/store"
)

func baseInput() *KeyInput {
	return &KeyInput{
		Method: "GET", Scheme: "https", Host: "example.com", Path: "/p", Query: "q=1",
		Headers:     []Header{{Name: "accept-encoding", Value: "gzip", Present: true}},
		CookieNames: []string{"lang"},
		Cookies:     []Cookie{{"lang", "en"}},
	}
}

func TestKeyEncodingMatchesSpec(t *testing.T) {
	// FR-KEY-1, FR-KEY-12: the bytes are fixed by 04 §3.2, with no
	// per-process seed, so every node derives the same key.
	in := &KeyInput{
		Method: "GET", Scheme: "http", Host: "h", Path: "/", Query: "",
		Headers:     []Header{{Name: "a", Value: "v", Present: true}, {Name: "b"}},
		CookieNames: []string{"c", "d"},
		Cookies:     []Cookie{{"d", "x"}},
	}
	want := "weir/key/v1" +
		"\x01\x03GET" + "\x02\x04http" + "\x03\x01h" + "\x04\x01/" + "\x05\x00" +
		"\x10\x01a\x01\x11\x01v" + "\x10\x01b\x00" +
		"\x20\x01c\x00" + "\x20\x01d\x01\x21\x01x"
	if got := string(appendKey(nil, in)); got != want {
		t.Fatalf("encoding\n got %q\nwant %q", got, want)
	}
	if got := PrimaryKey(in); got != sha256.Sum256([]byte(want)) {
		t.Fatalf("PrimaryKey is not sha256 of the encoding")
	}
}

func TestKeyEncodingInjective(t *testing.T) {
	// FR-KEY-1, T-3: shifting bytes across any field boundary, or turning
	// an absent field into an empty one, changes the encoding.
	fields := []func(*KeyInput) *string{
		func(k *KeyInput) *string { return &k.Method },
		func(k *KeyInput) *string { return &k.Scheme },
		func(k *KeyInput) *string { return &k.Host },
		func(k *KeyInput) *string { return &k.Path },
		func(k *KeyInput) *string { return &k.Query },
		func(k *KeyInput) *string { return &k.Headers[0].Name },
		func(k *KeyInput) *string { return &k.Headers[0].Value },
		func(k *KeyInput) *string { return &k.CookieNames[0] },
		func(k *KeyInput) *string { return &k.Cookies[0].Value },
	}
	fresh := func() *KeyInput {
		in := baseInput()
		in.Headers[0].Name = "x"
		in.CookieNames[0], in.Cookies[0].Name = "y", "y"
		return in
	}
	for i := 0; i+1 < len(fields); i++ {
		a, b := fresh(), fresh()
		*fields[i](a), *fields[i+1](a) = "a", "bc"
		*fields[i](b), *fields[i+1](b) = "ab", "c"
		if a.CookieNames[0] != a.Cookies[0].Name {
			// cookie name moved: keep the present cookie matched to it
			a.Cookies[0].Name, b.Cookies[0].Name = a.CookieNames[0], b.CookieNames[0]
		}
		if bytes.Equal(appendKey(nil, a), appendKey(nil, b)) {
			t.Errorf("fields %d and %d: (a,bc) and (ab,c) collide", i, i+1)
		}
	}

	pairs := []struct {
		name string
		a, b func(*KeyInput)
	}{
		{"akamai __ shape across path and query",
			func(k *KeyInput) { k.Path, k.Query = "/a__b", "" },
			func(k *KeyInput) { k.Path, k.Query = "/a", "b" }},
		{"delimiter inside host",
			func(k *KeyInput) { k.Host, k.Path = "h/x", "/" },
			func(k *KeyInput) { k.Host, k.Path = "h", "/x/" }},
		{"empty header versus absent",
			func(k *KeyInput) { k.Headers[0] = Header{Name: "accept-encoding", Present: true} },
			func(k *KeyInput) { k.Headers[0] = Header{Name: "accept-encoding"} }},
		{"empty cookie versus absent",
			func(k *KeyInput) { k.Cookies = []Cookie{{"lang", ""}} },
			func(k *KeyInput) { k.Cookies = nil }},
		{"header value spelled as next field",
			func(k *KeyInput) { k.Headers[0].Value = "gzip\x10\x01x" },
			func(k *KeyInput) { k.Headers = append(k.Headers, Header{Name: "x"}) }},
		{"cookie value moved to another name",
			func(k *KeyInput) { k.CookieNames, k.Cookies = []string{"a", "b"}, []Cookie{{"a", "v"}} },
			func(k *KeyInput) { k.CookieNames, k.Cookies = []string{"a", "b"}, []Cookie{{"b", "v"}} }},
		{"two-byte length prefix at 127 and 128 bytes",
			func(k *KeyInput) { k.Path, k.Query = "/"+strings.Repeat("a", 126), "b" },
			func(k *KeyInput) { k.Path, k.Query = "/"+strings.Repeat("a", 127), "" }},
		{"10 KiB path shifted one byte into query",
			func(k *KeyInput) { k.Path, k.Query = "/"+strings.Repeat("a", 10<<10), "b" },
			func(k *KeyInput) { k.Path, k.Query = "/"+strings.Repeat("a", 10<<10-1), "ab" }},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			a, b := baseInput(), baseInput()
			p.a(a)
			p.b(b)
			if PrimaryKey(a) == PrimaryKey(b) {
				t.Fatal("distinct tuples share a key")
			}
		})
	}
}

func TestKeyedCookiesFeedEncoder(t *testing.T) {
	// FR-KEY-2, T-3: appendKey pairs Cookies with CookieNames in one pass,
	// so keyedCookies must return config order whatever the request order.
	c := &Config{Cookies: []string{"a", "b"}, MaxKeyedHeaderBytes: 64}
	key := func(line string) store.Key {
		in := baseInput()
		in.CookieNames, in.Cookies = c.Cookies, keyedCookies([]string{line}, c)
		return PrimaryKey(in)
	}
	if key("b=2; a=1") == key("b=2; a=9") {
		t.Fatal("cookie a dropped from the key when sent after b")
	}
	if key("b=2; a=1") != key("a=1; b=2") {
		t.Fatal("request cookie order changed the key")
	}
}

func TestPrimaryKeyNoAllocs(t *testing.T) {
	// Hot path: the pooled buffer keeps key building allocation-free.
	in := baseInput()
	if n := testing.AllocsPerRun(100, func() { PrimaryKey(in) }); n != 0 {
		t.Fatalf("PrimaryKey allocates %v times per call", n)
	}
}

// tupleReader decodes fuzz bytes into key tuples. Absent headers and
// cookies carry no value, so reflect.DeepEqual matches encoding semantics.
type tupleReader struct{ b []byte }

func (r *tupleReader) byte() byte {
	if len(r.b) == 0 {
		return 0
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c
}

func (r *tupleReader) str() string {
	n := min(int(r.byte()%8), len(r.b))
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *tupleReader) tuple() *KeyInput {
	k := &KeyInput{Method: r.str(), Scheme: r.str(), Host: r.str(), Path: r.str(), Query: r.str()}
	for range r.byte() % 4 {
		h := Header{Name: r.str()}
		if r.byte()&1 == 1 {
			h.Present, h.Value = true, r.str()
		}
		k.Headers = append(k.Headers, h)
	}
	for range r.byte() % 4 {
		name := r.str()
		k.CookieNames = append(k.CookieNames, name)
		if r.byte()&1 == 1 {
			k.Cookies = append(k.Cookies, Cookie{name, r.str()})
		}
	}
	return k
}

func FuzzKeyEncodingInjective(f *testing.F) {
	// FR-KEY-1, INV-2, T-3
	f.Fuzz(func(t *testing.T, data []byte) {
		r := &tupleReader{data}
		a, b := r.tuple(), r.tuple()
		ea, eb := appendKey(nil, a), appendKey(nil, b)
		if reflect.DeepEqual(a, b) != bytes.Equal(ea, eb) {
			t.Fatalf("tuples %+v and %+v: equal=%v, encodings equal=%v",
				a, b, reflect.DeepEqual(a, b), bytes.Equal(ea, eb))
		}
	})
}
