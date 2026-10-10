package valkey

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func validConfig() Config { return Config{Addrs: []string{"127.0.0.1:6379"}} }

// S-2, 05 §7: Validate fills defaults and rejects values that would make
// the key layout unsafe (glob, brace or colon characters in Prefix/HashTag).
func TestConfigValidate(t *testing.T) {
	t.Run("defaults are filled", func(t *testing.T) {
		c, err := validConfig().Validate()
		if err != nil {
			t.Fatal(err)
		}
		if c.Prefix != "weir" || c.HashTag != "e" || c.MaxRetention != 24*time.Hour ||
			c.MaxClockSkew != time.Second || c.MaxHardEpochs != 10000 ||
			c.CallTimeout != 5*time.Second || c.HardEpochWait != 0 ||
			c.Cluster || c.CoLocateEntries || c.SkipPolicyCheck {
			t.Fatalf("defaults wrong: %+v", c)
		}
	})
	t.Run("explicit values are kept", func(t *testing.T) {
		in := validConfig()
		in.Prefix, in.HashTag, in.MaxHardEpochs = "site-1", "x_1", 5
		in.MaxClockSkew, in.CallTimeout = 3*time.Second, time.Second
		c, err := in.Validate()
		if err != nil {
			t.Fatal(err)
		}
		if c.Prefix != "site-1" || c.HashTag != "x_1" || c.MaxHardEpochs != 5 ||
			c.MaxClockSkew != 3*time.Second || c.CallTimeout != time.Second {
			t.Fatalf("values lost: %+v", c)
		}
	})
	t.Run("NoClockSkew means skew zero", func(t *testing.T) {
		in := validConfig()
		in.NoClockSkew = true
		c, err := in.Validate()
		if err != nil {
			t.Fatal(err)
		}
		if c.MaxClockSkew != 0 {
			t.Fatalf("MaxClockSkew = %v, want 0", c.MaxClockSkew)
		}
	})
	t.Run("NoClockSkew with a skew is contradictory", func(t *testing.T) {
		in := validConfig()
		in.NoClockSkew, in.MaxClockSkew = true, time.Second
		if _, err := in.Validate(); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("zero-value Validate does not mutate the receiver", func(t *testing.T) {
		in := validConfig()
		if _, err := in.Validate(); err != nil {
			t.Fatal(err)
		}
		if in.Prefix != "" {
			t.Fatal("receiver was modified")
		}
	})

	bad := []struct {
		name string
		mod  func(*Config)
	}{
		{"no addresses", func(c *Config) { c.Addrs = nil }},
		{"empty address", func(c *Config) { c.Addrs = []string{""} }},
		{"prefix with colon", func(c *Config) { c.Prefix = "a:b" }},
		{"prefix with glob star", func(c *Config) { c.Prefix = "a*" }},
		{"prefix with glob question mark", func(c *Config) { c.Prefix = "a?" }},
		{"prefix with glob bracket", func(c *Config) { c.Prefix = "a[b]" }},
		{"prefix with brace", func(c *Config) { c.Prefix = "a{b}" }},
		{"prefix with backslash", func(c *Config) { c.Prefix = `a\b` }},
		{"prefix with space", func(c *Config) { c.Prefix = "a b" }},
		{"prefix with non-ASCII", func(c *Config) { c.Prefix = "wéir" }},
		{"hash tag with brace", func(c *Config) { c.HashTag = "e}" }},
		{"hash tag with colon", func(c *Config) { c.HashTag = "e:x" }},
		{"hash tag with glob", func(c *Config) { c.HashTag = "*" }},
		{"negative MaxRetention", func(c *Config) { c.MaxRetention = -1 }},
		{"negative MaxClockSkew", func(c *Config) { c.MaxClockSkew = -time.Second }},
		{"negative CallTimeout", func(c *Config) { c.CallTimeout = -1 }},
		{"negative HardEpochWait", func(c *Config) { c.HardEpochWait = -1 }},
		{"HardEpochWait below one millisecond", func(c *Config) { c.HardEpochWait = time.Microsecond }},
		{"negative MaxHardEpochs", func(c *Config) { c.MaxHardEpochs = -1 }},
	}
	for _, tc := range bad {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mod(&c)
			if _, err := c.Validate(); err == nil {
				t.Fatal("want error")
			} else if !strings.HasPrefix(err.Error(), "store: valkey: ") {
				t.Fatalf("error %q lacks the store: valkey: prefix", err)
			}
		})
	}
}

// A password in a log line or panic dump is a leak.
func TestConfigRedacts(t *testing.T) {
	c := validConfig()
	c.Username, c.Password = "svc", "hunter2-secret"
	c.TLS = &tls.Config{ServerName: "cache.internal"}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("cfg", "config", c)
	log.Info("cfgptr", "config", &c)
	outputs := map[string]string{
		"String":     c.String(),
		"GoString":   c.GoString(),
		"%v":         fmt.Sprintf("%v", c),
		"%+v":        fmt.Sprintf("%+v", c),
		"%#v":        fmt.Sprintf("%#v", c),
		"%v pointer": fmt.Sprintf("%v", &c),
		"slog":       buf.String(),
	}
	for name, out := range outputs {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(out, "hunter2-secret") {
				t.Fatalf("password leaked: %s", out)
			}
			if strings.Contains(out, "cache.internal") {
				t.Fatalf("TLS settings leaked: %s", out)
			}
			if !strings.Contains(out, "127.0.0.1:6379") {
				t.Fatalf("addresses should stay visible: %s", out)
			}
		})
	}
}
