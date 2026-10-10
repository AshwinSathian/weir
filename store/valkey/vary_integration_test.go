//go:build integration

package valkey

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
)

// FR-KEY-10, NFR-3, T6.2, 05 V-1: 64 concurrent writers of different variants
// against a real server lose no reference: exactly the writers that report a
// swap are listed. The test itself stops at maxVariants (the engine owns the
// cap; TestEngineVaryCapHoldsAcrossWriters checks it), so this proves the
// server-side compare-and-set. Covers CoLocateEntries too.
func TestVaryCASConcurrentWriters(t *testing.T) {
	for _, co := range []bool{false, true} {
		t.Run("co-locate "+strconv.FormatBool(co), func(t *testing.T) {
			s, err := New(Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true, CoLocateEntries: co,
				Prefix: "v" + strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatBool(co)})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			const writers, maxVariants = 64, 8
			var k store.Key
			k[0] = 3
			now := time.Now()
			var wg sync.WaitGroup
			var won atomic.Int32
			for i := range writers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var ref store.VariantRef
					ref.Key[0], ref.Key[1], ref.Expires = 1, byte(i), now.Add(time.Hour)
					for {
						cur, err := s.Get(t.Context(), k)
						next := specEntry(now)
						if err == nil {
							next.Variants = append(next.Variants, cur.Variants...)
						}
						if len(next.Variants) >= maxVariants {
							return // full: the engine would delete its variant
						}
						next.Variants = append(next.Variants, ref)
						ok, err := s.SetVarySpec(t.Context(), k, cur, next)
						if err != nil {
							t.Error(err)
							return
						}
						if ok {
							won.Add(1)
							return
						}
					}
				}()
			}
			wg.Wait()
			got, err := s.Get(t.Context(), k)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Variants) != maxVariants || int(won.Load()) != maxVariants {
				t.Fatalf("listed = %d, swaps won = %d, want %d of %d writers", len(got.Variants), won.Load(), maxVariants, writers)
			}
		})
	}
}

// FR-KEY-10, NFR-3: through the engine, 64 languages on a cap of 8 leave at
// most 8 reachable from the server.
func TestEngineVaryCapHoldsAcrossWriters(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 128, 16)
	o.Default(testorigin.Behavior{Delay: 100 * time.Millisecond, Func: func(r *weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Accept-Language"}},
			Body:   io.NopCloser(strings.NewReader(r.Header.Get("Accept-Language")))}, nil
	}})
	cfg := weir.Config{}
	cfg.Forward.Allow = []string{"Accept-Language"}
	cfg.Key.MaxVariants = 8
	e := engineWith(t, cfg)
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := e.Serve(t.Context(), varyReqLang("/c", "l"+strconv.Itoa(i)), o)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	hits := 0
	for i := range 64 {
		resp, err := e.Serve(t.Context(), varyReqLang("/c", "l"+strconv.Itoa(i)), o)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.Cache.Hit {
			hits++
		}
	}
	if hits > 8 || hits == 0 {
		t.Fatalf("reachable variants = %d, want 1..8", hits)
	}
}

// 05 V-1, 05 §2.2: a nil prev swaps over a record past its Expires that the
// server still holds, and loses to a live one, on a real server.
func TestVaryCASNilPrevOverStaleRecord(t *testing.T) {
	s, err := New(Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true,
		Prefix: "vs" + strconv.FormatInt(time.Now().UnixNano(), 36)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var k store.Key
	k[0] = 5
	cl, err := s.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.Encode(specEntry(time.Now().Add(-2*time.Hour), "en")) // expired an hour ago
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.set(t.Context(), s.entryKey(k), stale, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(time.Now(), "fr")); !ok || err != nil {
		t.Fatalf("swap over the stale record = %v, %v", ok, err)
	}
	if ok, _ := s.SetVarySpec(t.Context(), k, nil, specEntry(time.Now(), "de")); ok {
		t.Fatal("nil prev swapped over a live record")
	}
}
