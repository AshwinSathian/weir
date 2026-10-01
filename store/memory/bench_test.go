package memory

import (
	"testing"
)

// BenchmarkMemoryStoreGetParallel measures concurrent Gets over 1024
// resident 1 KiB entries (07 §10, 05 §3): shard locking and the S3-FIFO hit
// bit under contention.
func BenchmarkMemoryStoreGetParallel(b *testing.B) {
	s, err := New(Config{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const n = 1024
	for i := range uint64(n) {
		if err := s.Set(b.Context(), numKey(i), entry(1024)); err != nil {
			b.Fatal(err)
		}
	}
	ctx := b.Context()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var i uint64
		for pb.Next() {
			if _, err := s.Get(ctx, numKey(i%n)); err != nil {
				b.Error(err)
				return
			}
			i++
		}
	})
}
