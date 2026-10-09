package memory

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"time"

	"github.com/AshwinSathian/weir/store"
)

const (
	snapHeaderLen  = len(snapMagic) + 1 + 8
	snapTrailerLen = 1 + 1 + 8 + 4 // kind, uvarint(8), count, CRC
	snapKeyLen     = len(store.Key{})
	snapEpochLen   = len(store.Tag{}) + 8
)

// snapLoadStats counts what the loader did with each record (FR-SNP-2).
type snapLoadStats struct {
	loaded  int // records now in the store
	skipped int // failed CRC or decoding, unknown kind, or rejected epoch
	expired int // past Expires
	dropped int // beyond MaxBytes (FR-SNP-3)
	// lost: a record that may have been a hard epoch could not be applied
	// (bad CRC, unknown kind, framing break, rejected epoch, or fewer
	// records than the trailer counts). The loader then fails closed.
	lost bool
	seen uint64 // framed records read, including skipped ones
}

// loadSnapshot reads the snapshot at snapPath, if any, and removes it
// afterwards so a later crash cannot reload old content (FR-SNP-2, T-33).
// It never fails New: a missing, unreadable or incomplete snapshot leaves
// the store empty. A file that does not start with the snapshot magic is
// not ours and is left alone.
func (s *Store) loadSnapshot() {
	f, err := os.Open(s.snapPath)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck // read-only
	size, ok := snapFileSize(f)
	if !ok {
		return
	}
	var hdr [snapHeaderLen]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[:len(snapMagic)]) != snapMagic {
		return
	}
	// From here the file is ours: whatever its state, it must not load twice.
	defer func() { _ = os.Remove(s.snapPath) }()
	count, ok := trailerCount(f, size)
	if hdr[len(snapMagic)] != snapVersion || !ok {
		return
	}
	if s.readRecords(bufio.NewReader(io.LimitReader(f, size-int64(snapHeaderLen)-snapTrailerLen))) != nil ||
		s.snapLoad.seen != count {
		s.snapLoad.lost = true
	}
	now := time.Now()
	// Everything loaded is stale as of now, whatever it was when written (T-33).
	_ = s.ep.set(s.ep.globalTag, store.Epoch{At: now, Mode: store.EpochSoft})
	if s.snapLoad.lost {
		// T-33: a purge's hard epoch may be among what was lost, and the
		// writer puts epochs last. Fail closed: nothing loaded may be served
		// stale.
		_ = s.ep.set(s.ep.globalTag, store.Epoch{At: now, Mode: store.EpochHard})
	}
}

func snapFileSize(f *os.File) (int64, bool) {
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < int64(snapHeaderLen+snapTrailerLen) {
		return 0, false
	}
	return fi.Size(), true
}

// trailerCount returns the record count in the last record if it is a
// well-formed trailer, so a file cut short by a crash is ignored whole
// (05 §5.5).
func trailerCount(f *os.File, size int64) (uint64, bool) {
	var t [snapTrailerLen]byte
	if _, err := f.ReadAt(t[:], size-snapTrailerLen); err != nil {
		return 0, false
	}
	if t[0] != snapKindTrailer || t[1] != 8 || binary.BigEndian.Uint32(t[10:]) != crc32.Checksum(t[:10], castagnoli) {
		return 0, false
	}
	return binary.BigEndian.Uint64(t[2:10]), true
}

// readRecords streams records until the framing breaks. A record with a bad
// CRC is skipped and the stream continues; a bad length prefix ends the load,
// since the next record cannot be found (T-21: no length is trusted before it
// is checked against maxRec and the bytes left). It returns an error when
// the stream ended before a clean end of file.
func (s *Store) readRecords(r *bufio.Reader) error {
	maxRec := uint64(s.MaxObjectBytes()) + uint64(snapKeyLen) + 1<<10 // headroom for codec framing
	for {
		kind, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		n, err := binary.ReadUvarint(r)
		if err != nil || n > maxRec {
			s.snapLoad.skipped++
			return errors.New("store: memory: snapshot: bad record length")
		}
		buf := make([]byte, 1+binary.MaxVarintLen64+int(n)+4) //nolint:gosec // n <= maxRec
		buf = append(buf[:0], kind)
		buf = binary.AppendUvarint(buf, n)
		start := len(buf)
		buf = buf[:start+int(n)+4] //nolint:gosec // n <= maxRec
		if _, err := io.ReadFull(r, buf[start:]); err != nil {
			s.snapLoad.skipped++
			return err
		}
		s.snapLoad.seen++
		body := buf[:start+int(n)] //nolint:gosec // n <= maxRec
		if binary.BigEndian.Uint32(buf[len(body):]) != crc32.Checksum(body, castagnoli) {
			s.snapLoad.skipped++
			s.snapLoad.lost = true // the damaged record may have been an epoch
			continue
		}
		s.loadRecord(kind, buf[start:len(body)])
	}
}

func (s *Store) loadRecord(kind byte, p []byte) {
	st := &s.snapLoad
	switch kind {
	case snapKindEntry:
		if len(p) <= snapKeyLen {
			st.skipped++
			return
		}
		e, err := store.Decode(p[snapKeyLen:], s.MaxObjectBytes())
		if err != nil {
			st.skipped++
			return
		}
		// A hand-edited file could carry a future request time and so sort
		// after the load-time soft epoch (T-33).
		if now := time.Now(); e.RequestTime.After(now) {
			e.RequestTime = now
		}
		switch s.put(store.Key(p[:snapKeyLen]), e, true) {
		case putStored:
			st.loaded++
		case putExpired:
			st.expired++
		default:
			st.dropped++
		}
	case snapKindEpoch:
		if len(p) != snapEpochLen {
			st.skipped++
			st.lost = true
			return
		}
		at := time.Unix(0, int64(binary.BigEndian.Uint64(p[len(store.Tag{}):]))) //nolint:gosec // bit pattern
		if err := s.ep.set(store.Tag(p[:len(store.Tag{})]), store.Epoch{At: at, Mode: store.EpochHard}); err != nil {
			if !errors.Is(err, store.ErrUnavailable) {
				st.skipped++
			} else {
				st.dropped++
			}
			st.lost = true
		}
	default:
		st.skipped++
		st.lost = true
	}
}
