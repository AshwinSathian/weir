package keys

import (
	"net/http"
	"strings"
	"testing"
)

// FR-FWD-1, T-1, T-31: an unkeyed Cache-Control or Pragma field reaches the
// origin only as a plain, short directive list.
func TestCacheDirectivesFiltered(t *testing.T) {
	strict := &Config{}
	keyed := &Config{Headers: []string{"Cache-Control"}, MaxKeyedHeaderBytes: 64}
	all := &Config{ForwardAll: true}
	nine := strings.Split(strings.Repeat("no-cache;", 9), ";")[:9]
	tests := []struct {
		name string
		cfg  *Config
		in   []string
		want bool // forwarded
	}{
		{"standard directives pass", strict, []string{"no-cache, max-age=0, max-stale=5, min-fresh=1, no-transform, only-if-cached"}, true},
		{"an extension directive passes", strict, []string{"x_vendor-ext=1"}, true},
		{"eight lines pass", strict, nine[:8], true},
		{"nine lines are dropped", strict, nine, false},
		{"256 bytes pass", strict, []string{strings.Repeat("a", 256)}, true},
		{"257 bytes are dropped", strict, []string{strings.Repeat("a", 257)}, false},
		{"length counts all lines", strict, []string{strings.Repeat("a", 200), strings.Repeat("a", 57)}, false},
		{"an empty line is dropped", strict, []string{"no-cache", ""}, false},
		{"markup is dropped", strict, []string{`no-cache, x="<script>alert(1)</script>"`}, false},
		{"a quoted argument is dropped", strict, []string{`max-stale="5"`}, false},
		{"a tab is dropped", strict, []string{"no-cache,\tno-store"}, false},
		{"a non-ASCII byte is dropped", strict, []string{"no-cach\xe9"}, false},
		{"a keyed field is normalized instead", keyed, []string{`x="<b>"`}, true},
		{"ForwardAll sends everything", all, []string{`x="<b>"`}, true},
	}
	for _, name := range []string{"Cache-Control", "Pragma"} {
		for _, tt := range tests {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				if name == "Pragma" && tt.cfg == keyed {
					t.Skip("the keyed row is about Cache-Control")
				}
				out, unkeyed := forwardHeader(http.Header{name: tt.in}, tt.cfg, nil, nil)
				if got := len(out[name]) > 0; got != tt.want {
					t.Fatalf("forwarded = %v, want %v (%q)", got, tt.want, out[name])
				}
				// T-31: a field that was not sent is not unkeyed input; one
				// that was still suppresses markers unless it is keyed.
				if wantUnkeyed := tt.want && tt.cfg != keyed; unkeyed != wantUnkeyed {
					t.Fatalf("unkeyed = %v, want %v", unkeyed, wantUnkeyed)
				}
			})
		}
	}
}
