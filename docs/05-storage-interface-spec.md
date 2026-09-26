# Weir storage interface specification

Status: v1.0
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md), [04-lld.md §2](04-lld.md)
Seed name: `02-storage-interface-spec.md` (renumbered, see [docs/README.md](README.md))

This is the contract between the engine and any store. The seed asked for it to be written before the second implementation exists and designed "against a second implementation on paper". Section 7 is that paper design for Valkey. If the Valkey design needs an interface change, the change happens here first and the memory store follows.

## 1. Interface

```go
package store

type Store interface {
	Get(ctx context.Context, k Key) (*Entry, error)
	Set(ctx context.Context, k Key, e *Entry) error
	Delete(ctx context.Context, k Key) error
	SetEpoch(ctx context.Context, t Tag, ep Epoch) error
	NewestEpoch(ctx context.Context, tags []Tag, since time.Time) (Epoch, bool, error)
	Info() Info
	Close() error
}
```

Types (`Key`, `Tag`, `Entry`, `Kind`, `Flags`, `Epoch`, `EpochMode`, `VariantRef`, `Info`) are defined in [04-lld.md §2](04-lld.md) and live in `store/store.go`.

## 2. Method contracts

### 2.1 General rules

- S-1. All methods are safe for concurrent use.
- S-2. Every method that takes a context MUST return promptly when the context is done, with an error for which `errors.Is(err, ErrUnavailable)` is true. Remote stores MUST NOT block past the context deadline. The engine sets deadlines only for stores whose `Info().Remote` is true.
- S-3. Errors: `ErrNotFound` means the key holds no live record. `ErrUnavailable` means the store could not answer (timeout, connection failure, overload, decode failure of a remote record). Stores wrap these with context using `%w`. The engine treats any other error as `ErrUnavailable`.
- S-4. A store MAY decline to keep any record, and MAY drop any record at any time before its `Expires`. The engine is correct with a store that keeps nothing. A store MUST NOT return a record after its `Expires` has passed by more than the store's clock granularity (1 s for Valkey's `PX`, exact for memory). The engine re-checks `Expires` anyway.
- S-5. Immutability: after `Set(k, e)` returns, the store MUST NOT modify `e` or anything reachable from it, and callers MUST NOT modify it either. `Get` MAY return the same pointer to many callers (memory store) or a fresh decoded copy (Valkey). No caller may depend on which.
- S-6. Stores MUST NOT interpret HTTP semantics. They never look at headers, status codes or freshness fields. The only time-based rule a store applies is `Expires`, and for epochs, the pruning rule in §4.4.

### 2.2 Get

Returns the record stored at `k`, of any `Kind`, or `ErrNotFound`. A record whose `Expires` is in the past is `ErrNotFound`. Memory stores increment the record's access frequency on a successful `Get` (§5.3); that is the only side effect `Get` may have.

### 2.3 Set

Stores `e` at `k`, replacing any existing record of any kind. `e.Expires` is the retention deadline. `Set` with `e.Expires` in the past is a no-op that returns nil. A store MAY clamp `Expires` to its maximum retention (§4.4). `Set` returns nil when the store declined to keep the record (S-4); declining is not an error.

### 2.4 Delete

Removes the record at `k`. Deleting a missing key returns nil. The engine uses `Delete` only for cleanup of hard-purged records it happens to find; purge itself never deletes (FR-INV-3, FR-PRG-4).

### 2.5 SetEpoch and NewestEpoch

See §4.

### 2.6 Info

`Info{Name, Remote}`. Constant for the life of the store.

### 2.7 Close

Releases resources. After `Close`, every method returns `ErrUnavailable`. `Close` is idempotent.

## 3. Record kinds

