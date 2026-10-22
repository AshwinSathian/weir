package weir

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// shareLagGrace is the total time the readers of a shared stream may spend
// waiting for the slowest one before it is moved off the stream (FR-COA-10).
// The waits add up across pauses, so a client that drips a few bytes at a
// time cannot hold the others longer than one that stops.
const shareLagGrace = time.Second

// errSharedTooSlow ends the read of a client that was moved off a shared
// stream and could not resume on a fetch of its own (FR-COA-10). Its
// connection is cut, as with a stream idle timeout (FR-TMO-2).
var errSharedTooSlow = errors.New("weir: client too slow for a shared stream")

// resumeFunc fetches the rest of the body from off, on a request of its own,
// for a reader moved off the stream. Nil when the response cannot be resumed.
type resumeFunc func(off int64) (io.ReadCloser, error)

// sharedStream gives one over-size origin body to every request that waited
// on its flight (FR-COA-10, T-18). Readers pull: the reader that needs bytes
// past what was read reads the origin, at most window bytes ahead of the
// slowest reader, into a ring of exactly window bytes. When the faster
// readers have waited shareLagGrace in total on a full window, the slowest
// readers are moved off: each resumes the body from its offset on a range
// request of its own when the response allows it, or is cut off. The origin
// body is closed when the last reader closes, or at once when no counted
// request takes it.
type sharedStream struct {
	src    io.ReadCloser
	status int
	header http.Header // the fetch's header, for the creator's response
	// shared is the header a follower gets: the fields a stored entry keeps
	// (FR-STO-11), so a Set-Cookie that StripSetCookie lets through to the
	// creator never reaches another client (FR-STO-6, T-8).
	shared http.Header
	vary   []string  // the response's Vary names; a follower shares only its own variant (FR-KEY-7)
	vk     store.Key // the variant key the response answers
	window int64
	resume resumeFunc

	mu      sync.Mutex
	cond    sync.Cond
	ring    []byte // window bytes, allocated on the first read
	base    int64  // the oldest offset the ring still holds
	end     int64  // bytes read from src
	err     error  // how src ended: io.EOF, or the read error
	reading bool   // a reader is in src.Read
	// readers attached and not closed; bounded by the flight's waiter count,
	// itself bounded by the limiter queue (04 §8.3).
	readers   map[*sharedReader]struct{}
	pending   int  // counted requests that have neither attached nor released
	closed    bool // src was closed
	waitSince time.Time
	waited    time.Duration // time readers waited on a full window since the last move-off
	wake      *time.Timer   // wakes waiting readers when the budget runs out; only broadcasts
	tmp       []byte
}

func newSharedStream(resp *Response, shared http.Header, vary []string, vk store.Key, window int64, resume resumeFunc) *sharedStream {
	s := &sharedStream{src: resp.Body, status: resp.StatusCode, header: resp.Header, shared: shared, vary: vary, vk: vk,
		window: window, resume: resume, readers: make(map[*sharedReader]struct{})}
	s.cond.L = &s.mu
	return s
}

// setPending records how many requests will attach or release. The flight
// calls it while it publishes (coalesce.Flight.PublishCounted).
func (s *sharedStream) setPending(n int) {
	s.mu.Lock()
	s.pending = n
	s.mu.Unlock()
}

// release is a counted request that will not read the stream: a HEAD, a
// request for another variant, a warm or background joiner, or one that left.
func (s *sharedStream) release() {
	s.mu.Lock()
	s.pending--
	s.unlockAndMaybeClose()
}

// closeIfUnused closes src when no request takes it.
func (s *sharedStream) closeIfUnused() {
	s.mu.Lock()
	s.unlockAndMaybeClose()
}

// attach gives a counted request a reader from the first byte, or nil when
// those bytes are gone because readers went ahead while this one was slow to
// attach; the caller then fetches for itself.
func (s *sharedStream) attach() *sharedReader {
	s.mu.Lock()
	s.pending--
	if s.base > 0 || s.closed {
		s.unlockAndMaybeClose()
		return nil
	}
	r := &sharedReader{s: s}
	s.readers[r] = struct{}{}
	s.mu.Unlock()
	return r
}

// unlockAndMaybeClose closes src, outside the lock, once nobody can read it.
func (s *sharedStream) unlockAndMaybeClose() {
	closeNow := !s.closed && s.pending <= 0 && len(s.readers) == 0
	if closeNow {
		s.closed = true
		if s.wake != nil {
			s.wake.Stop()
			s.wake = nil
		}
		s.ring = nil
	}
	s.advance()
	s.cond.Broadcast()
	s.mu.Unlock()
	if closeNow {
		_ = s.src.Close()
	}
}

// low is the offset of the slowest reader. While a counted request has not
// attached, nothing is dropped: it reads from the first byte.
func (s *sharedStream) low() int64 {
	if s.pending > 0 {
		return 0
	}
	low := s.end
	// ponytail: O(readers) per call, readers bounded by the flight's waiters;
	// a heap if a profile ever shows a large herd on one over-size object.
	for r := range s.readers {
		low = min(low, r.off)
	}
	return low
}

