package sfv

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrInvalid reports a field that is not a valid List of Strings or that
// exceeds the caller's limits. The caller treats the whole field as unusable
// (FR-STO-10).
var ErrInvalid = errors.New("weir: invalid structured field list")

// ParseStringList parses the field lines of one field as an RFC 9651 §4.2.1
// List whose members are all Strings. Parameters are checked and dropped. A
// member of any other type, a member longer than maxLen bytes after
// unescaping, or more than maxMembers members is an error, and an error
// returns no members: a partial group list would let a purge miss the
// response (FR-STO-10).
//
// Each line is parsed on its own, with the result RFC 9110 §5.3 gives for the
// lines joined by ", " except that a String cannot span two lines (RFC 9651
// §4.2 allows that failure). The work and the result are bounded by the input
// and by maxMembers*maxLen (T-21, T-23). Members without escapes share the
// line's memory.
func ParseStringList(lines []string, maxMembers, maxLen int) ([]string, error) {
	var out []string
	for i, s := range lines {
		// Only SP is skipped ahead of the whole field; a later line follows
		// a comma, where OWS is allowed.
		if i == 0 {
			s = strings.TrimLeft(s, " ")
		} else {
			s = strings.TrimLeft(s, " \t")
		}
		if s == "" {
			if len(lines) == 1 {
				return nil, nil
			}
			return nil, ErrInvalid // joined, this is an empty member
		}
		for {
			if len(out) >= maxMembers || s[0] != '"' {
				return nil, ErrInvalid
			}
			end, esc, ok := scanString(s)
			if !ok || end-2-esc > maxLen {
				return nil, ErrInvalid
			}
			out = append(out, unescape(s[1:end-1], esc))
			if s, ok = skipParams(s[end:]); !ok {
				return nil, ErrInvalid
			}
			if s = strings.TrimLeft(s, " \t"); s == "" {
				break
			}
			if s[0] != ',' {
				return nil, ErrInvalid
			}
			if s = strings.TrimLeft(s[1:], " \t"); s == "" {
				return nil, ErrInvalid // trailing comma
			}
		}
	}
	return out, nil
}

// scanString walks the String at the start of s, which begins with a quote
// (RFC 9651 §4.2.5). It returns the index after the closing quote and the
// number of escapes, so the caller knows the decoded length before it
// allocates.
func scanString(s string) (end, esc int, ok bool) {
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++
			if i == len(s) || s[i] != '"' && s[i] != '\\' {
				return 0, 0, false
			}
			esc++
		case c == '"':
			return i + 1, esc, true
		case c < 0x20 || c >= 0x7f:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// unescape removes the backslashes from the inside of a scanned String.
func unescape(s string, esc int) string {
	if esc == 0 {
		return s
	}
	b := make([]byte, 0, len(s)-esc)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
		}
		b = append(b, s[i])
	}
	return string(b)
}

// skipParams consumes the parameters at the start of s (RFC 9651 §4.2.3.2)
// and returns what follows them. Values are validated, never kept: a field
// with a malformed parameter is not a valid field.
func skipParams(s string) (string, bool) {
	for len(s) > 0 && s[0] == ';' {
		s = strings.TrimLeft(s[1:], " ")
		n := keyLen(s)
		if n == 0 {
			return "", false
		}
		s = s[n:]
		if len(s) > 0 && s[0] == '=' {
			var ok bool
			if s, ok = skipBareItem(s[1:]); !ok {
				return "", false
			}
		}
	}
	return s, true
}

// keyLen returns the length of the key at the start of s, 0 if there is none
// (RFC 9651 §4.2.3.3).
func keyLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c == '*':
		case i > 0 && (isDigit(c) || c == '_' || c == '-' || c == '.'):
		default:
			return i
		}
	}
	return len(s)
}

// skipBareItem consumes one bare item of any type at the start of s (RFC 9651
// §4.2.3.1) and returns what follows it.
func skipBareItem(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	switch c := s[0]; {
	case c == '"':
		end, _, ok := scanString(s)
		return s[end:], ok
	case c == '-' || isDigit(c):
		rest, _, ok := skipNumber(s)
		return rest, ok
	case c == '@':
		rest, integer, ok := skipNumber(s[1:])
		return rest, ok && integer
	case c == '?':
		if len(s) < 2 || s[1] != '0' && s[1] != '1' {
			return "", false
		}
		return s[2:], true
	case c == ':':
		return skipBytes(s)
	case c == '%':
		return skipDisplayString(s)
	case isAlpha(c) || c == '*':
		i := 1
		for i < len(s) && isTokenByte(s[i]) {
			i++
		}
		return s[i:], true
	}
	return "", false
}

// skipNumber consumes an Integer or Decimal (RFC 9651 §4.2.4): at most 15
// digits, or at most 12 digits, a dot and 1 to 3 digits.
func skipNumber(s string) (rest string, integer, ok bool) {
	s = strings.TrimPrefix(s, "-")
	i, dot := 0, -1
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' && dot < 0 {
			if i == 0 || i > 12 {
				return "", false, false
			}
			dot = i
			continue
		}
		if !isDigit(c) {
			break
		}
		if dot < 0 && i >= 15 || i >= 16 {
			return "", false, false
		}
	}
	if dot < 0 {
		return s[i:], true, i > 0
	}
	frac := i - dot - 1
	return s[i:], false, frac >= 1 && frac <= 3
}

// skipBytes consumes a Byte Sequence (RFC 9651 §4.2.7). Only the alphabet is
// checked, so undecodable base64 passes: the RFC lets a parser accept bad
// padding, and the value is dropped either way.
func skipBytes(s string) (string, bool) {
	end := strings.IndexByte(s[1:], ':')
	if end < 0 {
		return "", false
	}
	for _, c := range []byte(s[1 : 1+end]) {
		if !isAlpha(c) && !isDigit(c) && c != '+' && c != '/' && c != '=' {
			return "", false
		}
	}
	return s[end+2:], true
}

// skipDisplayString consumes a Display String (RFC 9651 §4.2.10), which must
// decode to valid UTF-8.
func skipDisplayString(s string) (string, bool) {
	if len(s) < 2 || s[1] != '"' {
		return "", false
	}
	// T-21: the buffer grows with this value only. Sized to the rest of the
	// line, a line of display strings cost its length squared.
	// ponytail: decodes to check UTF-8; validate rune by rune if this ever
	// shows in a profile.
	var buf []byte
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c < 0x20 || c >= 0x7f:
			return "", false
		case c == '"':
			return s[i+1:], utf8.Valid(buf)
		case c == '%':
			if i+2 >= len(s) {
				return "", false
			}
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if !ok1 || !ok2 {
				return "", false
			}
			buf = append(buf, hi<<4|lo)
			i += 2
		default:
			buf = append(buf, c)
		}
	}
	return "", false
}

// unhex returns the value of a lowercase hex digit.
func unhex(c byte) (byte, bool) {
	switch {
	case isDigit(c):
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// isTokenByte reports whether c may follow the first byte of a Token: tchar
// (RFC 9110 §5.6.2), ":" or "/".
func isTokenByte(c byte) bool {
	return isAlpha(c) || isDigit(c) || strings.IndexByte("!#$%&'*+-.^_`|~:/", c) >= 0
}