| Kind | Stored at | Meaning | Engine writes it |
|---|---|---|---|
| `KindResponse` | primary key (no `Vary`) or variant key | a stored response | after a storable fetch or a 304 freshening |
| `KindVarySpec` | primary key | the `Vary` names and live variants for this primary key | with each storable response that had `Vary` |
| `KindHitForMiss` | coalescing key | the resource was not storable recently | after a non-storable fetch |
| `KindNegative` | coalescing key | the origin failed recently | after an origin-health failure with nothing stale to serve |

A primary key holds either a response or a vary spec (or a marker or negative record when no spec is known). Writing a response with `Vary` to a primary key that holds a plain response replaces it with a vary spec; the old response becomes unreachable and expires.

## 4. Epochs

### 4.1 Model

An epoch is `(At, Mode)` attached to a `Tag`. Modes are ordered by severity: `EpochSoft < EpochInvalid < EpochHard`. Each response entry carries its tags in `Entry.Tags`: the global tag, its origin tag, its URI tag, and one tag per `Cache-Groups` member. Tag derivation is in [04-lld.md §2](04-lld.md).

### 4.2 Storage rules

- E-1. A store keeps, per tag, the latest `At` for each mode separately (exactly or as a conservative upper bound, §4.4). Recording a soft epoch never overwrites a hard epoch for the same tag, and vice versa. (If only the last epoch were kept, a soft purge after a hard purge would let hard-purged entries become servable again.)
- E-2. `SetEpoch(t, ep)` sets `latest[t][ep.Mode] = max(existing, ep.At)`.
- E-3. `NewestEpoch(tags, since)` considers every `(tag, mode)` whose `At >= since`. Among those it returns the most severe mode, and for that mode the latest `At`. It returns `ok = false` when none qualify.
- E-4. The engine passes the entry's `RequestTime` as `since` (FR-PRG-7).

Note on E-3: `At >= since` means an epoch written in the same clock tick as a request was sent applies to that request's response. Erring toward purging is the safe direction.

### 4.3 Clocks

Epochs compare the purging node's clock with the fetching node's clock. In Phase 1 both are the same process, and the memory store measures every epoch and `since` value as a monotonic offset from a base instant taken at `New` (`t.Sub(base)` uses monotonic readings when both values have them), so wall-clock steps cannot make a purge miss. Sketch cells hold those offsets in whole seconds, rounded up. In Phase 1.5 nodes must keep clocks within `MaxClockSkew` (a Valkey store option, default 1 s), and the Valkey store adds `MaxClockSkew` to `since` comparisons conservatively: `At + MaxClockSkew >= since`. This can purge a response fetched up to one skew interval after the purge, which is the safe direction.

### 4.4 Bounds

Tags are attacker-influenced: a flood of `POST /x?r=<random>` requests that the origin answers with 2xx creates one URI-invalidation epoch per request, and RFC 9111 §4.4 makes that invalidation a MUST. An exact per-tag table must therefore either grow without bound or, on overflow, fall back to something coarse like a global epoch, which would let about 100 000 cheap requests invalidate the whole cache. Neither is acceptable (T-29). Stores bound epoch memory as follows.