// advance drops what every reader has read, and ends a wait on a full
// window, adding it to the budget.
func (s *sharedStream) advance() {
	s.base = max(s.base, s.low())
	if !s.waitSince.IsZero() && s.end-s.base < s.window {
		s.waited += time.Since(s.waitSince)
		s.waitSince = time.Time{}
	}
}

// moveOffSlowest takes the readers at the low offset off the stream: they
// are what holds the window full. The budget starts again.
func (s *sharedStream) moveOffSlowest() {
	low := s.low()
	s.pending = 0 // a request that has not attached by now never will in time
	for r := range s.readers {
		if r.off == low {
			r.movedOff = true
			delete(s.readers, r)
		}
	}
	s.waited, s.waitSince = 0, time.Time{}
	s.advance()
}

// sharedReader is one request's view of a sharedStream. After it is moved
// off, own carries the rest of the body when it could resume.
type sharedReader struct {
	s        *sharedStream
	off      int64
	movedOff bool
	done     bool
	own      io.ReadCloser
}

func (r *sharedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.own != nil {
		n, err := r.own.Read(p)
		r.off += int64(n)
		return n, err
	}
	s := r.s
	s.mu.Lock()
	for {
		switch {
		case r.movedOff:
			s.unlockAndMaybeClose()
			return r.resumeOwn(p)
		case r.done:
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		case r.off < s.end:
			n := s.copyOut(p, r.off)
			r.off += int64(n)
			s.advance()
			s.cond.Broadcast()
			s.mu.Unlock()
			return n, nil
		case s.err != nil:
			err := s.err
			s.mu.Unlock()
			return 0, err
		case s.reading:
			s.cond.Wait()
		case s.end-s.low() < s.window:
			s.fill()
		default: // the window is full: the slowest reader holds everyone
			now := time.Now()
			if s.waitSince.IsZero() {
				s.waitSince = now
			}
			left := shareLagGrace - s.waited - now.Sub(s.waitSince)
			if left <= 0 {
				s.moveOffSlowest()
				s.cond.Broadcast()
				continue
			}
			if s.wake == nil {
				s.wake = time.AfterFunc(left, func() {
					s.mu.Lock()
					s.wake = nil
					s.cond.Broadcast()
					s.mu.Unlock()
				})
			}
			s.cond.Wait()
		}
	}
}

// resumeOwn continues a reader that was moved off on a fetch of its own.
func (r *sharedReader) resumeOwn(p []byte) (int, error) {
	if r.s.resume == nil {
		return 0, errSharedTooSlow
	}
	own, err := r.s.resume(r.off)
	if err != nil {
		return 0, errSharedTooSlow
	}
	r.own = own
	return r.Read(p)
}

// copyOut copies bytes from offset off into p and returns how many.
func (s *sharedStream) copyOut(p []byte, off int64) int {
	n := int(min(int64(len(p)), s.end-off))
	i := int(off % s.window)
	first := min(n, len(s.ring)-i)
	copy(p, s.ring[i:i+first])
	copy(p[first:n], s.ring[:n-first])
	return n
}

// fill reads the next bytes from src for every reader. Called with mu held;
// the read itself runs unlocked, and no other reader starts one meanwhile.
func (s *sharedStream) fill() {
	s.reading = true
	if s.ring == nil {
		s.ring = make([]byte, s.window)
		s.tmp = make([]byte, min(32<<10, s.window))
	}
	room := s.window - (s.end - s.low())
	buf := s.tmp[:min(int64(len(s.tmp)), room)]
	s.mu.Unlock()
	n, err := s.src.Read(buf)
	s.mu.Lock()
	s.reading = false
	if s.ring != nil { // nil: closed while the read ran
		i := int(s.end % s.window)
		first := min(n, len(s.ring)-i)
		copy(s.ring[i:], buf[:first])
		copy(s.ring, buf[first:n])
	}
	s.end += int64(n)
	if err != nil {
		s.err = err
	}
	s.cond.Broadcast()
}

// Close leaves the stream; the last reader to leave closes the origin body.
func (r *sharedReader) Close() error {
	s := r.s
	s.mu.Lock()
	if r.done {
		s.mu.Unlock()
		return nil
	}
	r.done = true
	delete(s.readers, r)
	own := r.own
	s.unlockAndMaybeClose()
	if own != nil {
		return own.Close()
	}
	return nil
}

// StreamIdle forwards the origin stream's idle bound, so weirhttp bounds each
// write to this client by it (FR-TMO-2).
func (r *sharedReader) StreamIdle() time.Duration {
	if b, ok := r.s.src.(interface{ StreamIdle() time.Duration }); ok {
		return b.StreamIdle()
	}
	return 0
}

// Trailer returns the origin's trailers once this reader has seen the end
// (FR-FWD-8).
func (r *sharedReader) Trailer() http.Header {
	if r.own != nil {
		return trailerOf(r.own)
	}
	s := r.s
	s.mu.Lock()
	ended := r.off == s.end && errors.Is(s.err, io.EOF)
	s.mu.Unlock()
	if !ended {
		return nil
	}
	return trailerOf(s.src)
}
