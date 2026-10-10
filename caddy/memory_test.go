package weircaddy

import (
	"bytes"
	"log/slog"
	"math"
	"runtime/debug"
	"strings"
	"testing"
)

// setLimit sets the process memory limit for one test. The limit is global,
// so these tests do not run in parallel.
func setLimit(t *testing.T, n int64) {
	t.Helper()
	old := debug.SetMemoryLimit(n)
	t.Cleanup(func() { debug.SetMemoryLimit(old) })
}

func logTo(buf *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(buf, nil)) }

// FR-MEM-1, T-43: auto-sized stores of one load share the 40% budget, stores
// with max_bytes are excluded, and an unset limit falls back to 256 MiB with
// one warning.
func TestMemorySizingSplit(t *testing.T) {
	const limit = 4 << 30
	budget, _ := memoryBudget(limit)
	if want := int64(limit / 100 * 40); budget != want {
		t.Fatalf("budget = %d, want %d", budget, want)
	}

	t.Run("each auto-sized store takes half of what the load has not granted", func(t *testing.T) {
		setLimit(t, limit)
		ctx := newCtx(t)
		a := mustLoad(t, ctx, `{"name":"split-a"}`)
		x := mustLoad(t, ctx, `{"name":"split-x","max_bytes":"200MiB"}`)
		b := mustLoad(t, ctx, `{"name":"split-b"}`)
		c := mustLoad(t, ctx, `{"name":"split-c"}`)
		sa, sb, sc := a.pool.size.Load(), b.pool.size.Load(), c.pool.size.Load()
		if sa != budget/2 || sb != (budget-sa)/2 || sc != (budget-sa-sb)/2 {
			t.Fatalf("sizes %d, %d, %d for budget %d", sa, sb, sc, budget)
		}
		if x.pool.size.Load() != 0 {
			t.Fatal("an explicit max_bytes store was counted against the budget")
		}
		if sa+sb+sc > budget {
			t.Fatalf("auto-sized stores sum to %d, above %d", sa+sb+sc, budget)
		}
	})

	t.Run("the 160 MiB floor raises a share and warns", func(t *testing.T) {
		var buf bytes.Buffer
		r := newStoreRegistry()
		load := new(int)
		r.loads[load] = map[string]*claim{"big": {size: 300 << 20}}
		// 409 MiB budget, 300 MiB granted: half of the rest is 54 MiB.
		got := r.autoSize(load, 1<<30, logTo(&buf), "small")
		if got != minStoreBytes || !strings.Contains(buf.String(), "floor") {
			t.Fatalf("size %d, log %q", got, buf.String())
		}
	})

	t.Run("an unset limit uses 256 MiB and warns once", func(t *testing.T) {
		var buf bytes.Buffer
		r := newStoreRegistry()
		for range 2 {
			if got := r.autoSize(new(int), math.MaxInt64, logTo(&buf), "n"); got != 256<<20 {
				t.Fatalf("size = %d, want 256 MiB", got)
			}
		}
		if n := strings.Count(buf.String(), "no memory limit"); n != 1 {
			t.Fatalf("warned %d times, log %q", n, buf.String())
		}
		if !strings.Contains(buf.String(), "GOMEMLIMIT") {
			t.Fatalf("warning does not recommend GOMEMLIMIT: %q", buf.String())
		}
	})
}

// 08 §7: the store interface has no resize, so a later load that adds a site
// leaves existing stores at the size and contents they had.
func TestMemoryShareFixedAfterBuild(t *testing.T) {
	setLimit(t, 4<<30)
	a1 := mustLoad(t, newCtx(t), `{"name":"fixed-a"}`)
	size := a1.pool.size.Load()
	st := a1.pool.store

	ctx2 := newCtx(t)
	a2 := mustLoad(t, ctx2, `{"name":"fixed-a"}`)
	b := mustLoad(t, ctx2, `{"name":"fixed-b"}`)
	if a2.pool != a1.pool || a2.pool.store != st {
		t.Fatal("the reload built a new store for an unchanged site")
	}
	if got := a2.pool.size.Load(); got != size {
		t.Fatalf("existing store resized from %d to %d", size, got)
	}
	// The reused store counts against its new load, so b gets half the rest.
	budget, _ := memoryBudget(4 << 30)
	if want := (budget - size) / 2; b.pool.size.Load() != want {
		t.Fatalf("new store = %d, want %d", b.pool.size.Load(), want)
	}
}

// T-43: live auto-sized stores summing above 40% log a warning that names
// max_bytes.
func TestMemoryOvercommitWarns(t *testing.T) {
	setLimit(t, 4<<30)
	// Three loads, each adding a site while the earlier ones are still live
	// (a reload overlap that never cleaned up): 3 x 800 MiB-ish > 40%.
	for _, n := range []string{"over-a", "over-b", "over-c"} {
		mustLoad(t, newCtx(t), `{"name":"`+n+`"}`)
	}
	var buf bytes.Buffer
	stores.checkOvercommit(logTo(&buf), "over-c")
	if out := buf.String(); !strings.Contains(out, "max_bytes") || !strings.Contains(out, "budget") {
		t.Fatalf("no overcommit warning naming max_bytes: %q", out)
	}

	t.Run("an empty registry is not over budget", func(t *testing.T) {
		if _, _, over := newStoreRegistry().overcommit(4 << 30); over {
			t.Fatal("empty registry reported overcommit")
		}
	})
}