- E-5. Global tag: kept exactly (three timestamps).
- E-6. Hard epochs: kept exactly per tag. Only operator `Purge` calls create them. The memory store caps them at `MaxHardEpochs` (default 10 000); `SetEpoch` for a new hard tag beyond the cap returns an error wrapping `ErrUnavailable`, and `Purge` reports it so the operator can use `All`. Entries older than `MaxRetention` cannot be affected, so hard epochs older than that are pruned.
- E-7. Soft and invalid epochs: kept in a fixed-size max-timestamp sketch, one plane per mode. A plane is an array of `EpochSlots` (default 2^19) `uint32` cells holding monotonic seconds since the store's base instant, rounded up (§4.3). `SetEpoch(t, ep)` raises the cell at each of `d = 2` positions derived from `t` (two halves of `maphash` of the tag with a per-process seed) to at least `ceil(ep.At)`. A lookup for tag `t` reads the minimum over its `d` cells.
- E-8. The sketch never under-invalidates: every cell a tag maps to is at least that tag's true epoch, so the minimum is too. It can over-invalidate an entry only when all `d` of its cells were raised by other tags after the entry's request time. With 60 000 distinct invalidations inside one entry lifetime and default sizing, the chance is about 4% (`(1 - e^{-2·60000/2^19})^2`), falling fast as volume drops. Over-invalidation means one extra conditional request for that entry, never an error. Rounding epoch times up to whole seconds also only over-invalidates.
- E-9. Memory is fixed: 2 planes × 2^19 cells × 4 bytes = 4 MiB at defaults, whatever the attack volume.
- E-10. Fast path: the store tracks the newest `At` written in any mode; when `since` is after it, `NewestEpoch` answers `ok = false` without touching the sketch. Between purges, every hit takes this path.
- E-11. Maximum retention: `Set` clamps `Expires` to `StoredAt + MaxRetention` (default 24 h). This bounds how long hard epochs must be kept. The `uint32` seconds in the sketch cover 136 years from the store's base time.

## 5. Memory store (`store/memory`)

### 5.1 Configuration

```go
type Config struct {
	MaxBytes     int64         // 0: 256 MiB
	Shards       int           // 0: 16; must be a power of two
	MaxRetention  time.Duration // 0: 24h
	MaxHardEpochs int           // 0: 10000
	EpochSlots    int           // 0: 1 << 19; power of two
	OnEvict      func(queue string, n int) // optional; "small", "main", "expired"
}

func New(cfg Config) (*Store, error)
func (s *Store) Bytes() int64 // current accounted bytes, for EngineStats
func (s *Store) MaxObjectBytes() int64 // largest record the store can admit: 10% of one shard
```

The engine, when it creates the default store, passes `Storable.MaxObjectBytes` through a check against `MaxObjectBytes()` (FR-LCY-1): with 256 MiB and 16 shards a shard is 16 MiB, the small queue 1.6 MiB, so the default 1 MiB object limit fits.

### 5.2 Sharding

Shard index = `maphash.Comparable(seed, key) & (Shards-1)` with a per-process random `maphash.Seed`. Using the raw SHA-256 bytes would let an attacker grind request inputs offline until many keys land in one shard and concentrate evictions there (ADR-3). Each shard has its own mutex and its own byte budget `MaxBytes / Shards`.

### 5.3 S3-FIFO per shard

Structures:

```go
type node struct {
	key   store.Key
	e     *store.Entry
	size  int64
	freq  atomic.Uint32 // 0..3, updated without the write lock
	queue uint8      // small or main
	prev, next *node // intrusive doubly linked FIFO
}

type shard struct {
	mu        sync.RWMutex
	m         map[store.Key]*node
	small     fifo  // capacity: 10% of shard bytes
	main      fifo  // capacity: 90% of shard bytes
	ghost     ghost // fingerprints of keys evicted from small; count-bounded
	bytes     int64
	cap       int64
}

type ghost struct {
	ring []uint64          // FIFO of fingerprints
	head int
	set  map[uint64]uint32 // fingerprint -> occurrences in ring
}
```

Algorithm (adapted from Yang et al., SOSP 2023, with byte weights):

- `Get(k)`: read-lock; look up; if missing, unlock and return `ErrNotFound`; if not expired, raise `freq` by one with a compare-and-swap loop capped at 3 (skipped when already 3, which is the common case for hot keys), unlock and return `e`. If expired: unlock, take the write lock, look the key up again (it may have been replaced), unlink it if it is still the same expired node, unlock, return `ErrNotFound`. Hits therefore take only the read lock, so a hot key does not serialize its readers.
- `Set(k, e)`: compute `size = e.Size()`. If `size > small.cap` return nil (declined). Lock. If `k` exists, replace the entry in place, adjust `bytes` by the size difference, keep queue and `freq`. Otherwise: if the key's fingerprint is in the ghost set, remove it from the ghost and insert at the head of `main`; else insert at the head of `small`. Then evict until `bytes <= cap`. Unlock.
- Evict: if `small.bytes > small.cap` (or `main` is empty), evict from `small`, else from `main`.
  - From `small` tail: if the node is expired, drop it. If `freq >= 2`, move it to the head of `main` with `freq = 0`. Otherwise drop it and add its fingerprint to the ghost.
  - From `main` tail: if expired, drop it. If `freq >= 1`, decrement `freq` and move it to the head of `main` (reinsertion). Otherwise drop it.
  - Loop until enough bytes are freed. Each iteration either frees bytes or decrements a frequency, and frequencies are at most 3, so the loop terminates within `4 × len(main) + len(small)` steps.
