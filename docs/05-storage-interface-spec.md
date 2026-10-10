# Weir storage interface specification

Status: v1.0
Date: 2026-10-11
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

Optional capability interfaces a store may implement (the engine checks with a type assertion on the interface, never on a concrete store type):

```go
type Scrubber interface { Scrub(ctx context.Context, tags []Tag) (int, error) } // M15, FR-PRG-8
type Sizer    interface { Bytes() int64; MaxObjectBytes() int64 }               // EngineStats, FR-LCY-1
type SharedTagEpochs interface {                                                 // M10-08, E-12, T-29
	NewestEpochShared(ctx context.Context, tags, shared []Tag, since time.Time) (Epoch, bool, error)
}
type VarySetter interface {                                                      // P25-05, 04 §6.7, FR-KEY-10
	SetVarySpec(ctx context.Context, k Key, prev, next *Entry) (swapped bool, err error)
}
```

## 2. Method contracts

### 2.1 General rules

- S-1. All methods are safe for concurrent use.
- S-2. Every method that takes a context MUST return promptly when the context is done, with an error for which `errors.Is(err, ErrUnavailable)` is true. Remote stores MUST NOT block past the context deadline. The engine sets deadlines only for stores whose `Info().Remote` is true.
- S-3. Errors: `ErrNotFound` means the key holds no live record. `ErrUnavailable` means the store could not answer (timeout, connection failure, overload, decode failure of a remote record). Stores wrap these with context using `%w`. The engine treats any other error as `ErrUnavailable`.
- S-4. A store MAY decline to keep any record, and MAY drop any record at any time before its `Expires`. The engine is correct with a store that keeps nothing. A store MUST NOT return a record after its `Expires` has passed by more than the store's clock granularity (1 s for Valkey's `PX`, exact for memory). The engine re-checks `Expires` anyway.
- S-5. Immutability: after `Set(k, e)` returns, the store MUST NOT modify `e` or anything reachable from it, and callers MUST NOT modify it either. `Get` MAY return the same pointer to many callers (memory store) or a fresh decoded copy (Valkey). No caller may depend on which.
- S-6. Stores MUST NOT interpret HTTP semantics. They never look at headers, status codes or freshness fields. The only time-based rule a store applies is `Expires`, and for epochs, the pruning rule in §4.4. `Entry.Owner` and `Entry.Tags` are opaque tags: a store may group by them (quotas, scrubbing) without knowing what they mean.

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

An epoch is `(At, Mode)` attached to a `Tag`. Modes are ordered by severity: `EpochSoft < EpochInvalid < EpochHard`. Each response entry carries its tags in `Entry.Tags`: the global tag, its URI tag, and one tag per distinct `Cache-Groups` member. The origin tag is the entry's `Owner` and is not among its tags. Tag derivation is in [04-lld.md §2](04-lld.md).

### 4.2 Storage rules

- E-1. A store keeps, per tag, the latest `At` for each mode separately (exactly or as a conservative upper bound, §4.4). Recording a soft epoch never overwrites a hard epoch for the same tag, and vice versa. (If only the last epoch were kept, a soft purge after a hard purge would let hard-purged entries become servable again.)
- E-2. `SetEpoch(t, ep)` sets `latest[t][ep.Mode] = max(existing, ep.At)`.
- E-3. `NewestEpoch(tags, since)` considers every `(tag, mode)` whose `At >= since`. Among those it returns the most severe mode, and for that mode the latest `At`. It returns `ok = false` when none qualify.
- E-4. The engine passes the entry's `RequestTime` as `since` (FR-PRG-7).

Note on E-3: `At >= since` means an epoch written in the same clock tick as a request was sent applies to that request's response. Erring toward purging is the safe direction.

### 4.3 Clocks

Epochs compare the purging node's clock with the fetching node's clock. In Phase 1 both are the same process, and the memory store measures every epoch and `since` value as a monotonic offset from a base instant taken at `New` (`t.Sub(base)` uses monotonic readings when both values have them), so wall-clock steps cannot make a purge miss. Sketch cells hold those offsets in whole seconds, rounded up. In Phase 2.5 nodes must keep clocks within `MaxClockSkew` (a Valkey store option, default 1 s), and the Valkey store adds `MaxClockSkew` to `since` comparisons conservatively: `At + MaxClockSkew >= since`. This can purge a response fetched up to one skew interval after the purge, which is the safe direction. The Valkey store stores epochs as Unix seconds rounded up (u32, saturating, valid to 2106) instead of offsets from a base instant, because nodes share no base. With rounding, the comparison window is up to `MaxClockSkew` + 1 s: a response fetched that long after a purge is purged again, so a URL invalidated every second never stays cached. Operators with one clock source set `NoClockSkew` (skew 0, since a zero `MaxClockSkew` means the 1 s default) and pay only the rounding second. Section 7 describes the shared sketch seed.

### 4.4 Bounds

Tags are attacker-influenced: a flood of `POST /x?r=<random>` requests that the origin answers with 2xx creates one URI-invalidation epoch per request, and RFC 9111 §4.4 makes that invalidation a MUST. An exact per-tag table must therefore either grow without bound or, on overflow, fall back to something coarse like a global epoch, which would let about 100 000 cheap requests invalidate the whole cache. Neither is acceptable (T-29). Stores bound epoch memory as follows.

