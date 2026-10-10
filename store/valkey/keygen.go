package valkey

import (
	"context"
	"fmt"

	"github.com/valkey-io/valkey-go"
)

// maxKeyGenBytes bounds the record a caller may store (P5). The adapter
// stores a 32-byte SHA-256.
const maxKeyGenBytes = 64

// keyGenKey is where the key-generation record lives: a plain string next to
// the entries, not in the epoch hash slot. It has no TTL, so a volatile-*
// policy never evicts it; its suffix is not 64 hex characters, so Scrub
// never reads it (05 §7).
func (s *Store) keyGenKey() string { return s.cfg.Prefix + ":keygen" }

// RecordKeyGen stores hash as the cache's key-generation record and reports
// whether a different record was already there (R-3, 08 §3). The caller
// hashes the settings that change what an unchanged key means (forwarding
// rules); a true answer means entries stored under the older rules may still
// be on the server, and the caller writes the hard epoch. No record yet
// (a fresh server, a flush) is not a change: there is nothing to compare, and
// a flush took the entries too. It is one atomic SET ... GET, so two nodes
// starting together see each other's record and never both miss a change.
// The record replaces the old one on a change, so each node that disagrees
// with the stored hash purges once per restart (the adapter warns).
func (s *Store) RecordKeyGen(ctx context.Context, hash []byte) (changed bool, err error) {
	if n := len(hash); n == 0 || n > maxKeyGenBytes {
		return false, fmt.Errorf("store: valkey: key-generation hash must be 1 to %d bytes, got %d", maxKeyGenBytes, n)
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return false, err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	prev, err := cl.swapString(ctx, s.keyGenKey(), hash)
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return false, nil
		}
		return false, mapError(err)
	}
	return string(prev) != string(hash), nil
}
