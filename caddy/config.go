package weircaddy

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir"
)

// maxNameLen bounds Handler.Name (08 §2).
const maxNameLen = 64

// ByteSize is a size in bytes. JSON accepts a non-negative integer, or a
// string such as "512MiB" (B, KB, MB, GB, TB, PB decimal; KiB, MiB, GiB, TiB, PiB
// binary; a bare digit string is bytes). The cap is 1 PiB.
type ByteSize int64

var byteUnits = []struct {
	suffix string
	mult   float64
}{
	// Longest suffix first so "MiB" is not read as "B".
	{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40}, {"pib", 1 << 50},
	{"kb", 1e3}, {"mb", 1e6}, {"gb", 1e9}, {"tb", 1e12}, {"pb", 1e15},
	{"b", 1},
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *ByteSize) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		return nil // JSON convention: null leaves the field alone
	}
	s := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	n, err := parseByteSize(s)
	if err != nil {
		return err
	}
	*b = ByteSize(n)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (b ByteSize) MarshalJSON() ([]byte, error) { return strconv.AppendInt(nil, int64(b), 10), nil }

func parseByteSize(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1.0
	for _, u := range byteUnits {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = strings.TrimSpace(num), u.mult
			break
		}
	}
	// Digits and at most the decimal point: no sign, exponent, hex or
	// underscores. Whole numbers go through ParseInt so values above 2^53
	// stay exact; only fractions and scaled values use floats.
	if s == "" || strings.Trim(s, "0123456789.") != "" {
		return 0, errors.New("weir: invalid byte size")
	}
	if mult == 1 {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return checkMaxBytes(n)
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || f < 0 {
		return 0, errors.New("weir: invalid byte size")
	}
	f *= mult
	if f > maxByteSize {
		return 0, errors.New("weir: byte size is above 1 PiB")
	}
	// A bare JSON number must be whole bytes; "1.5MiB" is fine because it
	// resolves to whole bytes after scaling.
	if f != math.Trunc(f) {
		return 0, errors.New("weir: byte size is not a whole number of bytes")
	}
	return checkMaxBytes(int64(f))
}

// maxByteSize caps ByteSize at 1 PiB so later budget sums and shard math
// (P2-02, P2-04) cannot overflow int64 (NFR-3).
const maxByteSize = 1 << 50

func checkMaxBytes(n int64) (int64, error) {
	if n > maxByteSize {
		return 0, errors.New("weir: byte size is above 1 PiB")
	}
	return n, nil
}

// KeyConfig mirrors weir.KeyConfig (FR-KEY).
type KeyConfig struct {
	QueryDrop      []string `json:"query_drop,omitempty"`
	QueryKeep      []string `json:"query_keep,omitempty"`
	QuerySort      bool     `json:"query_sort,omitempty"`
	NormalizePath  bool     `json:"normalize_path,omitempty"`
	Headers        []string `json:"headers,omitempty"`
	Cookies        []string `json:"cookies,omitempty"`
	AcceptEncoding []string `json:"accept_encoding,omitempty"`
}

// ForwardConfig mirrors weir.ForwardConfig (FR-FWD). Forward mode stays
// strict: ForwardAll has no JSON form (D4).
type ForwardConfig struct {
	Allow []string `json:"allow,omitempty"`
}

// BypassConfig mirrors weir.BypassConfig.
type BypassConfig struct {
	Cookies []string `json:"cookies,omitempty"`
	Headers []string `json:"headers,omitempty"`
}

// LimiterConfig mirrors weir.LimiterConfig (FR-LIM).
type LimiterConfig struct {
	MaxConcurrent   int            `json:"max_concurrent,omitempty"`
	MaxQueue        int            `json:"max_queue,omitempty"`
	MaxQueueWait    caddy.Duration `json:"max_queue_wait,omitempty"`
	MaxPerPartition int            `json:"max_per_partition,omitempty"`
	// MaxPerClient caps one client's requests at the origin (FR-FAIR-4). The
	// client is Caddy's client_ip, which honors trusted_proxies. 0: off.
	MaxPerClient int `json:"max_per_client,omitempty"`
}