- E-5. Global tag (`store.TagGlobal()`): kept exactly (three timestamps), and applied only to lookups whose tags include it.
- E-6. Hard epochs: kept exactly per tag. Only operator `Purge` calls create them. The memory store caps them at `MaxHardEpochs` (default 10 000); `SetEpoch` for a new hard tag beyond the cap returns an error wrapping `ErrUnavailable`, and `Purge` reports it so the operator can use `All`. The engine does not count a `Purge` write's error toward its store breaker (01 FR-STF-2), so the refusal cannot lock out lookups or `All`. Entries older than `MaxRetention` cannot be affected, so hard epochs older than that are pruned.
- E-7. Soft and invalid epochs: kept in a fixed-size max-timestamp sketch, one plane per mode. A plane is an array of `EpochSlots` (default 2^19) `uint32` cells holding monotonic seconds since the store's base instant, rounded up (§4.3). `SetEpoch(t, ep)` raises the cell at each of `d = 2` positions derived from `t` (two halves of `maphash` of the tag with a per-process seed; the Valkey store uses SHA-256 with a seed shared by all nodes, §7) to at least `ceil(ep.At)`. A lookup for tag `t` reads the minimum over its `d` cells.
- E-8. The sketch never under-invalidates: every cell a tag maps to is at least that tag's true epoch, so the minimum is too. It can over-invalidate an entry only when all `d` of its cells were raised by other tags after the entry's request time. With 60 000 distinct invalidations inside one entry lifetime and default sizing, the chance is about 4% per tag looked up (`(1 - e^{-2·60000/2^19})^2`), falling fast as volume drops. An entry looks up its URI tag and one tag per group, so an entry without groups sees about 4% and one with `g` groups about `1 - 0.96^(1+g)`; that is why the engine gives entries no tag that many of them share unless a purge can name it. A tag that many entries share fails for all of them at once. Group tags are the case: the engine writes only soft and hard epochs to them, so it looks them up through `SharedTagEpochs` (E-12), which never reads the invalid plane for them. A store without that capability keeps the old behavior: under a flood of URI invalidations a group has about a 4% chance that all its entries are revalidated before they are served, once each, and `CacheGroups.Ignore` does not change that. Writes count per tag as well: a response may invalidate up to `Limits.MaxGroups` (32) groups, each a soft epoch of its own. Over-invalidation means one extra conditional request for that entry, never an error. Rounding epoch times up to whole seconds also only over-invalidates which entries an epoch applies to: a response requested later in the purge's own second is purged again, once the fast path (E-10) no longer skips it. The returned `At` can be up to 1 s ahead of the clock. The engine treats an applicable epoch timed ahead of now as timed now (04 §4.3), so a soft purge is never delayed; its SWR and SIE windows, measured from `At`, can run up to 1 s long.
- E-9. Memory is fixed: 2 planes × 2^19 cells × 4 bytes = 4 MiB at defaults, whatever the attack volume.
- E-10. Fast path: the store tracks the newest `At` written in any mode; when `since` is after it, `NewestEpoch` answers `ok = false` without touching the sketch. Between purges, every hit takes this path.
- E-11. Maximum retention: `Set` clamps `Expires` to `RequestTime + MaxRetention` (default 24 h; `StoredAt` when `RequestTime` is zero, the time of the `Set` when both are). This bounds how long hard epochs must be kept: a hard epoch at `P` applies to records requested at or before `P`, so all of them are gone by `P + MaxRetention`. Clamping from `StoredAt` would let a record fetched across `P` outlive the pruned epoch by its fetch time. The `uint32` seconds in the sketch cover 136 years from the store's base time.
- V-1. Vary-spec compare-and-set: a store may implement `VarySetter`. `SetVarySpec(k, prev, next)` stores `next` at `k` only if the live record at `k` is still `prev`, the `*Entry` that `Get` returned (`prev == nil` means no live record; an expired record counts as none), and reports whether it did; a lost swap is `(false, nil)`. "Still `prev`" means the record the store holds, not the pointer's address: the memory store compares identity under the shard lock, a remote store compares the stored bytes (or a digest of them) in one server-side step. `next` that is too large or past its `Expires` is declined like a `Set` is (S-4) with `(true, nil)`, reported before `prev` is compared and without evicting the record at `k`. A store with per-owner quotas may still drop the old record when it refuses `next` for quota; the engine sets no owner on specs. A closed or unreachable store returns `ErrUnavailable`. The engine reads the spec, builds the next one, swaps, and on a lost swap rereads and rebuilds, at most 16 times. A writer whose variant ends up unlisted deletes it again, best effort (04 §6.7). A store without the capability keeps `Get` then `Set`, and concurrent writers of different variants can still lose references (04 §6.7). A wrapper that embeds only `Store` hides the capability; one that embeds a concrete store and overrides `Get` or `Set` must override `SetVarySpec` too, or the promoted method writes around the override.
- E-12. Shared tags: a store may implement `SharedTagEpochs`. `NewestEpochShared(tags, shared, since)` equals `NewestEpoch` over both lists, except that a tag in `shared` never matches in `EpochInvalid` mode. The engine splits `Entry.Tags` (`[global, URI, groups...]`, 04 §3) after the second tag and passes the groups as `shared`, so a sketch false positive in the invalid plane cannot revalidate a whole group at once. A soft or hard epoch on a shared tag is found as on any tag. One call, so a remote store keeps one round trip. The memory store implements it; the engine falls back to `NewestEpoch` when the store does not.

