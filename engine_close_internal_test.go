package weir

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

type ctxCloser struct {
	store.Store
	got context.Context
}

func (c *ctxCloser) CloseContext(ctx context.Context) error { c.got = ctx; return nil }

type plainCloser struct {
	store.Store
	closed bool
}

func (c *plainCloser) Close() error { c.closed = true; return nil }

// FR-SNP-1, FR-LCY-2: the adapter's grace context, not the store's own
// timeout, bounds a snapshot write.
func TestCloseStorePrefersCloseContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, 1)
	c := &ctxCloser{}
	if err := closeStore(ctx, c); err != nil || c.got != ctx {
		t.Errorf("closeStore used ctx %v, err %v; want the caller's ctx", c.got, err)
	}
	p := &plainCloser{}
	if err := closeStore(ctx, p); err != nil || !p.closed {
		t.Errorf("closeStore on a plain store: closed=%v err=%v", p.closed, err)
	}
}

// FR-SNP-1: a store that was never handed out (failed construction) must not
// overwrite a good snapshot, and a Close whose grace period is spent still
// gives the snapshot the store's own budget.
func TestCloseStoreWithCancelledContextWritesNoSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	m, err := memory.New(memory.Config{SnapshotPath: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = closeStore(ctx, m)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("snapshot written under a cancelled context: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Close with its own budget wrote no snapshot: %v", err)
	}
}
