package experiments

import (
	"encoding/base64"
	"strings"
	"testing"
)

var (
	keyA = []byte("0123456789abcdef0123456789abcdef-key-A")
	keyB = []byte("0123456789abcdef0123456789abcdef-key-B")
)

// enc encodes or fails the test.
func enc(t testing.TB, c Cookie, key []byte) string {
	t.Helper()
	v, ok := c.Encode(key)
	if !ok {
		t.Fatalf("Encode(%+v) refused", c)
	}
	return v
}

// forge signs an arbitrary "v1.<id>.<assignments>" body, as a holder of the
// key could, to reach the grammar behind the MAC.
func forge(body string, key []byte) string {
	return body + "." + base64.RawURLEncoding.EncodeToString(sign(key, body))
}

// altMAC changes only the unused trailing bits of the last MAC character.
func altMAC(v string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, v[len(v)-1])
	return v[:len(v)-1] + string(alphabet[last^1])
}

func sample() Cookie {
	return Cookie{VisitorID: "visitor1", Assignments: []Assignment{
		{"checkout-copy", "b", true}, {"hero", "control", false},
	}}
}

// E2, E3, T-35: a signed cookie decodes to what was encoded.
func TestCookieRoundTrip(t *testing.T) {
	v := enc(t, sample(), keyA)
	if !strings.HasPrefix(v, "v1.visitor1.checkout-copy:b!+hero:control.") {
		t.Fatalf("unexpected grammar: %s", v)
	}
	got, ok := ParseCookie(v, [][]byte{keyA})
	if !ok || got.VisitorID != "visitor1" || len(got.Assignments) != 2 ||
		got.Assignments[0] != (Assignment{"checkout-copy", "b", true}) ||
		got.Assignments[1] != (Assignment{"hero", "control", false}) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	empty, ok := ParseCookie(enc(t, Cookie{VisitorID: "x"}, keyA), [][]byte{keyA})
	if !ok || empty.VisitorID != "x" || len(empty.Assignments) != 0 {
		t.Fatalf("no assignments: %+v, %v", empty, ok)
	}
}

// E2, T-35: editing the assignment, the id or the MAC means no assignment.
func TestCookieRejectsBadMAC(t *testing.T) {
	v := enc(t, sample(), keyA)
	keys := [][]byte{keyA}
	for name, bad := range map[string]string{
		"edited variant":   strings.Replace(v, ":b!", ":a!", 1),
		"edited visitor":   strings.Replace(v, "visitor1", "visitor2", 1),
		"dropped exposed":  strings.Replace(v, "b!", "b", 1),
		"truncated mac":    v[:len(v)-2],
		"missing mac":      v[:strings.LastIndex(v, ".")],
		"other key":        enc(t, sample(), keyB),
		"extra field":      v + ".x",
		"empty":            "",
		"wrong version":    "v2" + v[2:],
		"bare version":     "v1.",
		"padded mac":       v + "=",
		"oversized":        strings.Repeat("a", MaxCookieLen+1),
		"duplicate exp":    forge("v1.v.e:a+e:b", keyA),
		"bad variant byte": forge("v1.v.e:a.b", keyA),
		"trailing plus":    forge("v1.v.e:a+", keyA),
		"empty item":       forge("v1.v.e:a++f:b", keyA),
		"double bang":      forge("v1.v.e:a!!", keyA),
		"mac last bits":    altMAC(enc(t, sample(), keyA)),
	} {
		t.Run(name, func(t *testing.T) {
			if c, ok := ParseCookie(bad, keys); ok {
				t.Fatalf("accepted %q as %+v", bad, c)
			}
		})
	}
}

// T-35: the first key signs, every configured key verifies.
func TestCookieKeyRotation(t *testing.T) {
	old := enc(t, sample(), keyB)
	if _, ok := ParseCookie(old, [][]byte{keyA, keyB}); !ok {
		t.Error("cookie signed with the previous key was refused")
	}
	if _, ok := ParseCookie(old, [][]byte{keyA}); ok {
		t.Error("cookie signed with a retired key was accepted")
	}
	if _, ok := ParseCookie(enc(t, sample(), keyA), [][]byte{keyA, keyB}); !ok {
		t.Error("cookie signed with the first key was refused")
	}
}

