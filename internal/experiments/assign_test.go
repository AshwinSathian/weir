package experiments

import (
	"strings"
	"testing"
)

func assignConfig() Config {
	c := validConfig()
	c.Experiments[1].Path = "/hero" // keep the two matchers apart
	return c
}

func rndOf(f float64) func() float64 { return func() float64 { return f } }

// E6: consent is a cookie whose value holds the configured element.
func TestConsentGranted(t *testing.T) {
	c := Consent{Cookie: "cmp", Value: "analytics=yes"}
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"element present", []string{"cmp=analytics=yes"}, true},
		{"element among others", []string{"a=1; cmp=ads=no&analytics=yes|x=y"}, true},
		{"cookie absent", []string{"other=analytics=yes"}, false},
		{"no header", nil, false},
		{"suffix of a longer element", []string{"cmp=xanalytics=yes"}, false},
		{"prefix of a longer element", []string{"cmp=analytics=yes2"}, false},
		{"conflicting duplicates", []string{"cmp=analytics=yes; cmp=analytics=no"}, false},
		{"same value twice", []string{"cmp=analytics=yes", "cmp=analytics=yes"}, true},
		{"name is case sensitive", []string{"CMP=analytics=yes"}, false},
		{"non-visible byte", []string{"cmp=analytics=yes\x01"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Granted(tt.lines); got != tt.want {
				t.Fatalf("Granted(%q) = %v", tt.lines, got)
			}
		})
	}
}

// E5, E6: consent config checks at load.
func TestConsentValidate(t *testing.T) {
	tests := []struct {
		name   string
		c      Consent
		errHas string
	}{
		{"unset", Consent{}, ""},
		{"valid", Consent{Cookie: "cmp", Value: "analytics=yes"}, ""},
		{"value without cookie", Consent{Value: "x"}, "consent"},
		{"cookie without value", Consent{Cookie: "cmp"}, "consent"},
		{"separator in value", Consent{Cookie: "cmp", Value: "a&b"}, "consent"},
		{"space in value", Consent{Cookie: "cmp", Value: "a b"}, "consent"},
		{"same cookie as the assignment cookie", Consent{Cookie: "weir_v", Value: "x"}, "consent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := assignConfig()
			c.Consent = tt.c
			err := c.Validate(8)
			if tt.errHas == "" && err != nil || tt.errHas != "" && (err == nil || !strings.Contains(err.Error(), tt.errHas)) {
				t.Fatalf("Validate = %v, want %q", err, tt.errHas)
			}
		})
	}
}

// E1 to E3: a new visitor is bucketed; a returning one keeps the variant.
func TestAssignNewAndReturning(t *testing.T) {
	c := assignConfig()
	r := c.Assign("shop.example", "/checkout/pay", nil, rndOf(0.25))
	if len(r.Picks) != 1 || r.Picks[0].Experiment != "checkout-copy" || !r.Changed || r.Cookie.VisitorID == "" {
		t.Fatalf("new visitor: %+v", r)
	}
	v := enc(t, r.Cookie, keyA)
	again := c.Assign("shop.example", "/checkout/pay", []string{"weir_v=" + v}, rndOf(0.9))
	if again.Changed || again.Picks[0] != r.Picks[0] || again.Cookie.VisitorID != r.Cookie.VisitorID {
		t.Fatalf("returning visitor changed: %+v vs %+v", again, r)
	}
}

// E3: a kept assignment survives a changed weight, but not a removed variant.
func TestAssignKeepsExistingUnlessVariantGone(t *testing.T) {
	c := assignConfig()
	ck := Cookie{VisitorID: "v1", Assignments: []Assignment{{"checkout-copy", "b", false}}}
	line := []string{"weir_v=" + enc(t, ck, keyA)}
	if r := c.Assign("h", "/checkout", line, rndOf(0)); r.Picks[0].Variant != "b" || r.Changed {
		t.Fatalf("kept: %+v", r)
	}
	gone := Cookie{VisitorID: "v1", Assignments: []Assignment{{"checkout-copy", "zzz", false}}}
	r := c.Assign("h", "/checkout", []string{"weir_v=" + enc(t, gone, keyA)}, rndOf(0))
	if !r.Changed || r.Picks[0].Variant == "zzz" || r.Cookie.VisitorID != "v1" {
		t.Fatalf("variant removed from config: %+v", r)
	}
}

// E2, T-35: a forged, truncated or foreign-key cookie is a new visitor.
func TestAssignIgnoresBadCookie(t *testing.T) {
	c := assignConfig()
	forged := forge("v1.attacker.checkout-copy:b", []byte("short"))
	for _, v := range []string{forged, "garbage", enc(t, Cookie{VisitorID: "x"}, keyB)} {
		r := c.Assign("h", "/checkout", []string{"weir_v=" + v}, rndOf(0.5))
		if !r.Changed || r.Cookie.VisitorID == "x" || r.Cookie.VisitorID == "attacker" {
			t.Fatalf("%q was trusted: %+v", v, r)
		}
	}
}

// E6: without consent the visitor gets control, even with a valid assignment.
func TestAssignConsentAbsentGetsControl(t *testing.T) {
	c := assignConfig()
	c.Consent = Consent{Cookie: "cmp", Value: "analytics=yes"}
	ck := Cookie{VisitorID: "v1", Assignments: []Assignment{{"checkout-copy", "b", false}}}
	line := "weir_v=" + enc(t, ck, keyA)
	r := c.Assign("h", "/checkout", []string{line}, rndOf(0.9))
	if r.Picks[0].Variant != "control" || r.Changed {
		t.Fatalf("no consent: %+v", r)
	}
	r = c.Assign("h", "/checkout", []string{line + "; cmp=analytics=yes"}, rndOf(0.9))
	if r.Picks[0].Variant != "b" {
		t.Fatalf("consent given keeps the assignment: %+v", r)
	}
}

// E4: a winner applies to every visitor and records nothing.
func TestAssignWinnerOverrides(t *testing.T) {
	c := assignConfig()
	c.Experiments[0].Winner = "b"
	c.Consent = Consent{Cookie: "cmp", Value: "analytics=yes"}
	r := c.Assign("h", "/checkout", nil, rndOf(0))
	if r.Picks[0].Variant != "b" || r.Changed {
		t.Fatalf("winner: %+v", r)
	}
}

// E1, E8: host and path decide which experiments apply; none means no work.
func TestAssignNoMatch(t *testing.T) {
	c := assignConfig()
	if r := c.Assign("h", "/other", nil, rndOf(0)); len(r.Picks) != 0 || r.Changed {
		t.Fatalf("no match: %+v", r)
	}
}
