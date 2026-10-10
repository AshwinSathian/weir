package valkey

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AshwinSathian/weir"
)

// Config configures a Store. The zero value of every field except Addrs
// selects the default noted on it (05 §7).
type Config struct {
	// Addrs lists the server addresses ("host:port"); at least one is
	// required. Standalone mode takes exactly one; in cluster mode they are
	// seed nodes.
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

	// Upper bounds: the prune margin MaxRetention + MaxClockSkew + 1s must
	// not overflow, and every table has a stated bound (P5, NFR-3).
	maxMaxRetention  = 10 * 365 * 24 * time.Hour
	maxMaxClockSkew  = time.Hour
	maxMaxHardEpochs = 1_000_000
	maxKeyPartLen    = 64
)

// cfgErr builds a config error that wraps weir.ErrInvalidConfig, so callers
// treat every Weir config error alike.
func cfgErr(format string, args ...any) error {
	return fmt.Errorf("store: valkey: config: %s: %w", fmt.Sprintf(format, args...), weir.ErrInvalidConfig)
}

// Validate returns a copy of c with defaults filled in, or a zero Config and
// an error wrapping weir.ErrInvalidConfig that names the first bad field. An
// empty Prefix or HashTag means the default, so neither can be set to empty.
// The copy does not alias c.Addrs or c.TLS.
func (c Config) Validate() (Config, error) {
	if err := c.check(); err != nil {
		return Config{}, err
	}
	c.Addrs = slices.Clone(c.Addrs)
	if c.TLS != nil {
		c.TLS = c.TLS.Clone()
	}
	if c.Prefix == "" {
		c.Prefix = defaultPrefix
	}
	if c.HashTag == "" {
		c.HashTag = defaultHashTag
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
	if c.HardEpochWait >= c.CallTimeout && c.HardEpochWait != 0 {
		return Config{}, cfgErr("HardEpochWait must be shorter than CallTimeout")
	}
	return c, nil
}

// check validates the fields as given, before defaults.
func (c Config) check() error {
	if len(c.Addrs) == 0 {
		return cfgErr("no addresses")
	}
	if !c.Cluster && len(c.Addrs) > 1 {
		// valkey-go's single-client mode uses only the first address, so
		// extra ones would be ignored and never policy-checked.
		return cfgErr("standalone mode takes one address, got %d (set Cluster for several seed nodes)", len(c.Addrs))
	}
	for i, a := range c.Addrs {
		if err := addrOK(a); err != nil {
			return err
		}
		if slices.Contains(c.Addrs[:i], a) {
			return cfgErr("duplicate address %q", a)
		}
	}
	for _, p := range []struct{ name, v string }{{"Prefix", c.Prefix}, {"HashTag", c.HashTag}} {
		if p.v != "" && !keyPartOK(p.v) {
			return cfgErr("%s %q must be 1-%d bytes of A-Z a-z 0-9 _ . -", p.name, p.v, maxKeyPartLen)
		}
	}
	for _, d := range []struct {
		name     string
		v, limit time.Duration
	}{
		{"MaxRetention", c.MaxRetention, maxMaxRetention},
		{"MaxClockSkew", c.MaxClockSkew, maxMaxClockSkew},
		{"CallTimeout", c.CallTimeout, 0},
		{"HardEpochWait", c.HardEpochWait, 0},
	} {
		if d.v < 0 {
			return cfgErr("%s is negative", d.name)
		}
		if d.limit != 0 && d.v > d.limit {
			return cfgErr("%s exceeds %v", d.name, d.limit)
		}
	}
	if c.HardEpochWait%time.Millisecond != 0 {
		// WAIT takes whole milliseconds and 0 means block forever.
		return cfgErr("HardEpochWait must be a whole number of milliseconds")
	}
	if c.MaxHardEpochs < 0 || c.MaxHardEpochs > maxMaxHardEpochs {
		return cfgErr("MaxHardEpochs must be 0-%d", maxMaxHardEpochs)
	}
	if c.NoClockSkew && c.MaxClockSkew != 0 {
		return cfgErr("NoClockSkew conflicts with MaxClockSkew")
	}
	return nil
}

// addrOK accepts "host:port" with a port in 1-65535 and no whitespace.
func addrOK(a string) error {
	host, port, err := net.SplitHostPort(a)
	if err != nil || host == "" || strings.ContainsAny(a, " \t\r\n") {
		return cfgErr("address %q must be host:port", a)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return cfgErr("address %q has a bad port", a)
	}
	return nil
}

// keyPartOK reports whether s is non-empty and uses only characters that
// are neither a glob, a brace nor a colon, so a Prefix cannot widen the
// Scrub pattern or break the hash tag (05 §7).
func keyPartOK(s string) bool {
	if s == "" || len(s) > maxKeyPartLen {
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

// MarshalJSON implements json.Marshaler so encoding a Config never writes
// the password (or fails on the TLS config).
func (c Config) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }
