package valkey

import (
	"context"
	"fmt"
	"strings"

	"github.com/valkey-io/valkey-go"
)

// client is the part of the Valkey client the store uses. It is a seam so
// tests can stand in a fake without implementing valkey.Client.
type client interface {
	// policies returns maxmemory-policy for every node the client knows
	// (all primaries and replicas in cluster mode), keyed by address.
	policies(ctx context.Context) (map[string]string, error)
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
		return nil, err
	}
	return valkeyClient{c}, nil
}

func (v valkeyClient) close() { v.c.Close() }

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

// checkPolicy refuses a server that may evict keys without a TTL: epoch
// state has none, and a lost epoch is a missed purge (T-29, 05 §7).
func checkPolicy(policies map[string]string) error {
	for addr, p := range policies {
		if strings.HasPrefix(p, "allkeys-") {
			return fmt.Errorf("store: valkey: %w: node %s has maxmemory-policy %q, which can evict epoch keys; use volatile-lfu or noeviction (or set SkipPolicyCheck)", errPolicy, addr, p)
		}
	}
	return nil
}
