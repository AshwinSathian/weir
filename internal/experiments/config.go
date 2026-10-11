// Package experiments holds the parts of A/B experiment bucketing that need
// no engine: the configuration and its load-time checks, the signed visitor
// cookie, and the deterministic variant pick (docs/10-experiments-spec.md
// §2 to §4, threat T-35). It imports only the standard library.
package experiments

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strings"
)

// Limits that keep the cookie and the key space bounded (NFR-3).
const (
	maxNameLen        = 32
	maxWeight         = 1000
	maxTotalWeight    = 10000
	minSigningKeyLn   = 32 // HMAC-SHA-256 block-size practice; an unset env key must fail the load
	maxSaltLen        = 64
	maxActiveLimit    = 8  // bounds the forwarded header and the key dimensions (NFR-3)
	maxExperiments    = 64 // bounds validateOverlap and the config size
	maxVariantsPerExp = 16
)

// reservedHeaders are names the forwarded variant header must not take: the
// forwarding step deletes and sets it, so one of these would remove the
// request's own cookie, framing or credentials (T-35).
var reservedHeaders = map[string]bool{
	"cookie": true, "set-cookie": true, "host": true, "content-length": true, "content-type": true,
	"transfer-encoding": true, "connection": true, "te": true, "upgrade": true, "keep-alive": true,
	"trailer": true, "vary": true, "authorization": true, "accept": true, "accept-encoding": true,
	"user-agent": true, "forwarded": true, "via": true, "cache-control": true,
}

// ErrConfig wraps every configuration error so callers can test for it.
var ErrConfig = errors.New("weir: invalid experiments config")

// Variant is one arm of an experiment. Weight is relative to the other arms.
type Variant struct {
	Name   string
	Weight int
}

