package store

import (
	"encoding/binary"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// gen builds values from fuzz bytes; it returns zeros once the bytes run out.
type gen struct{ b []byte }

func (g *gen) u8() byte {
	if len(g.b) == 0 {
		return 0
	}
	c := g.b[0]
	g.b = g.b[1:]
	return c
}

func (g *gen) take(n int) []byte {
	n = min(n, len(g.b))
	v := g.b[:n]
	g.b = g.b[n:]
	return v
}

func (g *gen) i64() int64 {
	var v [8]byte
	copy(v[:], g.take(8))
	return int64(binary.BigEndian.Uint64(v[:]))
}

func (g *gen) str() string { return string(g.take(int(g.u8() % 16))) }

// time returns the zero time for 0, else a UTC instant with no monotonic
// reading, which is what Decode produces.
func (g *gen) time() time.Time {
	if n := g.i64(); n != 0 {
		return time.Unix(0, n).UTC()
	}
	return time.Time{}
}

func (g *gen) key() (k [32]byte) {
	copy(k[:], g.take(32))
	return k
}

// genEntry builds an entry in the form Decode returns: empty collections are
// nil, and names are non-empty with at least one value per header name.
func genEntry(data []byte) *Entry {
	g := &gen{data}
	e := &Entry{
		Kind:                Kind(g.u8()%4 + 1),
		Flags:               Flags(uint16(g.u8())<<8 | uint16(g.u8())),
		Status:              int(binary.BigEndian.Uint16([]byte{g.u8(), g.u8()}) % 1000),
		StoredAt:            g.time(),
		RequestTime:         g.time(),
		ResponseTime:        g.time(),
		Date:                g.time(),
		CorrectedInitialAge: time.Duration(g.i64()),
		Lifetime:            time.Duration(g.i64()),
		SWR:                 time.Duration(g.i64()),
		SIE:                 time.Duration(g.i64()),
		ETag:                g.str(),
		LastModified:        g.time(),
		FetchDuration:       time.Duration(g.i64()),
		Owner:               g.key(),
		RetryAfter:          time.Duration(g.i64()),
		Expires:             g.time(),
	}
	if b := g.str(); b != "" {
		e.Body = []byte(b)
	}
	for range g.u8() % 4 {
		e.VaryNames = append(e.VaryNames, "v"+g.str())
	}
	for range g.u8() % 4 {
		e.Tags = append(e.Tags, g.key())
	}
	for range g.u8() % 4 {
		e.Variants = append(e.Variants, VariantRef{Key: g.key(), Expires: g.time()})
	}
	for range g.u8() % 4 {
		if e.Header == nil {
			e.Header = http.Header{}
		}
		name := http.CanonicalHeaderKey("h" + g.str()) // the codec refuses other spellings
		for range g.u8()%3 + 1 {
			e.Header[name] = append(e.Header[name], g.str())
		}
	}
	return e
}

func mustEncode(t *testing.T, e *Entry) []byte {
	t.Helper()
	b, err := Encode(e)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

// FR-SNP-1, FR-FRS-8, 05 §6, §8 CodecRoundTrip: decode(encode(e)) equals e
// for every field.
func FuzzCodecRoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("\x00\x01\x02\x00\xc8some bytes that fill times and durations and names"))
	f.Fuzz(func(t *testing.T, data []byte) {
		want := genEntry(data)
		b := mustEncode(t, want)
		got, err := Decode(b, int64(len(b)))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip mismatch\n got: %+v\nwant: %+v", got, want)
		}
	})
}

