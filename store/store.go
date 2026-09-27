package store

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Key is the 32-byte cache key of one stored record (04 §2).
type Key [32]byte

// Tag is a 32-byte purge tag. Tags are computed by internal/keys.
type Tag [32]byte

// Kind is the kind of record an Entry holds.
type Kind uint8

// Kind values. The zero Kind is invalid.
const (
	KindResponse   Kind = iota + 1 // a stored response
	KindVarySpec                   // the Vary names and variant list for a URI
	KindHitForMiss                 // a marker that the URI is not storable
	KindNegative                   // a cached origin error (negative caching)
)

// Flags records response directives that affect serving a stored response.
type Flags uint16

// Flags values.
const (
	FlagMustRevalidate  Flags = 1 << iota
	FlagProxyRevalidate       // proxy-revalidate
	FlagNoCache               // unqualified or qualified, treated the same
	FlagSMaxAge               // freshness came from s-maxage
	FlagHeuristic             // freshness is heuristic
	FlagPublic                // public
	FlagFromAuthorized        // stored from a request that carried Authorization
)

// Entry is one stored record. It is immutable after Set (P4): the store and
// the engine never write to an Entry, its Header map or its Body slice once
// it has been handed to Set.
type Entry struct {
	Kind     Kind
	StoredAt time.Time // keeps its monotonic reading in process; the codec strips it (FR-FRS-8)

	// KindResponse
	Status              int
	Header              http.Header // immutable after Set
	Body                []byte      // immutable after Set
	RequestTime         time.Time
	ResponseTime        time.Time
	Date                time.Time
	CorrectedInitialAge time.Duration
	Lifetime            time.Duration // jittered
	SWR, SIE            time.Duration // effective windows, 0 when not permitted
	Flags               Flags
	ETag                string
	LastModified        time.Time // zero when absent
	FetchDuration       time.Duration
	VaryNames           []string // canonical names this variant was keyed on; nil when no Vary
	Tags                []Tag    // implicit tags + groups
	Owner               Tag      // origin tag; opaque to stores, used for per-owner quotas (M14)

	// KindVarySpec: VaryNames (shared field) plus Variants.
	Variants []VariantRef // bounded by MaxVariants, copy-on-write

	// KindNegative: Status (shared field) plus RetryAfter.
	RetryAfter time.Duration

	// Every kind.
	Expires time.Time // absolute retention deadline; the store drops the record after this
}

// entryOverhead is the fixed per-record charge in Size, covering the struct
// and store bookkeeping.
const entryOverhead = 256

// Size returns the bytes a byte-weighted store accounts for e:
// len(Body) + header bytes + 32 per tag + 256 (04 §2).
func (e *Entry) Size() int64 {
	n := int64(len(e.Body)) + int64(len(e.Tags))*int64(len(Tag{})) + entryOverhead
	for name, vals := range e.Header {
		for _, v := range vals {
			n += int64(len(name) + len(v))
		}
	}
	return n
}

// VariantRef points from a KindVarySpec record to one variant.
type VariantRef struct {
	Key     Key
	Expires time.Time
}

// EpochMode is the severity of a purge epoch.
type EpochMode uint8

// EpochMode values, ordered by severity. The zero EpochMode is invalid.
const (
	EpochSoft    EpochMode = iota + 1 // stale as of the purge time
	EpochInvalid                      // invalidated by an unsafe method (FR-INV)
	EpochHard                         // unreachable
)

// Epoch is a purge marker for a tag: records carrying the tag and stored
// before At are treated according to Mode.
type Epoch struct {
	At   time.Time
	Mode EpochMode
}

// Info describes a Store to the engine.
type Info struct {
	Name   string
	Remote bool // true: the engine wraps calls in Timeouts.Store and the store breaker
}

// Store is the storage contract (05 §1). Implementations are safe for
// concurrent use.
type Store interface {
	Get(ctx context.Context, k Key) (*Entry, error)
	Set(ctx context.Context, k Key, e *Entry) error // retention is e.Expires
	Delete(ctx context.Context, k Key) error
	SetEpoch(ctx context.Context, t Tag, ep Epoch) error
	// NewestEpoch returns the most severe, then newest, epoch among tags whose
	// At >= since; ok=false when none qualifies.
	NewestEpoch(ctx context.Context, tags []Tag, since time.Time) (ep Epoch, ok bool, err error)
	Info() Info
	Close() error
}

// Scrubber is an optional Store capability: Scrub deletes response records
// whose tags intersect tags and returns how many it deleted (FR-PRG-8).
type Scrubber interface {
	Scrub(ctx context.Context, tags []Tag) (int, error)
}

// Sizer is an optional Store capability reporting current bytes and the
// largest record the store accepts (FR-LCY-1).
type Sizer interface {
	Bytes() int64
	MaxObjectBytes() int64
}

// Store errors.
var (
	ErrNotFound    = errors.New("store: not found")
	ErrUnavailable = errors.New("store: unavailable")
)
