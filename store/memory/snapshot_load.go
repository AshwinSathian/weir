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
	if hdr[len(snapMagic)] != snapVersion || !validTrailer(f, size) {
		return
	}
	s.readRecords(bufio.NewReader(io.LimitReader(f, size-int64(snapHeaderLen)-snapTrailerLen)))
	// Everything loaded is stale as of now, whatever it was when written (T-33).
	_ = s.ep.set(s.ep.globalTag, store.Epoch{At: time.Now(), Mode: store.EpochSoft})
}

func snapFileSize(f *os.File) (int64, bool) {
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < int64(snapHeaderLen+snapTrailerLen) {
		return 0, false
	}
	return fi.Size(), true
}

// validTrailer reports whether the last record is a well-formed trailer, so
// a file cut short by a crash is ignored whole (05 §5.5).
func validTrailer(f *os.File, size int64) bool {
	var t [snapTrailerLen]byte
	if _, err := f.ReadAt(t[:], size-snapTrailerLen); err != nil {
		return false
	}
	return t[0] == snapKindTrailer && t[1] == 8 &&
		binary.BigEndian.Uint32(t[10:]) == crc32.Checksum(t[:10], castagnoli)
}

// readRecords streams records until the framing breaks. A record with a bad
// CRC is skipped and the stream continues; a bad length prefix ends the load,
// since the next record cannot be found (T-21: no length is trusted before it
// is checked against maxRec and the bytes left).
func (s *Store) readRecords(r *bufio.Reader) {
	maxRec := uint64(s.MaxObjectBytes()) + uint64(snapKeyLen) + 1<<10 // headroom for codec framing
	for {
		kind, err := r.ReadByte()
		if err != nil {
			return
		}
		n, err := binary.ReadUvarint(r)
		if err != nil || n > maxRec {
			s.snapLoad.skipped++
			return
		}
		buf := make([]byte, 1+binary.MaxVarintLen64+int(n)+4) //nolint:gosec // n <= maxRec
		buf = append(buf[:0], kind)
		buf = binary.AppendUvarint(buf, n)
		start := len(buf)
		buf = buf[:start+int(n)+4] //nolint:gosec // n <= maxRec
		if _, err := io.ReadFull(r, buf[start:]); err != nil {
			s.snapLoad.skipped++
			return
		}
		body := buf[:start+int(n)] //nolint:gosec // n <= maxRec
		if binary.BigEndian.Uint32(buf[len(body):]) != crc32.Checksum(body, castagnoli) {
			s.snapLoad.skipped++
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
			return
		}
		at := time.Unix(0, int64(binary.BigEndian.Uint64(p[len(store.Tag{}):]))) //nolint:gosec // bit pattern
		if err := s.ep.set(store.Tag(p[:len(store.Tag{})]), store.Epoch{At: at, Mode: store.EpochHard}); err != nil {
			if !errors.Is(err, store.ErrUnavailable) {
				st.skipped++
				return
			}
			st.dropped++
		}
	default:
		st.skipped++
	}
}
