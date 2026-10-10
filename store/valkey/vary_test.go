package valkey

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.VarySetter = (*Store)(nil)

// evalVarySet is the fake of the compare-and-set script: it holds the same
// lock as the other fake commands, so the compare and the write are atomic as
// they are on the server.
func (f *fakeClient) evalVarySet(ctx context.Context, key string, prev []byte, hasPrev bool, val []byte, pxat int64) (bool, error) {
	ok, err := f.evalVarySetLocked(key, prev, hasPrev, val, pxat)
	if h := f.vary.afterEval; h != nil {
		h()
	}
	return ok, err
}

func (f *fakeClient) evalVarySetLocked(key string, prev []byte, hasPrev bool, val []byte, pxat int64) (bool, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return false, f.kv.err
	}
	cur, ok := f.kv.vals[key]
	if hasPrev != ok || (ok && !bytes.Equal(cur, prev)) {
		return false, nil
	}
	if f.kv.vals == nil {
		f.kv.vals, f.kv.pxat = map[string][]byte{}, map[string]int64{}
	}
	f.kv.vals[key], f.kv.pxat[key] = val, pxat
	f.kv.sets++
	return true, nil
}

// fakeVary holds test hooks that let a concurrent writer act at a chosen
// point of SetVarySpec's fallback.
type fakeVary struct {
	afterGet  func() // after a GET found its key
	afterEval func() // after a compare-and-set script ran
}

func specEntry(now time.Time, langs ...string) *store.Entry {
	e := &store.Entry{Kind: store.KindVarySpec, StoredAt: now, RequestTime: now, VaryNames: []string{"Accept-Language"}, Expires: now.Add(time.Hour)}
	for i, l := range langs {
		var k store.Key
		copy(k[:], l)
		k[31] = byte(i)
		e.Variants = append(e.Variants, store.VariantRef{Key: k, Expires: now.Add(time.Hour)})
	}
	return e
}

