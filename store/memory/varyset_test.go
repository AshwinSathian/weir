package memory

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.VarySetter = (*Store)(nil)

func spec() *store.Entry {
	return &store.Entry{Kind: store.KindVarySpec, VaryNames: []string{"Accept-Language"}, Expires: time.Now().Add(time.Hour)}
}

// FR-KEY-10, NFR-3, 05 §2.3: SetVarySpec swaps only while the record is still
// the one the caller read.
func TestSetVarySpec(t *testing.T) {
	k := numKey(1)
	t.Run("nil prev swaps into an empty key and once only", func(t *testing.T) {
		s := newStore(t, Config{})
		a, b := spec(), spec()
		if ok, err := s.SetVarySpec(t.Context(), k, nil, a); !ok || err != nil {
			t.Fatalf("first swap = %v, %v", ok, err)
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, nil, b); ok {
			t.Fatal("second swap with a nil prev succeeded over a live record")
		}
		if got, _ := s.Get(t.Context(), k); got != a {
			t.Fatal("the losing swap changed the record")
		}
	})
	t.Run("matching prev swaps, stale prev does not", func(t *testing.T) {
		s := newStore(t, Config{})
		a, b, c := spec(), spec(), spec()
		if err := s.Set(t.Context(), k, a); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, a, b); !ok {
			t.Fatal("swap with the current record failed")
		}
		if ok, _ := s.SetVarySpec(t.Context(), k, a, c); ok {
			t.Fatal("swap with a replaced record succeeded")
		}
		if got, _ := s.Get(t.Context(), k); got != b {
			t.Fatal("record is not the winner's")
		}
	})
	t.Run("a non-nil prev does not match an absent key", func(t *testing.T) {
		s := newStore(t, Config{})
		if ok, _ := s.SetVarySpec(t.Context(), k, spec(), spec()); ok {
			t.Fatal("swapped into an empty key with a prev")
		}
		if _, err := s.Get(t.Context(), k); err == nil {
			t.Fatal("a failed swap stored a record")
		}
	})
	t.Run("an expired record counts as none", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newStore(t, Config{})
			old := spec()
			old.Expires = time.Now().Add(time.Second)
			if err := s.Set(t.Context(), k, old); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Second)
			if ok, _ := s.SetVarySpec(t.Context(), k, nil, spec()); !ok {
				t.Fatal("nil prev did not match an expired record")
			}
		})
	})
	t.Run("a closed store is unavailable", func(t *testing.T) {
		s := newStore(t, Config{})
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetVarySpec(t.Context(), k, nil, spec()); err == nil {
			t.Fatal("no error from a closed store")
		}
	})
}

// 64 writers swap from the same prev; exactly one wins.
func TestSetVarySpecOneWinner(t *testing.T) {
	s := newStore(t, Config{})
	k := numKey(2)
	base := spec()
	if err := s.Set(t.Context(), k, base); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := s.SetVarySpec(t.Context(), k, base, spec()); ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d writers won, want 1", wins.Load())
	}
}
