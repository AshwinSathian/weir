package valkey

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// fakeKV is an in-memory stand-in for the three entry commands. It records
// the expiry each SET carried and never expires anything itself.
type fakeKV struct {
	mu   sync.Mutex
	vals map[string][]byte
	pxat map[string]int64
	sets int
	err  error // returned by every command when set
}

func (f *fakeClient) get(_ context.Context, key string) ([]byte, error) {
	f.kv.mu.Lock()
	err, v, ok := f.kv.err, f.kv.vals[key], false
	if err == nil {
		_, ok = f.kv.vals[key]
	}
	f.kv.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, valkey.Nil
	}
	if h := f.vary.afterGet; h != nil {
		h() // a concurrent writer acts between this read and the caller's next call
	}
	return v, nil
}

func (f *fakeClient) set(_ context.Context, key string, val []byte, pxat int64) error {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return f.kv.err
	}
	if f.kv.vals == nil {
		f.kv.vals, f.kv.pxat = map[string][]byte{}, map[string]int64{}
	}
	f.kv.vals[key], f.kv.pxat[key] = val, pxat
	f.kv.sets++
	return nil
}

func (f *fakeClient) del(_ context.Context, key string) error {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return f.kv.err
	}
	delete(f.kv.vals, key)
	return nil
}

func connected(t *testing.T, mutate func(*Config)) (*Store, *fakeClient) {
	t.Helper()
	cl := okClient()
	s := newFake(t, mutate, &fakeDialer{cl: cl})
	t.Cleanup(func() { _ = s.Close() })
	return s, cl
}

func testEntry(now time.Time) *store.Entry {
	return &store.Entry{
		Kind:         store.KindResponse,
		StoredAt:     now,
		RequestTime:  now,
		ResponseTime: now,
		Date:         now,
		Status:       200,
		Header:       http.Header{"Content-Type": {"text/plain"}},
		Body:         []byte("hello"),
		Expires:      now.Add(time.Hour),
	}
}

// S-2, INV-1, 05 §7: the key layout is part of the format; operators run two
// deployments on one server by prefix, and cluster mode needs the hash tag
// to co-locate entries with epochs.
func TestKeyLayout(t *testing.T) {
	var k store.Key
	k[0], k[31] = 0xab, 0xcd
	h := hex.EncodeToString(k[:])
	for _, tc := range []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"default prefix", nil, "weir:" + h},
		{"custom prefix", func(c *Config) { c.Prefix = "edge-1" }, "edge-1:" + h},
		{"co-located entries carry the epoch hash tag", func(c *Config) { c.CoLocateEntries = true }, "weir:{e}:" + h},
		{"co-located with custom prefix and tag", func(c *Config) {
			c.CoLocateEntries, c.Prefix, c.HashTag = true, "p", "tg"
		}, "p:{tg}:" + h},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cl := connected(t, tc.mut)
			if err := s.Set(t.Context(), k, testEntry(time.Now())); err != nil {
				t.Fatal(err)
			}
			if _, ok := cl.kv.vals[tc.want]; !ok {
				t.Fatalf("no value at %q; keys: %v", tc.want, cl.kv.vals)
			}
			if _, err := s.Get(t.Context(), k); err != nil {
				t.Fatalf("Get after Set: %v", err)
			}
		})
	}
}

