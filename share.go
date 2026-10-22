package weir

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// shareLagGrace is how long the readers of a shared stream wait on a full
// window before they judge the slowest (FR-COA-10). Short next to
// StreamIdle, so one slow or hostile client stalls the others for about a
// second at a time.
const shareLagGrace = time.Second

// shareMinProgress is the share of the window the slowest readers must take
// in each shareLagGrace while the others wait: an eighth of 1 MiB per second,
// about 1 Mbit/s. A client that drips one byte at a time would otherwise
// hold every other reader of the object (T-18).
const shareMinProgress = 8

// errSharedTooSlow ends the read of a client that fell a full window behind
// the others on a shared stream for shareLagGrace (FR-COA-10). Its
// connection is cut, as with a stream idle timeout (FR-TMO-2).
var errSharedTooSlow = errors.New("weir: client too slow for a shared stream")

// sharedStream gives one over-size origin body to every request that waited
// on its flight (FR-COA-10, T-18). Readers pull: whichever reader needs the
// next bytes reads the origin, at most window bytes ahead of the slowest
// reader, so memory per stream is bounded by window (Storable.MaxObjectBytes,
// the same amount a buffered fetch holds before it finds a body over-size).
// The origin body is closed when the last reader closes, or at once when no
// counted request takes it.
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

	mu      sync.Mutex
	cond    sync.Cond
	buf     []byte // bytes [base, end) of the body
	base    int64
	end     int64
	err     error // how src ended: io.EOF, or the read error
	reading bool  // a reader is in src.Read
	// readers attached and not closed; bounded by the flight's waiter count,
	// itself bounded by the limiter queue (04 §8.3).
	readers map[*sharedReader]struct{}
	pending int  // counted requests that have neither attached nor released
	closed  bool // src was closed
	lag     *time.Timer
	tmp     []byte
}

func newSharedStream(resp *Response, shared http.Header, vary []string, vk store.Key, window int64) *sharedStream {
	s := &sharedStream{src: resp.Body, status: resp.StatusCode, header: resp.Header, shared: shared, vary: vary, vk: vk, window: window,
		readers: make(map[*sharedReader]struct{})}
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
// request for another variant, or one that left.
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
		if s.lag != nil {
			s.lag.Stop()
			s.lag = nil
		}
	}
	s.trim()
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

// trim drops the bytes every reader has read.
func (s *sharedStream) trim() {
	if l := s.low(); l > s.base {
		s.buf = s.buf[l-s.base:]
		s.base = l
		if len(s.buf) == 0 {
			s.buf = nil // let a drained window's array go
		}
	}
}

// waitForSlow arms the lag timer when the window is full: after
// shareLagGrace, if the window is still full, every reader that has not
// moved shareMinProgress past where the slowest stood is detached.
func (s *sharedStream) waitForSlow() {
	if s.lag != nil {
		return
	}
	mark := s.low()
	s.lag = time.AfterFunc(shareLagGrace, func() {
		s.mu.Lock()
		s.lag = nil
		need := mark + max(s.window/shareMinProgress, 1)
		if s.end-s.low() >= s.window && s.low() < need {
			s.pending = 0 // a request that has not attached in a second never will in time
			for r := range s.readers {
				if r.off < need {
					r.detached = true
					delete(s.readers, r)
				}
			}
		}
		s.unlockAndMaybeClose()
	})
}

// sharedReader is one request's view of a sharedStream.
type sharedReader struct {
	s        *sharedStream
	off      int64
	detached bool
	done     bool
}

func (r *sharedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s := r.s
	s.mu.Lock()
	for {
		switch {
		case r.detached:
			s.mu.Unlock()
			return 0, errSharedTooSlow
		case r.done:
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		case r.off < s.end:
			n := copy(p, s.buf[r.off-s.base:])
			r.off += int64(n)
			s.trim()
			s.cond.Broadcast()
			s.mu.Unlock()
			return n, nil
		case s.err != nil:
			err := s.err
			s.mu.Unlock()
			return 0, err
		case !s.reading && s.end-s.low() < s.window:
			s.fill()
		default:
			if !s.reading {
				s.waitForSlow()
			}
			s.cond.Wait()
		}
	}
}

// fill reads the next bytes from src for every reader. Called with mu held;
// the read itself runs unlocked, and no other reader starts one meanwhile.
func (s *sharedStream) fill() {
	s.reading = true
	room := s.window - (s.end - s.low())
	if s.tmp == nil {
		s.tmp = make([]byte, 32<<10)
	}
	buf := s.tmp[:min(int64(len(s.tmp)), room)]
	s.mu.Unlock()
	n, err := s.src.Read(buf)
	s.mu.Lock()
	s.reading = false
	s.buf = append(s.buf, buf[:n]...)
	s.end += int64(n)
	if err != nil {
		s.err = err
	}
	if s.lag != nil && s.end-s.low() < s.window {
		s.lag.Stop()
		s.lag = nil
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
	s.unlockAndMaybeClose()
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
	s := r.s
	s.mu.Lock()
	ended := r.off == s.end && errors.Is(s.err, io.EOF)
	s.mu.Unlock()
	if !ended {
		return nil
	}
	return trailerOf(s.src)
}
