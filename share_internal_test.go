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
	return newTestShareResume(body, window, waiters, nil)
}

func newTestShareResume(body []byte, window int64, waiters int, resume resumeFunc) (*sharedStream, *closeCount) {
	src := &closeCount{Reader: bytes.NewReader(body)}
	s := newSharedStream(&Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: src}, http.Header{}, nil, store.Key{}, window, resume)
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
					maxHeld.Store(max(maxHeld.Load(), int64(cap(s.ring)), s.end-s.base))
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

// FR-COA-10: a reader that holds the window full for shareLagGrace is moved
// off, and with no way to resume it is cut off; the others finish. Without
// the cut one client that stops reading would hold every other client.
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

// FR-COA-10, T-18: a reader that keeps the window full by taking a little at
// a time is moved off too: the waits it causes add up.
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

// FR-COA-10: the waits a steady but slow reader causes add up, so the fast
// reader loses about shareLagGrace in total, not the slow reader's pace.
func TestSharedStreamSlowSteadyReaderCostsOneGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), 1<<20)
		s, _ := newTestShare(body, 64<<10, 2)
		fast, slow := s.attach(), s.attach()
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			p := make([]byte, 32<<10)
			for {
				if _, err := slow.Read(p); err != nil {
					return
				}
				time.Sleep(900 * time.Millisecond)
			}
		}()
		start := time.Now()
		if n, err := io.Copy(io.Discard, fast); err != nil || n != int64(len(body)) {
			t.Fatalf("fast reader: %d bytes, %v", n, err)
		}
		if d := time.Since(start); d > 2*shareLagGrace {
			t.Fatalf("fast reader took %v, want about one grace", d)
		}
		fast.Close()
		slow.Close()
		<-exited
	})
}

// FR-COA-10: a reader moved off resumes from its own offset on a fetch of
// its own and still gets every byte, in order.
func TestSharedStreamMovedOffReaderResumes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := make([]byte, 1<<20)
		for i := range body {
			body[i] = byte(i % 251)
		}
		var resumedAt atomic.Int64
		resumedAt.Store(-1)
		resume := func(off int64) (io.ReadCloser, error) {
			resumedAt.Store(off)
			return io.NopCloser(bytes.NewReader(body[off:])), nil
		}
		s, _ := newTestShareResume(body, 64<<10, 2, resume)
		fast, slow := s.attach(), s.attach()
		var got bytes.Buffer
		p := make([]byte, 1000)
		n, _ := slow.Read(p) // a little, then it stalls
		got.Write(p[:n])
		if m, err := io.Copy(io.Discard, fast); err != nil || m != int64(len(body)) {
			t.Fatalf("fast reader: %d bytes, %v", m, err)
		}
		if _, err := io.Copy(&got, slow); err != nil {
			t.Fatalf("slow reader: %v", err)
		}
		if !bytes.Equal(got.Bytes(), body) {
			t.Fatalf("slow reader got %d bytes, not the body", got.Len())
		}
		if resumedAt.Load() != int64(n) {
			t.Fatalf("resumed at %d, want %d", resumedAt.Load(), n)
		}
		fast.Close()
		slow.Close()
	})
}

// FR-COA-10, T-18: a Close that runs while a moved-off reader's resume fetch
// is on its way leaves no body open: resumeOwn closes the one that arrives.
func TestSharedStreamCloseDuringResume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := bytes.Repeat([]byte("q"), 1<<20)
		arrived := make(chan struct{})
		proceed := make(chan struct{})
		var resumed closeCount
		resume := func(off int64) (io.ReadCloser, error) {
			close(arrived)
			<-proceed
			resumed.Reader = bytes.NewReader(body[off:])
			return &resumed, nil
		}
		s, _ := newTestShareResume(body, 64<<10, 2, resume)
		fast, slow := s.attach(), s.attach()
		if _, err := io.Copy(io.Discard, fast); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := slow.Read(make([]byte, 10))
			done <- err
		}()
		<-arrived
		slow.Close()
		close(proceed)
		if err := <-done; !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read after close during resume: %v", err)
		}
		if resumed.closed.Load() != 1 {
			t.Fatal("the resumed body was not closed")
		}
		fast.Close()
	})
}
