package weir_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
)

func newEngine(t *testing.T, cfg weir.Config) *weir.Engine {
	t.Helper()
	e, err := weir.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func closeEngine(t *testing.T, e *weir.Engine) {
	t.Helper()
	if err := e.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func getReq(path string) *weir.Request {
	return &weir.Request{Method: "GET", Scheme: "https", Host: "example.com", Path: path, Header: http.Header{"Accept": {"*/*"}}}
}

// P3, FR-LCY-1: New with a nil Store builds a no-op store, and Serve reaches
// the origin through the single fetch function.
func TestServePassThroughStub(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Status: 201, Header: http.Header{"X-A": {"1"}}, Body: []byte("hello")})
		e := newEngine(t, weir.Config{})
		defer closeEngine(t, e)

		resp, err := e.Serve(t.Context(), getReq("/a"), o)
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "hello" || resp.StatusCode != http.StatusCreated || resp.Header.Get("X-A") != "1" {
			t.Fatalf("got %d %q %v, err %v", resp.StatusCode, body, resp.Header, err)
		}
		reqs := o.Requests()
		if len(reqs) != 1 || reqs[0].Path != "/a" || reqs[0].Host != "example.com" {
			t.Fatalf("forwarded %+v", reqs)
		}
	})
}

// P3, NFR-2: the fetch function turns origin misbehavior into *OriginError
// and an origin timeout into ErrOriginTimeout (04 §6.7, §1.3).
func TestServeOriginFailures(t *testing.T) {
	tests := []struct {
		name   string
		origin weir.Origin
		want   error
	}{
		{"panic becomes OriginError", weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			panic("boom")
		}), weir.ErrOrigin},
		{"nil nil becomes OriginError", weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return nil, nil
		}), weir.ErrOrigin},
		{"1xx becomes OriginError", weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: 103, Body: http.NoBody}, nil
		}), weir.ErrOrigin},
		{"transport error becomes OriginError", weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return nil, context.DeadlineExceeded // cannot pose as a caller timeout
		}), weir.ErrOrigin},
		{"origin timeout", weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}), weir.ErrOriginTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newEngine(t, weir.Config{Timeouts: weir.TimeoutsConfig{Origin: time.Second}})
				defer closeEngine(t, e)
				resp, err := e.Serve(t.Context(), getReq("/"), tt.origin)
				if resp != nil || !errors.Is(err, tt.want) {
					t.Fatalf("got %v, %v; want %v", resp, err, tt.want)
				}
				if errors.Is(tt.want, weir.ErrOrigin) {
					if _, ok := errors.AsType[*weir.OriginError](err); !ok {
						t.Fatalf("err %T is not *OriginError", err)
					}
				}
			})
		})
	}
}

// 01 §4 error table (no requirement ID): a canceled request context is the
// caller's error, context.Canceled, not the origin's.
func TestServeCallerCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, weir.Config{})
		defer closeEngine(t, e)
		ctx, cancel := context.WithCancel(t.Context())
		origin := weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
			cancel()
			<-ctx.Done()
			return nil, errors.New("aborted")
		})
		if _, err := e.Serve(ctx, getReq("/"), origin); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// FR-LIM-1: the origin timeout still bounds a streamed body after Serve
// returns.
func TestServeStreamBoundedByOriginTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Body: []byte("x"), BodyDelay: time.Hour})
		e := newEngine(t, weir.Config{Timeouts: weir.TimeoutsConfig{Origin: time.Second}})
		defer closeEngine(t, e)
		resp, err := e.Serve(t.Context(), getReq("/"), o)
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
		defer resp.Body.Close()
		if _, err := io.ReadAll(resp.Body); err == nil {
			t.Fatal("read of an hour-delayed body succeeded under a 1s origin timeout")
		}
	})
}

// FR-LCY-2: Close waits for engine goroutines until its context ends, then
// cancels them and waits again; none survive Close.
func TestCloseStopsGoroutines(t *testing.T) {
	t.Run("waits for goroutines that finish in the grace period", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := newEngine(t, weir.Config{})
			done := false
			weir.GoBackground(e, func(context.Context) {
				time.Sleep(time.Second)
				done = true
			})
			if err := e.Close(t.Context()); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if !done {
				t.Fatal("Close returned before the goroutine finished")
			}
		})
	})
	t.Run("cancels goroutines after the grace period", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := newEngine(t, weir.Config{})
			stopped := false
			weir.GoBackground(e, func(ctx context.Context) {
				<-ctx.Done()
				stopped = true
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			start := time.Now()
			if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close = %v, want DeadlineExceeded", err)
			}
			if !stopped || time.Since(start) != time.Second {
				t.Fatalf("stopped=%v after %v", stopped, time.Since(start))
			}
		})
	})
}

