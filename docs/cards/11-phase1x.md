# Phase 1.x cards: M11 to M16

## Phase 1.x

### [x] M11-01 Single-range responses from cache
- Plan: M11 · Size: M · Depends on: M10-06
- Read: 01 §13.1 FR-RNG-1..3, FR-RNG-5; 04 §13.1 (first paragraph)
- Touch: internal/httpcc/range.go, respond.go, tests, testdata/fuzz/FuzzRange/
- Tests: TestRangeSingleFromCache, TestRangeUnsatisfiable416, TestRangeMultiOrInvalidGets200, TestIfRangeStrongOnly, FuzzRange
- AC: `make check` passes

### [x] M11-02 Range miss background fill
- Plan: M11 · Size: S · Depends on: M11-01
- Read: 01 FR-RNG-4; 04 §13.1 (second paragraph); 06 T-37
- Touch: serve.go (pass path), tests
- Tests: TestRangeMissBackgroundFillBounded
- AC: `make check` passes

### [x] M12-01 Structured-field dictionary parser
- Plan: M12 · Size: S · Depends on: M11-02
- Read: 04 §13.2 (parser sentence); RFC 9651 §4.2.2
- Touch: internal/sfv/dict.go, dict_test.go, testdata/fuzz/FuzzSFDictionary/
- Tests: FuzzSFDictionary, RFC 9651 dictionary examples
- AC: `make check` passes

### [x] M12-02 Targeted cache-control precedence
- Plan: M12 · Size: S · Depends on: M12-01
- Read: 01 §13.2; 04 §13.2; 06 T-34
- Touch: internal/httpcc/directives.go, respond.go, tests
- Tests: TestTargetedFieldPrecedence, TestTargetedFieldKeepsPrivate, TestWeirCacheControlStripped; RFC 9213 examples in rfc9111_test.go
- AC: `make check` passes

### [ ] M13-01 Snapshot writer
- Plan: M13 · Size: M · Depends on: M12-02
- Read: 01 §13.3 FR-SNP-1; 05 §5.5; 04 §13.3
- Touch: store/memory/snapshot_write.go, tests
- Tests: TestSnapshotRespectsDeadline, write half of TestSnapshotRoundTrip
- AC: temp file mode 0600, fsync, atomic rename, trailer; `make check` passes

### [ ] M13-02 Snapshot loader
- Plan: M13 · Size: M · Depends on: M13-01
- Read: 01 FR-SNP-2, FR-SNP-3; 05 §5.5; 06 T-33
- Touch: store/memory/snapshot_load.go, tests
- Tests: TestSnapshotRoundTrip, TestSnapshotLoadIsSoftStale, TestSnapshotHardEpochSurvives, TestSnapshotCorruptRecordsSkipped
- AC: file deleted after successful load; `make check` passes
- Notes: security-sensitive (decoder of on-disk bytes): checklist in PR body.

### [ ] M14-01 Per-host limiter cap and per-owner quota
- Plan: M14 · Size: M · Depends on: M13-02
- Read: 01 §13.4; 05 §5.3 (quota paragraph); 04 §13.4; 06 T-32
- Touch: internal/limiter, store/memory/shard.go, entry.go (owner), tests
- Tests: TestPerHostLimiterCap, TestOwnerQuotaIsolatesTenants
- AC: defaults off in the library; `make check` passes

### [ ] M15-01 Eager hard purge
- Plan: M15 · Size: S · Depends on: M14-01
- Read: 01 §13.5; 05 §5.3 (scrub paragraph); 04 §13.5
- Touch: purge.go, store/memory/scrub.go, tests
- Tests: TestEagerHardPurgeDeletesAllPartitions, TestEagerSoftIsError, TestEagerUnsupportedStore
- AC: `make check` passes

### [ ] M16-01 Pointer-light layout: prototype and decision (D36)
- Plan: M10.5b follow-up · Size: S · Depends on: M10-05
- Read: 01 D36, NFR-5; 05 §3 (S3-FIFO), §4; store/memory/ (entry, shard); docs/benchmarks.md "M10 GC cost at 1M entries"
- Touch: store/memory/ (throwaway prototype, not merged), docs/benchmarks.md, docs/02-architecture.md (D36 note)
- Tests: `TestGCAt1MEntries` against the prototype (saturated GC share at 1M entries), `BenchmarkMemoryStoreGetParallel`
- AC: the share with a first-cut layout (one pointer-free `[]byte` per entry holding header and body, pointer-free index) is recorded next to the 13.6% to 15.4% baseline; a recommendation is written (go to M16-02, or keep the heap layout and revise the 10% line); the user decides; GC CPU per request (µs) and `ServeHitSmall` ns/op and allocs/op on the prototype are recorded against the baseline and the 20% rule (NFR-5); `GOGC=200` and an allocation cut on the heap layout are measured as alternatives; the recommendation names which gate to use (GC µs per request, or share with a stated denominator)
- Notes: approved 2026-10-08 as a throwaway prototype (Ashwin delegated the decision); D36 changes only through M16-02. The numbers were taken on a 4-core Xeon; rerun the baseline on the reference machine first if one is at hand.

### [ ] M16-02 Pointer-light memory store layout
- Plan: M10.5b follow-up · Size: M · Depends on: M16-01
- Read: M16-01's recommendation; 05 §3, §4; store/memory/
- Touch: store/memory/, docs/05-storage-interface-spec.md, loadtest/gc_test.go (gate), docs/benchmarks.md
- Tests: the store conformance suite unchanged; `Get` and `Set` allocation benchmarks; `TestGCAt1MEntries` asserts a saturated GC share of at most 10% at 1M entries
- AC: the share target holds on the box that measured 13.6% to 15.4%; `BenchmarkMemoryStoreGetParallel` does not regress over 20%; stored entries stay immutable (P4); `make check` passes
- Notes: only if M16-01 recommends it and the user approves; not pre-approved.
