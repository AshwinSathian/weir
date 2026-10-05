// Package missrate finds partitions whose requests are nearly all misses: a
// Space-Saving summary of requests and misses per partition over a fixed
// window, in O(TopK) memory whatever the number of partitions
// (docs/04-lld.md §8.4, docs/02-architecture.md ADR-9). It knows nothing
// about the limiter; the engine turns an Anomaly into an event and a throttle.
package missrate
