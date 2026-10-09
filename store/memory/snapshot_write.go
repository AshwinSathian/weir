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
	snapMagic       = "WEIRSNAP"
	snapVersion     = 0x01
	snapKindEntry   = 0x01
	snapKindEpoch   = 0x02
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
func (s *Store) CloseContext(ctx context.Context) error {
	if s.closed.Swap(true) || s.snapPath == "" {
		return nil
	}
	return s.writeSnapshot(ctx)
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
				w.entry(r)
			}
		}
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
	if err = w.err; err != nil {
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

// hardEpochs returns a copy of the hard epoch table.
func (ep *epochs) hardEpochs() map[store.Tag]time.Time {
	ep.mu.RLock()
	defer ep.mu.RUnlock()
	out := make(map[store.Tag]time.Time, len(ep.hard))
	for t, at := range ep.hard {
		out[t] = at
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
		return // cannot happen for a stored entry; skip rather than lose the rest
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
