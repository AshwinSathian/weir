package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"
)

// Wire format constants (05 §6).
const (
	codecMagic    = "WEIR"
	codecVersion  = 1
	codecPreamble = 8 // magic, version, kind, flags
	maxStatus     = 999
)

// Field tags (05 §6).
const (
	fStatus byte = iota + 1
	fStoredAt
	fRequestTime
	fResponseTime
	fDate
	fCorrInitAge
	fLifetime
	fSWR
	fSIE
	fETag
	fLastModified
	fFetchDuration
	fVaryName
	fTag
	fHeader
	fBody
	fVariantRef
	fRetryAfter
	fExpires
	fOwner
)

// Decode errors wrap ErrUnavailable: a store reports an undecodable record
// as unavailable and the engine treats it as a miss (05 §6).
var (
	errDecodeSize      = fmt.Errorf("store: decode entry: larger than the object limit: %w", ErrUnavailable)
	errDecodePreamble  = fmt.Errorf("store: decode entry: bad magic, version or kind: %w", ErrUnavailable)
	errDecodeTruncated = fmt.Errorf("store: decode entry: length beyond the buffer: %w", ErrUnavailable)
	errDecodeDuplicate = fmt.Errorf("store: decode entry: duplicate field: %w", ErrUnavailable)
	errDecodeValue     = fmt.Errorf("store: decode entry: malformed field value: %w", ErrUnavailable)

	errEncodeRange = errors.New("store: encode entry: kind, status or time out of range")
	errEncodeValue = errors.New("store: encode entry: empty vary name, header name or header value list")
)

// Encode serializes e in the store codec (05 §6), for stores that keep
// entries outside the process. Zero-valued singular fields are omitted.
// Times travel as Unix nanoseconds, so the monotonic reading and location
// are dropped (FR-FRS-8). An unknown kind, a status outside [0, 999], a non-zero time
// outside the years 1678 to 2262, an empty vary name, an empty header name
// or a header name with no values is an error.
func Encode(e *Entry) ([]byte, error) {
	// Decode rejects these, so encoding them would turn a successful Set
	// into a record every Get reports as unavailable.
	if e.Kind == 0 || e.Kind > KindNegative || e.Status < 0 || e.Status > maxStatus {
		return nil, errEncodeRange
	}
	w := encoder{b: make([]byte, 0, e.Size())}
	w.b = append(w.b, codecMagic...)
	w.b = append(w.b, codecVersion, byte(e.Kind))
	w.b = binary.BigEndian.AppendUint16(w.b, uint16(e.Flags))
	if e.Status != 0 {
		w.field(fStatus, binary.AppendUvarint(nil, uint64(e.Status)))
	}
	w.time(fStoredAt, e.StoredAt)
	w.time(fRequestTime, e.RequestTime)
	w.time(fResponseTime, e.ResponseTime)
	w.time(fDate, e.Date)
	w.int64(fCorrInitAge, int64(e.CorrectedInitialAge))
	w.int64(fLifetime, int64(e.Lifetime))
	w.int64(fSWR, int64(e.SWR))
	w.int64(fSIE, int64(e.SIE))
	if e.ETag != "" {
		w.field(fETag, []byte(e.ETag))
	}
	w.time(fLastModified, e.LastModified)
	w.int64(fFetchDuration, int64(e.FetchDuration))
	for _, name := range e.VaryNames {
		if name == "" {
			return nil, errEncodeValue
		}
		w.field(fVaryName, []byte(name))
	}
	for _, tg := range e.Tags {
		w.field(fTag, tg[:])
	}
	for _, name := range slices.Sorted(maps.Keys(e.Header)) {
		if name == "" || len(e.Header[name]) == 0 {
			return nil, errEncodeValue
		}
		v := appendBytes(nil, name)
		v = binary.AppendUvarint(v, uint64(len(e.Header[name])))
		for _, s := range e.Header[name] {
			v = appendBytes(v, s)
		}
		w.field(fHeader, v)
	}
	if len(e.Body) > 0 {
		w.field(fBody, e.Body)
	}
	for _, ref := range e.Variants {
		n, ok := unixNanos(ref.Expires)
		if !ok {
			return nil, errEncodeRange
		}
		w.field(fVariantRef, binary.BigEndian.AppendUint64(ref.Key[:], uint64(n))) //nolint:gosec // two's-complement bits, reversed in decodeInt64
	}
	w.int64(fRetryAfter, int64(e.RetryAfter))
	w.time(fExpires, e.Expires)
	if e.Owner != (Tag{}) {
		w.field(fOwner, e.Owner[:])
	}
	if w.err != nil {
		return nil, w.err
	}
	return w.b, nil
}

