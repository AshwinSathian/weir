package keys

import (
	"crypto/sha256"
	"testing"

	"github.com/AshwinSathian/weir/store"
)

func TestTagsMatchSpec(t *testing.T) {
	// FR-KEY-12, 04 §2: tag bytes are fixed, with no per-process seed.
	sum := func(s string) store.Tag { return sha256.Sum256([]byte(s)) }
	const o = "https://example.com"
	lp := "\x13" + o // uvarint(19) + origin
	tests := []struct {
		name      string
		got, want store.Tag
	}{
		{"global", TagGlobal(), sum("weir/tag/v1\x00global")},
		{"origin", TagOrigin(o), sum("weir/tag/v1\x01" + lp)},
		{"uri", TagURI(o, "/p", "q=1"), sum("weir/tag/v1\x02" + lp + "\x02/p" + "\x03q=1")},
		{"group", TagGroup(o, "news"), sum("weir/tag/v1\x03" + lp + "\x04news")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("tag bytes differ from 04 §2")
			}
		})
	}
}

func TestTagsDistinct(t *testing.T) {
	// T-3: length prefixes and kind bytes keep tags of different shapes apart.
	tags := []store.Tag{
		TagGlobal(),
		TagOrigin("o"),
		TagOrigin(""),
		TagURI("o", "/ab", "c"),
		TagURI("o", "/a", "bc"),
		TagURI("o", "", ""),
		TagGroup("o", ""),
		TagGroup("o", "g"),
		TagGroup("og", ""),
	}
	seen := map[store.Tag]int{}
	for i, tg := range tags {
		if j, ok := seen[tg]; ok {
			t.Errorf("tags %d and %d collide", j, i)
		}
		seen[tg] = i
	}
}