// E9: unconfigured experiments are dropped on rewrite, order is kept.
func TestPruneUnconfigured(t *testing.T) {
	c := Cookie{VisitorID: "v", Assignments: []Assignment{
		{"old", "a", true}, {"keep1", "a", false}, {"gone", "b", false}, {"keep2", "b", true},
	}}
	keep := func(n string) bool { return strings.HasPrefix(n, "keep") }
	if !c.Prune(keep) {
		t.Fatal("Prune reported nothing dropped")
	}
	if len(c.Assignments) != 2 || c.Assignments[0].Experiment != "keep1" || c.Assignments[1] != (Assignment{"keep2", "b", true}) {
		t.Fatalf("got %+v", c.Assignments)
	}
	if c.Prune(keep) {
		t.Error("second Prune reported a drop")
	}
}

// E9: the whole value stays within 512 bytes; the oldest assignments go first.
func TestCookieCap512(t *testing.T) {
	c := Cookie{VisitorID: NewVisitorID(func() float64 { return 0.5 })}
	for i := range 40 {
		c.Set("experiment-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "variant-name-"+string(rune('a'+i%26)))
	}
	v := enc(t, c, keyA)
	if len(v) > MaxCookieLen {
		t.Fatalf("value is %d bytes", len(v))
	}
	got, ok := ParseCookie(v, [][]byte{keyA})
	if !ok || len(got.Assignments) == 0 || len(got.Assignments) >= 40 {
		t.Fatalf("kept %d of 40, ok=%v", len(got.Assignments), ok)
	}
	if last := got.Assignments[len(got.Assignments)-1]; last != c.Assignments[39] {
		t.Errorf("newest assignment was dropped: last is %+v", last)
	}
	if got.Assignments[0] != c.Assignments[40-len(got.Assignments)] {
		t.Errorf("not dropped strictly from the front")
	}
	if len(c.Assignments) != 40 {
		t.Error("Encode changed the receiver")
	}
}

// E3: Set appends new experiments and keeps place when a variant changes.
func TestCookieSet(t *testing.T) {
	c := sample()
	c.Set("hero", "control")
	if c.Assignments[1].Exposed || len(c.Assignments) != 2 {
		t.Fatalf("%+v", c.Assignments)
	}
	c.Set("checkout-copy", "control")
	if c.Assignments[0] != (Assignment{"checkout-copy", "control", false}) {
		t.Errorf("changed variant should reset exposure: %+v", c.Assignments[0])
	}
	c.Set("new", "x")
	if len(c.Assignments) != 3 || c.Assignments[2].Experiment != "new" {
		t.Errorf("%+v", c.Assignments)
	}
}

func TestNewVisitorID(t *testing.T) {
	a := NewVisitorID(func() float64 { return 0.25 })
	b := NewVisitorID(func() float64 { return 0.75 })
	if a == b || !isToken(a, maxVisitorLen) || len(a) != 22 {
		t.Errorf("%q %q", a, b)
	}
	for _, f := range []float64{-1, 1, 2} {
		if id := NewVisitorID(func() float64 { return f }); !isToken(id, maxVisitorLen) {
			t.Errorf("rnd=%v gave %q", f, id)
		}
	}
}

// NFR-2, T-35: no input panics, and anything accepted re-encodes to itself.
func FuzzParseCookie(f *testing.F) {
	keys := [][]byte{keyA, keyB}
	for _, s := range []string{
		enc(f, sample(), keyA), enc(f, sample(), keyB), enc(f, Cookie{VisitorID: "x"}, keyA),
		"", "v1.", "v1..", "v1...", "v1....", "v1.a.b:c.", "v1.a.+.x", "v1.a.:.x", "v1.a.b:c!!.x",
		"v1.a.b:c+b:d.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "\x00v1.a..b", strings.Repeat("v1.", 200),
	} {
		f.Add(s, false)
	}
	for _, s := range []string{"v1.a.b:c+.", "v1.a.b:c!!.", "v1.a.+b:c.", "v1.a.b:c++d:e.", "v1.a.b::c.", "v1.a.!.", "v1.a.b:c!+d:e!."} {
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, in string, sign bool) {
		// With sign set, in is "v1.<id>.<assignments>." and gets a valid MAC,
		// so the fuzzer reaches the assignment grammar behind the HMAC.
		if sign {
			in += base64.RawURLEncoding.EncodeToString(signBody(keyA, in[:max(len(in)-1, 0)]))
		}
		c, ok := ParseCookie(in, keys)
		if !ok {
			if c.VisitorID != "" || c.Assignments != nil {
				t.Fatalf("rejected input returned %+v", c)
			}
			return
		}
		if len(in) > MaxCookieLen {
			t.Fatalf("accepted %d bytes", len(in))
		}
		// Every accepted spelling is the canonical one for the key that
		// signed it, so no value has two accepted forms.
		a, _ := c.Encode(keyA)
		b, _ := c.Encode(keyB)
		if in != a && in != b {
			t.Fatalf("accepted non-canonical input %q (canonical %q or %q)", in, a, b)
		}
		again := a
		back, ok := ParseCookie(again, keys)
		if !ok || back.VisitorID != c.VisitorID || len(back.Assignments) != len(c.Assignments) {
			t.Fatalf("round trip lost data: %+v vs %+v", c, back)
		}
	})
}

