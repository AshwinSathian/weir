package sfv

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDictionary(t *testing.T) {
	// FR-TCC-1; T-34
	tests := []struct {
		name  string
		lines []string
		want  Dict
		bad   bool
	}{
		{name: "no lines is empty", want: Dict{}},
		{name: "empty line is empty", lines: []string{""}, want: Dict{}},
		{name: "RFC 9651 A.1 style: integers and bare key", lines: []string{"max-age=3600, private"},
			want: Dict{"max-age": {Kind: Integer, Int: 3600}, "private": {Kind: Boolean, Bool: true}}},
		{name: "explicit booleans", lines: []string{"a=?0, b=?1"},
			want: Dict{"a": {Kind: Boolean}, "b": {Kind: Boolean, Bool: true}}},
		{name: "RFC 9651 §3.2 example, parameters ignored", lines: []string{"en=\"Applepie\", da=:w4ZibGV0w6ZydGU=:"},
			want: Dict{"en": {Kind: String, Str: "Applepie"}, "da": {Kind: Other}}},
		{name: "member parameters are dropped", lines: []string{"a=1;x=2, b;y, c=tok;z=\"s\""},
			want: Dict{"a": {Kind: Integer, Int: 1}, "b": {Kind: Boolean, Bool: true}, "c": {Kind: Token, Str: "tok"}}},
		{name: "RFC 9651 §3.2.1 inner list", lines: []string{"rating=1.5, feelings=(joy sadness)"},
			want: Dict{"rating": {Kind: Decimal, Dec: 1.5}, "feelings": {Kind: Other}}},
		{name: "negative integer", lines: []string{"a=-5"}, want: Dict{"a": {Kind: Integer, Int: -5}}},
		{name: "negative decimal", lines: []string{"a=-0.25"}, want: Dict{"a": {Kind: Decimal, Dec: -0.25}}},
		{name: "duplicate key keeps the last value", lines: []string{"a=1, b=2, a=3"},
			want: Dict{"a": {Kind: Integer, Int: 3}, "b": {Kind: Integer, Int: 2}}},
		{name: "lines are joined with a comma", lines: []string{"a=1", "b=2"},
			want: Dict{"a": {Kind: Integer, Int: 1}, "b": {Kind: Integer, Int: 2}}},
		{name: "string escapes", lines: []string{`a="x\"y\\z"`}, want: Dict{"a": {Kind: String, Str: `x"y\z`}}},
		{name: "tab separates members", lines: []string{"a=1,\tb=2"},
			want: Dict{"a": {Kind: Integer, Int: 1}, "b": {Kind: Integer, Int: 2}}},
		{name: "uppercase key", lines: []string{"A=1"}, bad: true},
		{name: "trailing comma", lines: []string{"a=1,"}, bad: true},
		{name: "leading comma", lines: []string{",a=1"}, bad: true},
		{name: "empty member between commas", lines: []string{"a=1,,b=2"}, bad: true},
		{name: "empty line among lines", lines: []string{"a=1", ""}, bad: true},
		{name: "missing comma", lines: []string{"a=1 b=2"}, bad: true},
		{name: "unterminated string", lines: []string{`a="x`}, bad: true},
		{name: "string spanning lines", lines: []string{`a="x`, `y"`}, bad: true},
		{name: "bad escape", lines: []string{`a="\n"`}, bad: true},
		{name: "control byte in string", lines: []string{"a=\"x\x01\""}, bad: true},
		{name: "integer with 16 digits", lines: []string{"a=1234567890123456"}, bad: true},
		{name: "decimal with 4 fraction digits", lines: []string{"a=1.2345"}, bad: true},
		{name: "decimal with trailing dot", lines: []string{"a=1."}, bad: true},
		{name: "bad boolean", lines: []string{"a=?2"}, bad: true},
		{name: "empty value", lines: []string{"a="}, bad: true},
		{name: "unterminated inner list", lines: []string{"a=(b c"}, bad: true},
		{name: "bad parameter key", lines: []string{"a=1;B=2"}, bad: true},
		{name: "bad base64", lines: []string{"a=:a$:"}, bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDictionary(tt.lines, 16)
			if tt.bad {
				if !errors.Is(err, ErrInvalid) || got != nil {
					t.Fatalf("got %v, %v; want ErrInvalid and no members", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, w := range tt.want {
				if g, ok := got[k]; !ok || g != w {
					t.Fatalf("key %q: got %v, want %v", k, g, w)
				}
			}
		})
	}
}

func TestParseDictionaryBounds(t *testing.T) {
	// FR-TCC-1; NFR-3, T-21
	t.Run("more members than the limit is invalid", func(t *testing.T) {
		got, err := ParseDictionary([]string{"a=1, b=2, c=3"}, 2)
		if !errors.Is(err, ErrInvalid) || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("members at the limit parse", func(t *testing.T) {
		if got, err := ParseDictionary([]string{"a=1, b=2"}, 2); err != nil || len(got) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("duplicates count against the limit", func(t *testing.T) {
		if _, err := ParseDictionary([]string{"a=1, a=2, a=3"}, 2); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("long run of inner list items terminates", func(t *testing.T) {
		in := "a=(" + strings.Repeat("x ", 50000) + "x)"
		if _, err := ParseDictionary([]string{in}, 4); err != nil {
			t.Fatal(err)
		}
	})
}

// FuzzSFDictionary checks that no input panics, that an error returns no
// members, and that a success respects the member limit.
func FuzzSFDictionary(f *testing.F) {
	// FR-TCC-1; NFR-2, T-21, T-34
	for _, s := range []string{
		"max-age=3600, private", "en=\"Applepie\", da=:w4ZibGV0w6ZydGU=:", "rating=1.5, feelings=(joy sadness)",
		"a=?0;x=1, b", "a=-0.25, a=7", "a=(b;c=1 d)", "a=1\na=2", "a=%\"caf%c3%a9\"", "a=@1700000000", "",
	} {
		f.Add(s, uint8(8))
	}
	f.Fuzz(func(t *testing.T, in string, maxMembers uint8) {
		got, err := ParseDictionary(strings.Split(in, "\n"), int(maxMembers))
		if err != nil {
			if !errors.Is(err, ErrInvalid) || got != nil {
				t.Fatalf("got %v, %v", got, err)
			}
			return
		}
		if got == nil || len(got) > int(maxMembers) {
			t.Fatalf("%d members, limit %d", len(got), maxMembers)
		}
	})
}
