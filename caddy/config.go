package weircaddy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir"
)

// maxNameLen bounds Handler.Name (08 §2).
const maxNameLen = 64

// ByteSize is a size in bytes. JSON accepts a non-negative integer, or a
// string such as "512MiB" (B, KB, MB, GB, TB decimal; KiB, MiB, GiB, TiB
// binary; a bare digit string is bytes).
type ByteSize int64

var byteUnits = []struct {
	suffix string
	mult   float64
}{
	// Longest suffix first so "MiB" is not read as "B".
	{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40},
	{"kb", 1e3}, {"mb", 1e6}, {"gb", 1e9}, {"tb", 1e12},
	{"b", 1},
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *ByteSize) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
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
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || f < 0 {
		return 0, errors.New("weir: invalid byte size")
	}
	f *= mult
	if f >= math.MaxInt64 {
		return 0, errors.New("weir: byte size too large")
	}
	// A bare JSON number must be whole bytes; "1.5MiB" is fine because it
	// resolves to whole bytes after scaling.
	if f != math.Trunc(f) {
		return 0, errors.New("weir: byte size is not a whole number of bytes")
	}
	return int64(f), nil
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
}

// StaleConfig sets the operator defaults for stale serving (D6): off unless
// set.
type StaleConfig struct {
	WhileRevalidate caddy.Duration `json:"while_revalidate,omitempty"`
	IfError         caddy.Duration `json:"if_error,omitempty"`
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
	return weir.Config{
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
		},
		Freshness: weir.FreshnessConfig{
			DefaultStaleWhileRevalidate: time.Duration(h.Stale.WhileRevalidate),
			DefaultStaleIfError:         time.Duration(h.Stale.IfError),
		},
	}
}