// StaleConfig sets the operator defaults for stale serving (D6): off unless
// set.
type StaleConfig struct {
	WhileRevalidate caddy.Duration `json:"while_revalidate,omitempty"`
	IfError         caddy.Duration `json:"if_error,omitempty"`
}

// ExperimentsConfig mirrors weir.ExperimentsConfig (docs/10 §3). Signing
// keys are strings; a whole {env.NAME} is resolved when the engine config is
// built, so the stored config never holds the secret, and no other placeholder
// or brace is accepted. An unset variable fails the load (T-35).
type ExperimentsConfig struct {
	Cookie                  string             `json:"cookie,omitempty"`
	SigningKeys             []string           `json:"signing_keys,omitempty"`
	Consent                 *ConsentConfig     `json:"consent,omitempty"`
	Header                  string             `json:"header,omitempty"`
	MaxActive               int                `json:"max_active,omitempty"`
	DownstreamKeysOnVariant bool               `json:"downstream_keys_on_variant,omitempty"`
	Experiments             []ExperimentConfig `json:"experiments,omitempty"`
}

// ConsentConfig mirrors weir.ExperimentConsent (E6).
type ConsentConfig struct {
	Cookie string `json:"cookie"`
	Value  string `json:"value"`
}

// ExperimentConfig mirrors weir.ExperimentConfig. The first variant is the
// control arm.
type ExperimentConfig struct {
	Name     string           `json:"name"`
	Match    *ExperimentMatch `json:"match,omitempty"`
	Salt     string           `json:"salt,omitempty"`
	Variants []VariantConfig  `json:"variants,omitempty"`
	Winner   string           `json:"winner,omitempty"`
}

// ExperimentMatch is the host and path matcher of one experiment (docs/10 E8).
type ExperimentMatch struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
}

// VariantConfig is one arm of an experiment.
type VariantConfig struct {
	Name   string `json:"name"`
	Weight int    `json:"weight"`
}

// envPlaceholder reports the variable name when k is exactly {env.NAME}. Only
// that form is expanded: a general replacer would also read files, the clock
// or the host name into a key, which changes across reloads or nodes and
// silently re-buckets visitors, and it would shorten a literal key that holds
// a brace pair.
func envPlaceholder(k string) (name string, ok bool) {
	name, ok = strings.CutPrefix(k, "{env.")
	if !ok {
		return "", false
	}
	name, ok = strings.CutSuffix(name, "}")
	if !ok || name == "" {
		return "", false
	}
	for i := range len(name) {
		c := name[i]
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return "", false
		}
	}
	return name, true
}

// validate checks what the engine cannot see through the placeholders: it
// refuses a block that enables nothing, any brace in a signing key other than
// a whole {env.NAME}, and an unset or empty variable (named, which is not a
// secret). Messages never carry a key value.
func (x *ExperimentsConfig) validate() error {
	if x == nil {
		return nil
	}
	if len(x.SigningKeys) == 0 && len(x.Experiments) == 0 {
		return errors.New("weir: experiments block needs signing_keys and at least one experiment")
	}
	for i, k := range x.SigningKeys {
		if name, ok := envPlaceholder(k); ok {
			if os.Getenv(name) == "" {
				return fmt.Errorf("weir: experiments signing key %d: environment variable %s is not set", i, name)
			}
		} else if strings.ContainsAny(k, "{}") {
			return fmt.Errorf("weir: experiments signing key %d: only a whole {env.NAME} placeholder is expanded", i)
		}
	}
	return nil
}

