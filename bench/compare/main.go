// Command compare is the cross-cache comparison harness (docs/11):
//
//	compare origin -listen :9000 -control :9001
//	compare run -target http://varnish:8080 -control http://origin:9001 -subject varnish -tier tuned -out results.jsonl
//	compare report results.jsonl
//
// The origin counts what reaches it, the runner drives one cache through the
// scenarios and reads those counts, and the report prints medians per subject.
// run.sh in this directory wires the three together with Docker Compose.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: compare origin|run|probe|report [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var err error
	switch os.Args[1] {
	case "origin":
		err = originCmd(ctx, os.Args[2:])
	case "run":
		err = runCmd(ctx, os.Args[2:])
	case "probe":
		err = probeCmd(ctx, os.Args[2:])
	case "report":
		err = reportCmd(os.Args[2:], os.Stdout)
	default:
		err = fmt.Errorf("compare: unknown command %q", os.Args[1])
	}
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

func originCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("origin", flag.ExitOnError)
	listen := fs.String("listen", ":9000", "address the caches fetch from")
	control := fs.String("control", ":9001", "address of the control API, which no cache proxies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o := newOrigin()
	data := &http.Server{Addr: *listen, Handler: o.data(), ReadHeaderTimeout: 10 * time.Second}
	ctl := &http.Server{Addr: *control, Handler: o.control(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	for _, s := range []*http.Server{data, ctl} {
		go func() { errc <- s.ListenAndServe() }()
	}
	log.Printf("compare: origin on %s, control on %s", *listen, *control)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return errors.Join(data.Shutdown(shut), ctl.Shutdown(shut))
}
