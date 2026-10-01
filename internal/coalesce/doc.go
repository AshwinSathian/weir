// Package coalesce holds the flight table that lets concurrent misses for
// one key share a single origin fetch (docs/04-lld.md §8.1, docs/03-hld.md
// §3.3). It knows nothing about the store or the origin (FR-COA-7).
package coalesce
