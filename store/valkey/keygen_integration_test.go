//go:build integration

package valkey

import "testing"

// R-3, 05 §7: against a real server the record round-trips through
// SET ... GET and GET and has no TTL.
func TestRecordKeyGenAgainstServer(t *testing.T) {
	s := scrubStore(t, nil)
	c := rawClient(t, s)
	h1, h2 := []byte("0123456789abcdef0123456789abcdef"), []byte("fedcba9876543210fedcba9876543210")
	for i, step := range []struct {
		record []byte // nil: check only
		check  []byte
		want   bool
	}{{nil, h1, false}, {h1, h1, false}, {nil, h2, true}, {h2, h2, false}} {
		if step.record != nil {
			if err := s.RecordKeyGen(t.Context(), step.record); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
		got, err := s.CheckKeyGen(t.Context(), step.check)
		if err != nil || got != step.want {
			t.Fatalf("step %d: = %v, %v; want %v", i, got, err, step.want)
		}
	}
	ttl, err := c.Do(t.Context(), c.B().Pttl().Key(s.keyGenKey()).Build()).AsInt64()
	if err != nil || ttl != -1 {
		t.Fatalf("PTTL = %d, %v; want -1 (no expiry)", ttl, err)
	}
}

// R-3, T-29, 05 §7: against a real server a value of another type at the
// record key is a change (it must not switch the check off), an oversized
// string is read in 65 bytes, an existing prefix with no record is a change,
// and RecordKeyGen repairs the key.
func TestCheckKeyGenPlantedAndExisting(t *testing.T) {
	s := scrubStore(t, nil)
	c := rawClient(t, s)
	h := []byte("0123456789abcdef0123456789abcdef")
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got {
		t.Fatalf("fresh prefix: %v, %v; want no change", got, err)
	}
	meta := epochKeys(s.cfg.Prefix, s.cfg.HashTag)[keyMeta]
	if err := c.Do(t.Context(), c.B().Hset().Key(meta).FieldValue().FieldValue("v", "1").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || !got {
		t.Fatalf("used prefix, no record: %v, %v; want a change", got, err)
	}
	if err := c.Do(t.Context(), c.B().Hset().Key(s.keyGenKey()).FieldValue().FieldValue("f", "v").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || !got {
		t.Fatalf("wrong type: %v, %v; want a change", got, err)
	}
	if err := s.RecordKeyGen(t.Context(), h); err != nil {
		t.Fatalf("RecordKeyGen over a hash: %v", err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || got {
		t.Fatalf("after repair: %v, %v; want no change", got, err)
	}
	big := make([]byte, 1<<20)
	if err := c.Do(t.Context(), c.B().Set().Key(s.keyGenKey()).Value(string(big)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckKeyGen(t.Context(), h); err != nil || !got {
		t.Fatalf("oversized: %v, %v; want a change", got, err)
	}
}
