package weircaddy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/headers"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

// perClientPrefixes start Caddy placeholders whose value depends on the
// client that triggered the fetch. The request context's replacer comes from
// that client (08 §6, T-4, T-45). Best effort: regexp Replace ops and
// placeholders built from Caddy vars set elsewhere are not inspected.
var perClientPrefixes = []string{
	"{http.request.header.", "{http.request.cookie.", "{http.request.remote",
	"{http.request.uri.query", "{http.auth.", "{http.request.tls.client",
	"{http.request.uri}", "{http.request.orig_uri", "{http.vars.", "{remote", "{client_ip}", "{uri", "{query", "{header.", "{cookie.",
}

// chainWarnings inspects the handlers that run after self in one route and
// returns operator warnings (08 §6). Best effort: it only sees handlers Caddy
// compiled into the same route.
func chainWarnings(route []caddyhttp.MiddlewareHandler, self *Handler) []string {
	i := slices.IndexFunc(route, func(m caddyhttp.MiddlewareHandler) bool { return m == caddyhttp.MiddlewareHandler(self) })
	if i < 0 {
		return nil
	}
	var out []string
	for _, m := range route[i+1:] {
		switch x := m.(type) {
		case *reverseproxy.Handler:
			var ops *headers.HeaderOps
			if x.Headers != nil {
				ops = x.Headers.Request
			}
			if ops == nil || !deletes(ops.Delete, "X-Forwarded-For") {
				out = append(out, fmt.Sprintf("weir %q: reverse_proxy after weir adds the client address in X-Forwarded-For to cacheable requests; "+
					"add header_up -X-Forwarded-For or the origin may vary responses on an unkeyed input (docs/08 §6)", self.Name))
			}
			out = append(out, placeholderWarnings(self, ops)...)
		case *headers.Handler:
			out = append(out, placeholderWarnings(self, x.Request)...)
		}
	}
	return out
}

func deletes(list []string, name string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, name) })
}

func placeholderWarnings(self *Handler, ops *headers.HeaderOps) []string {
	if ops == nil {
		return nil
	}
	for _, hdr := range []map[string][]string{ops.Set, ops.Add} {
		for name, vals := range hdr {
			for _, v := range vals {
				if hasPerClientPlaceholder(v) {
					return []string{fmt.Sprintf("weir %q: request header %q after weir uses a per-client placeholder (%s); "+
						"the first client's value reaches the origin on cacheable routes (docs/08 §6)", self.Name, name, v)}
				}
			}
		}
	}
	return nil
}

func hasPerClientPlaceholder(v string) bool {
	return slices.ContainsFunc(perClientPrefixes, func(p string) bool { return strings.Contains(v, p) })
}

// appWarnings finds self in the http app's routes, subroutes included, and
// returns chainWarnings for the route holding it.
func appWarnings(app *caddyhttp.App, self *Handler) []string {
	for _, srv := range app.Servers {
		if w, ok := routesWarnings(srv.Routes, self); ok {
			return w
		}
	}
	return nil
}

func routesWarnings(routes caddyhttp.RouteList, self *Handler) ([]string, bool) {
	for _, rt := range routes {
		if slices.ContainsFunc(rt.Handlers, func(m caddyhttp.MiddlewareHandler) bool { return m == caddyhttp.MiddlewareHandler(self) }) {
			return chainWarnings(rt.Handlers, self), true
		}
		for _, m := range rt.Handlers {
			if sub, ok := m.(*caddyhttp.Subroute); ok {
				if w, found := routesWarnings(sub.Routes, self); found {
					return w, true
				}
			}
		}
	}
	return nil, false
}
