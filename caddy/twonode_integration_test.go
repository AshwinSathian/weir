//go:build integration

package weircaddy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests run two real Caddy processes against one Valkey server. They
// use the real clock because the processes and the server expire entries on
// their own clocks (CLAUDE.md rule 6, integration tag). Each fixed pause is
// named where it is used.

// freePort returns a TCP port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// buildNode compiles the test Caddy once per test binary run.
func buildNode(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "e2enode")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./internal/e2enode")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build e2enode: %v\n%s", err, out)
	}
	return bin
}

// lockedBuf is a log sink that exec's copy goroutine and the test can use at
// the same time.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// node is one running Caddy process.
type node struct {
	site, admin int
	log         *lockedBuf
}

// startNode runs the binary with a Caddyfile that shares one Valkey keyspace
// (same prefix) with its sibling and proxies to origin.
func startNode(t *testing.T, bin, origin, valkeyAddr, prefix string) *node {
	t.Helper()
	n := &node{site: freePort(t), admin: freePort(t), log: &lockedBuf{}}
	cf := fmt.Sprintf(`{
	admin 127.0.0.1:%d
	auto_https off
	skip_install_trust
	grace_period 1ns
}
http://:%d {
	bind 127.0.0.1
	weir {
		name twonode
		store valkey {
			addrs %s
			prefix %s
		}
	}
	reverse_proxy %s {
		header_up -X-Forwarded-For
	}
}
`, n.admin, n.site, valkeyAddr, prefix, strings.TrimPrefix(origin, "http://"))
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(cf), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "run", "--config", path, "--adapter", "caddyfile")
	cmd.Stdout, cmd.Stderr = n.log, n.log
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("node :%d log:\n%s", n.site, n.log.String())
		}
	})
	// Poll the admin endpoint until the config is loaded.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/config/", n.admin)); err == nil {
			_ = resp.Body.Close()
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("node did not start:\n%s", n.log.String())
	return nil
}

func (n *node) get(t *testing.T, path string) (string, string) {
	t.Helper()
	// The Host is part of the key, so both nodes must see the same one, as
	// they would behind a load balancer.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", n.site, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "twonode.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Header.Get("Cache-Status"), string(b)
}

func (n *node) purgeAll(t *testing.T) {
	t.Helper()
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/weir/twonode/purge", n.admin),
		"application/json", strings.NewReader(`{"all":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("purge: %d", resp.StatusCode)
	}
}

// FR-STF-2, FR-PRG-1, T-38: a purge sent to node A reaches node B through the
// shared epochs, so B's next request for a cached URL goes to the origin.
func TestE2ETwoNodePurge(t *testing.T) {
	addr := os.Getenv("WEIR_VALKEY_ADDR")
	if addr == "" {
		t.Skip("WEIR_VALKEY_ADDR is not set")
	}
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Cache-Control", "max-age=300")
		_, _ = io.WriteString(w, "body "+r.URL.Path)
	}))
	defer origin.Close()

	bin := buildNode(t)
	prefix := fmt.Sprintf("twonode-%d", time.Now().UnixNano())
	a := startNode(t, bin, origin.URL, addr, prefix)
	b := startNode(t, bin, origin.URL, addr, prefix)

	// The first lookup on a new prefix finds no epoch state and writes a
	// global hard epoch at the server's time, rounded up to the second. A
	// response fetched within MaxClockSkew + 1 s of an epoch counts as older
	// than it (05 §4.3, E-7). Prime the prefix, then wait that window out so
	// the entry below is kept.
	a.get(t, "/prime")
	time.Sleep(2200 * time.Millisecond)

	// A fills the shared cache; B serves it as a hit without asking the origin.
	a.get(t, "/shared")
	// The store write happens after the response is sent, so poll for A's
	// own hit before asking B.
	var cs string
	for i := 0; i < 20; i++ {
		if cs, _ = a.get(t, "/shared"); strings.Contains(cs, "hit") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(cs, "hit") {
		t.Fatalf("node A Cache-Status = %q, want a hit on its own entry (check the node logs: an allkeys-* maxmemory-policy keeps the store unavailable)", cs)
	}
	before := calls.Load()
	if cs, _ := b.get(t, "/shared"); !strings.Contains(cs, "hit") {
		t.Fatalf("node B Cache-Status = %q, want a hit on A's entry", cs)
	}
	if got := calls.Load(); got != before {
		t.Fatalf("origin calls went from %d to %d: B's hit reached the origin", before, got)
	}

	// Epochs have a one-second grain (05 E-7): an entry stored in the same
	// second as the purge is not older than it.
	time.Sleep(1100 * time.Millisecond)
	a.purgeAll(t)

	// Node B learns the epoch from the server, not from A, and a future-dated
	// epoch counts as now (05 E-7), so the first request must show it. One
	// retry covers the epoch second rolling over under load; a longer poll
	// would hide a purge that is slow to propagate.
	for range 2 {
		if cs, _ = b.get(t, "/shared"); strings.Contains(cs, "fwd=stale") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("node B Cache-Status = %q after A purged, want fwd=stale", cs)
}
