// Package experiments holds the parts of A/B experiment bucketing that need
// no engine: the configuration and its load-time checks, the signed visitor
// cookie, and the deterministic variant pick (docs/10-experiments-spec.md
// §2 to §4, threat T-35). It imports only the standard library.
package experiments

import (
	"errors"
	"fmt"
	"strings"
)

// Limits that keep the cookie and the key space bounded (NFR-3).
const (
	maxNameLen      = 32
	maxWeight       = 1000
	maxTotalWeight  = 10000
	minSigningKeyLn = 16
)

// ErrConfig wraps every configuration error so callers can test for it.
var ErrConfig = errors.New("weir: invalid experiments config")

// Variant is one arm of an experiment. Weight is relative to the other arms.
type Variant struct {
	Name   string
	Weight int
}

// Experiment is one static experiment (E5). Variants[0] is the control arm,
// served when consent is missing (E6).
type Experiment struct {
	Name     string
	Salt     string
	Winner   string // a variant name ends the experiment; "" keeps it running
	Host     string // normalized, lower case; "" matches any host
	Path     string // exact path, or a prefix when it ends in "*"; "" matches any
	Variants []Variant
}

// Config is the validated shape of the experiments block (10 §3).
type Config struct {
	CookieName  string
	SigningKeys [][]byte // the first signs, all verify (rotation)
	Header      string
	MaxActive   int
	Experiments []Experiment
}

// Control returns the name of the control variant.
func (e *Experiment) Control() string {
	if len(e.Variants) == 0 {
		return ""
	}
	return e.Variants[0].Name
}

// Matches reports whether the experiment applies to a request. host must be
// the normalized host the key uses and path the validated path, so matching
// adds no unkeyed input (E8, P2).
func (e *Experiment) Matches(host, path string) bool {
	if e.Host != "" && e.Host != host {
		return false
	}
	if stem, prefix := strings.CutSuffix(e.Path, "*"); prefix {
		return strings.HasPrefix(path, stem)
	}
	return e.Path == "" || e.Path == path
}

// Active returns the experiments that apply to a request. When more than
// MaxActive match (Validate rejects statically overlapping matchers, so this
// is a safety net), the first MaxActive in config order apply (10 §4 step 1).
func (c *Config) Active(host, path string) []*Experiment {
	var out []*Experiment
	for i := range c.Experiments {
		e := &c.Experiments[i]
		if e.Matches(host, path) {
			out = append(out, e)
			if len(out) >= c.MaxActive {
				break
			}
		}
	}
	return out
}

// Lookup returns the experiment with the given name.
func (c *Config) Lookup(name string) (*Experiment, bool) {
	for i := range c.Experiments {
		if c.Experiments[i].Name == name {
			return &c.Experiments[i], true
		}
	}
	return nil, false
}

// Validate checks the configuration at load time. keyMaxVariants is the
// resolved Key.MaxVariants; no request may combine variants into more than
// keyMaxVariants*4 key variants (10 §4 step 1, T-27). It fills in no
// defaults: the caller that exposes the block decides them.
func (c *Config) Validate(keyMaxVariants int) error {
	if !isToken(c.CookieName, maxNameLen) {
		return cfgErr("cookie name %q is not a short token", c.CookieName)
	}
	if !isToken(c.Header, maxNameLen) {
		return cfgErr("header name %q is not a short token", c.Header)
	}
	if len(c.SigningKeys) == 0 {
		return cfgErr("no signing key")
	}
	for i, k := range c.SigningKeys {
		if len(k) < minSigningKeyLn {
			return cfgErr("signing key %d is shorter than %d bytes", i, minSigningKeyLn)
		}
	}
	if c.MaxActive < 1 {
		return cfgErr("max_active must be at least 1")
	}
	seen := make(map[string]bool, len(c.Experiments))
	for i := range c.Experiments {
		e := &c.Experiments[i]
		if seen[e.Name] {
			return cfgErr("experiment %q is defined twice", e.Name)
		}
		seen[e.Name] = true
		if err := e.validate(); err != nil {
			return err
		}
	}
	return c.validateOverlap(max(keyMaxVariants, 1) * 4)
}

func (e *Experiment) validate() error {
	if !isToken(e.Name, maxNameLen) {
		return cfgErr("experiment name %q is not a short token", e.Name)
	}
	if e.Salt == "" {
		return cfgErr("experiment %q has no salt", e.Name)
	}
	if err := e.validateMatcher(); err != nil {
		return err
	}
	if len(e.Variants) < 2 {
		return cfgErr("experiment %q needs at least two variants", e.Name)
	}
	total := 0
	names := make(map[string]bool, len(e.Variants))
	for _, v := range e.Variants {
		if !isToken(v.Name, maxNameLen) || names[v.Name] {
			return cfgErr("experiment %q: variant %q is not a unique short token", e.Name, v.Name)
		}
		names[v.Name] = true
		if v.Weight < 1 || v.Weight > maxWeight {
			return cfgErr("experiment %q: weight of %q must be 1 to %d", e.Name, v.Name, maxWeight)
		}
		total += v.Weight
	}
	if total > maxTotalWeight {
		return cfgErr("experiment %q: weights add up to more than %d", e.Name, maxTotalWeight)
	}
	if e.Winner != "" && !names[e.Winner] {
		return cfgErr("experiment %q: winner %q is not a variant", e.Name, e.Winner)
	}
	return nil
}

func (e *Experiment) validateMatcher() error {
	for i := 0; i < len(e.Host); i++ {
		b := e.Host[i]
		// The key's normalized host keeps a non-default port and a bracketed
		// IPv6 literal, so the matcher must be able to name them (E8).
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == ':' || b == '[' || b == ']') {
			return cfgErr("experiment %q: host %q must be the lower case normalized host, without wildcard", e.Name, e.Host)
		}
	}
	if e.Path == "" {
		return nil
	}
	if e.Path[0] != '/' || strings.Contains(strings.TrimSuffix(e.Path, "*"), "*") {
		return cfgErr("experiment %q: path %q must start with / and may end in one *", e.Name, e.Path)
	}
	return nil
}

// validateOverlap looks for a request that matches too many experiments, or
// whose matching experiments multiply into too many key variants. Path
// matchers are exact strings or prefixes, so any set of pairwise overlapping
// matchers shares a request made of the longest path stem and the one
// non-empty host; trying every host and stem is therefore exact, not a
// sample. The experiment count is small and static, so the cube is cheap.
func (c *Config) validateOverlap(maxProduct int) error {
	var hosts, stems []string
	for i := range c.Experiments {
		e := &c.Experiments[i]
		if e.Host != "" {
			hosts = append(hosts, e.Host)
		}
		stems = append(stems, strings.TrimSuffix(e.Path, "*"))
	}
	if len(hosts) == 0 {
		hosts = []string{""}
	}
	for _, h := range hosts {
		for _, p := range stems {
			n, product := 0, 1
			for i := range c.Experiments {
				e := &c.Experiments[i]
				if !e.Matches(h, p) {
					continue
				}
				n++
				if e.Winner == "" {
					product *= len(e.Variants)
				}
				if product > maxProduct {
					return cfgErr("requests for %q%s could combine into more than %d variants", h, p, maxProduct)
				}
			}
			if n > c.MaxActive {
				return cfgErr("%d experiments can apply to one request (host %q, path %q), max_active is %d", n, h, p, c.MaxActive)
			}
		}
	}
	return nil
}

func cfgErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...))
}

// isToken reports whether s is 1 to max bytes of [A-Za-z0-9_-]. These bytes
// are cookie-safe and cannot collide with the cookie's separators.
func isToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}
