// Package weir is a shared HTTP cache engine that sits between an HTTP server
// and an origin. It implements RFC 9111 caching and adds defenses against
// origin stampedes, origin and storage outages, and cache-key manipulation.
//
// # Usage
//
// Build an Engine with New, answer each request with Engine.Serve, and call
// Engine.Close on shutdown. An Origin fetches what the cache cannot answer;
// Serve is the only path that reaches it. The zero Config is valid and picks
// safe defaults, including an in-process memory store.
//
//	e, err := weir.New(weir.Config{})
//	if err != nil {
//		return err // wraps ErrInvalidConfig
//	}
//	defer e.Close(context.Background())
//	origin := &weirhttp.TransportOrigin{Target: originURL}
//	http.ListenAndServe(addr, weirhttp.Handler(e, origin))
//
// Package weirhttp wraps an Engine for net/http, and the Caddy adapter lives
// in the separate module github.com/AshwinSathian/weir/caddy.
//
// # Compatibility
//
// From v1.0.0 the exported API follows semantic versioning. Construct Config
// and the structs inside it with field names, never positionally, because
// minor releases may add fields; a new field's zero value keeps the old
// behavior. The store.Store interface is fixed for v1, and new storage
// features arrive as optional capability interfaces in package store
// (Scrubber, Sizer and others) that the engine finds with a type assertion.
// Event kinds, CacheInfo.Detail values and Event.Reason strings can also
// gain new values in a minor release, so match them with a default case.
//
// The design is specified in docs/ at the repository root; start with
// docs/README.md. Requirement IDs such as FR-COA-4 and threat IDs such as T-19
// in comments point into docs/01-technical-spec.md and docs/06-threat-model.md.
package weir
