package httpcc

import (
	"iter"
	"net/http"
	"strings"
)

// maxDelta is the delta-seconds ceiling from RFC 9111 §1.2.2.
const maxDelta = 2147483648

// Seconds is a parsed delta-seconds argument. Set reports that the directive
// appeared; Invalid reports a missing, signed or non-digit argument, which
// makes the lifetime zero (FR-FRS-2). V is 0 when Invalid.
type Seconds struct {
	V       int64
	Set     bool
	Invalid bool
}

// ResponseDirectives holds the response Cache-Control directives Weir acts on
// (RFC 9111 §5.2.2). Qualified no-cache and private count as unqualified.
type ResponseDirectives struct {
	MaxAge, SMaxAge, SWR, SIE       Seconds
	NoStore, NoCache, Private       bool
	Public, MustRevalidate          bool
	ProxyRevalidate, MustUnderstand bool
	// Duplicates reports a delta-seconds directive (max-age, s-maxage,
	// stale-while-revalidate, stale-if-error) repeated with different values,
	// which makes the lifetime zero (FR-FRS-2).
	Duplicates bool
	// Malformed reports a field whose meaning had to be guessed: an unclosed
	// quoted string, or an argument on public or must-revalidate. The
	// guesses only restrict; storing a response to an Authorization request
	// needs a field without them (FR-STO-5, T-8).
	Malformed bool
}

// Unusable reports an invalid or conflicting delta-seconds directive, which
// makes the lifetime zero (FR-FRS-2, RFC 9111 §4.2.1).
func (d *ResponseDirectives) Unusable() bool {
	return d.Duplicates || d.MaxAge.Invalid || d.SMaxAge.Invalid || d.SWR.Invalid || d.SIE.Invalid
}

// RequestDirectives holds the request cache directives (RFC 9111 §5.2.1).
// NoCache is also set by Pragma: no-cache without Cache-Control. A max-stale without argument
// accepts any staleness and parses as the delta-seconds ceiling.
type RequestDirectives struct {
	NoStore, NoCache, OnlyIfCached bool
	MaxAge, MinFresh, MaxStale     Seconds
}

// ParseResponse parses every Cache-Control line of h. It never fails:
// unknown directives are ignored (RFC 9111 §5.2.3) and malformed arguments
// are recorded as Invalid.
func ParseResponse(h http.Header) ResponseDirectives {
	var d ResponseDirectives
	publicArg := false
	for dv := range directives(h["Cache-Control"]) {
		name := dv.name
		var dup bool
		d.Malformed = d.Malformed || dv.loose
		switch {
		case equalFold(name, "max-age"):
			dup = d.MaxAge.add(dv)
		case equalFold(name, "s-maxage"):
			dup = d.SMaxAge.add(dv)
		case equalFold(name, "stale-while-revalidate"):
			dup = d.SWR.add(dv)
		case equalFold(name, "stale-if-error"):
			dup = d.SIE.add(dv)
		case equalFold(name, "no-store"):
			d.NoStore = true
		case equalFold(name, "no-cache"):
			d.NoCache = true
		case equalFold(name, "private"):
			d.Private = true
		case equalFold(name, "public"):
			// T-8: public only widens what may be stored (FR-STO-5), so
			// "public=no" and a public read out of an unclosed quote are
			// not it. Restricting directives keep any form.
			publicArg = publicArg || dv.hasArg
			d.Public = d.Public || !dv.hasArg && !dv.loose
		case equalFold(name, "must-revalidate"):
			d.MustRevalidate = true
			d.Malformed = d.Malformed || dv.hasArg
		case equalFold(name, "proxy-revalidate"):
			d.ProxyRevalidate = true
		case equalFold(name, "must-understand"):
			d.MustUnderstand = true
		}
		d.Duplicates = d.Duplicates || dup
	}
	if publicArg { // "public, public=no" conflicts and fails closed
		d.Public, d.Malformed = false, true
	}
	return d
}

// ParseRequest parses the Cache-Control lines of h, or its Pragma lines when
// it has no Cache-Control (RFC 9111 §5.4, FR-SRV-8). Repeated
// delta-seconds directives keep the first value; request directives are
// advisory (D5).
func ParseRequest(h http.Header) RequestDirectives {
	var d RequestDirectives
	for dv := range directives(h["Cache-Control"]) {
		name := dv.name
		switch {
		case equalFold(name, "no-store"):
			d.NoStore = true
		case equalFold(name, "no-cache"):
			d.NoCache = true
		case equalFold(name, "only-if-cached"):
			d.OnlyIfCached = true
		case equalFold(name, "max-age"):
			d.MaxAge.add(dv)
		case equalFold(name, "min-fresh"):
			d.MinFresh.add(dv)
		case equalFold(name, "max-stale"):
			if !dv.hasArg && !d.MaxStale.Set {
				d.MaxStale = Seconds{V: maxDelta, Set: true}
			} else {
				d.MaxStale.add(dv)
			}
		}
	}
	if len(h["Cache-Control"]) > 0 {
		return d
	}
	for dv := range directives(h["Pragma"]) {
		if !dv.hasArg && equalFold(dv.name, "no-cache") {
			d.NoCache = true
		}
	}
	return d
}