// Experiment is one static experiment (E5). The first listed variant is the
// control arm, served when consent is missing (E6, 10 §3). A prefix path
// ("/checkout*") also matches "/checkoutx"; end it with "/*" to stop at a
// segment boundary.
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
	if c.MaxActive < 1 {
		return nil
	}
	var out []*Experiment
	for i := range c.Experiments {
		e := &c.Experiments[i]
		if e.Matches(host, path) {
			if out == nil {
				out = make([]*Experiment, 0, min(c.MaxActive, len(c.Experiments)))
			}
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
// keyMaxVariants*4 key variants (10 §4 step 1, T-15, FR-KEY-10). It fills in no
// defaults: the caller that exposes the block decides them.
func (c *Config) Validate(keyMaxVariants int) error {
	if !isToken(c.CookieName, maxNameLen) {
		return cfgErr("cookie name %q is not a short token", c.CookieName)
	}
	if !isToken(c.Header, maxNameLen) || strings.Contains(c.Header, "_") ||
		reservedHeaders[strings.ToLower(c.Header)] || strings.HasPrefix(strings.ToLower(c.Header), "proxy-") {
		// An underscore is out: CGI-style origins map "Weir_Variant" and
		// "Weir-Variant" to one variable, so a client could spoof around the
		// delete step (T-35).
		return cfgErr("header name %q is reserved, has an underscore, or is not a short token", c.Header)
	}
	if len(c.SigningKeys) == 0 {
		return cfgErr("no signing key")
	}
	for i, k := range c.SigningKeys {
		if len(k) < minSigningKeyLn {
			return cfgErr("signing key %d is shorter than %d bytes", i, minSigningKeyLn)
		}
	}
	if c.MaxActive < 1 || c.MaxActive > maxActiveLimit {
		return cfgErr("max_active must be 1 to %d", maxActiveLimit)
	}
	if len(c.Experiments) > maxExperiments {
		return cfgErr("more than %d experiments", maxExperiments)
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
	limit := math.MaxInt
	if k := max(keyMaxVariants, 1); k <= math.MaxInt/4 {
		limit = k * 4
	}
	return c.validateOverlap(limit)
}

func (e *Experiment) validate() error {
	if !isToken(e.Name, maxNameLen) {
		return cfgErr("experiment name %q is not a short token", e.Name)
	}
	if e.Salt == "" || len(e.Salt) > maxSaltLen {
		return cfgErr("experiment %q: salt must be 1 to %d printable ASCII bytes", e.Name, maxSaltLen)
	}
	for i := 0; i < len(e.Salt); i++ {
		if e.Salt[i] <= ' ' || e.Salt[i] > '~' { // no NUL: it separates salt and id in Pick
			return cfgErr("experiment %q: salt must be printable ASCII without spaces", e.Name)
		}
	}
	if err := e.validateMatcher(); err != nil {
		return err
	}
	if len(e.Variants) < 2 || len(e.Variants) > maxVariantsPerExp {
		return cfgErr("experiment %q needs 2 to %d variants", e.Name, maxVariantsPerExp)
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

// validateMatcher accepts only spellings the key can produce, so a matcher
// never silently fails to match: hosts as keys.normalizeHost writes them
// (lower case, no trailing dot, compressed IPv6, no leading-zero port) and
// paths in the form keys.normalizePath leaves (escapes upper case and not
// decodable to an unreserved byte).
func (e *Experiment) validateMatcher() error {
	if e.Host != "" && !normalizedHost(e.Host) {
		return cfgErr("experiment %q: host %q is not in normalized form (lower case, no trailing dot, no wildcard)", e.Name, e.Host)
	}
	if e.Path == "" {
		return nil
	}
	stem, _ := strings.CutSuffix(e.Path, "*")
	if e.Path[0] != '/' || strings.Contains(stem, "*") || !normalizedPath(stem) {
		return cfgErr("experiment %q: path %q must start with /, be in normalized form and may end in one *", e.Name, e.Path)
	}
	return nil
}

func normalizedHost(h string) bool {
	if len(h) > 255 {
		return false
	}
	name, port := h, ""
	if h[0] == '[' {
		end := strings.IndexByte(h, ']')
		if end < 0 {
			return false
		}
		name, port = h[:end+1], h[end+1:]
		a, err := netip.ParseAddr(name[1 : len(name)-1])
		if err != nil || !a.Is6() || name != "["+a.String()+"]" {
			return false
		}
		if port != "" && port[0] != ':' {
			return false
		}
	} else {
		if i := strings.LastIndexByte(h, ':'); i >= 0 {
			name, port = h[:i], h[i:]
		}
		if name == "" || strings.HasSuffix(name, ".") {
			return false
		}
		for i := 0; i < len(name); i++ {
			b := name[i]
			if ok := b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '.'; !ok {
				return false
			}
		}
	}
	if port == "" {
		return true
	}
	digits := port[1:]
	if digits == "" || len(digits) > 5 || digits[0] == '0' {
		return false
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
		n = n*10 + int(digits[i]-'0')
	}
	return n <= 65535
}

func normalizedPath(p string) bool {
	for i := 0; i < len(p); i++ {
		b := p[i]
		if b <= ' ' || b > '~' || b == '?' || b == '#' {
			return false
		}
		if b != '%' {
			continue
		}
		if i+2 >= len(p) {
			return false
		}
		hi, okHi := hexVal(p[i+1])
		lo, okLo := hexVal(p[i+2])
		if !okHi || !okLo {
			return false
		}
		if c := hi<<4 | lo; unreservedByte(c) {
			return false // the key decodes it
		}
		i += 2
	}
	return true
}

// hexVal returns the value of an upper-case hex digit (lower case is not
// normalized form).
func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

func unreservedByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

// validateOverlap looks for a request that matches too many experiments, or
// whose matching experiments multiply into too many key variants. Path
// matchers are exact strings or prefixes, so any set of pairwise overlapping
// matchers shares a request made of the longest path stem and the one
// non-empty host; trying every host and stem is therefore exact, not a
// sample. The experiment count is small and static, so the cube is cheap.
func (c *Config) validateOverlap(maxProduct int) error {
	hosts := make([]string, 0, len(c.Experiments))
	stems := make([]string, 0, len(c.Experiments))
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
				// A winner still contributes its dimension to the key and the
				// header (10 §4 step 4) but only one value.
				if e.Winner == "" {
					if product > maxProduct/len(e.Variants) {
						product = maxProduct + 1
					} else {
						product *= len(e.Variants)
					}
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

// isToken reports whether s is 1 to limit bytes of [A-Za-z0-9_-]. These bytes
// are cookie-safe and cannot collide with the cookie's separators.
func isToken(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if ok := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'; !ok {
			return false
		}
	}
	return true
}
