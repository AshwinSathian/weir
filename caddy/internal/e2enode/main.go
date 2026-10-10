// Command e2enode is a Caddy build with the Weir module, used only by the
// two-node integration test (P25-07b). Caddy keeps one running config per
// process, so two nodes need two processes.
package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Standard modules and the Weir module register themselves on import.
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	_ "github.com/AshwinSathian/weir/caddy"
)

func main() { caddycmd.Main() }
