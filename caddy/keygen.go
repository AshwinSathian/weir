package weircaddy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"net/http"
	"slices"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
)

// keyGenHash digests the settings that change what an unchanged key means:
// Forward.Mode, Forward.Allow and Storable.StripSetCookie (08 §3, amended by
// P2-00). A reload that changes it writes a hard epoch, because an entry
// stored under the looser setting may hold personalized content (R-3).
// Settings that only change which key a request maps to, and host lists,
// are left out on purpose, so adding a domain never purges the cache.
func keyGenHash(cfg weir.Config) [sha256.Size]byte {
	return keyGenHashFor(cfg, [sha256.Size]byte{})
}

// keyGenHashFor also covers the store block's digest (08 §3). A zero digest,
// the memory store, adds nothing, so the hash of an existing memory site is
// unchanged and an upgrade does not drop its snapshot.
func keyGenHashFor(cfg weir.Config, storeDigest [sha256.Size]byte) [sha256.Size]byte {
	allow := make([]string, 0, len(cfg.Forward.Allow))
	for _, n := range cfg.Forward.Allow {
		allow = append(allow, http.CanonicalHeaderKey(n))
	}
	slices.Sort(allow)
	allow = slices.Compact(allow)

	h := sha256.New()
	h.Write([]byte("weir/caddy/keygen/v1\x00"))
	h.Write([]byte{byte(cfg.Forward.Mode)})
	var n [binary.MaxVarintLen64]byte
	h.Write(n[:binary.PutUvarint(n[:], uint64(len(allow)))])
	for _, name := range allow {
		// Length prefix: ["X-Ab"] and ["X-A", "b"] must not collide.
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(name)))])
		h.Write([]byte(name))
	}
	if cfg.Storable.StripSetCookie {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	if storeDigest != ([sha256.Size]byte{}) {
		h.Write([]byte("store\x00"))
		h.Write(storeDigest[:])
	}
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}

// keyGenRecorder is a store that keeps the forwarding hash on its server
// (the Valkey store, 05 §7). The memory store keeps its record beside the
// snapshot instead.
type keyGenRecorder interface {
	RecordKeyGen(ctx context.Context, hash []byte) (changed bool, err error)
}

// pendingKeyGen is written over the record when the purge that a change
// called for failed, so the next start sees a mismatch and purges again
// instead of trusting a record that describes entries still on the server.
var pendingKeyGen = []byte{0}

// syncKeyGen records the forwarding hash on the shared server and reports
// whether it differed from what another node (or an earlier run) recorded
// there (R-3, 08 §3). A server that cannot answer is not an error: FR-STF-2
// requires that an outage at start opens the store breaker instead of
// failing the load, so the check is skipped with a warning and the next
// start repeats it. A store with no record (memory) reports false.
func syncKeyGen(ctx context.Context, st store.Store, fwd [sha256.Size]byte, log *slog.Logger, name string) bool {
	rec, ok := st.(keyGenRecorder)
	if !ok {
		return false
	}
	changed, err := rec.RecordKeyGen(ctx, fwd[:])
	if err != nil {
		log.Warn("weir: key-generation record not checked; entries stored under older forwarding rules stay servable until the next start with the server reachable",
			"name", name, "error", err)
		return false
	}
	if changed {
		log.Warn("weir: forwarding rules differ from the key-generation record on the store; writing a hard epoch. Nodes that share this prefix must use the same forward settings, or they purge each other on every restart",
			"name", name)
	}
	return changed
}

// markKeyGenPending replaces the record with a value no hash can equal. It is
// best effort: it runs after a failed purge, usually because the server went
// away, in which case the record was not trusted either.
func markKeyGenPending(ctx context.Context, st store.Store) {
	if rec, ok := st.(keyGenRecorder); ok {
		_, _ = rec.RecordKeyGen(ctx, pendingKeyGen)
	}
}