// E-11, 05 §2.3: Expires is clamped to RequestTime + MaxRetention and sent
// as PXAT in milliseconds; a past Expires writes nothing.
func TestSetClampsToMaxRetention(t *testing.T) {
	var k store.Key
	now := time.Now()
	t.Run("expires beyond retention is clamped from the request time", func(t *testing.T) {
		s, cl := connected(t, func(c *Config) { c.MaxRetention = time.Hour })
		e := testEntry(now)
		e.RequestTime = now.Add(-10 * time.Minute)
		e.Expires = now.Add(48 * time.Hour)
		if err := s.Set(t.Context(), k, e); err != nil {
			t.Fatal(err)
		}
		want := e.RequestTime.Add(time.Hour).UnixMilli()
		if got := cl.kv.pxat[s.entryKey(k)]; got != want {
			t.Fatalf("PXAT = %d, want %d", got, want)
		}
	})
	t.Run("expires inside retention is kept", func(t *testing.T) {
		s, cl := connected(t, nil)
		e := testEntry(now)
		e.Expires = now.Add(time.Minute)
		if err := s.Set(t.Context(), k, e); err != nil {
			t.Fatal(err)
		}
		if got := cl.kv.pxat[s.entryKey(k)]; got != e.Expires.UnixMilli() {
			t.Fatalf("PXAT = %d, want %d", got, e.Expires.UnixMilli())
		}
	})
	t.Run("a request time in the future counts from now", func(t *testing.T) {
		s, cl := connected(t, func(c *Config) { c.MaxRetention = time.Hour })
		e := testEntry(now)
		e.RequestTime = now.Add(24 * time.Hour)
		e.Expires = now.Add(48 * time.Hour)
		before := time.Now()
		if err := s.Set(t.Context(), k, e); err != nil {
			t.Fatal(err)
		}
		after := time.Now()
		got, ok := cl.kv.pxat[s.entryKey(k)]
		if !ok {
			t.Fatal("nothing was written")
		}
		if lo, hi := before.Add(time.Hour).UnixMilli(), after.Add(time.Hour).UnixMilli(); got < lo || got > hi {
			t.Fatalf("PXAT %d outside [%d, %d]", got, lo, hi)
		}
	})
	t.Run("past expires is a no-op", func(t *testing.T) {
		s, cl := connected(t, nil)
		e := testEntry(now)
		e.Expires = now.Add(-time.Second)
		if err := s.Set(t.Context(), k, e); err != nil {
			t.Fatal(err)
		}
		if cl.kv.sets != 0 {
			t.Fatalf("%d SETs issued for an expired record", cl.kv.sets)
		}
	})
	t.Run("a record the codec rejects is declined without an error", func(t *testing.T) {
		s, cl := connected(t, nil)
		e := testEntry(now)
		e.Status = 5000
		if err := s.Set(t.Context(), k, e); err != nil {
			t.Fatalf("Set = %v, want nil (S-4)", err)
		}
		if cl.kv.sets != 0 {
			t.Fatal("an unencodable record was sent")
		}
	})
	// S-4: Set leaves the new record or nothing, never an older one.
	t.Run("a record the codec rejects removes the one it would replace", func(t *testing.T) {
		s, _ := connected(t, nil)
		if err := s.Set(t.Context(), k, testEntry(now)); err != nil {
			t.Fatal(err)
		}
		bad := testEntry(now)
		bad.Status = 5000
		if err := s.Set(t.Context(), k, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Get = %v, want ErrNotFound", err)
		}
	})
}

// T-21, 05 §6: a value another writer left at an entry key that does not
// decode is a miss for the engine (ErrUnavailable), never a panic or a
// partial entry.
func TestGetDecodeFailureIsUnavailable(t *testing.T) {
	var k store.Key
	for name, v := range map[string][]byte{
		"empty":         {},
		"not a record":  []byte("hello world, not a record"),
		"truncated":     {'W', 'E', 'I', 'R', 1},
		"wrong version": {'W', 'E', 'I', 'R', 99, 1, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			s, cl := connected(t, nil)
			_ = cl.set(t.Context(), s.entryKey(k), v, 1)
			e, err := s.Get(t.Context(), k)
			if e != nil || !errors.Is(err, store.ErrUnavailable) {
				t.Fatalf("Get = %v, %v; want nil, ErrUnavailable", e, err)
			}
		})
	}
}

// S-3, 05 §7: valkey.Nil is ErrNotFound for Get only; every other error is
// ErrUnavailable. Delete of a missing key succeeds.
func TestEntryCommandErrors(t *testing.T) {
	var k store.Key
	t.Run("missing key is not found", func(t *testing.T) {
		s, _ := connected(t, nil)
		if _, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Get = %v, want ErrNotFound", err)
		}
	})
	t.Run("server errors map to unavailable", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.kv.err = errors.New("OOM command not allowed")
		if _, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("Get = %v", err)
		}
		if err := s.Set(t.Context(), k, testEntry(time.Now())); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("Set = %v", err)
		}
		if err := s.Delete(t.Context(), k); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("Delete = %v", err)
		}
	})
	t.Run("delete removes and tolerates a missing key", func(t *testing.T) {
		s, _ := connected(t, nil)
		if err := s.Delete(t.Context(), k); err != nil {
			t.Fatal(err)
		}
		if err := s.Set(t.Context(), k, testEntry(time.Now())); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(t.Context(), k); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Get after Delete = %v", err)
		}
	})
}

// 05 §2.2: a record past Expires is not found even if the server still has
// it (its clock differs from ours).
func TestGetPastExpiresIsNotFound(t *testing.T) {
	var k store.Key
	s, cl := connected(t, nil)
	e := testEntry(time.Now())
	e.Expires = time.Now().Add(-time.Second)
	v, err := store.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	_ = cl.set(t.Context(), s.entryKey(k), v, 1)
	if _, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}
