// Package weirhttp adapts a weir.Engine to net/http: middleware and a
// handler that serve requests through the engine, conversions between
// net/http and weir types, and an Origin backed by an http.RoundTripper.
// The adapter's duties are listed in docs/03-hld.md §5 and its API in
// docs/04-lld.md §10.
package weirhttp
