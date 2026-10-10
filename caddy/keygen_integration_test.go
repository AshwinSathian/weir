//go:build integration

package weircaddy

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/valkey"
)

// Real-clock exception (CLAUDE.md rule 6, `integration` tag): epochs have
// whole-second resolution on the server clock, so telling one hard epoch from
// the next needs a pause of more than a second.
//
// R-3, 08 §3, 05 §7: the forwarding hash is kept on the server. A node that
// starts with tighter `forward` rules than the record writes the hard epoch; an
// unchanged config and a store-setting change alone (a password rotation) do
// not. Two site names with one explicit prefix stand in for two nodes: nothing
// in this process links them.
func TestValkeyKeyGenRecord(t *testing.T) {
	addr := os.Getenv("WEIR_VALKEY_ADDR")
	if addr == "" {
		t.Skip("WEIR_VALKEY_ADDR is not set")
	}
	prefix := fmt.Sprintf("kg-%d", time.Now().UnixNano())
	site := func(name, fwd, callTimeout string) string {
		return fmt.Sprintf(`{"name":%q,"store":{"type":"valkey","addrs":[%q],"prefix":%q,"call_timeout":%q},"forward":{"allow":[%s]}}`,
			name, addr, prefix, callTimeout, fwd)
	}
	probe, err := valkey.New(valkey.Config{Addrs: []string{addr}, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = probe.Close() })
	hardAt := func() time.Time {
		t.Helper()
		ep, ok, err := probe.NewestEpoch(t.Context(), []store.Tag{store.TagGlobal()}, time.Now().Add(-time.Hour))
		if err != nil || !ok {
			t.Fatalf("NewestEpoch = %v, %v, %v", ep, ok, err)
		}
		return ep.At
	}
	pause := func() { time.Sleep(2100 * time.Millisecond) } // epoch times are whole seconds

	mustLoad(t, newCtx(t), site("node-a", `"X-A"`, "5s"))
	base := hardAt() // a fresh prefix repairs itself with one hard epoch
	pause()

	t.Run("an unchanged config writes no epoch", func(t *testing.T) {
		mustLoad(t, newCtx(t), site("node-b", `"X-A"`, "5s"))
		if got := hardAt(); !got.Equal(base) {
			t.Fatalf("epoch moved from %v to %v", base, got)
		}
	})
	t.Run("a store setting alone writes no epoch", func(t *testing.T) {
		mustLoad(t, newCtx(t), site("node-c", `"X-A"`, "6s"))
		if got := hardAt(); !got.Equal(base) {
			t.Fatalf("epoch moved from %v to %v", base, got)
		}
	})
	t.Run("tighter forward on a node with no live peer writes the hard epoch", func(t *testing.T) {
		mustLoad(t, newCtx(t), site("node-d", `"X-A","X-B"`, "5s"))
		if got := hardAt(); !got.After(base) {
			t.Fatalf("epoch stayed at %v after a forwarding change", got)
		}
	})
	t.Run("the same tighter config again writes nothing more", func(t *testing.T) {
		pause()
		before := hardAt()
		mustLoad(t, newCtx(t), site("node-e", `"X-A","X-B"`, "5s"))
		if got := hardAt(); !got.Equal(before) {
			t.Fatalf("epoch moved from %v to %v", before, got)
		}
	})
}
