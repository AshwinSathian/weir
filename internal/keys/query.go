package keys

import (
	"slices"
	"strings"
)

// rewriteQuery applies Key.QueryDrop, Key.QueryKeep and Key.QuerySort to raw
// '&'-separated segments without decoding (FR-KEY-5, 04 §3.4). The result is
// both keyed and forwarded (P2). Validate has already bounded the segment
// count by MaxQueryParams and rejected control bytes.
func rewriteQuery(raw string, c *Config) string {
	if raw == "" {
		return ""
	}
	out := make([]string, 0, strings.Count(raw, "&")+1)
	changed := false
	for s := range strings.SplitSeq(raw, "&") {
		// T-2: ';' is not a separator. "a=1;utm_x=2" is one segment named
		// "a", kept and forwarded whole, so the origin only ever parses
		// keyed bytes.
		name, _, _ := strings.Cut(s, "=")
		if s == "" || len(c.QueryKeep) > 0 && !match(c.QueryKeep, name) || match(c.QueryDrop, name) {
			changed = true
			continue
		}
		out = append(out, s)
	}
	if c.QuerySort && !slices.IsSorted(out) {
		// Equal segments are identical bytes, so an unstable sort gives
		// the same result as the stable one FR-KEY-5 asks for.
		slices.Sort(out)
		changed = true
	}
	if !changed {
		return raw
	}
	return strings.Join(out, "&")
}

// match reports whether name equals a pattern or starts with the prefix of
// a pattern ending in '*'. Comparison is on raw bytes: "utm%5Fx" is not
// "utm_x" (04 §3.4).
func match(patterns []string, name string) bool {
	for _, p := range patterns {
		if prefix, ok := strings.CutSuffix(p, "*"); ok && strings.HasPrefix(name, prefix) || p == name {
			return true
		}
	}
	return false
}