// weirExperiments maps the adapter block onto weir.ExperimentsConfig. It
// resolves {env.VAR} in the signing keys; no error or log line carries a key.
func (x *ExperimentsConfig) weirExperiments() weir.ExperimentsConfig {
	if x == nil {
		return weir.ExperimentsConfig{}
	}
	out := weir.ExperimentsConfig{
		Cookie: x.Cookie, Header: x.Header, MaxActive: x.MaxActive,
		DownstreamKeysOnVariant: x.DownstreamKeysOnVariant,
	}
	for _, k := range x.SigningKeys {
		if name, ok := envPlaceholder(k); ok {
			k = os.Getenv(name) // unset gives an empty key, which the engine refuses (T-35)
		}
		out.SigningKeys = append(out.SigningKeys, []byte(k))
	}
	if x.Consent != nil {
		out.Consent = weir.ExperimentConsent{Cookie: x.Consent.Cookie, Value: x.Consent.Value}
	}
	for _, e := range x.Experiments {
		ec := weir.ExperimentConfig{Name: e.Name, Salt: e.Salt, Winner: e.Winner}
		if e.Match != nil {
			ec.Host, ec.Path = e.Match.Host, e.Match.Path
		}
		for _, v := range e.Variants {
			ec.Variants = append(ec.Variants, weir.VariantConfig{Name: v.Name, Weight: v.Weight})
		}
		out.Experiments = append(out.Experiments, ec)
	}
	return out
}

// decodeStrict decodes like Caddy does for module config: unknown keys are
// an error, so a misspelled setting never silently keeps its default.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// validateName enforces the charset of 08 §2. The name reaches the admin
// URL, a metrics label and the snapshot file name, so nothing else is
// accepted.
func validateName(name string) error {
	if name == "" {
		return errors.New("weir: name is required")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("weir: name is longer than %d bytes", maxNameLen)
	}
	// A leading dot would make "." and ".." admin path segments that clients
	// and the mux collapse, and hides the snapshot file.
	if name[0] == '.' {
		return errors.New("weir: name must not start with a dot")
	}
	for i := range len(name) {
		c := name[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("weir: name has a character outside [A-Za-z0-9._-] at byte %d", i)
		}
	}
	return nil
}

// weirConfig maps the adapter settings onto weir.Config. Rand, Observer,
// Logger and Store have no JSON form; the caller sets them.
func (h *Handler) weirConfig() weir.Config {
	cfg := weir.Config{
		Key: weir.KeyConfig{
			QueryDrop: h.Key.QueryDrop, QueryKeep: h.Key.QueryKeep, QuerySort: h.Key.QuerySort,
			NormalizePath: h.Key.NormalizePath, Headers: h.Key.Headers, Cookies: h.Key.Cookies,
			AcceptEncoding: h.Key.AcceptEncoding,
		},
		Forward: weir.ForwardConfig{Allow: h.Forward.Allow},
		Bypass:  weir.BypassConfig{Cookies: h.Bypass.Cookies, Headers: h.Bypass.Headers},
		Limiter: weir.LimiterConfig{
			MaxConcurrent: h.Limiter.MaxConcurrent, MaxQueue: h.Limiter.MaxQueue,
			MaxQueueWait:    time.Duration(h.Limiter.MaxQueueWait),
			MaxPerPartition: h.Limiter.MaxPerPartition,
			MaxPerClient:    h.Limiter.MaxPerClient,
		},
		Freshness: weir.FreshnessConfig{
			DefaultStaleWhileRevalidate: time.Duration(h.Stale.WhileRevalidate),
			DefaultStaleIfError:         time.Duration(h.Stale.IfError),
		},
	}
	cfg.Experiments = h.Experiments.weirExperiments()
	if h.MultiHost {
		// FR-FAIR-3: 25% of MaxConcurrent, at least 1.
		cfg.Limiter.MaxPerHost = max(1, cmp.Or(h.Limiter.MaxConcurrent, defaultMaxConcurrent)/4)
	}
	return cfg
}

// defaultMaxConcurrent mirrors weir.LimiterConfig.MaxConcurrent's default
// (0: 64), which the fairness cap is a share of.
const defaultMaxConcurrent = 64
