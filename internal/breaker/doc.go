// Package breaker is the engine's origin circuit breaker: a failure ratio
// over a rolling window with a minimum volume, counting gateway failures
// only (docs/04-lld.md §8.3, docs/02-architecture.md ADR-7). It knows
// nothing about responses; the engine classifies each fetch into an Outcome.
package breaker
