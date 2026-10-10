package weircaddy

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir/store/valkey"
)

// StoreConfig selects and configures a shared store in place of the default
// memory store (08 §2). Only "valkey" exists. The fields are the approved
// valkey.Config fields (05 §7); a zero value means that field's default.
type StoreConfig struct {
	// Type must be "valkey".
	Type string `json:"type,omitempty"`
	// Addrs are "host:port" addresses: exactly one for a standalone server,
	// seed nodes in cluster mode. A URL is refused (see validate).
	Addrs []string `json:"addrs,omitempty"`
	// Username and Password authenticate to the server. In a Caddyfile the
	// password comes from {$VAR}; String, GoString and LogValue hide both.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// TLS turns TLS on with default settings: system roots, the server name
	// taken from the address, TLS 1.2 or later.
	TLS             bool           `json:"tls,omitempty"`
	Cluster         bool           `json:"cluster,omitempty"`
	Prefix          string         `json:"prefix,omitempty"`
	HashTag         string         `json:"hash_tag,omitempty"`
	CoLocateEntries bool           `json:"co_locate_entries,omitempty"`
	MaxRetention    caddy.Duration `json:"max_retention,omitempty"`
	MaxClockSkew    caddy.Duration `json:"max_clock_skew,omitempty"`
	NoClockSkew     bool           `json:"no_clock_skew,omitempty"`
	MaxHardEpochs   int            `json:"max_hard_epochs,omitempty"`
	CallTimeout     caddy.Duration `json:"call_timeout,omitempty"`
	HardEpochWait   caddy.Duration `json:"hard_epoch_wait,omitempty"`
	SkipPolicyCheck bool           `json:"skip_policy_check,omitempty"`
}

// valkeyConfig maps the block onto valkey.Config. Username and password may
// hold {env.VAR}, resolved here, so the stored config can carry the
// placeholder instead of the secret. An unset prefix becomes the site name:
// the library default would put every site on a server into one keyspace, and
// a purge on one would flush the others.
func (s *StoreConfig) valkeyConfig(name string) valkey.Config {
	repl := caddy.NewReplacer()
	prefix := s.Prefix
	if prefix == "" {
		prefix = name
	}
	c := valkey.Config{
		Addrs: s.Addrs, Username: repl.ReplaceAll(s.Username, ""), Password: repl.ReplaceAll(s.Password, ""), Cluster: s.Cluster,
		Prefix: prefix, HashTag: s.HashTag, CoLocateEntries: s.CoLocateEntries,
		MaxRetention: time.Duration(s.MaxRetention), MaxClockSkew: time.Duration(s.MaxClockSkew),
		NoClockSkew: s.NoClockSkew, MaxHardEpochs: s.MaxHardEpochs, CallTimeout: time.Duration(s.CallTimeout),
		HardEpochWait: time.Duration(s.HardEpochWait), SkipPolicyCheck: s.SkipPolicyCheck,
	}
	if s.TLS {
		c.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return c
}

// validate checks the block. It never prints the password: the only place a
// secret could land in a value the store package echoes is an address, so an
// address with credentials in it (a URL or user:pass@host) is refused here
// without being quoted.
func (s *StoreConfig) validate(name string) error {
	if s.Type != "valkey" {
		return fmt.Errorf("weir: unknown store type %q (only \"valkey\")", s.Type)
	}
	// valkey.Config.Validate quotes a bad address, and an operator can paste a
	// secret into addrs (user:pass, a URL, a mis-nested line). Check the shape
	// here and name the index only.
	for i, a := range s.Addrs {
		host, port, err := net.SplitHostPort(a)
		n, perr := strconv.Atoi(port)
		if err != nil || host == "" || perr != nil || n < 1 || n > 65535 || strings.ContainsAny(a, "@/ \t\r\n") {
			return fmt.Errorf("weir: store addrs[%d] must be host:port (credentials go in username and password)", i)
		}
	}
	_, err := s.valkeyConfig(name).Validate()
	return err
}

// digestFor identifies the effective settings for the pool key and the
// key-generation hash. It hashes the config after defaults are filled in, so an
// explicit default and an unset field are one store, and it covers the
// resolved secrets (a changed or rotated credential is a different store). It
// is a hash, so a pool key printed in a log or test failure holds no secret.
// Every field is length-prefixed, so values cannot run together.
func (s *StoreConfig) digestFor(name string) (out [sha256.Size]byte) {
	if s == nil {
		return out
	}
	vc := s.valkeyConfig(name)
	if n, err := vc.Validate(); err == nil {
		vc = n
	}
	h := sha256.New()
	h.Write([]byte("weir/caddy/store/valkey/v2\x00"))
	str := func(v string) {
		var n [binary.MaxVarintLen64]byte
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(v)))])
		h.Write([]byte(v))
	}
	num := func(v int64) { str(fmt.Sprint(v)) }
	flag := func(v bool) {
		if v {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	}
	num(int64(len(vc.Addrs)))
	for _, a := range vc.Addrs {
		str(a) // order matters: the first address is the standalone one
	}
	str(vc.Username)
	str(vc.Password)
	str(vc.Prefix)
	str(vc.HashTag)
	flag(vc.TLS != nil)
	flag(vc.Cluster)
	flag(vc.CoLocateEntries)
	flag(vc.NoClockSkew)
	flag(vc.SkipPolicyCheck)
	num(int64(vc.MaxRetention))
	num(int64(vc.MaxClockSkew))
	num(int64(vc.MaxHardEpochs))
	num(int64(vc.CallTimeout))
	num(int64(vc.HardEpochWait))
	h.Sum(out[:0])
	return out
}

// storeWarnings lists settings that are accepted but only half apply.
func (h *Handler) storeWarnings() []string {
	if h.Store != nil && h.MultiHost {
		return []string{"weir: multi_host with a store block turns on the per-host limiter cap only; the per-owner store byte cap is a memory-store feature and does not apply to Valkey (docs/08 §4b)"}
	}
	return nil
}

// String describes the block without its credentials.
func (s *StoreConfig) String() string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("store{type:%s addrs:%v credentials:%t}", s.Type, s.Addrs, s.Username != "" || s.Password != "")
}

// GoString implements fmt.GoStringer so %#v hides the credentials.
func (s *StoreConfig) GoString() string { return s.String() }

// LogValue implements slog.LogValuer.
func (s *StoreConfig) LogValue() slog.Value { return slog.StringValue(s.String()) }
