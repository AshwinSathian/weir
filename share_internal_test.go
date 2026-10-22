package weir

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// closeCount is an origin body that counts its Close calls.
type closeCount struct {
	io.Reader
	closed atomic.Int32
}

func (c *closeCount) Close() error { c.closed.Add(1); return nil }

func newTestShare(body []byte, window int64, waiters int) (*sharedStream, *closeCount) {
	src := &closeCount{Reader: bytes.NewReader(body)}
	s := newSharedStream(&Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: src}, http.Header{}, nil, store.Key{}, window)
	s.setPending(waiters)
	return s, src
}

// FR-COA-10, T-18: every reader gets the whole body, the stream never holds
// more than its window, and the origin body is closed once, after the last
// reader.
func TestSharedStreamReadersGetWholeBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("0123456789"), 100_000) // 1 MB
		const window = 64 << 10
		s, src := newTestShare(body, window, 3)
		var wg sync.WaitGroup
		var maxHeld atomic.Int64
		for range 3 {
			r := s.attach()
			if r == nil {
				t.Fatal("attach failed")
			}
			wg.Go(func() {
				defer r.Close()
				var got bytes.Buffer
				p := make([]byte, 7000)
				for {
					n, err := r.Read(p)
					got.Write(p[:n])
					s.mu.Lock()
					maxHeld.Store(max(maxHeld.Load(), int64(len(s.buf))))
					s.mu.Unlock()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Errorf("read: %v", err)
						return
					}
				}
				if !bytes.Equal(got.Bytes(), body) {
					t.Errorf("got %d bytes, want the whole %d", got.Len(), len(body))
				}
			})
		}
		wg.Wait()
		if n := maxHeld.Load(); n > window {
			t.Fatalf("held %d bytes, window %d", n, window)
		}
		if n := src.closed.Load(); n != 1 {
			t.Fatalf("origin body closed %d times, want 1", n)
		}
	})
}

// FR-COA-10: a reader that falls a full window behind for shareLagGrace is
// cut off, and the others finish. Without the cut one client that stops
// reading would hold every other client of the object.
func TestSharedStreamDetachesSlowReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), 1<<20)
		s, src := newTestShare(body, 64<<10, 2)
		fast, slow := s.attach(), s.attach()
		start := time.Now()
		n, err := io.Copy(io.Discard, fast)
		if err != nil || n != int64(len(body)) {
			t.Fatalf("fast reader: %d bytes, %v", n, err)
		}
		if d := time.Since(start); d != shareLagGrace {
			t.Fatalf("fast reader took %v, want one lag grace (%v)", d, shareLagGrace)
		}
		if _, err := slow.Read(make([]byte, 10)); !errors.Is(err, errSharedTooSlow) {
			t.Fatalf("slow reader: %v, want errSharedTooSlow", err)
		}
		fast.Close()
		slow.Close()
		if src.closed.Load() != 1 {
			t.Fatal("origin body not closed")
		}
	})
}

// FR-COA-10: while a counted request has not attached, nothing is dropped,
// so it still reads from the first byte; a request that attaches after the
// others moved on gets nil and fetches for itself.
func TestSharedStreamLateAttach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("y"), 10_000)
		s, _ := newTestShare(body, 1<<20, 2)
		first := s.attach()
		if n, err := io.Copy(io.Discard, first); err != nil || n != int64(len(body)) {
			t.Fatalf("first: %d, %v", n, err)
		}
		second := s.attach()
		if second == nil {
			t.Fatal("a counted request could not attach")
		}
		if n, err := io.Copy(io.Discard, second); err != nil || n != int64(len(body)) {
			t.Fatalf("second: %d, %v", n, err)
		}
		first.Close()
		second.Close()

		s2, src2 := newTestShare(body, 1<<20, 1)
		r := s2.attach()
		if _, err := io.Copy(io.Discard, r); err != nil {
			t.Fatal(err)
		}
		s2.mu.Lock()
		s2.pending = 1 // an uncounted latecomer
		s2.mu.Unlock()
		if s2.attach() != nil {
			t.Fatal("a request attached after the first bytes were dropped")
		}
		r.Close()
		if src2.closed.Load() != 1 {
			t.Fatal("origin body not closed")
		}
	})
}

// FR-COA-10: with no request left to read it, the origin body is closed at
// once.
func TestSharedStreamUnusedIsClosed(t *testing.T) {
	s, src := newTestShare([]byte("z"), 1<<20, 2)
	s.release()
	if src.closed.Load() != 0 {
		t.Fatal("closed while a request was still counted")
	}
	s.release()
	if src.closed.Load() != 1 {
		t.Fatal("not closed after the last release")
	}
	s0, src0 := newTestShare([]byte("z"), 1<<20, 0)
	s0.closeIfUnused()
	if src0.closed.Load() != 1 {
		t.Fatal("not closed with no waiter")
	}
}

// FR-COA-10, T-18: a reader that keeps the window full by taking a few
// bytes at a time is cut off too; moving at all is not enough.
func TestSharedStreamDetachesDrippingReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), 1<<20)
		s, _ := newTestShare(body, 64<<10, 2)
		fast, drip := s.attach(), s.attach()
		done := make(chan error, 1)
		go func() {
			p := make([]byte, 16)
			for {
				if _, err := drip.Read(p); err != nil {
					done <- err
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
		if n, err := io.Copy(io.Discard, fast); err != nil || n != int64(len(body)) {
			t.Fatalf("fast reader: %d bytes, %v", n, err)
		}
		if err := <-done; !errors.Is(err, errSharedTooSlow) {
			t.Fatalf("dripping reader: %v, want errSharedTooSlow", err)
		}
		fast.Close()
		drip.Close()
	})
}
