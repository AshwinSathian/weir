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

// FR-PRG-1, R-3, 05 §7: the record sits at <prefix>:keygen, the first writer
// finds nothing to compare, an equal hash is unchanged and a different one
// reports a change and replaces the record.
func TestRecordKeyGen(t *testing.T) {
	a, b := []byte("hash-a-0123456789abcdef0123456789"), []byte("hash-b-0123456789abcdef0123456789")
	s, cl := connected(t, func(c *Config) { c.Prefix = "edge" })
	steps := []struct {
		name string
		hash []byte
		want bool
	}{
		{"first record is not a change", a, false},
		{"same hash is not a change", a, false},
		{"different hash is a change", b, true},
		{"the new hash is now the record", b, false},
		{"going back is a change again", a, true},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			got, err := s.RecordKeyGen(t.Context(), st.hash)
			if err != nil || got != st.want {
				t.Fatalf("RecordKeyGen = %v, %v; want %v", got, err, st.want)
			}
		})
	}
	if string(cl.kv.vals["edge:keygen"]) != string(a) {
		t.Fatalf("record = %q, want %q at edge:keygen", cl.kv.vals["edge:keygen"], a)
	}
	if _, ok := cl.kv.pxat["edge:keygen"]; ok && cl.kv.pxat["edge:keygen"] != 0 {
		t.Fatal("the record must carry no expiry: a volatile-* policy would never evict it, but a TTL would lose it")
	}
}

// T-29, 05 §7: a failed call is ErrUnavailable and an empty or oversized
// hash is refused before any round trip.
func TestRecordKeyGenErrors(t *testing.T) {
	s, cl := connected(t, nil)
	cl.kv.err = errors.New("boom")
	if _, err := s.RecordKeyGen(t.Context(), []byte("h")); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	cl.kv.err = nil
	for _, h := range [][]byte{nil, make([]byte, maxKeyGenBytes+1)} {
		if _, err := s.RecordKeyGen(t.Context(), h); err == nil {
			t.Fatalf("len %d accepted", len(h))
		}
	}
}
