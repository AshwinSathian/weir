package weir

import (
	"context"
	"testing"

	"github.com/AshwinSathian/weir/store"
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
