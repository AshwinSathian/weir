package weir

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/AshwinSathian/weir"

// NFR-6, P7: the root module imports only the standard library and itself,
// test-only imports and build-tagged files included, and requires no module.
func TestNoThirdPartyImports(t *testing.T) {
	// Tag sets whose files would otherwise escape the check: loadtest/ builds
	// only under "load" (docs/07 §1).
	for _, tags := range []string{"", "load"} {
		t.Run("tags="+tags, func(t *testing.T) {
			out := goCmd(t, "list", "-deps", "-test", "-tags="+tags, "-json=ImportPath,Standard,Module", "./...")
			dec := json.NewDecoder(bytes.NewReader(out))
			for {
				var p struct {
					ImportPath string
					Standard   bool
					Module     *struct{ Path string }
				}
				if err := dec.Decode(&p); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatalf("decode go list output: %v", err)
				}
				// Module path, not import-path prefix: sibling modules
				// (observe/prom, store/valkey) share the prefix.
				if p.Standard || (p.Module != nil && p.Module.Path == modulePath) {
					continue
				}
				t.Errorf("third-party dependency %q", p.ImportPath)
			}
		})
	}
	t.Run("go.mod requires nothing", func(t *testing.T) {
		mods := strings.Fields(string(goCmd(t, "list", "-m", "all")))
		if len(mods) != 1 || mods[0] != modulePath {
			t.Errorf("go list -m all = %q, want only %s", mods, modulePath)
		}
	})
}

// goCmd runs the go tool on the root module alone: workspace mode would pull
// in the sibling modules and their legitimate dependencies.
func goCmd(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	return out
}