// 05 §6, NFR-2, INV-6, T-21: Decode never panics, allocates in proportion to its
// input, and whatever it accepts re-encodes to an equal entry.
func FuzzDecodeEntry(f *testing.F) {
	seed, err := Encode(genEntry([]byte("seed entry with some header and body bytes in it")))
	if err != nil {
		f.Fatalf("Encode: %v", err)
	}
	f.Add(seed)
	f.Add([]byte("WEIR\x01\x01\x00\x00\x10\xff\xff\xff\xff\xff\xff\xff\xff\x7f"))
	f.Add([]byte("WEIR\x01\x01\x00\x00\x0f\x06\x01a\xff\xff\xff\x0f"))
	f.Fuzz(func(t *testing.T, b []byte) {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		e, err := Decode(b, 1<<20)
		runtime.ReadMemStats(&after)
		if got, limit := after.TotalAlloc-before.TotalAlloc, 64*uint64(len(b))+64<<10; got > limit {
			t.Fatalf("Decode of %d bytes allocated %d bytes, limit %d", len(b), got, limit)
		}
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Decode error %v does not wrap ErrUnavailable", err)
			}
			return
		}
		again, err := Decode(mustEncode(t, e), 1<<20)
		if err != nil {
			t.Fatalf("re-decode: %v", err)
		}
		if !reflect.DeepEqual(again, e) {
			t.Fatalf("re-encode changed the entry\n got: %+v\nwant: %+v", again, e)
		}
	})
}

// field appends one tag-length-value field.
func field(b []byte, tag byte, v ...byte) []byte {
	b = append(b, tag)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// NFR-2, T-21, 05 §6: bad input is a decode error (reported as
// ErrUnavailable).
func TestDecodeRejects(t *testing.T) {
	hdr := []byte("WEIR\x01\x01\x00\x00")
	with := func(fs ...[]byte) []byte {
		b := append([]byte{}, hdr...)
		for _, f := range fs {
			b = append(b, f...)
		}
		return b
	}
	eight := make([]byte, 8)
	eight[7] = 1
	header := func(name string, vals ...string) []byte {
		v := binary.AppendUvarint(nil, uint64(len(name)))
		v = append(v, name...)
		v = binary.AppendUvarint(v, uint64(len(vals)))
		for _, s := range vals {
			v = binary.AppendUvarint(v, uint64(len(s)))
			v = append(v, s...)
		}
		return field(nil, 0x0F, v...)
	}
	tests := []struct {
		name string
		b    []byte
		max  int64
	}{
		{"empty input", nil, 100},
		{"short preamble", []byte("WEIR\x01"), 100},
		{"wrong magic", []byte("WEIX\x01\x01\x00\x00"), 100},
		{"wrong version", []byte("WEIR\x02\x01\x00\x00"), 100},
		{"zero kind", []byte("WEIR\x01\x00\x00\x00"), 100},
		{"unknown kind", []byte("WEIR\x01\x05\x00\x00"), 100},
		{"input over the object limit", with(field(nil, 0x10, 'x')), 10},
		{"length beyond the buffer", with([]byte{0x10, 0x05, 'a'}), 100},
		{"huge uvarint length", with([]byte{0x10, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}), 100},
		{"truncated length", with([]byte{0x10}), 100},
		{"duplicate singular field", with(field(nil, 0x02, eight...), field(nil, 0x02, eight...)), 100},
		{"duplicate body", with(field(nil, 0x10, 'a'), field(nil, 0x10, 'b')), 100},
		{"time not 8 bytes", with(field(nil, 0x02, 1, 2, 3)), 100},
		{"tag not 32 bytes", with(field(nil, 0x0E, 1)), 100},
		{"variant ref not 40 bytes", with(field(nil, 0x11, make([]byte, 39)...)), 100},
		{"status with trailing bytes", with(field(nil, 0x01, 200&0x7f|0x80, 1, 0)), 100},
		{"empty status", with(field(nil, 0x01)), 100},
		{"status over 999", with(field(nil, 0x01, 0xe8, 0x07)), 100},
		{"header value count beyond the field", with(field(nil, 0x0F, 1, 'a', 0xff, 0xff, 0x03)), 100},
		{"header value beyond the field", with(field(nil, 0x0F, 1, 'a', 1, 5, 'x')), 100},
		{"header field with trailing bytes", with(field(nil, 0x0F, 1, 'a', 1, 1, 'x', 'y')), 100},
		{"header names out of order", with(header("B", "1"), header("A", "1")), 100},
		{"duplicate header name", with(header("A", "1"), header("A", "2")), 100},
		{"empty header name", with(header("", "1")), 100},
		{"header with no values", with(header("A")), 100},
		{"header name not canonical", with(header("set-cookie", "1")), 100}, // T-8
		{"header name in mixed case", with(header("Cache-control", "1")), 100},
		{"empty vary name", with(field(nil, 0x0D)), 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Decode(tt.b, tt.max)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Decode = %+v, %v; want an error wrapping ErrUnavailable", e, err)
			}
		})
	}
}

