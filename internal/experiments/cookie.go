package experiments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// MaxCookieLen caps the whole cookie value, "v1." through the MAC (E9).
const MaxCookieLen = 512

const (
	cookiePrefix  = "v1."
	exposedMark   = '!'
	maxVisitorLen = 64
	macLen        = sha256.Size
)

// Assignment records the variant a visitor was given for one experiment.
// Exposed is set once the exposure event has been emitted (10 §4 step 7).
type Assignment struct {
	Experiment string
	Variant    string
	Exposed    bool
}

// Cookie is the decoded visitor cookie: an opaque visitor id and the
// assignments in creation order (E3, E9).
type Cookie struct {
	VisitorID   string
	Assignments []Assignment
}

// ParseCookie verifies value against every key and decodes it. Any defect
// (shape, length, MAC, grammar, duplicate experiment) yields ok=false, which
// the caller treats as "no assignment" and reassigns (E2, T-35). The MAC is
// checked before the assignments are read.
func ParseCookie(value string, keys [][]byte) (c Cookie, ok bool) {
	if len(value) > MaxCookieLen || !strings.HasPrefix(value, cookiePrefix) {
		return Cookie{}, false
	}
	rest := value[len(cookiePrefix):]
	id, rest, found := strings.Cut(rest, ".")
	if !found || !isToken(id, maxVisitorLen) {
		return Cookie{}, false
	}
	assigns, macStr, found := strings.Cut(rest, ".")
	if !found || strings.Contains(macStr, ".") {
		return Cookie{}, false
	}
	// Decode panics when dst is too short, so the length is checked first.
	var mac [macLen]byte
	if len(macStr) != base64.RawURLEncoding.EncodedLen(macLen) {
		return Cookie{}, false
	}
	if _, err := base64.RawURLEncoding.Decode(mac[:], []byte(macStr)); err != nil {
		return Cookie{}, false
	}
	body := value[:len(value)-len(macStr)-1]
	verified := false
	for _, k := range keys {
		// Do not stop early: the loop's length is the key count either way.
		if hmac.Equal(sign(k, body), mac[:]) {
			verified = true
		}
	}
	if !verified {
		return Cookie{}, false
	}
	as, ok := parseAssignments(assigns)
	if !ok {
		return Cookie{}, false
	}
	return Cookie{VisitorID: id, Assignments: as}, true
}

func parseAssignments(s string) ([]Assignment, bool) {
	if s == "" {
		return nil, true
	}
	var out []Assignment
	for more := true; more; {
		var item string
		item, s, more = strings.Cut(s, "+")
		item, exposed := strings.CutSuffix(item, string(exposedMark))
		exp, variant, found := strings.Cut(item, ":")
		if !found || !isToken(exp, maxNameLen) || !isToken(variant, maxNameLen) {
			return nil, false
		}
		for _, a := range out {
			if a.Experiment == exp {
				return nil, false
			}
		}
		out = append(out, Assignment{exp, variant, exposed})
	}
	return out, true
}

// Get returns the assignment for an experiment.
func (c *Cookie) Get(experiment string) (Assignment, bool) {
	for _, a := range c.Assignments {
		if a.Experiment == experiment {
			return a, true
		}
	}
	return Assignment{}, false
}

// Set records a variant for an experiment. A new experiment is appended, so
// order stays creation order; a changed variant keeps its place and loses
// its exposure mark, because it is a new exposure.
func (c *Cookie) Set(experiment, variant string) {
	for i, a := range c.Assignments {
		if a.Experiment == experiment {
			if a.Variant != variant {
				c.Assignments[i] = Assignment{experiment, variant, false}
			}
			return
		}
	}
	c.Assignments = append(c.Assignments, Assignment{experiment, variant, false})
}

// Prune drops assignments whose experiment is no longer configured (E9) and
// reports whether it dropped any. The caller runs it on every rewrite.
func (c *Cookie) Prune(configured func(experiment string) bool) bool {
	kept := c.Assignments[:0:0]
	for _, a := range c.Assignments {
		if configured(a.Experiment) {
			kept = append(kept, a)
		}
	}
	dropped := len(kept) != len(c.Assignments)
	c.Assignments = kept
	return dropped
}

// Encode signs the cookie with key. If the value would pass MaxCookieLen,
// assignments are dropped from the front, oldest first (E9). The receiver is
// not changed.
func (c Cookie) Encode(key []byte) string {
	as := c.Assignments
	for {
		body := cookiePrefix + c.VisitorID + "." + formatAssignments(as)
		v := body + "." + base64.RawURLEncoding.EncodeToString(sign(key, body))
		if len(v) <= MaxCookieLen || len(as) == 0 {
			return v
		}
		as = as[1:]
	}
}

func formatAssignments(as []Assignment) string {
	var b strings.Builder
	for i, a := range as {
		if i > 0 {
			b.WriteByte('+')
		}
		b.WriteString(a.Experiment)
		b.WriteByte(':')
		b.WriteString(a.Variant)
		if a.Exposed {
			b.WriteByte(exposedMark)
		}
	}
	return b.String()
}

func sign(key []byte, body string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(body))
	return m.Sum(nil)
}

// NewVisitorID builds a fresh visitor id from rnd (Config.Rand, [0,1)), 106
// bits in 22 token characters. The id is not a secret; the MAC is what
// makes the cookie trustworthy. A Rand outside [0,1) is a contract breach
// and yields a constant id, so every such visitor shares one bucket.
func NewVisitorID(rnd func() float64) string {
	var raw [16]byte
	for i := 0; i < 2; i++ {
		f := rnd()
		if !(f >= 0 && f < 1) {
			f = 0
		}
		u := uint64(f * (1 << 53))
		for j := 0; j < 8; j++ {
			raw[i*8+j] = byte(u >> (8 * j))
		}
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
