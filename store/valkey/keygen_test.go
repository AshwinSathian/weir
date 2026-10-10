package valkey

import (
	"context"
	"errors"
	"testing"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// swapString is the fake of SET key val GET: it stores val and returns the
// previous value, or valkey.Nil when there was none.
func (f *fakeClient) swapString(_ context.Context, key string, val []byte) ([]byte, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return nil, f.kv.err
	}
	if f.kv.vals == nil {
		f.kv.vals, f.kv.pxat = map[string][]byte{}, map[string]int64{}
	}
	prev, ok := f.kv.vals[key]
	f.kv.vals[key] = val
	if !ok {
		return nil, valkey.Nil
	}
	return prev, nil
}

// R-3, 05 §7: the record sits at <prefix>:keygen; CheckKeyGen only reads, so
// a node that never records leaves the old value and every start still sees
// the change; RecordKeyGen replaces it.
func TestRecordKeyGen(t *testing.T) {
	a, b := []byte("hash-a-0123456789abcdef0123456789"), []byte("hash-b-0123456789abcdef0123456789")
	s, cl := connected(t, func(c *Config) { c.Prefix = "edge" })
	check := func(h []byte, want bool) {
		t.Helper()
		if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got != want {
			t.Fatalf("CheckKeyGen = %v, %v; want %v", got, err, want)
		}
	}
	record := func(h []byte) {
		t.Helper()
		if err := s.RecordKeyGen(t.Context(), h); err != nil {
			t.Fatal(err)
		}
	}
	check(a, false) // no record is not a change
	if _, ok := cl.kv.vals["edge:keygen"]; ok {
		t.Fatal("CheckKeyGen wrote the record")
	}
	record(a)
	if string(cl.kv.vals["edge:keygen"]) != string(a) {
		t.Fatalf("record = %q at edge:keygen, want %q", cl.kv.vals["edge:keygen"], a)
	}
	check(a, false)
	check(b, true)
	check(b, true) // not recorded yet: a crash before the purge repeats it
	record(b)
	check(b, false)
	check(a, true)
}

// R-3, FR-STF-2: a failed call is ErrUnavailable and an empty or oversized
// hash is refused before any round trip.
func TestRecordKeyGenErrors(t *testing.T) {
	s, cl := connected(t, nil)
	cl.kv.err = errors.New("boom")
	if _, err := s.CheckKeyGen(t.Context(), []byte("h")); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("CheckKeyGen err = %v, want ErrUnavailable", err)
	}
	if err := s.RecordKeyGen(t.Context(), []byte("h")); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("RecordKeyGen err = %v, want ErrUnavailable", err)
	}
	cl.kv.err = nil
	for _, h := range [][]byte{nil, make([]byte, maxKeyGenBytes+1)} {
		if _, err := s.CheckKeyGen(t.Context(), h); err == nil {
			t.Fatalf("CheckKeyGen accepted len %d", len(h))
		}
		if err := s.RecordKeyGen(t.Context(), h); err == nil {
			t.Fatalf("RecordKeyGen accepted len %d", len(h))
		}
	}
}
