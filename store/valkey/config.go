package valkey

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Config configures a Store. The zero value of every field except Addrs
// selects the default noted on it (05 §7).
type Config struct {
	// Addrs lists the server addresses ("host:port"); at least one is
	// required. In cluster mode they are seed nodes.
	Addrs []string
	// Username and Password authenticate to the server. Both are hidden by
	// String, GoString and LogValue.
	Username string
	Password string
	// TLS, when non-nil, makes connections use TLS. It is hidden by String,
	// GoString and LogValue.
	TLS *tls.Config
	// Cluster selects cluster mode instead of a standalone server (false).
	// The client's auto-detection by error text is not relied on.
	Cluster bool
	// Prefix starts every key ("weir"); two deployments on one server need
	// distinct prefixes. Characters: A-Z a-z 0-9 _ . -
	Prefix string
	// HashTag is the cluster hash tag shared by all epoch keys ("e"), the
	// text inside {}. Same character set as Prefix.
	HashTag string
	// CoLocateEntries puts entry keys in the epoch slot too (false). The
	// whole cache then lives on one primary.
	CoLocateEntries bool
	// MaxRetention is how long a hard epoch is kept (24h, E-6).
	MaxRetention time.Duration
	// MaxClockSkew is the tolerated difference between node and server
	// clocks (1s). Use NoClockSkew for zero.
	MaxClockSkew time.Duration
	// NoClockSkew sets the skew to 0 for single-clock deployments. It
	// cannot be combined with a non-zero MaxClockSkew.
	NoClockSkew bool
	// MaxHardEpochs caps distinct tags with a hard epoch (10000, E-6).
	MaxHardEpochs int
	// CallTimeout bounds a call whose context has no deadline (5s, S-2).
	CallTimeout time.Duration
	// HardEpochWait, when non-zero, issues WAIT 1 <ms> after a hard epoch
	// write (0, off).
	HardEpochWait time.Duration
	// SkipPolicyCheck skips the maxmemory-policy check on connect, for
	// managed services that disable CONFIG (false). The operator then owns
	// the guarantee that epoch keys are never evicted.
	SkipPolicyCheck bool
}

const (
	defaultPrefix        = "weir"
	defaultHashTag       = "e"
	defaultMaxRetention  = 24 * time.Hour
	defaultMaxClockSkew  = time.Second
	defaultMaxHardEpochs = 10000
	defaultCallTimeout   = 5 * time.Second
)

// Validate returns a copy of c with defaults filled in, or an error that
// names the first bad field. An empty Prefix or HashTag means the default,
// so neither can be set to empty.
func (c Config) Validate() (Config, error) {
	if len(c.Addrs) == 0 {
		return c, errors.New("store: valkey: config: no addresses")
	}
	for _, a := range c.Addrs {
		if a == "" {
			return c, errors.New("store: valkey: config: empty address")
		}
	}
	if c.Prefix == "" {
		c.Prefix = defaultPrefix
	}
	if c.HashTag == "" {
		c.HashTag = defaultHashTag
	}
	if !keyPartOK(c.Prefix) {
		return c, fmt.Errorf("store: valkey: config: Prefix %q must use only A-Z a-z 0-9 _ . -", c.Prefix)
	}
	if !keyPartOK(c.HashTag) {
		return c, fmt.Errorf("store: valkey: config: HashTag %q must use only A-Z a-z 0-9 _ . -", c.HashTag)
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"MaxRetention", c.MaxRetention},
		{"MaxClockSkew", c.MaxClockSkew},
		{"CallTimeout", c.CallTimeout},
		{"HardEpochWait", c.HardEpochWait},
	} {
		if d.v < 0 {
			return c, fmt.Errorf("store: valkey: config: %s is negative", d.name)
		}
	}
	if c.HardEpochWait > 0 && c.HardEpochWait < time.Millisecond {
		// WAIT takes whole milliseconds and 0 means block forever.
		return c, errors.New("store: valkey: config: HardEpochWait is below 1ms")
	}
	if c.MaxHardEpochs < 0 {
		return c, errors.New("store: valkey: config: MaxHardEpochs is negative")
	}
	if c.NoClockSkew && c.MaxClockSkew != 0 {
		return c, errors.New("store: valkey: config: NoClockSkew conflicts with MaxClockSkew")
	}
	if c.MaxRetention == 0 {
		c.MaxRetention = defaultMaxRetention
	}
	if c.NoClockSkew {
		c.MaxClockSkew = 0
	} else if c.MaxClockSkew == 0 {
		c.MaxClockSkew = defaultMaxClockSkew
	}
	if c.MaxHardEpochs == 0 {
		c.MaxHardEpochs = defaultMaxHardEpochs
	}
	if c.CallTimeout == 0 {
		c.CallTimeout = defaultCallTimeout
	}
	return c, nil
}

// keyPartOK reports whether s is non-empty and uses only characters that
// are neither a glob, a brace nor a colon, so a Prefix cannot widen the
// Scrub pattern or break the hash tag (05 §7).
func keyPartOK(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9',
			b == '_', b == '.', b == '-':
		default:
			return false
		}
	}
	return true
}

// String describes c without its credentials or TLS settings.
func (c Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "valkey.Config{Addrs:%v", c.Addrs)
	if c.Username != "" {
		b.WriteString(" Username:<redacted>")
	}
	if c.Password != "" {
		b.WriteString(" Password:<redacted>")
	}
	if c.TLS != nil {
		b.WriteString(" TLS:<redacted>")
	}
	fmt.Fprintf(&b, " Cluster:%t Prefix:%q HashTag:%q CoLocateEntries:%t", c.Cluster, c.Prefix, c.HashTag, c.CoLocateEntries)
	fmt.Fprintf(&b, " MaxRetention:%v MaxClockSkew:%v NoClockSkew:%t MaxHardEpochs:%d", c.MaxRetention, c.MaxClockSkew, c.NoClockSkew, c.MaxHardEpochs)
	fmt.Fprintf(&b, " CallTimeout:%v HardEpochWait:%v SkipPolicyCheck:%t}", c.CallTimeout, c.HardEpochWait, c.SkipPolicyCheck)
	return b.String()
}

// GoString implements fmt.GoStringer so %#v does not print credentials.
func (c Config) GoString() string { return c.String() }

// LogValue implements slog.LogValuer so structured logs never carry the
// password or TLS settings.
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }
