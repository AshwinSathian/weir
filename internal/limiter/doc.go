// Package limiter bounds concurrent origin fetches: a global cap, a cap per
// partition, a foreground reserve and a bounded FIFO queue
// (docs/04-lld.md §8.2, docs/02-architecture.md ADR-8). It knows nothing
// about requests or the origin; the engine acquires a slot in fetch.
package limiter
