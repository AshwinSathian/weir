package weir

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func expTestConfig() Config {
	return Config{Experiments: ExperimentsConfig{
		SigningKeys: [][]byte{[]byte("0123456789abcdef0123456789abcdef-k")},
		Experiments: []ExperimentConfig{{
			Name: "e", Salt: "s", Path: "/x*",
			Variants: []VariantConfig{{"control", 1}, {"b", 1}},
		}},
	}}
}

// E5, FR-LCY-1: defaults apply only when experiments are on.
func TestExperimentsDefaults(t *testing.T) {
	c, err := prepareConfig(Config{})
	if err != nil || c.Experiments.Cookie != "" || c.Experiments.Header != "" {
		t.Fatalf("off: %+v, %v", c.Experiments, err)
	}
	c, err = prepareConfig(expTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if x := c.Experiments; x.Cookie != "weir_v" || x.Header != "Weir-Variant" || x.MaxActive != 4 {
		t.Fatalf("defaults: %+v", x)
	}
}

// E5, T-35, T-15: load rejects what would leak, spoof or blow up the key space.
func TestExperimentsConfigRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"Key.Cookies names the experiment cookie", func(c *Config) { c.Key.Cookies = []string{"a", "weir_v"} }},
		{"Bypass.Cookies names it", func(c *Config) { c.Bypass.Cookies = []string{"weir_v"} }},
		{"Key.Headers names the variant header", func(c *Config) { c.Key.Headers = []string{"Weir-Variant"} }},
		{"Forward.Allow names it, with an underscore", func(c *Config) { c.Forward.Allow = []string{"weir_variant"} }},
		{"short signing key", func(c *Config) { c.Experiments.SigningKeys = [][]byte{[]byte("short")} }},
		{"unset previous key", func(c *Config) { c.Experiments.SigningKeys = append(c.Experiments.SigningKeys, nil) }},
		{"header with an underscore", func(c *Config) { c.Experiments.Header = "Weir_Variant" }},
		{"header is Cookie", func(c *Config) { c.Experiments.Header = "Cookie" }},
		{"variant product above Key.MaxVariants*4", func(c *Config) {
			c.Key.MaxVariants = 1
			vs := &c.Experiments.Experiments[0].Variants
			*vs = append(*vs, VariantConfig{"c", 1}, VariantConfig{"d", 1}, VariantConfig{"e", 1})
		}},
		{"consent value without a cookie", func(c *Config) { c.Experiments.Consent.Value = "x" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := expTestConfig()
			tt.edit(&c)
			if _, err := prepareConfig(c); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("prepareConfig = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

// E6: no consent gate logs a warning once, at New; a gate does not.
func TestExperimentsConsentWarning(t *testing.T) {
	if e, err := New(Config{Experiments: ExperimentsConfig{SigningKeys: [][]byte{[]byte("0123456789abcdef0123456789abcdef-k")}}}); err != nil {
		t.Fatal(err)
	} else {
		_ = e.Close(t.Context())
	}
	for _, gated := range []bool{false, true} {
		var buf bytes.Buffer
		c := expTestConfig()
		c.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		if gated {
			c.Experiments.Consent = ExperimentConsent{Cookie: "cmp", Value: "a=yes"}
		}
		e, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		_ = e.Close(t.Context())
		if got := strings.Count(buf.String(), "no Consent gate"); got != map[bool]int{false: 1, true: 0}[gated] {
			t.Fatalf("gated=%v: %d warnings in %q", gated, got, buf.String())
		}
	}
}

// NFR-3: New copies the caller's slices.
func TestExperimentsConfigDetached(t *testing.T) {
	in := expTestConfig()
	c, err := prepareConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Experiments.SigningKeys[0][0] = 'X'
	if c.Experiments.SigningKeys[0][0] == 'X' {
		t.Fatal("signing key aliases the caller's slice")
	}
}

// T-35: a configured header spelling is stored canonical, so Vary and
// Header.Get on the forwarded request find it.
func TestExperimentsHeaderCanonical(t *testing.T) {
	in := expTestConfig()
	in.Experiments.Header = "weir-variant"
	c, err := prepareConfig(in)
	if err != nil || c.Experiments.Header != "Weir-Variant" {
		t.Fatalf("header %q, err %v", c.Experiments.Header, err)
	}
}

// T-35: cookie names compare case-insensitively where the key strips them,
// so load rejects a differently-cased Key.Cookies entry too.
func TestExperimentsRejectsCookieAnyCase(t *testing.T) {
	c := expTestConfig()
	c.Key.Cookies = []string{"WEIR_V"}
	if _, err := prepareConfig(c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("prepareConfig = %v", err)
	}
}
