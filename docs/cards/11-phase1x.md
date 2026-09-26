# Phase 1.x cards: M11 to M15

## Phase 1.x

### [ ] M11-01 Single-range responses from cache
- Plan: M11 · Size: M · Depends on: M10-06
- Read: 01 §13.1 FR-RNG-1..3, FR-RNG-5; 04 §13.1 (first paragraph)
- Touch: internal/httpcc/range.go, respond.go, tests, testdata/fuzz/FuzzRange/
- Tests: TestRangeSingleFromCache, TestRangeUnsatisfiable416, TestRangeMultiOrInvalidGets200, TestIfRangeStrongOnly, FuzzRange
- AC: `make check` passes

### [ ] M11-02 Range miss background fill
- Plan: M11 · Size: S · Depends on: M11-01
- Read: 01 FR-RNG-4; 04 §13.1 (second paragraph); 06 T-37
- Touch: serve.go (pass path), tests
- Tests: TestRangeMissBackgroundFillBounded
- AC: `make check` passes

### [ ] M12-01 Structured-field dictionary parser
- Plan: M12 · Size: S · Depends on: M11-02
- Read: 04 §13.2 (parser sentence); RFC 9651 §4.2.2
- Touch: internal/sfv/dict.go, dict_test.go, testdata/fuzz/FuzzSFDictionary/
- Tests: FuzzSFDictionary, RFC 9651 dictionary examples
- AC: `make check` passes

### [ ] M12-02 Targeted cache-control precedence
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
