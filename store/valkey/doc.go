// Package valkey is a store.Store that keeps entries and purge epochs on a
// Valkey server, so several Weir processes share one cache and a purge sent
// to any of them reaches all. It lives in its own module
// (github.com/AshwinSathian/weir/store/valkey) so the root module imports
// only the Go standard library.
//
// Config.Addrs names the server. Standalone mode takes exactly one address;
// set Config.Cluster to give several seed nodes of a cluster. Processes that
// should share a cache must use the same Prefix, MaxRetention and
// MaxClockSkew.
//
// The server's maxmemory-policy must be volatile-lfu (recommended), another
// volatile-* policy or noeviction. Purge epochs have no TTL, and an allkeys-*
// policy could evict one, which would turn a purge into a silent no-op. The
// store checks the policy on every node it knows when it connects and stays
// unavailable (errors wrap store.ErrUnavailable) while a node reports
// anything else. Config.SkipPolicyCheck turns the check off for managed
// services that disable CONFIG; the operator then owns that guarantee.
//
// New validates the configuration but does not connect. The first call that
// needs the server dials it, so an unreachable server shows up as store
// errors that open the engine's store breaker instead of stopping the
// process. Calls made soon after a failed attempt fail fast with the last
// error, and the store retries about once a second.
//
// Config hides Username, Password and TLS from String, GoString and slog
// output. Close the store yourself after closing the engine; an engine does
// not close a store it did not create.
//
//	s, err := valkey.New(valkey.Config{
//		Addrs:  []string{"valkey.internal:6379"},
//		Prefix: "site-a",
//	})
//	if err != nil {
//		return err
//	}
//	defer s.Close()
//	e, err := weir.New(weir.Config{Store: s})
//
// The store contract is in docs/05-storage-interface-spec.md section 7, and
// the multi-node deployment advice is in section 8 of docs/runbook.md.
package valkey
