package weircaddy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
)

// keyGenHash digests the settings that change what an unchanged key means:
// Forward.Mode, Forward.Allow and Storable.StripSetCookie (08 §3, amended by
// P2-00). A reload that changes it writes a hard epoch, because an entry
// stored under the looser setting may hold personalized content (R-3).
// Settings that only change which key a request maps to, and host lists,
// are left out on purpose, so adding a domain never purges the cache. The
// version tag below is part of the hash, and the hash is also the record kept
// on a shared Valkey server: changing what this function covers purges every
// shared cache once on upgrade.
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

// keyGenCallTimeout bounds the record's read and write at Provision. It is
// shorter than closeTimeout because a load that waits on a dead server delays
// every site after it (08 §3).
const keyGenCallTimeout = 2 * time.Second

// keyGenRecorder is a store that keeps the forwarding hash on its server
// (the Valkey store, 05 §7). The memory store keeps its record beside the
// snapshot instead.
type keyGenRecorder interface {
	CheckKeyGen(ctx context.Context, hash []byte) (changed bool, err error)
	RecordKeyGen(ctx context.Context, hash []byte) error
}

// reconcileServerKeyGen is the key-generation step of Provision (R-3, 08 §3): read
// the record on the shared server, purge when it differs or the caller already
// knows the hash changed (keyChanged), then record the hash. The order is the
// point:
//
//   - the record is replaced only after the purge succeeded, so a process that
//     dies in between, or a failed purge, leaves the old record and the next
//     start purges again;
//   - the record is not written when the check could not be made, because
//     overwriting a record nobody compared would lose a purge for good.
//
// A server that cannot answer is not an error: FR-STF-2 requires that an
// outage at start opens the store breaker instead of failing the load, so the
// check is skipped with a warning and repeated by a background retry.
// A failed purge is returned. A store with no record (memory) only purges
// when keyChanged. The check and the record call each get keyGenCallTimeout, so a
// blackholed server delays a load by at most twice that per site; the purge gets
// closeTimeout.
func reconcileServerKeyGen(ctx context.Context, st store.Store, fwd [sha256.Size]byte, keyChanged bool, purge func(context.Context) error, log *slog.Logger, name string, quiet bool) (retry bool, err error) {
	rec, hasRec := st.(keyGenRecorder)
	// A background retry repeats the same failures for as long as an outage
	// lasts; its attempts log them at Debug, the start's Warn said it once.
	lvl := slog.LevelWarn
	if quiet {
		lvl = slog.LevelDebug
	}
	checked := false
	if hasRec {
		cctx, cancel := context.WithTimeout(ctx, keyGenCallTimeout)
		changed, err := rec.CheckKeyGen(cctx, fwd[:])
		cancel()
		switch {
		case err != nil:
			retry = true
			log.Log(ctx, lvl, "weir: key-generation record not checked; entries stored under older forwarding rules stay servable until the check succeeds (a background retry is running)",
				"name", name, "error", err)
		case changed:
			checked, keyChanged = true, true
			log.Warn("weir: forwarding rules differ from the key-generation record on the store, or the cache predates the record; writing a hard epoch. Nodes that share this prefix must use the same forward settings, or they purge each other on every start or reload",
				"name", name)
		default:
			checked = true
		}
	}
	if keyChanged {
		pctx, cancel := context.WithTimeout(ctx, closeTimeout)
		err := purge(pctx)
		cancel()
		if err != nil {
			return retry, err
		}
	}
	if checked {
		rctx, cancel := context.WithTimeout(ctx, keyGenCallTimeout)
		defer cancel()
		if err := rec.RecordKeyGen(rctx, fwd[:]); err != nil {
			// Retried too: a record left stale makes every node's next start
			// purge the shared cache again.
			retry = true
			log.Log(ctx, lvl, "weir: key-generation record not written; a background retry is running", "name", name, "error", err)
		}
	}
	return retry, nil
}

// Backoff of the background key-generation retry: doubling from the first
// value to the cap, so an outage costs a few calls a minute, not a storm.
const (
	keyGenRetryFirst = time.Second
	keyGenRetryMax   = 30 * time.Second
)

// keyGenRetry repeats the key-generation step in the background after a
// Provision whose check could not be made or whose record could not be written
// (FR-STF-2, R-3). It narrows the
// window in which entries stored under older forwarding rules stay servable
// but cannot close it: they are servable from the moment the server returns
// until the retry's purge lands. The handler owns it: Cleanup calls stop,
// which waits for the goroutine, before the engine closes (hard rule 10).
type keyGenRetry struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startKeyGenRetry runs reconcileServerKeyGen with backoff until the check
// is made and the purge, if one is due, and the record write have all
// succeeded (a failed record write is retried, and the retry's check then
// sees the old record and purges once more, on the safe side). A
// failed purge is retried too. purge must not close the engine, unlike
// Provision's. Each attempt is bounded by keyGenCallTimeout and closeTimeout,
// which is why stop returns promptly.
func startKeyGenRetry(st store.Store, fwd [sha256.Size]byte, purge func(context.Context) error, log *slog.Logger, name string) *keyGenRetry {
	ctx, cancel := context.WithCancel(context.Background())
	r := &keyGenRetry{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		wait := keyGenRetryFirst
		var lastErr string
		for {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			// keyChanged is false on purpose: the retry relies on the record
			// on the server, not on process state, so a record another node
			// already updated purges nothing here.
			retry, err := reconcileServerKeyGen(ctx, st, fwd, false, purge, log, name, true)
			if ctx.Err() != nil {
				return // stop() cancelled the attempt: nothing to report
			}
			switch {
			case err != nil:
				// Once per distinct error, so a persistent failure surfaces
				// without a line every 30 s.
				if msg := err.Error(); msg != lastErr {
					log.Warn("weir: key-generation purge failed; retrying", "name", name, "error", err)
					lastErr = msg
				}
			case !retry:
				log.Info("weir: key-generation check made after the server returned", "name", name)
				return
			}
			wait = min(2*wait, keyGenRetryMax)
		}
	}()
	return r
}

// stop ends the retry and waits for it. It is safe to call more than once.
func (r *keyGenRetry) stop() {
	r.cancel()
	<-r.done
}
