package weir_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// D30, FR-KEY-5, T-2: the preset is opt-in. With it, tracking parameters
// leave both the key and the forwarded query; without it they are keyed.
func TestTrackingParamsPreset(t *testing.T) {
	const tracked = "utm_source=news&a=1&gclid=x&fbclid=y&msclkid=z&b=2&utm_campaign=c"
	tests := []struct {
		name      string
		drop      []string
		forwarded string
		hit       bool
	}{
		{"preset drops tracking parameters", weir.TrackingParams(), "a=1&b=2", true},
		{"default keeps them", nil, tracked, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(cacheable("v"))
				cfg := cacheCfg
				cfg.Key.QueryDrop = tt.drop
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				first := getReq("/a")
				first.RawQuery = tracked
				serve(t, e, first, o)
				if got := o.Requests()[0].RawQuery; got != tt.forwarded {
					t.Fatalf("forwarded query %q, want %q", got, tt.forwarded)
				}
				clean := getReq("/a")
				clean.RawQuery = "a=1&b=2"
				if resp, _ := serve(t, e, clean, o); resp.Cache.Hit != tt.hit {
					t.Errorf("clean URL hit = %v, want %v", resp.Cache.Hit, tt.hit)
				}
			})
		})
	}

	t.Run("each call returns its own slice", func(t *testing.T) {
		a := weir.TrackingParams()
		a[0] = "changed"
		if b := weir.TrackingParams(); slices.Contains(b, "changed") || len(b) == 0 {
			t.Errorf("preset shares state between calls: %q", b)
		}
	})
}
