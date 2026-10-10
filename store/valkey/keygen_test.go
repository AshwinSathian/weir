package valkey

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/AshwinSathian/weir/store"
)

func (f *fakeClient) setString(_ context.Context, key string, val []byte) error {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return f.kv.err
	}
	if f.kv.vals == nil {
		f.kv.vals, f.kv.pxat = map[string][]byte{}, map[string]int64{}
	}
	f.kv.vals[key] = val
	delete(f.kv.pxat, key)
	return nil
}

// getRange fakes GETRANGE: an absent key is empty and the range is clipped.
func (f *fakeClient) getRange(_ context.Context, key string, start, end int64) ([]byte, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return nil, f.kv.err
	}
	v := f.kv.vals[key]
	if int64(len(v)) > end+1 {
		v = v[:end+1]
	}
	return v[min(int64(len(v)), start):], nil
}

func (f *fakeClient) exists(_ context.Context, key string) (bool, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.kv.err != nil {
		return false, f.kv.err
	}
	_, ok := f.kv.vals[key]
	return ok, nil
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

// R-3, 05 §7: a prefix a Weir without the record has used (its meta key
// exists) is a change when the record is absent, so an upgrade rolled out with
// a tighter forward purges once; a fresh or flushed server does not.
func TestCheckKeyGenExistingPrefixWithoutRecord(t *testing.T) {
	h := []byte("hash-a-0123456789abcdef0123456789")
	s, cl := connected(t, func(c *Config) { c.Prefix = "edge" })
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got {
		t.Fatalf("fresh server: %v, %v; want no change", got, err)
	}
	cl.kv.vals = map[string][]byte{"edge:{e}:meta": []byte("x")}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || !got {
		t.Fatalf("used prefix, no record: %v, %v; want a change", got, err)
	}
	if err := s.RecordKeyGen(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got {
		t.Fatalf("after recording: %v, %v; want no change", got, err)
	}
}

// R-3, T-29: an oversized value planted at the record key is a change, the
// read is bounded, and RecordKeyGen replaces it. Another type (WRONGTYPE) is
// covered against a real server.
func TestCheckKeyGenPlantedValues(t *testing.T) {
	h := []byte("hash-a-0123456789abcdef0123456789")
	s, cl := connected(t, func(c *Config) { c.Prefix = "edge" })
	cl.kv.vals = map[string][]byte{"edge:keygen": append(bytes.Clone(h), make([]byte, 1<<20)...)}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || !got {
		t.Fatalf("oversized value: %v, %v; want a change", got, err)
	}
	if err := s.RecordKeyGen(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got {
		t.Fatalf("after recording: %v, %v; want no change", got, err)
	}
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
