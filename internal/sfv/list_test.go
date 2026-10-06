package sfv

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseStringList(t *testing.T) {
	// FR-STO-10, FR-INV-2; T-21, T-23
	long := strings.Repeat("a", 128)
	tests := []struct {
		name  string
		lines []string
		want  []string
		bad   bool
	}{
		{name: "no field lines is an empty list"},
		{name: "empty value is an empty list", lines: []string{""}},
		{name: "spaces only is an empty list", lines: []string{"   "}},
		{name: "RFC 9875 example", lines: []string{`"scripts", "styles"`}, want: []string{"scripts", "styles"}},
		{name: "RFC 9651 string example", lines: []string{`"hello world"`}, want: []string{"hello world"}},
		{name: "RFC 9651 list split over two lines", lines: []string{`"sugar", "tea"`, `"rum"`}, want: []string{"sugar", "tea", "rum"}},
		{name: "single member", lines: []string{`"a"`}, want: []string{"a"}},
		{name: "empty string member", lines: []string{`""`}, want: []string{""}},
		{name: "no space after comma", lines: []string{`"a","b"`}, want: []string{"a", "b"}},
		{name: "tabs and spaces around comma", lines: []string{"\"a\" \t,\t \"b\""}, want: []string{"a", "b"}},
		{name: "leading and trailing spaces", lines: []string{`  "a"  `}, want: []string{"a"}},
		{name: "escaped quote and backslash", lines: []string{`"a\"b\\c"`}, want: []string{`a"b\c`}},
		{name: "comma and semicolon inside a string", lines: []string{`"a, b;c", "d"`}, want: []string{"a, b;c", "d"}},
		{name: "duplicates are kept", lines: []string{`"a", "a"`}, want: []string{"a", "a"}},
		{name: "two lines append", lines: []string{`"a", "b"`, `"c"`}, want: []string{"a", "b", "c"}},
		{name: "second line may start with a tab", lines: []string{`"a"`, "\t\"b\""}, want: []string{"a", "b"}},

		{name: "boolean true parameter ignored", lines: []string{`"a";x`}, want: []string{"a"}},
		{name: "parameters of every type ignored", lines: []string{`"a";i=-12;d=1.5;s="q, \"";t=*tok/en:x;b=?0;y=:aGk=:;at=@1659578233;ds=%"f%c3%bc"; z, "b"`}, want: []string{"a", "b"}},
		{name: "parameter integer of 15 digits", lines: []string{`"a";x=123456789012345`}, want: []string{"a"}},
		{name: "parameter decimal of 12 and 3 digits", lines: []string{`"a";x=-123456789012.123`}, want: []string{"a"}},
		{name: "parameter byte sequences empty, unpadded, part padded and padded", lines: []string{`"a";w=::;x=:aGk:;y=:YQ=:;z=:YQ==:`}, want: []string{"a"}},
		{name: "parameter key with digits and punctuation", lines: []string{`"a";*k_1-2.3=1`}, want: []string{"a"}},

		{name: "token member", lines: []string{`a`}, bad: true},
		{name: "integer member", lines: []string{`"a", 1`}, bad: true},
		{name: "boolean member", lines: []string{`?1`}, bad: true},
		{name: "byte sequence member", lines: []string{`:aGk=:`}, bad: true},
		{name: "inner list member", lines: []string{`("a" "b")`}, bad: true},
		{name: "display string member", lines: []string{`%"a"`}, bad: true},

		{name: "unterminated string", lines: []string{`"a`}, bad: true},
		{name: "string ends on a backslash", lines: []string{`"a\`}, bad: true},
		{name: "invalid escape", lines: []string{`"a\n"`}, bad: true},
		{name: "control byte in a string", lines: []string{"\"a\x00\""}, bad: true},
		{name: "tab in a string", lines: []string{"\"a\tb\""}, bad: true},
		{name: "DEL in a string", lines: []string{"\"a\x7f\""}, bad: true},
		{name: "non-ASCII in a string", lines: []string{"\"caf\xc3\xa9\""}, bad: true},
		{name: "trailing comma", lines: []string{`"a",`}, bad: true},
		{name: "leading comma", lines: []string{`, "a"`}, bad: true},
		{name: "empty member", lines: []string{`"a", , "b"`}, bad: true},
		{name: "missing comma", lines: []string{`"a" "b"`}, bad: true},
		{name: "leading tab on the first line", lines: []string{"\t\"a\""}, bad: true},
		{name: "string split across lines", lines: []string{`"a`, `b"`}, bad: true},
		{name: "empty line among others", lines: []string{`"a"`, ``}, bad: true},
		{name: "empty first line", lines: []string{``, `"a"`}, bad: true},

		{name: "parameter without a key", lines: []string{`"a";`}, bad: true},
		{name: "uppercase parameter key", lines: []string{`"a";X=1`}, bad: true},
		{name: "parameter with no value after =", lines: []string{`"a";x=`}, bad: true},
		{name: "space before a parameter", lines: []string{`"a" ;x`}, bad: true},
		{name: "parameter integer of 16 digits", lines: []string{`"a";x=1234567890123456`}, bad: true},
		{name: "parameter decimal with 4 fraction digits", lines: []string{`"a";x=1.2345`}, bad: true},
		{name: "parameter decimal ending in a dot", lines: []string{`"a";x=1.`}, bad: true},
		{name: "parameter decimal with 13 integer digits", lines: []string{`"a";x=1234567890123.1`}, bad: true},
		{name: "parameter lone minus", lines: []string{`"a";x=-`}, bad: true},
		{name: "parameter boolean other than 0 or 1", lines: []string{`"a";x=?2`}, bad: true},
		{name: "parameter unterminated byte sequence", lines: []string{`"a";x=:aGk=`}, bad: true},
		{name: "parameter byte sequence outside base64", lines: []string{`"a";x=:a!:`}, bad: true},
		{name: "parameter byte sequence with padding inside", lines: []string{`"a";x=:a=Gk:`}, bad: true},
		{name: "parameter byte sequence starting with padding", lines: []string{`"a";x=:=a=:`}, bad: true},
		{name: "parameter byte sequence with too much padding", lines: []string{`"a";x=:aGk==:`}, bad: true},
		{name: "parameter byte sequence padded on a full group", lines: []string{`"a";x=:YWJj=:`}, bad: true},
		{name: "parameter byte sequence of one leftover character", lines: []string{`"a";x=:YWJjZ:`}, bad: true},
		{name: "parameter date with a fraction", lines: []string{`"a";x=@1.5`}, bad: true},
		{name: "parameter display string with uppercase hex", lines: []string{`"a";x=%"%C3%BC"`}, bad: true},
		{name: "parameter display string with invalid UTF-8", lines: []string{`"a";x=%"%c3"`}, bad: true},
		{name: "parameter display string unterminated", lines: []string{`"a";x=%"a`}, bad: true},
		{name: "parameter inner list", lines: []string{`"a";x=(1 2)`}, bad: true},

		{name: "member at the length limit", lines: []string{`"` + long + `"`}, want: []string{long}},
		{name: "member one byte over the length limit", lines: []string{`"` + long + `a"`}, bad: true},
		{name: "length counts decoded bytes", lines: []string{`"` + long[:127] + `\""`}, want: []string{long[:127] + `"`}},
		{name: "members at the count limit", lines: []string{strings.Repeat(`"a", `, 31) + `"a"`}, want: slices.Repeat([]string{"a"}, 32)},
		{name: "one member over the count limit", lines: []string{strings.Repeat(`"a", `, 32) + `"a"`}, bad: true},
		{name: "count limit spans lines", lines: slices.Repeat([]string{`"a"`}, 33), bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStringList(tt.lines, 32, 128)
			if tt.bad {
				if !errors.Is(err, ErrInvalid) || got != nil {
					t.Fatalf("got %q, %v; want nil, ErrInvalid", got, err)
				}
				return
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestParseStringListZeroLimits(t *testing.T) {
	// FR-STO-10: a limit of zero admits nothing rather than everything.
	if got, err := ParseStringList([]string{`"a"`}, 0, 128); !errors.Is(err, ErrInvalid) || got != nil {
		t.Fatalf("maxMembers 0: got %q, %v", got, err)
	}
	if got, err := ParseStringList([]string{`"a"`}, 32, 0); !errors.Is(err, ErrInvalid) || got != nil {
		t.Fatalf("maxLen 0: got %q, %v", got, err)
	}
	if got, err := ParseStringList([]string{`""`}, 32, 0); err != nil || len(got) != 1 {
		t.Fatalf("empty member with maxLen 0: got %q, %v", got, err)
	}
}

func TestParseStringListParameterCostLinear(t *testing.T) {
	// T-21: one member with 64 KiB of display-string parameters once cost
	// 385 MB, a buffer of the remaining line per parameter.
	line := `"a"` + strings.Repeat(`;a=%"b"`, 64<<10/7)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := ParseStringList([]string{line}, 32, 128)
	runtime.ReadMemStats(&after)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %q, %v", got, err)
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Fatalf("allocated %d bytes for a %d byte line", n, len(line))
	}
}

// FuzzSFStringList checks that no input panics, that a successful parse stays
// inside the limits, that parsing line by line agrees with parsing the joined
// field, and that the members survive serialization.
func FuzzSFStringList(f *testing.F) {
	// FR-STO-10; NFR-2, T-21, T-23
	f.Fuzz(func(t *testing.T, in string, maxMembers, maxLen uint8) {
		lines := strings.Split(in, "\n")
		got, err := ParseStringList(lines, int(maxMembers), int(maxLen))
		if err != nil {
			if !errors.Is(err, ErrInvalid) || got != nil {
				t.Fatalf("got %q, %v", got, err)
			}
			return
		}
		if len(got) > int(maxMembers) {
			t.Fatalf("%d members, limit %d", len(got), maxMembers)
		}
		joined, err := ParseStringList([]string{strings.Join(lines, ", ")}, int(maxMembers), int(maxLen))
		if err != nil || !slices.Equal(joined, got) {
			t.Fatalf("joined: got %q, %v; line by line %q", joined, err, got)
		}
		var sb strings.Builder
		for i, m := range got {
			if len(m) > int(maxLen) {
				t.Fatalf("member of %d bytes, limit %d", len(m), maxLen)
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteByte('"')
			for _, c := range []byte(m) {
				if c < 0x20 || c > 0x7e {
					t.Fatalf("byte %#x in member %q", c, m)
				}
				if c == '"' || c == '\\' {
					sb.WriteByte('\\')
				}
				sb.WriteByte(c)
			}
			sb.WriteByte('"')
		}
		again, err := ParseStringList([]string{sb.String()}, int(maxMembers), int(maxLen))
		if err != nil || !slices.Equal(again, got) {
			t.Fatalf("round trip of %q: got %q, %v", got, again, err)
		}
	})
}
