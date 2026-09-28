package weir

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"runtime/debug"
	"strings"
	"testing"
)

// FR-MEM-1: the default memory store takes 40% of GOMEMLIMIT, clamped
// to [16 MiB, 8 GiB], or 256 MiB when no limit is set.
func TestDefaultStoreSizeFromMemLimit(t *testing.T) {
	for _, tt := range []struct {
		name  string
		limit int64
		want  int64
		set   bool
	}{
		{"1 GiB limit gives 40%", 1 << 30, (1 << 30) / 5 * 2, true},
		{"small limit clamps to 16 MiB", 10 << 20, 16 << 20, true},
		{"large limit clamps to 8 GiB", 64 << 30, 8 << 30, true},
		{"no limit gives 256 MiB", math.MaxInt64, 256 << 20, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, set := defaultStoreBytes(tt.limit)
			if got != tt.want || set != tt.set {
				t.Fatalf("defaultStoreBytes(%d) = %d, %v; want %d, %v", tt.limit, got, set, tt.want, tt.set)
			}
		})
	}

	t.Run("no limit warns at New", func(t *testing.T) {
		defer debug.SetMemoryLimit(debug.SetMemoryLimit(math.MaxInt64))
		var buf bytes.Buffer
		e, err := New(Config{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := e.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		if !strings.Contains(buf.String(), "GOMEMLIMIT") {
			t.Fatalf("no GOMEMLIMIT warning; log: %q", buf.String())
		}
	})
}