## 5. Memory store (`store/memory`)

### 5.1 Configuration

```go
type Config struct {
	MaxBytes     int64         // 0: 256 MiB
	Shards       int           // 0: 16; a power of two, at most 1 << 16 (MaxShards)
	MaxRetention     time.Duration // 0: 24h
	MaxHardEpochs    int           // 0: 10000
	EpochSlots       int           // 0: 1 << 19; a power of two, at most 1 << 26 (MaxEpochSlots)
	MaxBytesPerOwner int64         // 0: off; per shard (M14, FR-FAIR-2)
	SnapshotPath     string        // "": off (M13, FR-SNP-1..3)
	SnapshotTimeout  time.Duration // 0: 5s; bounds the snapshot written by Close
	OnEvict      func(queue string, n int) // optional; "small", "main", "expired"
}

func New(cfg Config) (*Store, error)
func (s *Store) Bytes() int64 // current accounted bytes, for EngineStats
func (s *Store) CloseContext(ctx context.Context) error // Close with the snapshot bounded by ctx (M13)
func (s *Store) MaxObjectBytes() int64 // largest record the store can admit: 10% of one shard
```

The engine, when it creates the default store, passes `Storable.MaxObjectBytes` through a check against `MaxObjectBytes()` (FR-LCY-1): with 256 MiB and 16 shards a shard is 16 MiB, the small queue 1.6 MiB, so the default 1 MiB object limit fits. A smaller store sized from `GOMEMLIMIT` (FR-MEM-1, down to 16 MiB) would not fit it with 16 shards, so the engine halves the shard count, down to 1, until one shard's small queue holds `Storable.MaxObjectBytes`.

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
- `Set(k, e)`: compute `size = e.Size()`. If `size > small.cap`, delete any record at `k` and return nil (declined), so a declined replacement never leaves the older record in place. Lock. If `k` exists, replace the entry in place, adjust `bytes` by the size difference, keep queue and `freq`. Otherwise: if the key's fingerprint is in the ghost set, remove it from the ghost and insert at the head of `main`; else insert at the head of `small`. Then evict until `bytes <= cap`. Unlock.
- Evict: if `small.bytes > small.cap` (or `main` is empty), evict from `small`, else from `main`.
  - From `small` tail: if the node is expired, drop it. If `freq >= 2`, move it to the head of `main` with `freq = 0`. Otherwise drop it and add its fingerprint to the ghost.
  - From `main` tail: if expired, drop it. If `freq >= 1`, decrement `freq` and move it to the head of `main` (reinsertion). Otherwise drop it.
  - Loop until enough bytes are freed. Each iteration either frees bytes or decrements a frequency, and frequencies are at most 3, so the loop terminates within `4 × len(main) + len(small)` steps.
- Ghost size: at most as many fingerprints as `main` currently holds entries, with a floor of 1024. Fingerprint = low 64 bits of `maphash` of the key with a second per-process seed.
- `Delete(k)`: lock; unlink; adjust bytes; unlock.

Why this shape: entries requested once (the signature of a query-string busting flood, T6.8) sit only in `small` and are evicted from there, so they cannot displace entries in `main`. That is the admission control asked for in seed T6.11. The promotion threshold `freq >= 2` matches the reference implementation's `move-to-main-threshold=2` in libCacheSim.

`OnEvict` is called after the shard lock is released, with counts batched per `Set` call.

Per-owner quota (M14): each shard keeps `map[store.Tag]int64` of bytes per owner; entries are removed from it when their node is unlinked, so its size is bounded by the owners present in the shard. The over-quota path in FR-FAIR-2 scans at most 64 nodes from each queue tail (128 in all) and never touches another owner's nodes.

Scrub (M15): for each shard in turn, take the write lock, walk every node, unlink response records whose `Tags` intersect the given tags, release. Worst case O(entries) total, but never more than one shard's worth of work under one lock. Matching is by exact tag, not the epoch sketch, and only response records are removed (vary specs, markers and negative records carry no tags). The context is checked between shards: on cancellation Scrub returns the count so far with `ErrUnavailable`. Scrub does not compare request times with the epoch, so it can also delete a response stored fresh after the purge, and it cannot stop a flight from storing one afterwards; the epoch, not the scan, is what makes entries unreachable.

### 5.4 Epoch table

The global tag's three timestamps and the newest-epoch value are atomics. Hard epochs live in a `sync.RWMutex`-protected `map[store.Tag]time.Time`, pruned opportunistically inside `SetEpoch` when a new tag finds the table full (at most 64 expired tags per call, no background goroutine; expired hard epochs left in place only match records that have already expired). The soft and invalid sketch planes are `[]atomic.Uint32`; raising a cell is a compare-and-swap loop, reading is a plain atomic load, so neither takes a lock.

### 5.5 Snapshot file (M13)

