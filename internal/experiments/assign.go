package experiments

import "strings"

// Consent names the cookie that gates bucketing (E6). A request has consent
// when the cookie Cookie is present and one element of its value equals
// Value. Elements are separated by '&', '|' or ','; the value is not URL
// decoded. The zero value means no gate.
type Consent struct {
	Cookie string
	Value  string // for example "analytics=yes"
}

// Configured reports whether a consent gate is set.
func (c Consent) Configured() bool { return c.Cookie != "" }

func (c Consent) validate(assignmentCookie string) error {
	if c.Cookie == "" && c.Value == "" {
		return nil
	}
	if !isToken(c.Cookie, maxNameLen) || c.Cookie == assignmentCookie {
		return cfgErr("consent cookie %q is not a short token, or is the assignment cookie", c.Cookie)
	}
	if c.Value == "" || len(c.Value) > maxNameLen {
		return cfgErr("consent needs a value of 1 to %d bytes", maxNameLen)
	}
	for i := 0; i < len(c.Value); i++ {
		if b := c.Value[i]; b <= ' ' || b >= 0x7f || strings.IndexByte("&|,;\"\\", b) >= 0 {
			return cfgErr("consent value %q has a byte outside the element set", c.Value)
		}
	}
	return nil
}

// Granted reports whether the Cookie lines carry consent. A missing,
// repeated-with-different-values or non-visible-ASCII cookie is no consent,
// so malformed input falls to the control arm (T-13).
func (c Consent) Granted(lines []string) bool {
	v, ok := CookieValue(lines, c.Cookie)
	if !ok {
		return false
	}
	for len(v) > 0 {
		elem := v
		if i := strings.IndexAny(v, "&|,"); i >= 0 {
			elem, v = v[:i], v[i+1:]
		} else {
			v = ""
		}
		if elem == c.Value {
			return true
		}
	}
	return false
}

// CookieValue returns the value of cookie name in the Cookie lines. It
// reports false when the cookie is absent, repeated with different values,
// or has a byte outside visible ASCII, the same rule the key applies to
// keyed cookies.
func CookieValue(lines []string, name string) (string, bool) {
	var val string
	found := false
	for _, line := range lines {
		for pair := range strings.SplitSeq(line, ";") {
			n, v, ok := strings.Cut(strings.Trim(pair, " \t"), "=")
			if !ok || n != name {
				continue
			}
			for i := 0; i < len(v); i++ {
				if v[i] <= ' ' || v[i] >= 0x7f {
					return "", false
				}
			}
			if found && v != val {
				return "", false
			}
			val, found = v, true
		}
	}
	return val, found
}

// Choice is one experiment's variant for a request.
type Choice struct {
	Experiment, Variant string
}

// Result is the outcome of Assign for one request.
type Result struct {
	Picks   []Choice // one per active experiment, config order
	Cookie  Cookie   // the verified cookie, or a fresh one; updated when Changed
	Changed bool     // an assignment was created or replaced, so the cookie needs a rewrite (P3-03)
}

// Assign runs 10 §4 steps 1 to 3 for a request on the normalized host and
// the validated path. cookieLines are the client's Cookie lines. rnd mints
// the visitor id of a new visitor (Config.Rand). With no matching experiment
// it returns the zero Result without parsing anything, so unmatched
// requests pay for the matcher scan only.
func (c *Config) Assign(host, path string, cookieLines []string, rnd func() float64) Result {
	active := c.Active(host, path)
	if len(active) == 0 {
		return Result{}
	}
	var res Result
	rotated := false
	if v, ok := CookieValue(cookieLines, c.CookieName); ok {
		var primary, ok bool
		res.Cookie, primary, ok = ParseCookieKeyed(v, c.SigningKeys) // T-35: a bad MAC leaves the zero Cookie
		rotated = ok && !primary
	}
	consent := !c.Consent.Configured() || c.Consent.Granted(cookieLines)
	// Signed under a rotated-out key: re-sign with the first key and restart
	// the Max-Age, so removing the old key later does not re-bucket the
	// visitor. Without consent nothing is written (E6).
	if rotated && consent {
		res.Changed = true
	}
	res.Picks = make([]Choice, 0, len(active))
	for _, e := range active {
		res.Picks = append(res.Picks, Choice{e.Name, c.choose(e, &res, consent, rnd)})
	}
	if res.Changed {
		res.Cookie.Prune(func(name string) bool { _, ok := c.Lookup(name); return ok })
	}
	return res
}

func (c *Config) choose(e *Experiment, res *Result, consent bool, rnd func() float64) string {
	if e.Winner != "" {
		return e.Winner // E4: no bucketing, nothing recorded
	}
	if !consent || rnd == nil {
		return e.Control() // E6
	}
	if a, ok := res.Cookie.Get(e.Name); ok && e.hasVariant(a.Variant) {
		return a.Variant // E3
	}
	if res.Cookie.VisitorID == "" {
		res.Cookie.VisitorID = NewVisitorID(rnd)
	}
	v := Pick(e.Salt, res.Cookie.VisitorID, e.Variants)
	if v == "" {
		return e.Control()
	}
	res.Cookie.Set(e.Name, v)
	res.Changed = true
	return v
}

func (e *Experiment) hasVariant(name string) bool {
	for _, v := range e.Variants {
		if v.Name == name {
			return true
		}
	}
	return false
}
