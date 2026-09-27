package testorigin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
)

func get(path string) *weir.Request {
	return &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: path, Header: http.Header{}}
}

// fetchAsync runs Fetch in a goroutine and reports the error on the returned channel.
func fetchAsync(ctx context.Context, o *Origin, path string) <-chan error {
	done := make(chan error, 1)
	go func() {
		resp, err := o.Fetch(ctx, get(path))
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	return done
}

func TestServesBehavior(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Route("/a", Behavior{Status: http.StatusNotFound, Header: http.Header{"X-A": {"1"}}, Body: []byte("a")})
		o.Default(Behavior{Body: []byte("default")})

		resp, err := o.Fetch(t.Context(), get("/a"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-A") != "1" || string(body) != "a" {
			t.Errorf("/a = %d %v %q", resp.StatusCode, resp.Header, body)
		}
		resp.Header.Set("X-A", "mutated")

		resp, err = o.Fetch(t.Context(), get("/a"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-A") != "1" {
			t.Errorf("response header shares the behavior's map")
		}

		resp, err = o.Fetch(t.Context(), get("/other"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "default" {
			t.Errorf("/other = %d %q, want 200 default", resp.StatusCode, body)
		}

		if o.Calls("/a") != 2 || o.Calls("/other") != 1 || o.TotalCalls() != 3 {
			t.Errorf("calls /a=%d /other=%d total=%d", o.Calls("/a"), o.Calls("/other"), o.TotalCalls())
		}
		if reqs := o.Requests(); len(reqs) != 3 || reqs[2].Path != "/other" {
			t.Errorf("Requests() = %v", reqs)
		}
		o.Reset()
		if o.TotalCalls() != 0 || len(o.Requests()) != 0 || o.MaxInflight() != 0 {
			t.Errorf("Reset left state behind")
		}
	})
}

func TestGateBlocksUntilClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := New()
		o.Default(Behavior{Gate: gate})

		done := fetchAsync(t.Context(), o, "/")
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("Fetch returned before the gate opened: %v", err)
		default:
		}
		close(gate)
		if err := <-done; err != nil {
			t.Fatalf("Fetch after gate = %v", err)
		}
	})
}

func TestGateUnblocksOnContextDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Gate: make(chan struct{})})

		ctx, cancel := context.WithCancel(t.Context())
		done := fetchAsync(ctx, o, "/")
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Fetch = %v, want context.Canceled", err)
		}
	})
}

func TestDelayTakesFakeTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Delay: time.Hour})

		start := time.Now()
		resp, err := o.Fetch(t.Context(), get("/"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := time.Since(start); got != time.Hour {
			t.Errorf("Fetch took %v, want 1h of fake time", got)
		}

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if _, err := o.Fetch(ctx, get("/")); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Fetch with short deadline = %v, want DeadlineExceeded", err)
		}
	})
}

func TestBodyDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Body: []byte("x"), BodyDelay: time.Minute})

		start := time.Now()
		resp, err := o.Fetch(t.Context(), get("/"))
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 0 {
			t.Errorf("headers delayed by BodyDelay")
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "x" || time.Since(start) != time.Minute {
			t.Errorf("body %q err %v after %v, want x after 1m", body, err, time.Since(start))
		}
	})
}

func TestPanicBehaviorPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Panic: true})
		defer func() {
			if recover() == nil {
				t.Error("Fetch did not panic")
			}
		}()
		_, _ = o.Fetch(t.Context(), get("/"))
	})
}

func TestErrAndDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		o := New()
		o.Default(Behavior{Err: boom})
		if _, err := o.Fetch(t.Context(), get("/")); !errors.Is(err, boom) {
			t.Errorf("Fetch = %v, want boom", err)
		}

		o.Default(Behavior{})
		o.SetDown(true)
		if _, err := o.Fetch(t.Context(), get("/")); !errors.Is(err, ErrDown) {
			t.Errorf("Fetch while down = %v, want ErrDown", err)
		}
		o.SetDown(false)
		resp, err := o.Fetch(t.Context(), get("/"))
		if err != nil {
			t.Fatalf("Fetch after SetDown(false) = %v", err)
		}
		resp.Body.Close()
	})
}

func TestTruncateFailsBodyRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Body: []byte("0123456789"), Truncate: 4})
		resp, err := o.Fetch(t.Context(), get("/"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "0123" || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("read %q, %v; want 0123, ErrUnexpectedEOF", body, err)
		}
	})
}

func TestFuncHasFullControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		o.Default(Behavior{Status: http.StatusTeapot, Func: func(r *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusAccepted, Header: http.Header{"X-Path": {r.Path}}, Body: http.NoBody}, nil
		}})
		resp, err := o.Fetch(t.Context(), get("/f"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Path") != "/f" {
			t.Errorf("got %d %v, want 202 from Func with X-Path /f", resp.StatusCode, resp.Header)
		}
	})
}

// INV-1: Requests records what the origin saw, unchanged by later caller edits.
func TestRequestsAreSnapshots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New()
		req := get("/")
		req.Header.Set("X-K", "v")
		resp, err := o.Fetch(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		req.Header.Set("X-K", "changed")
		if got := o.Requests()[0].Header.Get("X-K"); got != "v" {
			t.Errorf("recorded header = %q, want v", got)
		}
	})
}

// INV-7: high-water marks, total and per partition.
func TestMaxInflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := New()
		o.Default(Behavior{Gate: gate})

		var dones []<-chan error
		for _, p := range []string{"/a", "/a", "/a", "/b"} {
			dones = append(dones, fetchAsync(t.Context(), o, p))
		}
		synctest.Wait()
		close(gate)
		for _, d := range dones {
			if err := <-d; err != nil {
				t.Fatal(err)
			}
		}
		if o.MaxInflight() != 4 || o.MaxInflightPartition() != 3 {
			t.Errorf("MaxInflight=%d partition=%d, want 4 and 3", o.MaxInflight(), o.MaxInflightPartition())
		}
	})
}

// fakeTB records failures instead of failing the real test.
type fakeTB struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fakeTB) failures() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.errors)
}

// INV-7: NewChecked fails the test when either concurrency bound is exceeded.
func TestNewCheckedFailsOnOverConcurrency(t *testing.T) {
	tests := []struct {
		name         string
		paths        []string
		wantFailures int
	}{
		{"within bounds", []string{"/a", "/a", "/b"}, 0},
		{"over total", []string{"/a", "/b", "/c", "/d"}, 1},
		{"over partition", []string{"/a", "/a", "/a"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tb := &fakeTB{TB: t}
				gate := make(chan struct{})
				o := NewChecked(tb, 3, 2)
				o.Default(Behavior{Gate: gate})

				var dones []<-chan error
				for _, p := range tt.paths {
					dones = append(dones, fetchAsync(t.Context(), o, p))
					synctest.Wait()
				}
				close(gate)
				var errs int
				for _, d := range dones {
					if err := <-d; errors.Is(err, ErrOverConcurrency) {
						errs++
					}
				}
				if tb.failures() != tt.wantFailures || errs != tt.wantFailures {
					t.Errorf("failures=%d errors=%d, want %d", tb.failures(), errs, tt.wantFailures)
				}
			})
		})
	}
}
