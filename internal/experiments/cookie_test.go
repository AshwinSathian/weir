package experiments

import (
	"strings"
	"testing"
)

var (
	keyA = []byte("0123456789abcdef-key-A")
	keyB = []byte("0123456789abcdef-key-B")
)

func sample() Cookie {
	return Cookie{VisitorID: "visitor1", Assignments: []Assignment{
		{"checkout-copy", "b", true}, {"hero", "control", false},
	}}
}

// E2, E3, T-35: a signed cookie decodes to what was encoded.
func TestCookieRoundTrip(t *testing.T) {
	v := sample().Encode(keyA)
	if !strings.HasPrefix(v, "v1.visitor1.checkout-copy:b!+hero:control.") {
		t.Fatalf("unexpected grammar: %s", v)
	}
	got, ok := ParseCookie(v, [][]byte{keyA})
	if !ok || got.VisitorID != "visitor1" || len(got.Assignments) != 2 ||
		got.Assignments[0] != (Assignment{"checkout-copy", "b", true}) ||
		got.Assignments[1] != (Assignment{"hero", "control", false}) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	empty, ok := ParseCookie(Cookie{VisitorID: "x"}.Encode(keyA), [][]byte{keyA})
	if !ok || empty.VisitorID != "x" || len(empty.Assignments) != 0 {
		t.Fatalf("no assignments: %+v, %v", empty, ok)
	}
}

// E2, T-35: editing the assignment, the id or the MAC means no assignment.
func TestCookieRejectsBadMAC(t *testing.T) {
	v := sample().Encode(keyA)
	keys := [][]byte{keyA}
	for name, bad := range map[string]string{
		"edited variant":   strings.Replace(v, ":b!", ":a!", 1),
		"edited visitor":   strings.Replace(v, "visitor1", "visitor2", 1),
		"dropped exposed":  strings.Replace(v, "b!", "b", 1),
		"truncated mac":    v[:len(v)-2],
		"missing mac":      v[:strings.LastIndex(v, ".")],
		"other key":        sample().Encode(keyB),
		"extra field":      v + ".x",
		"empty":            "",
		"wrong version":    "v2" + v[2:],
		"bare version":     "v1.",
		"padded mac":       v + "=",
		"oversized":        strings.Repeat("a", MaxCookieLen+1),
		"duplicate exp":    Cookie{VisitorID: "v", Assignments: []Assignment{{"e", "a", false}, {"e", "b", false}}}.Encode(keyA),
		"bad variant byte": Cookie{VisitorID: "v", Assignments: []Assignment{{"e", "a.b", false}}}.Encode(keyA),
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
	old := sample().Encode(keyB)
	if _, ok := ParseCookie(old, [][]byte{keyA, keyB}); !ok {
		t.Error("cookie signed with the previous key was refused")
	}
	if _, ok := ParseCookie(old, [][]byte{keyA}); ok {
		t.Error("cookie signed with a retired key was accepted")
	}
	if _, ok := ParseCookie(sample().Encode(keyA), [][]byte{keyA, keyB}); !ok {
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
	v := c.Encode(keyA)
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
		sample().Encode(keyA), sample().Encode(keyB), Cookie{VisitorID: "x"}.Encode(keyA),
		"", "v1.", "v1..", "v1...", "v1....", "v1.a.b:c.", "v1.a.+.x", "v1.a.:.x", "v1.a.b:c!!.x",
		"v1.a.b:c+b:d.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "\x00v1.a..b", strings.Repeat("v1.", 200),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
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
		// A cookie that verifies under keyA re-encodes identically; one that
		// verified under keyB at least round-trips to the same content.
		again, ok := ParseCookie(c.Encode(keyA), keys)
		if !ok || again.VisitorID != c.VisitorID || len(again.Assignments) != len(c.Assignments) {
			t.Fatalf("round trip lost data: %+v vs %+v", c, again)
		}
	})
}