```
magic "WEIRSNAP", version 0x01, base wall time int64
then records: kind byte (0x01 entry, 0x02 hard epoch), uvarint length, payload, CRC-32C (Castagnoli) of kind+length+payload
entry payload: 32-byte key + codec-encoded Entry (§6)
hard-epoch payload: 32-byte tag + int64 at
trailer: record 0xFF with the record count; a file without a valid trailer is incomplete and ignored
```

Writer order: main-queue records (head to tail), then small-queue records, then hard epochs, then trailer. Loader behavior is FR-SNP-2 and FR-SNP-3; its failure modes are in docs/04 §13.3.

## 6. Entry encoding (`store/codec.go`)

Needed by remote stores. Implemented in Phase 1 so `storetest` can round-trip it and so the format is reviewed before Phase 2.5 depends on it.

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
  0x0F header        name as uvarint length + bytes, then uvarint count, then each value as uvarint length + bytes (repeated per name, sorted by name)
  0x10 body          bytes
  0x11 variantRef    32-byte key + int64 expires (repeated)
  0x12 retryAfter    int64 nanos
  0x13 expires       int64
  0x14 owner         32 bytes
```

```go
func Encode(e *Entry) ([]byte, error)
func Decode(b []byte, maxBytes int64) (*Entry, error) // errors wrap ErrUnavailable
```

Times are Unix nanoseconds and 0 stands for the zero time (so the instant 1970-01-01T00:00:00Z decodes as zero); `Encode` returns an error for an unknown kind, a non-zero time outside the int64 nanosecond range (years 1678 to 2262) a status outside [0, 999], an empty vary name, an empty header name, a header name not in canonical form (`http.CanonicalHeaderKey`) or a header name with no values. Zero-valued singular fields and empty collections are omitted, and decode as zero values and nil.

Decoding rules: unknown field tags are skipped (forward compatibility); a length beyond the remaining buffer, a duplicate singular field, a fixed-size field of the wrong size, a status over 999, header names not strictly ascending, an empty vary name, an empty header name, a header name not in canonical form or a header value count of 0, an unknown kind, or a wrong magic or version is a decode error, which the store reports as `ErrUnavailable` and the engine treats as a miss. Total encoded size is bounded by the store's object limit (`maxBytes`) before decoding allocates anything, and every length and header value count is checked against the remaining bytes before use. The decoded entry does not alias `b`. `FuzzDecodeEntry` covers the decoder.

Header names are canonical on both sides because every engine check reads stored headers by canonical name: a record carrying `set-cookie`, or `etag` beside `ETag`, would be served with fields no check saw (T-8). `Encode` refuses what `Decode` refuses, so a `Set` never succeeds on a record every `Get` then reports unavailable; the engine canonicalizes origin headers before it builds an entry (`TestStoredEntriesEncode`). Non-minimal uvarints are accepted: they decode to the same value, and nothing compares or hashes encoded records, so rejecting them would add a check per integer and protect nothing.

## 7. Valkey store (Phase 2.5)

This section exists to prove the interface above does not assume in-process semantics. It was a paper design until card P25-00 checked it against valkey-go v1.0.78 and Valkey semantics (2026-10-10).

| Interface call | Valkey operation |
|---|---|
| `Get(k)` | `GET <prefix>:<hex(k)>`, decode (`<prefix>:{e}:<hex(k)>` with `CoLocateEntries`) |
| `Set(k, e)` | `SET <key> <encoded> PXAT <e.Expires in ms>` |
| `Delete(k)` | `DEL` |
| `SetEpoch(t, ep)` | one Lua script; every epoch key (`hardidx`, `global`, both planes, `newest`, `meta`) is declared in `KEYS` on every call, so cluster mode accepts it, and the mandatory `{e}` puts them in one slot. Hard: `ZADD GT hardidx <At> <tag>`, after pruning members whose score is older than server `TIME` minus `MaxRetention + MaxClockSkew + 1 s` and refusing a new member beyond `MaxHardEpochs` (E-6; `ZSCORE` first, so re-purging a tag already present never fails on the cap). Global tag: a hash `global` with fields `soft`, `invalid`, `hard`, raised to the max (E-5). Soft and invalid on other tags: raise the `d = 2` cells of the plane string `sketch:<mode>` (2 MiB, created full-size with `SETRANGE`) with `BITFIELD ... OVERFLOW SAT` `GET`/`SET` `u32` (`BITFIELD` has no max operation, so the script reads and conditionally sets). Every write also raises `newest` to the max |
| `NewestEpoch(tags, since)`, `NewestEpochShared(tags, shared, since)` | E-10 fast path is a plain `GET newest`: present and older than `since` (minus skew) answers `ok = false` with no script. Newer means `newest + MaxClockSkew >= floor(since)` (`At` rounded up, `since` rounded down). Absent or newer: one read-only script (`EVALSHA_RO`, `BITFIELD_RO`) over the declared keys, one round trip (E-12). An absent `newest` with `meta` present is never read as "no epochs": the script computes the answer from the other keys and the next write rebuilds `newest` |
| `Info()` | `{Name: "valkey", Remote: true}` |

Notes that follow from the table and require nothing new from the interface:

- Key layout: `<prefix>` defaults to `weir`, so two deployments on one server need distinct prefixes. `{e}` is an operator-configured hash tag. Epoch keys (`hardidx`, `global`, `sketch:soft`, `sketch:invalid`, `newest`, `meta`) always carry it so one script touches one slot. Entry keys omit it by default, so cluster mode spreads entries over all slots; `CoLocateEntries` puts them in the epoch slot (the whole cache then lives on one primary).
- Epoch state is never evictable. Epoch keys carry no TTL, and the server must not evict keys without a TTL: `maxmemory-policy` is `volatile-lfu` (recommended), another `volatile-*` or `noeviction`. Every entry has a TTL from `PXAT`, so `volatile-*` evicts entries only. `allkeys-*` can evict an epoch key, and a lost epoch is a missed purge (T-29), not a cache miss. The first successful connect reads `CONFIG GET maxmemory-policy` on every node the client knows (all primaries and replicas in cluster mode) and the store stays `ErrUnavailable` on `allkeys-*`; `SkipPolicyCheck` is for managed services that disable `CONFIG`, where the operator owns the guarantee. Other applications writing TTL-less keys to the same server also defeat the guarantee, and a full server with no evictable keys refuses `SET` with OOM (`ErrUnavailable`). Under `noeviction`, a full server refuses writes; `SetEpoch` then returns an error wrapping `ErrUnavailable`, which `Purge` reports.
- Loss detection and repair: `meta` is a hash holding the sketch seed. Loss means `meta` is absent (flush, restart without persistence) or a sketch plane is absent while `meta` exists; an absent `hardidx` is the normal empty state (Valkey deletes empty sorted sets) and never counts as loss. The read script is read-only, so it only reports loss; the store then runs the repair, which is the same script as `SetEpoch(TagGlobal, {Hard, now})`: it recreates `meta` and the planes and sets `global.hard` to `ceil(server now)`, and the lookup retries. A hard global epoch is what the memory store writes at snapshot load (FR-SNP-2), so everything fetched before the loss is refetched once, however many lookups follow; entries fetched in the next `MaxClockSkew` + 1 s are purged once more (the same rounding window as 4.3, not a loop). It over-invalidates after a flush and never under-invalidates.
- Epoch writes (P25-03): the write script treats a loss as a loss in every call, not only in the repair: loss is `meta` without its field `v` (the seed alone, written first by a client, does not count) or a sketch plane absent. The script recreates `v`, creates missing planes full-size with `SETRANGE` (2 MiB each, no TTL), raises `global.hard` and `newest` to `ceil(server now)` and then does its own work, so recreating `meta` can never hide a loss. The first write or lookup on a fresh server therefore writes one global hard epoch at server time (entries fetched in that second are purged once more, as above). A server that ran P25-03 code has `meta.v` but no planes, so its first lookup or write after the upgrade reports a loss and purges everything fetched before it, once. `SetEpoch` with a mode outside soft, invalid, hard is refused with `ErrUnavailable`. Soft and invalid on any other tag (P25-03b) raise two cells of `sketch:soft` or `sketch:invalid`: the script checks the seed first and replies `SEED_CHANGED` with no effect on a mismatch, then repairs any loss, then reads both cells with `BITFIELD GET` and sets those below the new time with `OVERFLOW SAT SET`. The plane size is fixed at 2^19 cells (not a config field), so every node sharing a `Prefix` agrees on it. The read script checks loss before the seed (after a flush the repair comes first, then one reseed), takes the minimum of a tag's two cells (a zero cell means unset), and reads the arguments four per non-global tag: raw tag, two positions, shared flag. A lookup that names only the global tag needs no seed. `NewestEpochShared` is the same call with the shared flag set; a tag named in both lists is plain, as in the memory store; a shared global tag skips its `invalid` field. Because the store adds `MaxClockSkew` to the comparison (4.3), a more severe epoch of another tag that collided into the cells can be reported up to the skew before `since`; `EpochNeverUnderInvalidates` therefore checks the time only when the reported mode equals the tag's own. The cap refusal is a server error reply (`WEIRCAP`) mapped to `ErrUnavailable`. `NewestEpoch` with no tags answers "none" without a round trip. With `HardEpochWait` the hard write and `WAIT 1 <ms>` are one pipelined call on a dedicated connection (`EVAL`, not `EVALSHA`), because `WAIT` covers only writes made on its own connection; the replica count is ignored. The test `TestHardEpochWaitWaitsForReplica` needs a pausable replica (`WEIR_VALKEY_REPLICA_ADDR`) and skips without one, so CI does not run it.
- Sketch positions: `SHA-256(seed || tag)` split into two u32 values modulo the plane size, computed in Go (Valkey's Lua has only `redis.sha1hex` and no secret-grade random). The client draws 16 seed bytes from `crypto/rand` and runs `HSETNX meta seed <bytes>`, reads back the stored seed, and caches it. Scripts receive the positions and the first 8 bytes of the seed as arguments and reply `SEED_CHANGED` when `meta.seed` differs (after a flush); the store refetches the seed and retries once, so the first call and the call after a flush take two round trips. `maphash.Seed` is opaque and cannot be shared between nodes. Tags are SHA-256 outputs an attacker can compute for any URL; the secret seed is what stops targeted cell collisions. Epoch values are clamped to `[1, 2^32-1]` and written with `OVERFLOW SAT`, so a zero time or a negative argument cannot wrap to 2106.
- Shared settings and interim limits (P25-03 review): every store that shares a `Prefix` must use the same `MaxRetention` and `MaxClockSkew`, because the prune margin comes from the writing node and entries are clamped by the setting node's value; a node with a shorter `MaxRetention` prunes hard epochs that another node's entries still depend on. A hard epoch with a far-future `At` is never pruned and holds a cap slot until then; the engine only passes the current time. P25-03b removed the earlier refusal of soft and invalid writes on URI tags, so the engine's RFC 9111 4.4 invalidations no longer count toward the store breaker.
- Retention uses the server clock (`TIME`) for pruning; the prune margin `MaxClockSkew + 1 s` is the whole tolerance, so a node further behind the server than that loses a hard epoch early and the engine's `Expires` for entries (`PXAT`). Between the node clock and the server clock `PXAT` is only approximate; S-4 makes the engine re-check `Expires`, so correctness holds.
- Entry writes and reads (P25-02): `Set` that cannot encode a record (the codec rejects it) deletes the record at the key and returns nil, so `Set` leaves the new record or nothing, as the memory store does for an oversized one; a past-expiry or clamped-away `Set` is a no-op that leaves an older record (§2.3). `Get` returns `ErrNotFound` for a record past its `Expires` even if the server still holds it. `Decode` is bounded by the server's 512 MiB value limit; `GET` has already moved the bytes by then, so a value planted at an entry key by a writer with server access costs one download per `Get`. Planted values (a hostile string, or a wrong type) and decode failures count as `ErrUnavailable` and can open the store breaker; that needs write access to the server and is accepted. A per-store value cap would need a new config field (not added). Two deployments on one server must differ in `Prefix`, not only in `HashTag`: with the same prefix and no co-location they would share entry keys but not epochs, a silent missed purge. The scrubber (FR-PRG-8) must `GET` only keys whose suffix is 64 hex characters, since `SCAN MATCH <prefix>:*` also returns epoch keys, including the 2 MiB sketch strings.
- Known cost under an invalidation flood (P25-03b review): while epochs are being written, `newest` stays within the skew of now, so every lookup for an older entry is `GET newest` plus one `EVALSHA_RO` on Valkey's single thread, with two SHA-256 per tag computed in Go and four script arguments per tag. The memory store pays this in process; a remote store pays two round trips per hit. A short-lived store-side cache of `newest` is the upgrade if measurements (P25-04) show it matters. The seed fetch has no single-flight: cold concurrent callers each draw and `HSETNX` once, bounded by the callers and convergent.
- Key-generation record (P25-07c, R-3): `CheckKeyGen(ctx, hash)` and `RecordKeyGen(ctx, hash)` are store methods outside the `store.Store` interface, found by the adapter through a type assertion (like the optional capabilities). `CheckKeyGen` is a `GET <prefix>:keygen` and reports whether a different value is there (no value is not a change); `RecordKeyGen` is a `SET` with no TTL, so `volatile-*` never evicts it. The caller records only after the purge a change called for has succeeded, so a crash in between repeats the purge. The caller hashes the settings that change what an unchanged key means (08 §3). The hash is 1 to 64 bytes (P5). The key is outside the `{e}` slot (a single-key command) and its suffix is not 64 hex characters, so Scrub never reads it. A failed call maps to `ErrUnavailable` and counts toward nothing else: it is a start-up call, not a request-path call.
- Eviction of entries is Valkey's. The engine does not care which policy applies to entries (seed T6.11: "the storage interface must not assume a single eviction policy"); the epoch rule above is the only constraint.
- Timeouts: the client is built with `DisableRetry` (no retry of reads on network errors) and `MaxMovedRedirections` set to 3; `valkey-go` still bounds its own retries by the context. Every call runs under the engine's `Timeouts.Store` deadline; when the caller's context has none, the store applies `CallTimeout` (5 s), because a remote call must never wait forever. A call whose context ends may still have been executed by the server; every script is idempotent (a max or a replace), so the store may retry `SetEpoch` once itself after a network error, under the same context. Replica reads stay off: a replica read is a stale epoch.
- Errors: `valkey.Nil` is `ErrNotFound` for `Get` only. Every other error (context, `valkey.ErrClosing`, network, `io.EOF`, `valkey.ErrNoSlot`, any `*valkey.ValkeyError` such as OOM, READONLY, CLUSTERDOWN, LOADING, BUSY) maps to `ErrUnavailable`, wrapped with `%w`.
- Connection: `New` validates the config and fails on a bad one, but an unreachable server does not fail it (FR-STF-2: an outage at startup must open the store breaker, not stop the process). The client is built on first use and retried at most once per second; calls in between return `ErrUnavailable`. `Cluster` (default false) selects standalone or cluster mode; the library's auto-detection by error text is not relied on. Every method checks the context and the closed flag before it dials, a `Close` during a connect closes the client just built, no client is built after `Close`, and no lock is held across the dial (P8). A failed policy check is an error that wraps `ErrUnavailable` and names the policy. The policy check fails closed: zero nodes, or a node that reports no policy, is a failure (`SkipPolicyCheck` is the escape). The dial is shared by all waiting callers and runs under `CallTimeout`, not the first caller's deadline; a caller that gives up only stops waiting. `valkey.NewClient` takes no context, so `Close` waits for a real dial in flight, bounded by valkey-go's dial and handshake timeouts (each `CallTimeout`, per seed address in cluster mode); a second `Close` waits the same way. Standalone mode (`Cluster` false) takes exactly one address, because valkey-go's single-client mode uses only the first and would never check the others. The policy check is an allowlist (`noeviction` or `volatile-*`), so a future eviction policy fails closed; it runs once per connect, so a later `CONFIG SET` or a node joining the cluster is not rechecked. The gap between attempts runs from the end of the last one. Errors are prefixed `store: valkey:`. Valkey 7 or later is required (scripts replicate by effects, so `TIME` in scripts is allowed).
- Config validation (P25-01): errors wrap `weir.ErrInvalidConfig`. An empty `Prefix` or `HashTag` means the default (`weir`, `e`); both are 1 to 64 bytes of `A-Z a-z 0-9 _ . -`. `Addrs` are unique `host:port` with a port in 1 to 65535. Bounds: `MaxRetention` at most 10 years, `MaxClockSkew` at most 1 h, `MaxHardEpochs` at most 1 000 000 (so the prune margin cannot overflow and the table is bounded, P5). `HardEpochWait` is a whole number of milliseconds (`WAIT` with 0 blocks forever) and shorter than `CallTimeout`. `NoClockSkew` with a non-zero `MaxClockSkew` is an error. `Config` hides its credentials in `String`, `GoString`, slog and JSON.
- Replication: Valkey replication is asynchronous, so a failover can lose an acknowledged purge. `HardEpochWait` (default 0, off) issues `WAIT 1 <ms>` after a hard-epoch write; the multi-node guide explains the trade-off. `WAIT` returns early only when a replica has acknowledged; with no replica (a single node, or the replica down) it blocks for the full `HardEpochWait`, so a hard `Purge` over N tags then takes N times that. Replication still loses purges in two ways it cannot see: a failover to a lagging replica, and a restart from an old RDB or AOF both keep `meta` and `newest`, so purges since the last sync vanish with no loss signal (known T-29 residual risk; hard writes with `HardEpochWait` narrow it, soft and invalid writes do not).
- Client-side caching (RESP3 tracking) can make `Get` for hot keys local; it is an optimization inside the store, off until a later card (`DisableCache` true).
- Scrub (FR-PRG-8, P25-06) is synchronous and implements `store.Scrubber`. It asks each node for `ROLE` and visits the primaries only (`Client.Nodes()` also lists replicas, which would read every entry twice and refuse the `DEL`). On each primary it runs `SCAN <cursor> MATCH <entry prefix>* COUNT 1000` in a loop the store drives itself (so each batch gets its own `CallTimeout`, and the context is checked between batches), keeps the keys that are `<prefix>:[{tag}:]` followed by 64 lowercase hex characters, pipelines `GET` for them 100 keys at a time (so at most 100 records are in memory; cluster mode splits the pipeline by slot), decodes each value and pipelines `DEL` for the response records that carry a scrubbed tag. Epoch keys, sketch planes and other keys under the prefix are never read; a value that does not decode, that is not a response record, or whose key holds another type (`WRONGTYPE`) is left alone and is not an error. The count is what `DEL` reports, summed over the whole pipeline. With no primary node (an address list of replicas only, or a failover in progress) it returns `ErrUnavailable` instead of an empty success; any other failed command in a pipeline (MOVED during resharding) fails the scrub with `ErrUnavailable` and the count so far. On a cancelled context it returns the count so far with `ErrUnavailable`. It reads every entry (`ponytail:` ceiling: O(keyspace) per call; upgrade path is a tag index maintained by `Set`), and a concurrent fresh `Set` can be deleted, an extra miss as in the memory store. Known limit: the engine runs `Scrub` under `Timeouts.Store` (50 ms by default for a remote store) for the whole call, so on a real keyspace an eager purge returns `ErrUnavailable` with the deletes made so far (the epochs are already written, so correctness holds). A scrub failure does not count toward the store breaker (`storeGuard.scrub`, 07 §6), but an open breaker refuses the scrub before any scan. A separate scrub deadline in the engine is a follow-up, not part of this card.
- Eviction storm (P25-06, [09 §7](09-research-notes.md)): a flood of one-hit entries into a server at `maxmemory 16mb` with `volatile-lfu` evicted entries only; the epoch keys survived and a hard-purged entry was never served again. The `allkeys-*` refusal is the rule that makes that hold.
- What the paper design exposed: the vary-spec read-modify-write race ([04-lld.md §6.7](04-lld.md)) is wider across nodes. It stays bounded by the per-partition cap per node times the node count. The mitigation is the `VarySetter` capability (V-1), decided in P25-05 over a version field on `Entry`: it leaves the codec and the memory store's entry layout alone and costs a store with no cross-node writers nothing. The Valkey implementation (P25-05b) is one script on the spec's entry key: `GET`, compare with the encoded `prev` (empty expectation means the key must be absent), then `SET ... PXAT`. It compares bytes, not a digest, because a spec lists at most `MaxVariants` refs; re-encoding a decoded spec reproduces the stored bytes (the codec sorts headers). Only the entry key is touched, so it works in cluster mode with or without `CoLocateEntries`. A nil `prev` that loses re-reads the key: a record past its `Expires` that the server still holds is swapped against its own bytes, so the swap does not fail until the server clock catches up. The same fallback swaps over a record that does not decode, but the engine never reaches it: `Get` reports such a record as `ErrUnavailable`, so `setVariantCAS` gives up before `SetVarySpec` (the same planted-value cost as in the entry-writes bullet). Costs and limits (P25-05b review): `prev` goes to the server in full on every attempt, and the engine passes the primary-key record as `prev` even when it is a full response, so up to `MaxObjectBytes` times 16 attempts cross the wire in the worst case (a digest compare would need SHA-1, `redis.sha1hex` being Lua's only hash); the fallback is up to three round trips under one `CallTimeout` or `Timeouts.Store` deadline; a node whose clock runs ahead of another's by up to `MaxClockSkew` may judge a live record expired and overwrite it. During a rolling upgrade that adds a codec field, a spec written by the newer version does not round-trip through an older node's decode and encode (Decode skips unknown tags), so the older node loses its swaps and deletes its variant until the upgrade finishes; compare the raw bytes `Get` saw if that matters.
- Client library (decided 2026-10-10): `github.com/valkey-io/valkey-go` v1.0.78 (needs Go 1.25, the project needs 1.27), pure Go, in the `store/valkey` module only (the root module stays standard-library only, NFR-6).

## 8. Conformance suite (`store/storetest`)

```go
func Run(t *testing.T, newStore func(t *testing.T) store.Store, opts ...Option)

