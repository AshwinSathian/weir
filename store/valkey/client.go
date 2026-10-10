package valkey

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/valkey-io/valkey-go"
)

// client is the part of the Valkey client the store uses. It is a seam so
// tests can stand in a fake without implementing valkey.Client.
type client interface {
	// policies returns maxmemory-policy for every node the client knows
	// (all primaries and replicas in cluster mode), keyed by address.
	policies(ctx context.Context) (map[string]string, error)
	// get returns the value at key, or an error for which valkey.IsValkeyNil
	// is true when the key is absent.
	get(ctx context.Context, key string) ([]byte, error)
	// set stores val at key until the Unix millisecond pxat.
	set(ctx context.Context, key string, val []byte, pxat int64) error
	del(ctx context.Context, key string) error
	// evalWrite runs the epoch write script (scripts.go); evalRead the
	// read-only lookup script and returns its integer array reply.
	evalWrite(ctx context.Context, keys, args []string) error
	evalRead(ctx context.Context, keys, args []string) ([]int64, error)
	// evalWriteWait runs the write script and then WAIT replicas ms on the
	// same connection: WAIT only covers writes made on its own connection,
	// and the shared multiplexed one gives no such guarantee.
	evalWriteWait(ctx context.Context, keys, args []string, replicas, ms int64) error
	close()
}

// dialFunc builds a client. It may block, so the store never holds a lock
// across it (P8).
type dialFunc func(ctx context.Context, cfg Config) (client, error)

// maxMovedRedirections bounds MOVED/ASK chasing so a misconfigured cluster
// cannot loop until the context ends (05 §7).
const maxMovedRedirections = 3

// clientOption builds the valkey-go options from a validated Config:
// no retry of reads, no client-side cache, one connection mode chosen by
// the operator, no replica reads (a replica read is a stale epoch).
func clientOption(cfg Config) valkey.ClientOption {
	opt := valkey.ClientOption{
		InitAddress:       cfg.Addrs,
		Username:          cfg.Username,
		Password:          cfg.Password,
		TLSConfig:         cfg.TLS,
		DisableRetry:      true,
		DisableCache:      true,
		ForceSingleClient: !cfg.Cluster,
	}
	opt.Dialer.Timeout = cfg.CallTimeout
	opt.ClusterOption.MaxMovedRedirections = maxMovedRedirections
	return opt
}

type valkeyClient struct{ c valkey.Client }

// dialValkey is the production dialFunc. valkey.NewClient takes no context,
// so its dial is bounded by Dialer.Timeout (CallTimeout).
func dialValkey(_ context.Context, cfg Config) (client, error) {
	c, err := valkey.NewClient(clientOption(cfg))
	if err != nil {
		if c != nil { // single-client mode returns the client with the error
			c.Close()
		}
		return nil, err
	}
	return valkeyClient{c}, nil
}

func (v valkeyClient) close() { v.c.Close() }

func (v valkeyClient) get(ctx context.Context, key string) ([]byte, error) {
	return v.c.Do(ctx, v.c.B().Get().Key(key).Build()).AsBytes()
}

func (v valkeyClient) set(ctx context.Context, key string, val []byte, pxat int64) error {
	return v.c.Do(ctx, v.c.B().Set().Key(key).Value(string(val)).PxatMillisecondsTimestamp(pxat).Build()).Error()
}

func (v valkeyClient) del(ctx context.Context, key string) error {
	return v.c.Do(ctx, v.c.B().Del().Key(key).Build()).Error()
}

func (v valkeyClient) evalWrite(ctx context.Context, keys, args []string) error {
	return writeEpochScript.Exec(ctx, v.c, keys, args).Error()
}

func (v valkeyClient) evalRead(ctx context.Context, keys, args []string) ([]int64, error) {
	return readEpochScript.Exec(ctx, v.c, keys, args).AsIntSlice()
}

func (v valkeyClient) evalWriteWait(ctx context.Context, keys, args []string, replicas, ms int64) error {
	return v.c.Dedicated(func(dc valkey.DedicatedClient) error {
		// EVAL, not EVALSHA: a dedicated connection cannot fall back from
		// NOSCRIPT, and hard epochs are rare. The first command carries keys,
		// which cluster mode needs to pick the node.
		eval := dc.B().Eval().Script(writeEpochSrc).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()
		res := dc.DoMulti(ctx, eval, dc.B().Wait().Numreplicas(replicas).Timeout(ms).Build())
		for _, r := range res {
			if err := r.Error(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (v valkeyClient) policies(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string)
	for addr, n := range v.c.Nodes() {
		m, err := n.Do(ctx, n.B().ConfigGet().Parameter("maxmemory-policy").Build()).AsStrMap()
		if err != nil {
			return nil, fmt.Errorf("config get maxmemory-policy on %s: %w", addr, err)
		}
		out[addr] = m["maxmemory-policy"]
	}
	return out, nil
}

// policyOK is an allowlist: only a policy known to leave TTL-less keys alone
// passes, so a future allkeys-like policy fails closed.
func policyOK(p string) bool {
	return p == "noeviction" || strings.HasPrefix(p, "volatile-")
}

// checkPolicy refuses a server that may evict keys without a TTL: epoch
// state has none, and a lost epoch is a missed purge (T-29, 05 §7). It runs
// once per successful connect, so a later CONFIG SET or a node that joins
// the cluster afterwards is not rechecked.
func checkPolicy(policies map[string]string) error {
	if len(policies) == 0 {
		return fmt.Errorf("store: valkey: %w: no node reported maxmemory-policy; set SkipPolicyCheck if the service hides it", errPolicy)
	}
	for _, addr := range slices.Sorted(maps.Keys(policies)) {
		p := policies[addr]
		if p == "" {
			return fmt.Errorf("store: valkey: %w: node %s reported no maxmemory-policy; set SkipPolicyCheck if the service hides it", errPolicy, addr)
		}
		if !policyOK(p) {
			return fmt.Errorf("store: valkey: %w: node %s has maxmemory-policy %q, which can evict epoch keys; use volatile-lfu or noeviction (or set SkipPolicyCheck)", errPolicy, addr, p)
		}
	}
	return nil
}
