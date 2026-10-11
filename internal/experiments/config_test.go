package experiments

import (
	"errors"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		CookieName:  "weir_v",
		SigningKeys: [][]byte{keyA},
		Header:      "Weir-Variant",
		MaxActive:   2,
		Experiments: []Experiment{
			{Name: "checkout-copy", Salt: "s1", Path: "/checkout*", Variants: []Variant{{"control", 50}, {"b", 50}}},
			{Name: "hero", Salt: "s2", Host: "shop.example", Path: "/", Variants: []Variant{{"control", 1}, {"b", 1}}},
		},
	}
}

// E5, E8: load-time checks. T-27: variant products stay below Key.MaxVariants*4.
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		edit   func(*Config)
		errHas string
	}{
		{"valid", func(*Config) {}, ""},
		{"short signing key", func(c *Config) { c.SigningKeys = [][]byte{[]byte("short")} }, "signing key 0"},
		{"no signing key", func(c *Config) { c.SigningKeys = nil }, "no signing key"},
		{"cookie name with separator", func(c *Config) { c.CookieName = "a;b" }, "cookie name"},
		{"header name empty", func(c *Config) { c.Header = "" }, "header name"},
		{"zero max_active", func(c *Config) { c.MaxActive = 0 }, "max_active"},
		{"duplicate experiment", func(c *Config) { c.Experiments[1].Name = "checkout-copy" }, "twice"},
		{"experiment name with dot", func(c *Config) { c.Experiments[0].Name = "a.b" }, "short token"},
		{"missing salt", func(c *Config) { c.Experiments[0].Salt = "" }, "no salt"},
		{"one variant", func(c *Config) { c.Experiments[0].Variants = c.Experiments[0].Variants[:1] }, "two variants"},
		{"zero weight", func(c *Config) { c.Experiments[0].Variants[1].Weight = 0 }, "weight"},
		{"duplicate variant", func(c *Config) { c.Experiments[0].Variants[1].Name = "control" }, "unique"},
		{"winner not a variant", func(c *Config) { c.Experiments[0].Winner = "zzz" }, "winner"},
		{"winner is a variant", func(c *Config) { c.Experiments[0].Winner = "b" }, ""},
		{"upper case host", func(c *Config) { c.Experiments[1].Host = "Shop.Example" }, "host"},
		{"host with port", func(c *Config) { c.Experiments[1].Host = "shop.example:8080" }, "host"},
		{"relative path", func(c *Config) { c.Experiments[0].Path = "checkout" }, "path"},
		{"star in the middle", func(c *Config) { c.Experiments[0].Path = "/a*b" }, "path"},
		{"three overlapping above max_active", func(c *Config) {
			c.Experiments = append(c.Experiments, Experiment{Name: "third", Salt: "s", Path: "/checkout/pay",
				Variants: []Variant{{"control", 1}, {"b", 1}}})
			c.Experiments = append(c.Experiments, Experiment{Name: "fourth", Salt: "s", Path: "/check*",
				Variants: []Variant{{"control", 1}, {"b", 1}}})
		}, "max_active"},
		{"disjoint matchers are not an overlap", func(c *Config) {
			c.MaxActive = 1
			c.Experiments[1].Host = ""
			c.Experiments[1].Path = "/home"
		}, ""},
		{"overlap on host only across paths is fine", func(c *Config) {
			c.MaxActive = 1
			c.Experiments[0].Host = "a.example"
			c.Experiments[1].Host = "b.example"
		}, ""},
		{"variant product over the limit", func(c *Config) {
			c.MaxActive = 4
			for _, n := range []string{"x", "y", "z"} {
				c.Experiments = append(c.Experiments, Experiment{Name: n, Salt: n, Path: "/*",
					Variants: []Variant{{"control", 1}, {"b", 1}, {"c", 1}}})
			}
		}, "variants"},
		{"a winner contributes no variants", func(c *Config) {
			c.MaxActive = 4
			for _, n := range []string{"x", "y", "z"} {
				c.Experiments = append(c.Experiments, Experiment{Name: n, Salt: n, Path: "/*", Winner: "b",
					Variants: []Variant{{"control", 1}, {"b", 1}, {"c", 1}}})
			}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.edit(&c)
			err := c.Validate(8)
			if tt.errHas == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), tt.errHas) {
				t.Fatalf("got %v, want an ErrConfig mentioning %q", err, tt.errHas)
			}
		})
	}
}

// E8: matching is by path and host only; Active applies config order.
func TestMatchAndActive(t *testing.T) {
	c := validConfig()
	c.MaxActive = 1
	if got := c.Active("shop.example", "/checkout/pay"); len(got) != 1 || got[0].Name != "checkout-copy" {
		t.Errorf("%v", got)
	}
	if got := c.Active("other.example", "/"); len(got) != 0 {
		t.Errorf("host mismatch matched: %v", got)
	}
	c.MaxActive = 2
	if got := c.Active("shop.example", "/"); len(got) != 1 || got[0].Name != "hero" {
		t.Errorf("exact path: %v", got)
	}
	if e, ok := c.Lookup("hero"); !ok || e.Control() != "control" {
		t.Error("Lookup failed")
	}
}