// add records one occurrence of a delta-seconds directive and reports
// whether it conflicts with an earlier one. The first occurrence wins.
func (s *Seconds) add(dv directive) (conflict bool) {
	v, ok := parseDelta(dv)
	next := Seconds{V: v, Set: true, Invalid: !ok}
	if s.Set {
		return *s != next
	}
	*s = next
	return false
}

// parseDelta parses a delta-seconds argument in token or quoted-string form.
// It rejects a missing argument, signs and non-digits, and clamps above
// maxDelta (RFC 9111 §1.2.2) without overflowing.
func parseDelta(dv directive) (int64, bool) {
	if !dv.hasArg || dv.spaced {
		return 0, false
	}
	a := dv.arg
	if len(a) > 0 && a[0] == '"' {
		if len(a) < 2 || a[len(a)-1] != '"' {
			return 0, false
		}
		// Escapes and inner quotes are non-digits, so they fail below. Not
		// unescaping quoted-pairs is deliberate: Invalid means lifetime zero.
		a = a[1 : len(a)-1]
	}
	if a == "" {
		return 0, false
	}
	var v int64
	for i := 0; i < len(a); i++ {
		c := a[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		if v < maxDelta {
			v = v*10 + int64(c-'0')
		}
	}
	return min(v, maxDelta), true
}

// directive is one Cache-Control or Pragma element.
type directive struct {
	name, arg string
	hasArg    bool // an '=' was present
	spaced    bool // whitespace surrounded the '=', which RFC 9111 §5.2 does not allow
	loose     bool // read from the rescan of a line with an unclosed quote
}

// directives yields each comma-separated element of lines, splitting only on
// commas outside quoted strings. A quoted string opens only as the first byte
// of an argument, so a stray '"' elsewhere is an ordinary byte. When a quote is
// still open at the end of a line, the line is rescanned from that quote with
// every comma splitting: a malformed element must never hide a later private
// or no-store (FR-STO-4). Empty elements are skipped; names and arguments are
// trimmed of optional whitespace.
func directives(lines []string) iter.Seq[directive] {
	return func(yield func(directive) bool) {
		for _, line := range lines {
			start, open := 0, -1 // open is the index of an unclosed '"'
			eq, argStart, rescan := false, false, false
			for i := 0; ; i++ {
				if i >= len(line) {
					if open >= 0 {
						i, open, rescan = open, -1, true
						continue
					}
					if !emit(line[start:], rescan, yield) {
						return
					}
					break
				}
				c := line[i]
				switch {
				case open >= 0:
					switch c {
					case '\\':
						i++
					case '"':
						open = -1
					}
				case c == ',':
					if !emit(line[start:i], rescan, yield) {
						return
					}
					start, eq, argStart = i+1, false, false
				case argStart && (c == ' ' || c == '\t'):
				case argStart:
					argStart = false
					if c == '"' && !rescan {
						open = i
					}
				case c == '=' && !eq:
					eq, argStart = true, true
				}
			}
		}
	}
}

func emit(elem string, loose bool, yield func(directive) bool) bool {
	name, arg, hasArg := strings.Cut(elem, "=")
	// FR-FRS-2: "max-age =3600" is not delta-seconds, so it is read as invalid
	// rather than trimmed into a lifetime. Whitespace between elements is
	// still OWS and was trimmed by the split.
	spaced := hasArg && (strings.TrimRight(name, " \t") != name || strings.TrimLeft(arg, " \t") != arg)
	name = trimOWS(name)
	if name == "" {
		return true
	}
	return yield(directive{name: name, arg: trimOWS(arg), hasArg: hasArg, spaced: spaced, loose: loose})
}

func trimOWS(s string) string { return strings.Trim(s, " \t") }

// equalFold compares s with the lowercase ASCII directive name want, folding
// ASCII letters only. strings.EqualFold folds Unicode too, so "ſ-maxage"
// (long s) would match "s-maxage": a name no RFC 9111 cache downstream
// recognizes, which would make Weir's lifetime diverge from theirs.
func equalFold(s, want string) bool {
	if len(s) != len(want) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != want[i] {
			return false
		}
	}
	return true
}