func signBody(key []byte, body string) []byte { return sign(key, body) }

// T-35: a key below the minimum never verifies and never signs, so an unset
// environment key cannot mint cookies.
func TestCookieRefusesShortKeys(t *testing.T) {
	short := [][]byte{nil, {}, []byte("short")}
	for _, k := range short {
		if v, ok := (Cookie{VisitorID: "x"}).Encode(k); ok || v != "" {
			t.Errorf("Encode signed with a %d-byte key", len(k))
		}
		// A cookie MAC'd with that key must not verify under it either.
		forged := forge("v1.x.", k)
		if _, ok := ParseCookie(forged, [][]byte{k}); ok {
			t.Errorf("a %d-byte key verified a cookie", len(k))
		}
	}
	if _, ok := ParseCookie(enc(t, sample(), keyA), nil); ok {
		t.Error("no keys at all verified a cookie")
	}
}

// T-35: a MAC has one spelling; the last character's unused bits are checked.
func TestCookieMACHasOneSpelling(t *testing.T) {
	v := enc(t, sample(), keyA)
	if _, ok := ParseCookie(altMAC(v), [][]byte{keyA}); ok {
		t.Fatal("a MAC with altered padding bits verified")
	}
}

// E9: Encode refuses values that cannot be valid or cannot fit.
func TestCookieEncodeRefusals(t *testing.T) {
	for name, c := range map[string]Cookie{
		"oversized id":     {VisitorID: strings.Repeat("a", 600)},
		"id with dot":      {VisitorID: "a.b"},
		"empty id":         {},
		"variant bad byte": {VisitorID: "x", Assignments: []Assignment{{"e", "a.b", false}}},
	} {
		t.Run(name, func(t *testing.T) {
			if v, ok := c.Encode(keyA); ok {
				t.Errorf("encoded %q", v)
			}
		})
	}
	// The worst case that still fits: a 64-byte id and 32+32-byte names.
	c := Cookie{VisitorID: strings.Repeat("v", maxVisitorLen)}
	c.Set(strings.Repeat("e", maxNameLen), strings.Repeat("w", maxNameLen))
	v := enc(t, c, keyA)
	got, ok := ParseCookie(v, [][]byte{keyA})
	if !ok || len(got.Assignments) != 1 {
		t.Fatalf("worst-case pair lost: %v, %+v", ok, got)
	}
}

// E3, E9: copies of a Cookie do not see each other's Set or MarkExposed.
func TestCookieCopiesDoNotAlias(t *testing.T) {
	a := sample()
	b := a
	b.Set("hero", "other")
	if !b.MarkExposed("hero") {
		t.Fatal("MarkExposed reported no change")
	}
	if b.MarkExposed("hero") || b.MarkExposed("absent") {
		t.Error("MarkExposed changed something twice or for an unknown experiment")
	}
	if a.Assignments[1] != (Assignment{"hero", "control", false}) {
		t.Errorf("original changed: %+v", a.Assignments)
	}
	var nilPrune Cookie
	if nilPrune.Prune(nil) {
		t.Error("Prune(nil) reported a drop")
	}
}