type encoder struct {
	b   []byte
	err error
}

func (w *encoder) field(tag byte, v []byte) {
	w.b = binary.AppendUvarint(append(w.b, tag), uint64(len(v)))
	w.b = append(w.b, v...)
}

func (w *encoder) int64(tag byte, n int64) {
	if n != 0 {
		w.field(tag, binary.BigEndian.AppendUint64(nil, uint64(n))) //nolint:gosec // two's-complement bits, reversed in decodeInt64
	}
}

func (w *encoder) time(tag byte, t time.Time) {
	n, ok := unixNanos(t)
	if !ok {
		w.err = errEncodeRange
	}
	w.int64(tag, n)
}

// appendBytes appends s with a uvarint length prefix.
func appendBytes(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// unixNanos maps the zero time to 0 and any other time to its Unix
// nanoseconds; ok is false when t is outside the int64 range. The instant
// 1970-01-01T00:00:00Z therefore decodes as the zero time.
func unixNanos(t time.Time) (int64, bool) {
	if t.IsZero() {
		return 0, true
	}
	n := t.UnixNano()
	return n, time.Unix(0, n).Equal(t)
}

// Decode parses a record written by Encode. Input longer than maxBytes (the
// store's object limit) is rejected before anything is allocated, and every
// length is checked against the remaining input before it is used, so
// allocation stays proportional to len(b) (T-21). Unknown field tags are
// skipped. The result does not alias b. Errors wrap ErrUnavailable.
func Decode(b []byte, maxBytes int64) (*Entry, error) {
	if int64(len(b)) > maxBytes {
		return nil, errDecodeSize
	}
	if len(b) < codecPreamble || string(b[:4]) != codecMagic || b[4] != codecVersion ||
		b[5] == 0 || Kind(b[5]) > KindNegative {
		return nil, errDecodePreamble
	}
	d := decoder{e: &Entry{Kind: Kind(b[5]), Flags: Flags(binary.BigEndian.Uint16(b[6:8]))}}
	for r := b[codecPreamble:]; len(r) > 0; {
		tag := r[0]
		v, rest, ok := cutBytes(r[1:])
		if !ok {
			return nil, errDecodeTruncated
		}
		r = rest
		if err := d.field(tag, v); err != nil {
			return nil, err
		}
	}
	return d.e, nil
}

type decoder struct {
	e          *Entry
	seen       uint32 // singular tags already decoded
	lastHeader string // header names must be strictly ascending
}

func (d *decoder) field(tag byte, v []byte) error {
	e := d.e
	switch tag {
	case fVaryName, fTag, fHeader, fVariantRef:
	default:
		if tag >= fStatus && tag <= fOwner {
			if d.seen&(1<<tag) != 0 {
				return errDecodeDuplicate
			}
			d.seen |= 1 << tag
		}
	}
	var err error
	switch tag {
	case fStatus:
		s, k := binary.Uvarint(v)
		if k <= 0 || k != len(v) || s > maxStatus {
			return errDecodeValue
		}
		e.Status = int(s)
	case fStoredAt:
		e.StoredAt, err = decodeTime(v)
	case fRequestTime:
		e.RequestTime, err = decodeTime(v)
	case fResponseTime:
		e.ResponseTime, err = decodeTime(v)
	case fDate:
		e.Date, err = decodeTime(v)
	case fCorrInitAge:
		e.CorrectedInitialAge, err = decodeDuration(v)
	case fLifetime:
		e.Lifetime, err = decodeDuration(v)
	case fSWR:
		e.SWR, err = decodeDuration(v)
	case fSIE:
		e.SIE, err = decodeDuration(v)
	case fETag:
		e.ETag = string(v)
	case fLastModified:
		e.LastModified, err = decodeTime(v)
	case fFetchDuration:
		e.FetchDuration, err = decodeDuration(v)
	case fVaryName:
		// Every repeated item carries at least one byte, so a record cannot
		// decode into far more slice headers than it has bytes (T-21).
		if len(v) == 0 {
			return errDecodeValue
		}
		e.VaryNames = append(e.VaryNames, string(v))
	case fTag:
		if len(v) != len(Tag{}) {
			return errDecodeValue
		}
		e.Tags = append(e.Tags, Tag(v))
	case fHeader:
		err = d.header(v)
	case fBody:
		if len(v) > 0 { // an empty body decodes as nil, as Encode omits it
			e.Body = bytes.Clone(v)
		}
	case fVariantRef:
		if len(v) != len(Key{})+8 {
			return errDecodeValue
		}
		exp, _ := decodeTime(v[len(Key{}):])
		e.Variants = append(e.Variants, VariantRef{Key: Key(v[:len(Key{})]), Expires: exp})
	case fRetryAfter:
		e.RetryAfter, err = decodeDuration(v)
	case fExpires:
		e.Expires, err = decodeTime(v)
	case fOwner:
		if len(v) != len(Tag{}) {
			return errDecodeValue
		}
		e.Owner = Tag(v)
	}
	return err
}

// header decodes one header field: name, value count, then the values.
func (d *decoder) header(v []byte) error {
	nameBytes, v, ok := cutBytes(v)
	if !ok {
		return errDecodeTruncated
	}
	name := string(nameBytes)
	if name == "" || d.e.Header != nil && name <= d.lastHeader {
		return errDecodeValue
	}
	count, k := binary.Uvarint(v)
	// Each value takes at least its one-byte length, so count is bounded by
	// the bytes left before anything is allocated (T-21).
	if k <= 0 || count == 0 || count > uint64(len(v)-k) { //nolint:gosec // k <= len(v) when k > 0
		return errDecodeTruncated
	}
	v = v[k:]
	vals := make([]string, 0, count)
	for range count {
		s, rest, ok := cutBytes(v)
		if !ok {
			return errDecodeTruncated
		}
		vals = append(vals, string(s))
		v = rest
	}
	if len(v) != 0 {
		return errDecodeValue
	}
	if d.e.Header == nil {
		d.e.Header = http.Header{}
	}
	d.e.Header[name] = vals
	d.lastHeader = name
	return nil
}

// cutBytes splits a uvarint length prefix and that many bytes off b.
func cutBytes(b []byte) (v, rest []byte, ok bool) {
	n, k := binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) { //nolint:gosec // k <= len(b) when k > 0
		return nil, nil, false
	}
	end := k + int(n) //nolint:gosec // n <= len(b)-k, checked above
	return b[k:end], b[end:], true
}

func decodeInt64(v []byte) (int64, error) {
	if len(v) != 8 {
		return 0, errDecodeValue
	}
	return int64(binary.BigEndian.Uint64(v)), nil //nolint:gosec // two's-complement bits written by Encode
}

func decodeDuration(v []byte) (time.Duration, error) {
	n, err := decodeInt64(v)
	return time.Duration(n), err
}

// decodeTime returns a UTC time, or the zero time for 0.
func decodeTime(v []byte) (time.Time, error) {
	n, err := decodeInt64(v)
	if err != nil || n == 0 {
		return time.Time{}, err
	}
	return time.Unix(0, n).UTC(), nil
}
