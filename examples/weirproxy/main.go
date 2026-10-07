// Command weirproxy is a caching reverse proxy built from weir and
// weirhttp with default settings:
//
//	weirproxy -listen :8080 -origin http://localhost:9000
//
// -forward-allow lists request headers the origin may see although they are
// not keyed; the cache-tests job uses it for the harness headers.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	origin := flag.String("origin", "", "origin scheme and host, for example http://localhost:9000 (a path is ignored)")
	allow := flag.String("forward-allow", "", "comma-separated request headers to forward to the origin without keying them (Forward.Allow)")
	flag.Parse()
	if err := run(*listen, *origin, splitList(*allow)); err != nil {
		log.Fatal(err)
	}
}

func run(listen, origin string, allow []string) error {
	target, err := url.Parse(origin)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return errors.New("weirproxy: -origin must be an absolute URL such as http://localhost:9000")
	}
	e, err := weir.New(weir.Config{Forward: weir.ForwardConfig{Allow: allow}})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           weirhttp.Handler(e, &weirhttp.TransportOrigin{Target: target}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("weirproxy: %s -> %s", listen, target)

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(err, srv.Shutdown(shutdown), e.Close(shutdown))
}

// splitList splits a comma-separated flag value, dropping empty items.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
