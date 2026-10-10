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

// CheckKeyGen reports whether the key-generation record holds a value other
// than hash (R-3, 08 §3). The caller hashes the settings that change what an
// unchanged key means (forwarding rules); true means entries stored under
// older rules may still be on the server, and the caller writes the hard
// epoch and then RecordKeyGen. No record (a fresh server, a flush) is not a
// change: there is nothing to compare, and a flush took the entries too. It
// only reads, so a node that dies before its purge leaves the old record and
// the next start purges again.
func (s *Store) CheckKeyGen(ctx context.Context, hash []byte) (changed bool, err error) {
	if err := checkKeyGenLen(hash); err != nil {
		return false, err
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return false, err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	prev, err := cl.get(ctx, s.keyGenKey())
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return false, nil
		}
		return false, mapError(err)
	}
	return string(prev) != string(hash), nil
}

// RecordKeyGen stores hash as the record, with no expiry. Call it after the
// purge that CheckKeyGen called for has succeeded. Two nodes that start
// together may both see a change and both purge: extra work, on the safe
// side. Each node that disagrees with the record purges on every start or
// reload (the adapter warns).
func (s *Store) RecordKeyGen(ctx context.Context, hash []byte) error {
	if err := checkKeyGenLen(hash); err != nil {
		return err
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	if _, err := cl.swapString(ctx, s.keyGenKey(), hash); err != nil && !valkey.IsValkeyNil(err) {
		return mapError(err)
	}
	return nil
}

func checkKeyGenLen(hash []byte) error {
	if n := len(hash); n == 0 || n > maxKeyGenBytes {
		return fmt.Errorf("store: valkey: key-generation hash must be 1 to %d bytes, got %d", maxKeyGenBytes, n)
	}
	return nil
}
