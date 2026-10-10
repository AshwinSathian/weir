package weircaddy

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
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

// valkeyConfig maps the block onto valkey.Config.
func (s *StoreConfig) valkeyConfig() valkey.Config {
	c := valkey.Config{
		Addrs: s.Addrs, Username: s.Username, Password: s.Password, Cluster: s.Cluster,
		Prefix: s.Prefix, HashTag: s.HashTag, CoLocateEntries: s.CoLocateEntries,
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
func (s *StoreConfig) validate() error {
	if s.Type != "valkey" {
		return fmt.Errorf("weir: unknown store type %q (only \"valkey\")", s.Type)
	}
	for _, a := range s.Addrs {
		if strings.Contains(a, "@") || strings.Contains(a, "://") {
			return errors.New("weir: store addrs take host:port only; put credentials in username and password")
		}
	}
	_, err := s.valkeyConfig().Validate()
	return err
}

// digest identifies the settings for the pool key and the key-generation
// hash. It covers the password (a changed credential is a different store)
// but is a hash, so a pool key printed in a log or test failure holds no
// secret. Every field is length-prefixed, so values cannot run together.
func (s *StoreConfig) digest() (out [sha256.Size]byte) {
	if s == nil {
		return out
	}
	h := sha256.New()
	h.Write([]byte("weir/caddy/store/valkey/v1\x00"))
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
	num(int64(len(s.Addrs)))
	for _, a := range s.Addrs {
		str(a) // order matters: the first address is the standalone one
	}
	str(s.Username)
	str(s.Password)
	str(s.Prefix)
	str(s.HashTag)
	flag(s.TLS)
	flag(s.Cluster)
	flag(s.CoLocateEntries)
	flag(s.NoClockSkew)
	flag(s.SkipPolicyCheck)
	num(int64(s.MaxRetention))
	num(int64(s.MaxClockSkew))
	num(int64(s.MaxHardEpochs))
	num(int64(s.CallTimeout))
	num(int64(s.HardEpochWait))
	h.Sum(out[:0])
	return out
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