- Ghost size: at most as many fingerprints as `main` currently holds entries, with a floor of 1024. Fingerprint = low 64 bits of `maphash` of the key with a second per-process seed.
- `Delete(k)`: lock; unlink; adjust bytes; unlock.

Why this shape: entries requested once (the signature of a query-string busting flood, T6.8) sit only in `small` and are evicted from there, so they cannot displace entries in `main`. That is the admission control asked for in seed T6.11. The promotion threshold `freq >= 2` matches the reference implementation's `move-to-main-threshold=2` in libCacheSim.

`OnEvict` is called after the shard lock is released, with counts batched per `Set` call.

### 5.4 Epoch table

The global tag's three timestamps and the newest-epoch value are atomics. Hard epochs live in a `sync.RWMutex`-protected `map[store.Tag]time.Time`, pruned opportunistically inside `SetEpoch` (at most 64 expired tags per call, no background goroutine). The soft and invalid sketch planes are `[]atomic.Uint32`; raising a cell is a compare-and-swap loop, reading is a plain atomic load, so neither takes a lock.

## 6. Entry encoding (`store/codec.go`)

Needed by remote stores. Implemented in Phase 1 so `storetest` can round-trip it and so the format is reviewed before Phase 1.5 depends on it.

```
magic   "WEIR"                 4 bytes
version 0x01                   1 byte
kind    uint8
flags   uint16 big-endian
then a sequence of fields, each: tag uint8, length uvarint, value bytes
  0x01 status        uvarint
  0x02 storedAt      int64 unix nanos (8 bytes big-endian)
  0x03 requestTime   int64
  0x04 responseTime  int64
  0x05 date          int64
  0x06 corrInitAge   int64 nanos
  0x07 lifetime      int64 nanos
  0x08 swr           int64 nanos
  0x09 sie           int64 nanos
  0x0A etag          bytes
  0x0B lastModified  int64 (0 = absent)
  0x0C fetchDuration int64 nanos
  0x0D varyName      bytes (repeated, in order)
  0x0E tag           32 bytes (repeated)
  0x0F header        name bytes, then uvarint count, then each value as uvarint length + bytes (repeated per name, sorted by name)
  0x10 body          bytes
  0x11 variantRef    32-byte key + int64 expires (repeated)
  0x12 retryAfter    int64 nanos
  0x13 expires       int64
```

Decoding rules: unknown field tags are skipped (forward compatibility); a length beyond the remaining buffer, a duplicate singular field, or a wrong magic or version is a decode error, which the store reports as `ErrUnavailable` and the engine treats as a miss. Total encoded size is bounded by the store's object limit before decoding allocates anything. `FuzzDecodeEntry` covers the decoder.

## 7. Valkey store on paper (Phase 1.5)

This section exists to prove the interface above does not assume in-process semantics.

