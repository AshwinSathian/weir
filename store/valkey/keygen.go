package valkey

import (
	"context"
	"fmt"
)

// maxKeyGenBytes bounds the record a caller may store (P5). The adapter
// stores a 32-byte SHA-256.
const maxKeyGenBytes = 64

// keyGenKey is where the key-generation record lives: a plain string next to
// the entries, not in the epoch hash slot. It has no TTL, so a volatile-*
// policy never evicts it; its suffix is not 64 hex characters, so Scrub
// never reads it (05 §7).
func (s *Store) keyGenKey() string { return s.cfg.Prefix + ":keygen" }

// CheckKeyGen reports whether the cache on this server may hold entries
// stored under other forwarding rules than hash (R-3, 08 §3). The caller
// hashes the settings that change what an unchanged key means; true means the
// caller writes the hard epoch and then RecordKeyGen. It is a change when
//
//   - the record holds another value, or a value longer than any hash, or a
//     key of another type (a planted value must not switch the check off); or
//   - there is no record but the prefix has been used before (its meta key
//     exists), which is a cache written by a Weir without this record. A fresh
//     server or a flushed one has neither key and is not a change.
//
// It reads at most maxKeyGenBytes+1 bytes of the record, so a planted huge
// value costs nothing, and it never writes: a node that dies before its purge
// leaves the old record and the next start purges again.
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
	prev, err := cl.getRange(ctx, s.keyGenKey(), 0, maxKeyGenBytes)
	if err != nil {
		if isWrongType(err) {
			return true, nil
		}
		return false, mapError(err)
	}
	if len(prev) > 0 {
		return string(prev) != string(hash), nil
	}
	used, err := cl.exists(ctx, epochKeys(s.cfg.Prefix, s.cfg.HashTag)[keyMeta])
	if err != nil {
		return false, mapError(err)
	}
	return used, nil
}

// RecordKeyGen stores hash as the record with a plain SET: no expiry, and it
// replaces a planted value of any type. Call it after the purge that
// CheckKeyGen called for has succeeded. Two nodes that start together may both
// see a change and both purge: extra work, on the safe side. Each node that
// disagrees with the record purges on every start or reload (the adapter
// warns).
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
	return mapError(cl.setString(ctx, s.keyGenKey(), hash))
}

func checkKeyGenLen(hash []byte) error {
	if n := len(hash); n == 0 || n > maxKeyGenBytes {
		return fmt.Errorf("store: valkey: key-generation hash must be 1 to %d bytes, got %d", maxKeyGenBytes, n)
	}
	return nil
}