// FR-KEY-10, 05 V-1, T6.2: SetVarySpec swaps only while the stored record is
// still the one the caller read.
func TestSetVarySpec(t *testing.T) {
	var k store.Key
	k[0] = 7
	now := time.Now()
	t.Run("nil prev swaps into an empty key and once only", func(t *testing.T) {
		s, _ := connected(t, nil)
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "en")); !ok || err != nil {
			t.Fatalf("first swap = %v, %v", ok, err)
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); ok {
			t.Fatal("second swap with a nil prev succeeded over a live record")
		}
	})
	t.Run("a prev read back from Get matches the stored bytes", func(t *testing.T) {
		s, _ := connected(t, nil)
		if err := s.Set(t.Context(), k, specEntry(now, "en")); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(t.Context(), k)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := s.SetVarySpec(t.Context(), k, got, specEntry(now, "en", "fr")); !ok || err != nil {
			t.Fatalf("swap with the record Get returned = %v, %v (re-encoding must reproduce the stored bytes)", ok, err)
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, got, specEntry(now, "en", "de")); ok {
			t.Fatal("swap with a replaced record succeeded")
		}
		cur, _ := s.Get(t.Context(), k)
		if len(cur.Variants) != 2 {
			t.Fatalf("variants = %d, want the winner's 2", len(cur.Variants))
		}
	})
	t.Run("a non-nil prev does not match an absent key", func(t *testing.T) {
		s, cl := connected(t, nil)
		if ok, _ := s.SetVarySpec(t.Context(), k, specEntry(now, "en"), specEntry(now, "fr")); ok {
			t.Fatal("swapped into an empty key with a prev")
		}
		if cl.kv.sets != 0 {
			t.Fatal("a failed swap stored a record")
		}
	})
	t.Run("nil prev replaces a record past its Expires", func(t *testing.T) {
		// Get reports such a record as absent while the server still holds
		// it, so the engine passes nil; the swap must not fail forever.
		s, cl := connected(t, nil)
		old := specEntry(now.Add(-2*time.Hour), "en") // expired an hour ago
		b, err := store.Encode(old)
		if err != nil {
			t.Fatal(err)
		}
		_ = cl.set(t.Context(), s.entryKey(k), b, time.Now().Add(time.Hour).UnixMilli())
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); !ok || err != nil {
			t.Fatalf("swap over an expired record = %v, %v", ok, err)
		}
	})
	t.Run("nil prev replaces a record that does not decode", func(t *testing.T) {
		s, cl := connected(t, nil)
		_ = cl.set(t.Context(), s.entryKey(k), []byte("not a record"), time.Now().Add(time.Hour).UnixMilli())
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); !ok || err != nil {
			t.Fatalf("swap over an undecodable record = %v, %v", ok, err)
		}
		if got, err := s.Get(t.Context(), k); err != nil || len(got.Variants) != 1 {
			t.Fatalf("record after the swap = %v, %v", got, err)
		}
	})
	t.Run("nil prev loses to a live record and leaves it untouched", func(t *testing.T) {
		s, cl := connected(t, nil)
		if err := s.Set(t.Context(), k, specEntry(now, "en")); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); ok {
			t.Fatal("swapped over a live record")
		}
		if cl.kv.sets != 1 {
			t.Fatalf("sets = %d, the live record must be untouched", cl.kv.sets)
		}
	})
	t.Run("nil prev swaps when the record vanishes after the script lost", func(t *testing.T) {
		s, cl := connected(t, nil)
		if err := s.Set(t.Context(), k, specEntry(now, "en")); err != nil {
			t.Fatal(err)
		}
		cl.vary.afterEval = func() { // the server expired it after the script said no
			cl.vary.afterEval = nil
			_ = cl.del(t.Context(), s.entryKey(k))
		}
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); !ok || err != nil {
			t.Fatalf("swap after the record vanished = %v, %v", ok, err)
		}
	})
	t.Run("a server error on the re-read is ErrUnavailable", func(t *testing.T) {
		s, cl := connected(t, nil)
		if err := s.Set(t.Context(), k, specEntry(now, "en")); err != nil {
			t.Fatal(err)
		}
		cl.vary.afterEval = func() { // only the re-read fails
			cl.vary.afterEval = nil
			cl.kv.mu.Lock()
			cl.kv.err = fmt.Errorf("boom")
			cl.kv.mu.Unlock()
		}
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); ok || !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("swap = %v, %v, want false, ErrUnavailable", ok, err)
		}
	})
	t.Run("the second swap loses to a writer that replaced the stale record", func(t *testing.T) {
		s, cl := connected(t, nil)
		b, err := store.Encode(specEntry(now.Add(-2*time.Hour), "en")) // expired an hour ago
		if err != nil {
			t.Fatal(err)
		}
		_ = cl.set(t.Context(), s.entryKey(k), b, time.Now().Add(time.Hour).UnixMilli())
		cl.vary.afterGet = func() { // a concurrent writer wins between the re-read and the second script
			cl.vary.afterGet = nil
			if err := s.Set(t.Context(), k, specEntry(now, "de")); err != nil {
				t.Error(err)
			}
		}
		if ok, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "fr")); ok || err != nil {
			t.Fatalf("swap = %v, %v, want a lost swap", ok, err)
		}
		if got, err := s.Get(t.Context(), k); err != nil || len(got.Variants) != 1 || got.Variants[0].Key[0] != 'd' {
			t.Fatalf("record = %v, %v, want the concurrent writer's", got, err)
		}
	})
	t.Run("a next that is past its expiry is declined and leaves the record", func(t *testing.T) {
		s, _ := connected(t, nil)
		a := specEntry(now, "en")
		if err := s.Set(t.Context(), k, a); err != nil {
			t.Fatal(err)
		}
		dead := specEntry(now, "fr")
		dead.Expires = now.Add(-time.Minute)
		if ok, err := s.SetVarySpec(t.Context(), k, nil, dead); !ok || err != nil {
			t.Fatalf("declined swap = %v, %v, want true, nil (S-4)", ok, err)
		}
		if got, err := s.Get(t.Context(), k); err != nil || len(got.Variants) != 1 {
			t.Fatalf("record after a declined swap = %v, %v", got, err)
		}
	})
	t.Run("server errors map to ErrUnavailable and a closed store refuses", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.kv.err = fmt.Errorf("boom")
		if _, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "en")); err == nil {
			t.Fatal("no error from a failing server")
		}
		_ = s.Close()
		if _, err := s.SetVarySpec(t.Context(), k, nil, specEntry(now, "en")); err == nil {
			t.Fatal("closed store accepted a swap")
		}
	})
}

// FR-KEY-10, NFR-3, T6.2: concurrent writers that each re-read and retry never
// lose a variant reference.
func TestSetVarySpecConcurrentWritersLoseNothing(t *testing.T) {
	var k store.Key
	k[0] = 9
	s, _ := connected(t, nil)
	now := time.Now()
	const writers = 32
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ref store.VariantRef
			ref.Key[0], ref.Key[1], ref.Expires = 1, byte(i), now.Add(time.Hour)
			for range 1000 { // a regression fails the count below instead of hanging
				cur, err := s.Get(t.Context(), k)
				next := specEntry(now)
				if err == nil {
					next.Variants = append(next.Variants, cur.Variants...)
				}
				next.Variants = append(next.Variants, ref)
				if ok, err := s.SetVarySpec(t.Context(), k, cur, next); err != nil || ok {
					return
				}
			}
		}()
	}
	wg.Wait()
	got, err := s.Get(t.Context(), k)
	if err != nil || len(got.Variants) != writers {
		t.Fatalf("listed variants = %d (err %v), want %d", len(got.Variants), err, writers)
	}
}