| Interface call | Valkey operation |
|---|---|
| `Get(k)` | `GET weir:{e}:<hex(k)>`, decode |
| `Set(k, e)` | `SET weir:{e}:<hex(k)> <encoded> PXAT <e.Expires in ms>` |
| `Delete(k)` | `DEL` |
| `SetEpoch(t, ep)` | hard: Lua `max` update of `weir:{e}:epoch:hard:<hex(t)>` with expiry `MaxRetention`; soft and invalid: Lua raising the `d` sketch cells with `BITFIELD ... u32` on one 2 MiB string per plane (`weir:{e}:sketch:<mode>`), which keeps memory fixed as in E-9; plus the newest-epoch key |
| `NewestEpoch(tags, since)` | fast path on a client-side cached newest-epoch key; otherwise one pipelined round trip: `BITFIELD GET u32` for the sketch cells of all tags plus `MGET` of their hard keys |
| `Info()` | `{Name: "valkey", Remote: true}` |

Notes that follow from the table and require nothing new from the interface:

- `{e}` is an operator-configured hash tag, so in cluster mode the per-request keys can be spread (`{e}` omitted for entries) while epoch keys share a slot.
- `PXAT` gives absolute expiry from `Entry.Expires`, so retention needs no clock translation.
- Eviction is Valkey's (`maxmemory-policy allkeys-lfu` recommended). The engine does not care which policy (seed T6.11: "the storage interface must not assume a single eviction policy").
- Timeouts: every call runs under the engine's `Timeouts.Store` deadline; the client library is configured with no internal retries longer than that.
- Client-side caching (RESP3 tracking) can make `Get` for hot keys local; invalidation messages from Valkey keep it coherent. This is an optimization inside the store and invisible to the engine.
- What the paper design exposed: the vary-spec read-modify-write race ([04-lld.md §6.7](04-lld.md)) is wider across nodes. It stays bounded by the per-partition cap per node times the node count. A Lua compare-and-set for vary specs is the planned mitigation, still behind the same `Set` call.

## 8. Conformance suite (`store/storetest`)

```go
func Run(t *testing.T, newStore func(t *testing.T) store.Store)
```

Every store implementation calls `storetest.Run` from its tests. Cases (each a subtest):

| Case | Asserts |
|---|---|
| `GetMissing` | `ErrNotFound` |
| `SetGetRoundTrip` | every field of every kind survives (deep equality, times compared with `Equal`) |
| `SetReplacesAnyKind` | response replaced by vary spec and back |
| `ExpiredIsNotFound` | record with past `Expires` is not returned (uses synctest for memory; real clock with 2 s margin for remote) |
| `SetPastExpiresIsNoop` | |
| `DeleteMissingOK` | |
| `ContextCanceled` | canceled context yields an error that `errors.Is` `ErrUnavailable` within 100 ms (remote stores; memory store may ignore context and succeed) |
| `ClosedStore` | all calls return `ErrUnavailable` after `Close`; `Close` twice is fine |
| `EpochPerModeKept` | hard then soft on one tag: `NewestEpoch` returns hard for entries before the hard epoch |
| `EpochNeverUnderInvalidates` | after 200 000 random soft epochs, every tag's lookup is at least its own epoch (property test) |
| `EpochHardCap` | the hard-epoch cap returns an error wrapping `ErrUnavailable`; soft epochs beyond any count are accepted |
| `EpochSinceBoundary` | `At == since` applies |
| `EpochFastPath` | no tags newer than since: `ok == false` |
| `EpochsMaxAcrossTags` | newest across several tags, most severe first |
| `ConcurrentSetGet` | 64 goroutines, random keys, `-race` clean, every `Get` returns either `ErrNotFound` or an entry previously `Set` at that key |
| `NoMutationAfterSet` | stores do not alter entries passed to `Set` (compare against a deep copy) |
| `CodecRoundTrip` | `Encode`/`Decode` identity over generated entries |

Memory-store-specific tests (in `store/memory`): byte accounting never exceeds `MaxBytes` after `Set` returns; scan resistance (`TestS3FIFOScanResistance`: a working set of hot keys survives a flood of 100× its size in one-hit keys with at least 90% of hot keys still present); ghost promotion; oversized record declined; shard distribution is uniform for adversarially chosen keys that share their first bytes.
