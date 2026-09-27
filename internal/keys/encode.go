package keys

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/AshwinSathian/weir/store"
)

// keyVersion prefixes every primary-key encoding. Bump it when a change
// would make old encodings collide or change meaning (06 §6 item 2).
const keyVersion = "weir/key/v1"

// Field tags of the primary-key encoding (04 §3.2).
const (
	tagMethod      = 0x01
	tagScheme      = 0x02
	tagHost        = 0x03
	tagPath        = 0x04
	tagQuery       = 0x05
	tagHeaderName  = 0x10
	tagHeaderValue = 0x11
	tagCookieName  = 0x20
	tagCookieValue = 0x21
)

// Header is one Key.Headers entry after normalization. Value is ignored
// when Present is false.
type Header struct {
	Name, Value string
	Present     bool
}

// KeyInput is every input to the primary key (FR-KEY-2), already
// normalized and rewritten as forwarded.
type KeyInput struct {
	Method      string // method class: "GET" for GET and HEAD
	Scheme      string
	Host        string
	Path        string
	Query       string
	Headers     []Header // one per Key.Headers name, config order
	CookieNames []string // Key.Cookies, config order
	Cookies     []Cookie // present cookies as keyedCookies returns them
}

// bufPool holds encoding buffers, retained at their largest size. That
// size is bounded by the request limits (MaxPathBytes, MaxQueryBytes,
// MaxKeyedHeaderBytes) that every input passed before reaching the encoder.
var bufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 512)
	return &b
}}

// PrimaryKey returns SHA-256 over the canonical encoding of in (FR-KEY-1,
// ADR-3). No per-process seed enters it (FR-KEY-12).
func PrimaryKey(in *KeyInput) store.Key {
	bp := bufPool.Get().(*[]byte)
	*bp = appendKey((*bp)[:0], in)
	k := store.Key(sha256.Sum256(*bp))
	bufPool.Put(bp)
	return k
}

// appendKey appends the 04 §3.2 encoding. T-3: every field is tagged and
// length-prefixed, so no field's bytes can pass for another's.
func appendKey(b []byte, in *KeyInput) []byte {
	b = append(b, keyVersion...)
	b = appendField(b, tagMethod, in.Method)
	b = appendField(b, tagScheme, in.Scheme)
	b = appendField(b, tagHost, in.Host)
	b = appendField(b, tagPath, in.Path)
	b = appendField(b, tagQuery, in.Query)
	for _, h := range in.Headers {
		b = appendField(b, tagHeaderName, h.Name)
		if !h.Present {
			b = append(b, 0)
			continue
		}
		b = append(b, 1)
		b = appendField(b, tagHeaderValue, h.Value)
	}
	// Cookies is a subsequence of CookieNames, so one pass pairs them.
	cs := in.Cookies
	for _, name := range in.CookieNames {
		b = appendField(b, tagCookieName, name)
		if len(cs) == 0 || cs[0].Name != name {
			b = append(b, 0)
			continue
		}
		b = append(b, 1)
		b = appendField(b, tagCookieValue, cs[0].Value)
		cs = cs[1:]
	}
	// Leftovers mean a caller broke that contract. Key them anyway, since
	// the same slice builds the forwarded Cookie header (INV-1): the cost
	// of the bug is a cache split, never a value the key misses.
	for _, ck := range cs {
		b = appendField(b, tagCookieName, ck.Name)
		b = append(b, 1)
		b = appendField(b, tagCookieValue, ck.Value)
	}
	return b
}

func appendField(b []byte, tag byte, s string) []byte {
	b = append(b, tag)
	return appendLenPrefixed(b, s)
}

func appendLenPrefixed(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}