// 05 §6: bounds are checked before allocation, so a huge claimed length or
// value count costs nothing (T-21).
func TestDecodeBoundsBeforeAllocation(t *testing.T) {
	hdr := "WEIR\x01\x01\x00\x00"
	tests := []struct {
		name string
		b    []byte
	}{
		{"huge body length", []byte(hdr + "\x10\xff\xff\xff\xff\xff\xff\xff\xff\x7f")},
		{"huge header value count", []byte(hdr + "\x0f\x07\x01a\xff\xff\xff\xff\x0f")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 100 {
				if _, err := Decode(tt.b, 1<<20); err == nil {
					t.Fatal("Decode accepted a length beyond the buffer")
				}
			}
			runtime.ReadMemStats(&after)
			if got := after.TotalAlloc - before.TotalAlloc; got > 100*1024 {
				t.Fatalf("100 rejected decodes allocated %d bytes, want < 100 KiB", got)
			}
		})
	}
}

// FR-SNP-1, 05 §6: unknown field tags are skipped, including repeated ones.
func TestDecodeSkipsUnknownFields(t *testing.T) {
	want := genEntry([]byte("an entry that will get unknown fields appended"))
	b := mustEncode(t, want)
	b = field(b, 0x15, 1, 2, 3)
	b = field(b, 0x15)
	b = field(b, 0xFF, make([]byte, 300)...)
	got, err := Decode(b, int64(len(b)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown fields changed the entry\n got: %+v\nwant: %+v", got, want)
	}
}

// P4, 05 §6: the decoded entry does not alias the input, so a store may reuse
// its read buffer (P4).
func TestDecodeCopiesInput(t *testing.T) {
	want := &Entry{Kind: KindResponse, Status: 200, Body: []byte("body"), ETag: `"e"`, Header: http.Header{"A": {"v"}}}
	b := mustEncode(t, want)
	got, err := Decode(b, int64(len(b)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	clear(b)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entry changed when the input was cleared\n got: %+v\nwant: %+v", got, want)
	}
}

// 05 §6, FR-FRS-8: times cross the codec as Unix nanoseconds; values the
// format cannot hold are an Encode error, not a silent change.
func TestEncodeRejectsUnrepresentable(t *testing.T) {
	tests := []struct {
		name string
		e    *Entry
	}{
		{"zero kind", &Entry{}},
		{"unknown kind", &Entry{Kind: KindNegative + 1}},
		{"negative status", &Entry{Kind: KindResponse, Status: -1}},
		{"status over 999", &Entry{Kind: KindResponse, Status: 1000}},
		{"time after 2262", &Entry{Kind: KindResponse, Date: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{"empty vary name", &Entry{Kind: KindVarySpec, VaryNames: []string{""}}},
		{"empty header name", &Entry{Kind: KindResponse, Header: http.Header{"": {"v"}}}},
		{"header name with no values", &Entry{Kind: KindResponse, Header: http.Header{"A": nil}}},
		{"header name not canonical", &Entry{Kind: KindResponse, Header: http.Header{"etag": {"v"}}}},
		{"variant expiry before 1678", &Entry{Kind: KindVarySpec, Variants: []VariantRef{{Expires: time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if b, err := Encode(tt.e); err == nil {
				t.Fatalf("Encode = %x, nil; want an error", b)
			}
		})
	}
}

// FR-FRS-8: the codec strips the monotonic reading and keeps the instant.
func TestCodecStripsMonotonic(t *testing.T) {
	now := time.Now()
	e := &Entry{Kind: KindHitForMiss, StoredAt: now, Expires: now.Add(time.Hour)}
	b := mustEncode(t, e)
	got, err := Decode(b, int64(len(b)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !got.StoredAt.Equal(now) || got.StoredAt != got.StoredAt.Round(0) {
		t.Fatalf("StoredAt = %v, want %v without a monotonic reading", got.StoredAt, now)
	}
}
