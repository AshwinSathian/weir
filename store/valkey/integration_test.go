//go:build integration

package valkey

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/storetest"
)

// serverAddr returns the test server address. CI sets WEIR_VALKEY_ADDR; a
// developer without a server gets a skip, but CI (CI=true) must not skip.
func serverAddr(t *testing.T) string {
	t.Helper()
	a := os.Getenv("WEIR_VALKEY_ADDR")
	if a == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("WEIR_VALKEY_ADDR is not set in CI")
		}
		t.Skip("WEIR_VALKEY_ADDR is not set; start a Valkey server and export it (make test-valkey)")
	}
	return a
}

// FR-STF-2, S-1..S-4, 05 §8: the conformance suite against a real server.
// Epochs arrive with P25-03. ExpiredIsNotFound runs here on the real clock
// because the server expires keys on its own clock (CLAUDE.md hard rule 6).
func TestStoreConformance(t *testing.T) {
	addr := serverAddr(t)
	// A fresh prefix per store and per run: keys from an earlier run live on
	// the server for up to an hour.
	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	n := 0
	storetest.Run(t, func(t *testing.T) store.Store {
		n++
		s, err := New(Config{Addrs: []string{addr}, Prefix: "t" + run + "x" + strconv.Itoa(n)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}, storetest.WithoutEpochs())
}
