package sfv

import (
	"strconv"
	"strings"
)

// Kind is the type of a Dictionary member value.
type Kind uint8

// Member value types. Other covers values Weir never reads: Byte Sequences,
// Dates, Display Strings and Inner Lists. They are validated, not kept.
const (
	Boolean Kind = iota
	Integer
	Decimal
	String
	Token
	Other
)

// Item is one Dictionary member value with its parameters dropped. Only the
// field matching Kind is set; Str holds both Strings and Tokens.
type Item struct {
	Kind Kind
	Bool bool
	Int  int64
	Dec  float64
	Str  string
}

// Dict maps member keys to values. A repeated key keeps its last value
// (RFC 9651 §4.2.2); order is not kept because no caller needs it.
type Dict map[string]Item

// ParseDictionary parses the field lines of one field as an RFC 9651 §4.2.2
// Dictionary (FR-TCC-1). Lines are joined with ", " as RFC 9110 §5.3 says, so
// a String cannot span two lines. Parameters are checked and dropped. Any
// error, or more than maxMembers members (repeats included), is ErrInvalid
// and returns no members: RFC 9213 §2.1 ignores a field that does not parse,
// and a half-read field could grant a lifetime the origin never meant (T-34).
// The work is linear in the input and the result is bounded by maxMembers
// (T-21, NFR-3).
func ParseDictionary(lines []string, maxMembers int) (Dict, error) {
	d := Dict{}
	n := 0
	for _, s := range lines {
		// Leading and trailing SP are discarded (RFC 9651 §4.2).
		if s = strings.Trim(s, " "); s == "" {
			if len(lines) == 1 {
				return d, nil
			}
			return nil, ErrInvalid // joined, this is an empty member
		}
		if !parseLine(d, s, maxMembers, &n) {
			return nil, ErrInvalid
		}
	}
	return d, nil
}

// parseLine adds the members of one non-empty line to d. The limit counts
// member in n, repeats included, so it bounds the work as well as d.
func parseLine(d Dict, s string, maxMembers int, n *int) bool {
	for {
		if *n >= maxMembers {
			return false
		}
		*n++
		k := keyLen(s)
		if k == 0 {
			return false
		}
		key := s[:k]
		s = s[k:]
		item, ok := Item{Kind: Boolean, Bool: true}, true
		if s != "" && s[0] == '=' {
			item, s, ok = parseValue(s[1:])
		} else {
			s, ok = skipParams(s)
		}
		if !ok {
			return false
		}
		d[key] = item
		if s = strings.TrimLeft(s, " \t"); s == "" {
			return true
		}
		if s[0] != ',' {
			return false
		}
		if s = strings.TrimLeft(s[1:], " \t"); s == "" {
			return false // trailing comma, or an empty member on the next line
		}
	}
}

// parseValue parses a member value: an Item or an Inner List, then its
// parameters.
func parseValue(s string) (Item, string, bool) {
	var item Item
	var ok bool
	if s != "" && s[0] == '(' {
		item = Item{Kind: Other}
		s, ok = skipInnerList(s)
	} else {
		item, s, ok = parseBareItem(s)
	}
	if !ok {
		return Item{}, "", false
	}
	s, ok = skipParams(s)
	return item, s, ok
}

// skipInnerList consumes an Inner List (RFC 9651 §4.2.1.2), whose items are
// separated by one or more spaces and may carry parameters, and its own
// parameters.
func skipInnerList(s string) (string, bool) {
	s = s[1:]
	for {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			return "", false
		}
		if s[0] == ')' {
			return skipParams(s[1:])
		}
		var ok bool
		if s, ok = skipBareItem(s); !ok {
			return "", false
		}
		if s, ok = skipParams(s); !ok {
			return "", false
		}
		if s == "" || s[0] != ' ' && s[0] != ')' {
			return "", false
		}
	}
}

// parseBareItem parses one bare item (RFC 9651 §4.2.3.1) and returns what
// follows it.
func parseBareItem(s string) (Item, string, bool) {
	if s == "" {
		return Item{}, "", false
	}
	switch c := s[0]; {
	case c == '"':
		end, esc, ok := scanString(s)
		if !ok {
			return Item{}, "", false
		}
		return Item{Kind: String, Str: unescape(s[1:end-1], esc)}, s[end:], true
	case c == '-' || isDigit(c):
		rest, integer, ok := skipNumber(s)
		if !ok {
			return Item{}, "", false
		}
		text := s[:len(s)-len(rest)]
		if integer {
			v, err := strconv.ParseInt(text, 10, 64)
			return Item{Kind: Integer, Int: v}, rest, err == nil
		}
		v, err := strconv.ParseFloat(text, 64)
		return Item{Kind: Decimal, Dec: v}, rest, err == nil
	case c == '?':
		if len(s) < 2 || s[1] != '0' && s[1] != '1' {
			return Item{}, "", false
		}
		return Item{Kind: Boolean, Bool: s[1] == '1'}, s[2:], true
	case isAlpha(c) || c == '*':
		rest, ok := skipBareItem(s)
		return Item{Kind: Token, Str: s[:len(s)-len(rest)]}, rest, ok
	}
	rest, ok := skipBareItem(s)
	return Item{Kind: Other}, rest, ok
}
