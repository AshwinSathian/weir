package valkey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.Store = (*Store)(nil)

// errPolicy marks a failed maxmemory-policy check; it is wrapped together
// with store.ErrUnavailable.
var errPolicy = errors.New("unsafe maxmemory-policy")

// errClosed is returned by every method after Close.
var errClosed = fmt.Errorf("store: valkey: %w: closed", store.ErrUnavailable)

// reconnectEvery is the minimum gap between connection attempts. Calls in
// between fail fast with the last error (FR-STF-2, 05 §7).
const reconnectEvery = time.Second

// Store is a store.Store backed by a Valkey server or cluster. It is safe
// for concurrent use.
type Store struct {
	cfg  Config
	dial dialFunc

	closing chan struct{}  // closed by Close so a dial in flight stops early
	wg      sync.WaitGroup // dial goroutines (FR-LCY-2)

	mu         sync.Mutex // guards the fields below; never held across a dial
	cl         client     // non-nil once connected and the policy check passed
	closed     bool
	connecting chan struct{} // non-nil while one caller is dialing (single flight)
	lastTry    time.Time
	lastErr    error
}

// New validates cfg and returns a Store. It does not connect: an unreachable
// server must open the store breaker, not stop the process (FR-STF-2). The
// client is built on first use.
func New(cfg Config) (*Store, error) {
	return newStore(cfg, dialValkey)
}

func newStore(cfg Config, dial dialFunc) (*Store, error) {
	cfg, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	return &Store{cfg: cfg, dial: dial, closing: make(chan struct{})}, nil
}

// Info reports a remote store, so the engine applies Timeouts.Store and the
// store breaker.
func (s *Store) Info() store.Info { return store.Info{Name: "valkey", Remote: true} }

// Close stops dials in flight, closes the client and is safe to call twice.
// No client is built after it. valkey.NewClient takes no context, so Close
// waits for a real dial in flight: bounded by valkey-go's own dial and
// handshake timeouts (each CallTimeout), per seed address in cluster mode.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait() // a second Close returns only once everything has stopped
		return nil
	}
	s.closed = true
	cl := s.cl
	s.cl = nil
	s.mu.Unlock()
	close(s.closing)
	if cl != nil {
		cl.close()
	}
	s.wg.Wait()
	return nil
}

// callCtx applies CallTimeout when ctx has no deadline: a remote call must
// never wait forever (S-2).
func (s *Store) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.CallTimeout)
}

// acquire returns a connected client. It checks the context and the closed
// flag before it dials, dials at most once per reconnectEvery, and lets one
// caller dial while the others wait on a channel, so no lock is held across
// the dial or the policy check (P8).
func (s *Store) acquire(ctx context.Context) (client, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, mapError(err)
		}
		s.mu.Lock()
		switch {
		case s.closed:
			s.mu.Unlock()
			return nil, errClosed
		case s.cl != nil:
			cl := s.cl
			s.mu.Unlock()
			return cl, nil
		case s.connecting != nil:
			wait := s.connecting
			s.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, mapError(ctx.Err())
			}
		case !s.lastTry.IsZero() && time.Since(s.lastTry) < reconnectEvery:
			err := s.lastErr
			s.mu.Unlock()
			return nil, err
		}
		done := make(chan struct{})
		s.connecting = done
		s.wg.Add(1) // under mu, so Close cannot Wait first
		s.mu.Unlock()
		return s.connect(ctx, done)
	}
}

type dialResult struct {
	cl  client
	err error
}

// connect dials and checks the policy in a goroutine the store owns. The
// goroutine installs the client itself, so a caller whose context ends can
// stop waiting without leaking what the dial returns, and Close always
// reaches it (no client is built after Close).
func (s *Store) connect(ctx context.Context, done chan struct{}) (client, error) {
	res := make(chan dialResult, 1)
	go func() {
		defer s.wg.Done()
		cl, err := s.dialChecked(ctx)
		s.mu.Lock()
		s.connecting = nil
		s.lastTry = time.Now() // the gap runs from the end of an attempt
		close(done)
		switch {
		case err != nil:
			s.lastErr = err
		case s.closed:
			err = errClosed
			s.lastErr = err
		default:
			s.cl = cl
		}
		s.mu.Unlock()
		if err != nil && cl != nil {
			cl.close()
			cl = nil
		}
		res <- dialResult{cl, err}
	}()
	select {
	case r := <-res:
		return r.cl, r.err
	case <-ctx.Done():
		return nil, mapError(ctx.Err())
	}
}

func (s *Store) dialChecked(ctx context.Context) (client, error) {
	// The dial is shared by every waiting caller, so it must not die with
	// the first caller's deadline; callers only stop waiting.
	dctx, cancel := s.callCtx(context.WithoutCancel(ctx))
	defer cancel()
	dctx, stop := context.WithCancel(dctx)
	defer stop()
	s.wg.Add(1) // the caller's goroutine is counted, so the counter is above zero
	go func() { // ends with dctx; Close ends a dial in flight
		defer s.wg.Done()
		select {
		case <-s.closing:
			stop()
		case <-dctx.Done():
		}
	}()
	cl, err := s.dial(dctx, s.cfg)
	if err != nil {
		return nil, mapError(err)
	}
	if !s.cfg.SkipPolicyCheck {
		p, err := cl.policies(dctx)
		if err == nil {
			err = checkPolicy(p)
		}
		if err != nil {
			cl.close()
			return nil, mapError(err)
		}
	}
	return cl, nil
}

// errNotYet marks calls that arrive with the next cards (P25-03 onward).
var errNotYet = fmt.Errorf("store: valkey: %w: not implemented", store.ErrUnavailable)

// ponytail: the epoch methods are placeholders until P25-03; they already
// honor the connect path so its behavior is testable.

// SetEpoch is not implemented yet (P25-03).
func (s *Store) SetEpoch(ctx context.Context, _ store.Tag, _ store.Epoch) error {
	if _, err := s.acquire(ctx); err != nil {
		return err
	}
	return errNotYet
}

// NewestEpoch is not implemented yet (P25-03).
func (s *Store) NewestEpoch(ctx context.Context, _ []store.Tag, _ time.Time) (store.Epoch, bool, error) {
	_, err := s.acquire(ctx)
	if err == nil {
		err = errNotYet
	}
	return store.Epoch{}, false, err
}
