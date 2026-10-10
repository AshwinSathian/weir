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