func WithoutEpochs() Option // epoch cases t.Skip (a store before its epoch support lands)
func EpochModes(m ...store.EpochMode) Option // epoch cases use only these modes (a store whose soft/invalid support lands later); cases that need a missing mode skip. The Valkey store no longer passes it
func Parallel(n int) Option // EpochNeverUnderInvalidates issues its calls from n goroutines (remote stores auto-pipeline them)
func Synctest() Option      // each time-dependent case runs in its own synctest bubble
func HardEpochCap(n int) Option // the store's hard-epoch cap; EpochHardCap is skipped without it
```

Every store implementation calls `storetest.Run` from its tests. Cases (each a subtest):

| Case | Asserts |
|---|---|
| `GetMissing` | `ErrNotFound` |
| `SetGetRoundTrip` | every field of every kind survives (deep equality, times compared with `Equal`) |
| `SetReplacesAnyKind` | response replaced by vary spec and back |
| `ExpiredIsNotFound` | record with past `Expires` is not returned: 1 s expiry, then `time.Sleep(3 s)`, which is fake inside a bubble with `Synctest()` (memory) and real otherwise (remote, 2 s margin). Remote expiry runs on the server clock, so without `Synctest()` the case runs only under the `integration` build tag and skips otherwise (CLAUDE.md hard rule 6). `synctest.Test` forbids `t.Run` inside a bubble, so the bubble is per case, not around `Run` |
| `SetPastExpiresIsNoop` | |
| `DeleteMissingOK` | |
| `ContextCanceled` | canceled context yields an error that `errors.Is` `ErrUnavailable` within 100 ms (remote stores; memory store may ignore context and succeed) |
| `ClosedStore` | all calls return `ErrUnavailable` after `Close`; `Close` twice is fine |
| `EpochPerModeKept` | hard then soft on one tag: `NewestEpoch` returns hard for entries before the hard epoch |
| `EpochNeverUnderInvalidates` | three phases on fresh stores: 100 000 random soft epochs, then 100 000 invalid epochs, each strict (every lookup reports exactly that mode at or after the tag's own time, since one mode alone cannot collide upward), then 20 000 of both mixed (at least the tag's own mode, and its own time when the mode is its own; a more severe colliding epoch is valid and skew can place it before `since`). Property test; `Parallel` runs it from n goroutines |
| `EpochHardCap` | the hard-epoch cap returns an error wrapping `ErrUnavailable`; soft epochs beyond any count are accepted |
| `EpochSinceBoundary` | `At == since` applies |
| `EpochFastPath` | no tags newer than since: `ok == false` |
| `EpochsMaxAcrossTags` | newest across several tags, most severe first |
| `ConcurrentSetGet` | 64 goroutines, random keys, `-race` clean, every `Get` returns either `ErrNotFound` or an entry previously `Set` at that key |
| `NoMutationAfterSet` | stores do not alter entries passed to `Set` (compare against a deep copy) |
| `CodecRoundTrip` | `Encode`/`Decode` identity over generated entries |

Memory-store-specific tests (in `store/memory`): byte accounting never exceeds `MaxBytes` after `Set` returns; scan resistance (`TestS3FIFOScanResistance`: a working set of hot keys survives a flood of 100× its size in one-hit keys with at least 90% of hot keys still present); ghost promotion; oversized record declined; shard distribution is uniform for adversarially chosen keys that share their first bytes.