// FR-LCY-2: goBackground racing Close either refuses to start or is waited
// for; no goroutine it starts runs after Close returns.
func TestCloseRacingGoBackground(t *testing.T) {
	for range 50 {
		synctest.Test(t, func(t *testing.T) {
			e := newEngine(t, weir.Config{})
			var running atomic.Int32
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					weir.GoBackground(e, func(context.Context) {
						running.Add(1)
						time.Sleep(time.Millisecond)
						running.Add(-1)
					})
				})
			}
			if err := e.Close(t.Context()); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if n := running.Load(); n != 0 {
				t.Fatalf("%d goroutines running after Close", n)
			}
			wg.Wait()
		})
	}
}

// FR-LCY-2: Serve after Close fails with ErrClosed and never reaches the origin.
func TestServeAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		e := newEngine(t, weir.Config{})
		closeEngine(t, e)
		resp, err := e.Serve(t.Context(), getReq("/"), o)
		if resp != nil || !errors.Is(err, weir.ErrClosed) {
			t.Fatalf("got %v, %v; want ErrClosed", resp, err)
		}
		if o.TotalCalls() != 0 {
			t.Fatalf("origin called %d times", o.TotalCalls())
		}
		if err := e.Close(t.Context()); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})
}

// nilStore is a caller-owned store that reports a size limit.
type nilStore struct {
	max    int64
	closed bool
}

func (*nilStore) Get(context.Context, store.Key) (*store.Entry, error) { return nil, store.ErrNotFound }
func (*nilStore) Set(context.Context, store.Key, *store.Entry) error   { return nil }
func (*nilStore) Delete(context.Context, store.Key) error              { return nil }
func (*nilStore) SetEpoch(context.Context, store.Tag, store.Epoch) error {
	return nil
}
func (*nilStore) NewestEpoch(context.Context, []store.Tag, time.Time) (store.Epoch, bool, error) {
	return store.Epoch{}, false, nil
}
func (*nilStore) Info() store.Info        { return store.Info{Name: "nil"} }
func (s *nilStore) Close() error          { s.closed = true; return nil }
func (*nilStore) Bytes() int64            { return 0 }
func (s *nilStore) MaxObjectBytes() int64 { return s.max }

// FR-LCY-1: New rejects a typed-nil Store and a MaxObjectBytes the store
// cannot admit.
func TestNewRejectsStore(t *testing.T) {
	var typedNil *nilStore
	if _, err := weir.New(weir.Config{Store: typedNil}); !errors.Is(err, weir.ErrInvalidConfig) {
		t.Errorf("typed-nil Store: err = %v", err)
	}
	s := &nilStore{max: 1 << 10}
	if _, err := weir.New(weir.Config{Store: s, Storable: weir.StorableConfig{MaxObjectBytes: 1 << 11}}); !errors.Is(err, weir.ErrInvalidConfig) {
		t.Errorf("MaxObjectBytes above store max: err = %v", err)
	}
	e, err := weir.New(weir.Config{Store: s, Storable: weir.StorableConfig{MaxObjectBytes: 1 << 10}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Close(context.Background()); err != nil || s.closed {
		t.Fatalf("Close = %v, store closed = %v; caller-owned store must stay open", err, s.closed)
	}
}

// 01 §4: Cache on an origin's Response is ignored; an origin cannot claim a
// hit. A body-less response keeps http.NoBody so adapters can skip the body.
func TestServeIgnoresOriginCacheInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, weir.Config{})
		defer closeEngine(t, e)
		origin := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusNoContent, Cache: weir.CacheInfo{Hit: true, Stored: true, Detail: "forged"}}, nil
		})
		resp, err := e.Serve(t.Context(), getReq("/"), origin)
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
		defer resp.Body.Close()
		if resp.Cache != (weir.CacheInfo{}) {
			t.Errorf("Cache = %+v, want zero", resp.Cache)
		}
		if resp.Body != http.NoBody || resp.Header == nil {
			t.Errorf("Body = %T, Header nil = %v; want http.NoBody and a non-nil Header", resp.Body, resp.Header == nil)
		}
	})
}

// R-3: New warns when forwarding sends unkeyed credentials to the origin.
func TestNewWarnsOnCredentialForwarding(t *testing.T) {
	tests := []struct {
		name string
		fwd  weir.ForwardConfig
		want int
	}{
		{"strict with no allow list is quiet", weir.ForwardConfig{}, 0},
		{"ForwardAll warns", weir.ForwardConfig{Mode: weir.ForwardAll}, 1},
		{"credential headers in Allow warn", weir.ForwardConfig{Allow: []string{"cookie", "Authorization", "proxy-authorization", "X-Trace"}}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			e := newEngine(t, weir.Config{Forward: tt.fwd, Logger: log})
			closeEngine(t, e)
			if got := strings.Count(buf.String(), "level=WARN"); got != tt.want {
				t.Fatalf("%d warnings, want %d:\n%s", got, tt.want, buf.String())
			}
		})
	}
}
