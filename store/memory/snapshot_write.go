package memory

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Snapshot file framing (05 §5.5).
const (
	snapMagic     = "WEIRSNAP"
	snapVersion   = 0x01
	snapKindEntry = 0x01
	snapKindEpoch = 0x02
	// snapKindKept marks a file written with SnapshotKeepFreshness: it
	// holds no entry that a soft, invalidating or hard epoch covered when
	// it was written (FR-SNP-5). Payload: one byte, 1. A loader that does
	// not know the kind counts a lost record and starts cold, which is the
	// safe reading.
	snapKindKept    = 0x03
	snapKindTrailer = 0xFF

	defaultSnapshotTimeout = 5 * time.Second
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// snapRec is one live record copied out of a shard. Entries are immutable
// (P4), so encoding runs after the shard lock is released.
type snapRec struct {
	key store.Key
	e   *store.Entry
}

// Close marks the store closed and, when Config.SnapshotPath is set, writes
// the snapshot within Config.SnapshotTimeout. It is idempotent.
func (s *Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.snapTimeout)
	defer cancel()
	return s.CloseContext(ctx)
}

// CloseContext is Close with the snapshot bounded by ctx, so an adapter's
// shutdown grace period bounds it (FR-SNP-1). A snapshot cut short by ctx is
// discarded and the error wraps ctx's. The store is closed either way.
// Concurrent calls wait for each other; once a snapshot is written later
// calls return nil, and after a failure a later call tries again (the store
// accepts no writes by then, so the content does not change).
func (s *Store) CloseContext(ctx context.Context) error {
	// Exclusive with SetEpoch: an epoch that was acknowledged is in place
	// before the walk below decides what a kept snapshot may hold (T-33).
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.closed.Store(true)
	if s.snapPath == "" || s.snapDone {
		return nil
	}
	if err := s.writeSnapshot(ctx); err != nil {
		return err
	}
	s.snapDone = true
	return nil
}

// writeSnapshot writes every live response and vary-spec record and all hard
// epochs to a 0600 temp file beside the target, then fsyncs and renames it
// into place. On any error the temp file is removed, so a partial snapshot
// is never visible (FR-SNP-1).
func (s *Store) writeSnapshot(ctx context.Context) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(s.snapPath), ".weir-snap-*")
	if err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	w := &snapWriter{ctx: ctx, w: bufio.NewWriter(f)}
	w.header(time.Now())
	// Main-queue records first, so hot entries load first (FR-SNP-3).
	for _, queue := range [...]uint8{queueMain, queueSmall} {
		for i := range s.shards {
			for _, r := range s.shards[i].live(queue) {
				if s.snapKeep && s.covered(r.e) {
					continue
				}
				w.entry(r)
			}
		}
	}
	if s.snapKeep {
		w.record(snapKindKept, []byte{1})
		w.count++
	}
	for t, at := range s.ep.hardEpochs() {
		w.epoch(t, at)
	}
	w.trailer()
	if w.err == nil {
		w.err = w.w.Flush()
	}
	if w.err == nil {
		w.err = f.Sync()
	}
	if err = w.err; err == nil {
		// The deadline can pass during Flush or Sync; do not rename after it.
		err = ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	if err = os.Rename(f.Name(), s.snapPath); err != nil {
		return fmt.Errorf("store: memory: snapshot: %w", err)
	}
	// Best effort: persist the rename itself. The file is already complete.
	if d, derr := os.Open(filepath.Dir(s.snapPath)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// live copies the unexpired response and vary-spec records of one queue,
// head to tail, under the read lock. Markers and negative entries are not
// persisted (FR-SNP-1).
// covered reports whether any epoch applies to e now. Soft and invalidating
// epochs live in a sketch keyed with a per-process seed and cannot be
// written out, so a kept snapshot leaves such entries out instead: they come
// back as misses, never as fresh hits (FR-SNP-5, FR-STL-5). Every tag is
// read in every plane, also the group tags the engine reads as shared (05
// E-12): the store cannot know its caller's layout, and a sketch false
// positive here only leaves out an entry that could have been kept.
func (s *Store) covered(e *store.Entry) bool {
	_, ok := s.ep.newestOf(e.Tags, nil, e.RequestTime)
	return ok
}

func (sh *shard) live(queue uint8) []snapRec {
	now := time.Now()
	q := &sh.small
	if queue == queueMain {
		q = &sh.main
	}
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	out := make([]snapRec, 0, q.len)
	for n := q.head; n != nil; n = n.next {
		if n.expired(now) || (n.e.Kind != store.KindResponse && n.e.Kind != store.KindVarySpec) {
			continue
		}
		out = append(out, snapRec{n.key, n.e})
	}
	return out
}

// hardEpochs returns a copy of the hard epochs still in force: the table
// (minus entries past retention, which are pruned lazily and cannot apply to
// any live record) and the global hard epoch, which lives outside the table
// (E-5). Dropping the global one would let a hard purge degrade to soft-stale
// after a restart.
func (ep *epochs) hardEpochs() map[store.Tag]time.Time {
	cutoff := time.Now().Add(-ep.retention)
	ep.mu.RLock()
	defer ep.mu.RUnlock()
	out := make(map[store.Tag]time.Time, len(ep.hard)+1)
	for t, at := range ep.hard {
		if !at.Before(cutoff) {
			out[t] = at
		}
	}
	if g := ep.global[store.EpochHard-1].Load(); g != noEpoch {
		out[ep.globalTag] = ep.base.Add(time.Duration(g))
	}
	return out
}

// snapWriter frames records with a sticky error and stops at ctx's deadline.
type snapWriter struct {
	ctx   context.Context
	w     *bufio.Writer
	err   error
	count uint64
}

func (w *snapWriter) header(base time.Time) {
	b := append([]byte(snapMagic), snapVersion)
	b = binary.BigEndian.AppendUint64(b, uint64(base.UnixNano())) //nolint:gosec // two's-complement bits
	w.write(b)
}

func (w *snapWriter) entry(r snapRec) {
	if w.live() != nil {
		return
	}
	enc, err := store.Encode(r.e)
	if err != nil {
		// Cannot happen for a stored entry. Skip it rather than lose the
		// rest; the trailer counts written records only.
		return
	}
	w.record(snapKindEntry, append(r.key[:], enc...))
	w.count++
}

func (w *snapWriter) epoch(t store.Tag, at time.Time) {
	if w.live() != nil {
		return
	}
	p := binary.BigEndian.AppendUint64(t[:], uint64(at.UnixNano())) //nolint:gosec // two's-complement bits
	w.record(snapKindEpoch, p)
	w.count++
}

func (w *snapWriter) trailer() {
	if w.live() != nil {
		return
	}
	w.record(snapKindTrailer, binary.BigEndian.AppendUint64(nil, w.count))
}

// live reports whether writing may continue, latching ctx's error. The
// deadline is checked per record, so one Flush or Sync is not interruptible.
func (w *snapWriter) live() error {
	if w.err == nil {
		w.err = w.ctx.Err()
	}
	return w.err
}

// record writes kind, uvarint length, payload and a CRC-32C over all three.
func (w *snapWriter) record(kind byte, payload []byte) {
	b := binary.AppendUvarint([]byte{kind}, uint64(len(payload)))
	b = append(b, payload...)
	w.write(binary.BigEndian.AppendUint32(b, crc32.Checksum(b, castagnoli)))
}

func (w *snapWriter) write(b []byte) {
	if w.err == nil {
		_, w.err = w.w.Write(b)
	}
}
