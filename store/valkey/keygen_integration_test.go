//go:build integration

package valkey

import "testing"

// R-3, 05 §7: against a real server the record round-trips through
// SET ... GET and has no TTL.
func TestRecordKeyGenAgainstServer(t *testing.T) {
	s := scrubStore(t, nil)
	c := rawClient(t, s)
	h1, h2 := []byte("0123456789abcdef0123456789abcdef"), []byte("fedcba9876543210fedcba9876543210")
	for i, tc := range []struct {
		h    []byte
		want bool
	}{{h1, false}, {h1, false}, {h2, true}, {h2, false}} {
		got, err := s.RecordKeyGen(t.Context(), tc.h)
		if err != nil || got != tc.want {
			t.Fatalf("step %d: = %v, %v; want %v", i, got, err, tc.want)
		}
	}
	ttl, err := c.Do(t.Context(), c.B().Pttl().Key(s.keyGenKey()).Build()).AsInt64()
	if err != nil || ttl != -1 {
		t.Fatalf("PTTL = %d, %v; want -1 (no expiry)", ttl, err)
	}
}
