package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"time"
)

// Key is the 32-byte cache key of one stored record (04 §2).
type Key [32]byte

// Tag is a 32-byte purge tag. Tags are computed by internal/keys.
type Tag [32]byte

// TagGlobal returns the tag every stored entry carries, used by a purge of
// everything. It lives here, not in internal/keys, so stores can keep it
// exactly (05 E-5) while importing only this package (02 §3).
func TagGlobal() Tag { return sha256.Sum256([]byte("weir/tag/v1\x00global")) }

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
	FlagMustRevalidate  Flags = 1 << iota // must-revalidate
	FlagProxyRevalidate                   // proxy-revalidate
	FlagNoCache                           // unqualified or qualified, treated the same
	FlagSMaxAge                           // freshness came from s-maxage
	FlagHeuristic                         // freshness is heuristic
	FlagPublic                            // public
	FlagFromAuthorized                    // stored from a request that carried Authorization
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
	Tags                []Tag    // [global, URI, groups...] in this order; the engine splits on it (05 E-12)
	Owner               Tag      // origin tag; opaque to stores, used for per-owner byte quotas (memory store MaxBytesPerOwner)

	// KindVarySpec: VaryNames (shared field) plus Variants.
	Variants []VariantRef // bounded by MaxVariants, copy-on-write

	// KindNegative: Status (shared field) plus RetryAfter.
	RetryAfter time.Duration

	// Every kind.
	Expires time.Time // absolute retention deadline; the store drops the record after this
}

// Size constants (04 §2). entryOverhead is the fixed per-record charge,
// covering the struct and store bookkeeping; variantRefSize is one VariantRef
// (a 32-byte Key and a 24-byte time.Time). The other two are what a header
// costs in memory beyond its bytes on a 64-bit platform: stringOverhead is a
// string header, charged per header value and per vary name, and
// headerNameOverhead is a map slot (a string header for the name, a slice
// header for its values, and the map's own bookkeeping). T-21: without them
// a response made of thousands of one-byte values took about 16 times the
// memory it was charged for.
const (
	entryOverhead      = 256
	variantRefSize     = 56
	stringOverhead     = 16
	headerNameOverhead = 48
)

// Size returns the bytes a byte-weighted store accounts for e: len(Body) +
// header bytes + 48 per header name + 16 per header value + vary name bytes
// + 16 per vary name + 32 per tag + 56 per variant ref + 256 (04 §2). Header
// bytes are the name and the value for every value. Vary specs carry no
// body, so their name and ref bytes must count or the store's byte bound
// undercounts them (NFR-3).
func (e *Entry) Size() int64 {
	n := int64(len(e.Body)) + int64(len(e.Tags))*int64(len(Tag{})) +
		int64(len(e.Variants))*variantRefSize + entryOverhead
	for _, name := range e.VaryNames {
		n += int64(len(name)) + stringOverhead
	}
	for name, vals := range e.Header {
		n += headerNameOverhead
		for _, v := range vals {
			n += int64(len(name)+len(v)) + stringOverhead
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
// Matching is exact and other kinds are left alone. On a context that ends
// mid-scan it returns the count so far and ErrUnavailable. The engine writes
// the epochs first, so Scrub frees memory and never decides reachability.
type Scrubber interface {
	Scrub(ctx context.Context, tags []Tag) (int, error)
}

// SharedTagEpochs is an optional Store capability: NewestEpochShared equals
// NewestEpoch over tags and shared together, except that a tag in shared
// never matches in EpochInvalid mode. The engine passes group tags there,
// which it writes only soft and hard epochs to, so an invalid-plane match on
// one is a sketch false positive that would hit every entry of the group at
// once (05 E-12, T-29). One call, so a remote store keeps one round trip.
// A wrapper that embeds only Store hides the capability and brings the
// false positive back, so it must forward this method too.
type SharedTagEpochs interface {
	NewestEpochShared(ctx context.Context, tags, shared []Tag, since time.Time) (Epoch, bool, error)
}

// VarySetter is an optional Store capability: a compare-and-set for the vary
// spec at a primary key (04 §6.7, 05 §2.3). SetVarySpec stores next at k only
// if the live record at k is still prev, the *Entry that Get returned (a nil
// prev means no live record), and reports whether it did. A store compares
// the record it holds, not the pointer's address: a memory store compares
// identity, a remote store the stored bytes. Declining next (too large, past
// its Expires) is reported as swapped with a nil error, like Set (S-4). The
// engine retries a lost swap a bounded number of times, so concurrent writers
// cannot lose each other's variant references. A wrapper that embeds only
// Store hides the capability and the engine falls back to Get then Set.
type VarySetter interface {
	SetVarySpec(ctx context.Context, k Key, prev, next *Entry) (swapped bool, err error)
}

// FetchTracker is an optional capability of an in-process Store: a count,
// per key, of the fetches in progress that may still store a record under it
// (01 FR-INV-1). The engine calls FetchBegin before it sends a request that
// may be stored under k and FetchEnd once whatever it stored is in the store.
// An unsafe request then asks FetchInProgress before it decides that a URI
// with no record has nothing to invalidate. The count lives in the store, not
// in the engine, because several engines can share one store, and a fetch one
// of them runs must be visible to the others. A remote store cannot offer it:
// another node's fetch is invisible. A wrapper that embeds only Store hides
// the capability, and the engine then writes every invalidation epoch.
type FetchTracker interface {
	FetchBegin(k Key)
	FetchEnd(k Key)
	FetchInProgress(k Key) bool
}

// Locker is an optional Store capability: a lock per key that the engines
// sharing a store use to fetch a missing response once among them (01
// FR-COA-11). Lock either takes k's lock and returns the func that releases
// it, or reports that another holder has it. A lock whose holder never
// releases it ends after ttl. Held reports whether anyone holds k's lock.
// release bounds its own call and never blocks past the store's call
// timeout; only the holder's release removes the lock.
type Locker interface {
	Lock(ctx context.Context, k Key, ttl time.Duration) (release func(), ok bool, err error)
	Held(ctx context.Context, k Key) (bool, error)
}

// Sizer is an optional Store capability reporting current bytes and the
// largest record the store accepts (FR-LCY-1).
type Sizer interface {
	Bytes() int64
	MaxObjectBytes() int64
}

// Store errors.
var (
	// ErrNotFound is returned by Get when no live record has the key. It is a
	// normal miss, not a failure.
	ErrNotFound = errors.New("store: not found")
	// ErrUnavailable means the store could not answer: a timeout, a lost
	// connection, overload, an undecodable remote record or a closed store.
	// The engine treats any other store error the same way, counts it toward
	// the store breaker and serves from the origin instead.
	ErrUnavailable = errors.New("store: unavailable")
)
