package valkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// Sketch geometry (05 §7, E-7, E-9): two planes of 2^19 uint32 cells, 2 MiB
// each, created full-size so the keyspace cost is fixed whatever the attack
// volume. Two cells per tag. Not configurable: every node sharing a prefix
// must agree on the plane size.
const (
	sketchSlots = 1 << 19
	seedLen     = 16 // bytes drawn from crypto/rand
	seedIDLen   = 8  // bytes the scripts compare
)

// positions returns the two cells of t: the first two big-endian words of
// SHA-256(seed || t) modulo the plane size. Computed here because the
// server's Lua has no secret-grade hash; the seed is what keeps an attacker,
// who can compute any URL's tag, from aiming at its cells (T-29).
func positions(seed *[seedLen]byte, t store.Tag) (uint32, uint32) {
	var in [seedLen + len(store.Tag{})]byte
	copy(in[:], seed[:])
	copy(in[seedLen:], t[:])
	sum := sha256.Sum256(in[:])
	return binary.BigEndian.Uint32(sum[0:4]) % sketchSlots, binary.BigEndian.Uint32(sum[4:8]) % sketchSlots
}

// getSeed returns the shared sketch seed. The first call on a store draws 16
// bytes from crypto/rand, stores them with HSETNX and reads back whichever
// seed won, so every node ends up with the same one. The result is cached
// until a script reports SEED_CHANGED.
func (s *Store) getSeed(ctx context.Context, cl client) (*[seedLen]byte, error) {
	if sd := s.seed.Load(); sd != nil {
		return sd, nil
	}
	var fresh [seedLen]byte
	if _, err := rand.Read(fresh[:]); err != nil {
		return nil, unavailable("seed: %v", err)
	}
	b, err := cl.seed(ctx, s.ekeys[keyMeta], fresh[:])
	if valkey.IsValkeyNil(err) {
		// A flush landed between HSETNX and HGET: the seed is gone, not an
		// error the caller can tell from an outage (T-29: fail closed).
		return nil, unavailable("sketch seed vanished while it was being stored")
	}
	if err != nil {
		return nil, mapError(err)
	}
	if len(b) != seedLen {
		return nil, unavailable("stored sketch seed is %d bytes, want %d", len(b), seedLen)
	}
	sd := new([seedLen]byte)
	copy(sd[:], b)
	s.seed.Store(sd)
	return sd, nil
}

// dropSeed forgets sd unless another goroutine has already replaced it.
func (s *Store) dropSeed(sd *[seedLen]byte) { s.seed.CompareAndSwap(sd, nil) }

// setSketch writes a soft or invalid epoch on a non-global tag: raise the two
// cells of its plane. The script replies SEED_CHANGED if the server's seed is
// not the cached one (after a flush); the store refetches and retries once,
// a second reply is an error (05 §7).
func (s *Store) setSketch(ctx context.Context, cl client, t store.Tag, ep store.Epoch) error {
	for range 2 {
		sd, err := s.getSeed(ctx, cl)
		if err != nil {
			return err
		}
		args := s.writeArgs(ep.Mode, false, t, ep.At, false)
		p1, p2 := positions(sd, t)
		args[argPos1], args[argPos2] = itoa(int64(p1)), itoa(int64(p2))
		args[argSeedID] = string(sd[:seedIDLen])
		err = s.writeEpoch(ctx, cl, args)
		if !isSeedChanged(err) {
			return err
		}
		s.dropSeed(sd)
	}
	return unavailable("sketch seed changed twice in a row")
}

// NewestEpochShared equals NewestEpoch over tags and shared together, except
// that a tag in shared never matches in invalid mode: a sketch false positive
// there would revalidate a whole group at once (E-12, T-29). One script call.
func (s *Store) NewestEpochShared(ctx context.Context, tags, shared []store.Tag, since time.Time) (store.Epoch, bool, error) {
	return s.newest(ctx, tags, shared, since)
}

// readArgs builds the read script's arguments (scripts.go): since, skew, the
// global flag ('0' absent, '1' plain, '2' shared), the seed id, then four per
// other tag. A tag named in both lists is plain, as in the memory store.
// Callers pass at most a handful of tags (the engine: 2 + MaxGroups), so the
// argument list is bounded by the caller (P5).
func (s *Store) readArgs(tags, shared []store.Tag, since, skew int64, sd *[seedLen]byte) []string {
	g := store.TagGlobal()
	a := make([]string, argFirstTag, argFirstTag+tagStride*(len(tags)+len(shared)))
	a[argSince], a[argSkew], a[argHasGlobal], a[argReadSeed] = itoa(since), itoa(skew), "0", ""
	if sd != nil {
		a[argReadSeed] = string(sd[:seedIDLen])
	}
	add := func(t store.Tag, flag string) {
		if t == g {
			a[argHasGlobal] = "1"
			if flag == "1" {
				a[argHasGlobal] = "2" // never in the tags list, so not overridden
			}
			return
		}
		p1, p2 := positions(sd, t)
		a = append(a, string(t[:]), itoa(int64(p1)), itoa(int64(p2)), flag)
	}
	for _, t := range tags {
		add(t, "0")
	}
	for _, t := range shared {
		if !slices.Contains(tags, t) {
			add(t, "1")
		}
	}
	return a
}

// needsSeed reports whether a lookup names any tag but the global one.
func needsSeed(tags, shared []store.Tag) bool {
	g := store.TagGlobal()
	for _, ts := range [][]store.Tag{tags, shared} {
		for _, t := range ts {
			if t != g {
				return true
			}
		}
	}
	return false
}

func isSeedChanged(err error) bool { return errors.Is(err, errSeedChanged) }
