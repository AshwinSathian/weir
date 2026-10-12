// Package weircaddy is the Caddy adapter for Weir. It registers the HTTP
// handler module http.handlers.weir and the weir Caddyfile directive, which
// run the Weir engine in front of a reverse proxy (docs/08-caddy-adapter-spec.md).
//
// The import path is github.com/AshwinSathian/weir/caddy and the package name
// is weircaddy. Most users never import it from Go code. Build Caddy with it
// instead:
//
//	xcaddy build --with github.com/AshwinSathian/weir@v1.0.0 --with github.com/AshwinSathian/weir/caddy@v1.0.0
//
// then check that ./caddy list-modules shows http.handlers.weir. All Weir
// modules share one version number, so do not mix versions.
//
// The JSON configuration and the Caddyfile syntax are the stable API of this
// module, and they follow semantic versioning from v1.0.0. The exported Go
// types mirror that surface so Caddy can decode it; construct them with field
// names, and expect fields to be added in minor releases. Operating advice,
// including multi-node deployment with a shared Valkey store, is in
// docs/runbook.md sections 7 and 8.
package weircaddy
