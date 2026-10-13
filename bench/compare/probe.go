package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The security probes of docs/11 §5.3. The origin's /p/ routes are unsafe on
// purpose: each one does what a careless application does (reflects a header
// the cache does not key, personalizes without saying so, honors a method
// override). A probe sends an attacker's request, then a victim's, and reads
// the victim's response. The verdict says what the cache in between let
// through, not whether the origin was right to behave that way.

var probeCalls atomic.Int64

// probeOrigin serves /p/{probe}/{key...}. It reflects request input into
// text/plain bodies on purpose: that is the unsafe behavior under test.
//
//nolint:gosec // G705: the reflection is the probe
func probeOrigin(w http.ResponseWriter, r *http.Request) {
	n := probeCalls.Add(1)
	h := w.Header()
	h.Set("Content-Type", "text/plain")
	h.Set("X-Origin-Call", strconv.FormatInt(n, 10))
	cacheable := func() { h.Set("Cache-Control", "max-age=60") }
	switch r.PathValue("probe") {
	case "reflect-host": // an unkeyed header in the body (Kettle 2018)
		cacheable()
		_, _ = fmt.Fprintf(w, "script=//%s/app.js", firstNonEmpty(r.Header.Get("X-Forwarded-Host"), "origin.example"))
	case "reflect-url": // X-Original-URL picks the page
		cacheable()
		_, _ = fmt.Fprintf(w, "page=%s", firstNonEmpty(r.Header.Get("X-Original-Url"), r.URL.Path))
	case "fat-get": // a GET body the application reads
		cacheable()
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
		_, _ = fmt.Fprintf(w, "q=%s", firstNonEmpty(string(b), "none"))
	case "method-override": // a framework that honors the override header
		cacheable()
		if m := r.Header.Get("X-Http-Method-Override"); m != "" && m != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = io.WriteString(w, "method not allowed")
			return
		}
		_, _ = io.WriteString(w, "page")
	case "big-header": // an origin with a smaller header limit than the cache
		if len(r.Header.Get("X-Padding")) > 4096 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "header too large")
			return
		}
		cacheable()
		_, _ = io.WriteString(w, "page")
	case "cookie": // personalized by cookie, marked cacheable, no Vary
		cacheable()
		_, _ = fmt.Fprintf(w, "hello %s", cookieUser(r))
	case "auth": // personalized by Authorization, marked max-age without public
		cacheable()
		_, _ = fmt.Fprintf(w, "account of %s", firstNonEmpty(r.Header.Get("Authorization"), "nobody"))
	case "set-cookie": // a session cookie on a cacheable response
		cacheable()
		h.Set("Set-Cookie", "sid="+strconv.FormatInt(n, 10)+"; Path=/")
		_, _ = io.WriteString(w, "page")
	case "deception": // /account/x.css: per-user content, no caching headers at all
		_, _ = fmt.Fprintf(w, "account of %s", cookieUser(r))
	case "range": // honors Range, as a file server does
		cacheable()
		if r.Header.Get("Range") != "" {
			h.Set("Content-Range", "bytes 0-0/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "0")
			return
		}
		_, _ = io.WriteString(w, "0123456789")
	case "head": // HEAD has no body
		cacheable()
		h.Set("Content-Length", "10")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "0123456789")
		}
	case "no-cache": // counts what client revalidation demands cost
		cacheable()
		_, _ = io.WriteString(w, "page")
	case "error-200": // always fails
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	default:
		http.NotFound(w, r)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func cookieUser(r *http.Request) string {
	if c, err := r.Cookie("user"); err == nil {
		return c.Value
	}
	return "anonymous"
}

// exchange is one request and what came back, kept in the results so a
// verdict can be checked by reading it.
type exchange struct {
	Method string `json:"method"`
	Header string `json:"header,omitempty"`
	Status int    `json:"status"` // 0: transport error
	Body   string `json:"body"`
	SetCk  string `json:"set_cookie,omitempty"`
}

type probeRow struct {
	Subject  string   `json:"subject"`
	Tier     string   `json:"tier"`
	Probe    string   `json:"probe"`
	Verdict  string   `json:"verdict"` // safe, unsafe, or a count for the amplification probe
	Why      string   `json:"why"`
	Attacker exchange `json:"attacker"`
	Victim   exchange `json:"victim"`
}

type probe struct {
	name string
	// attack builds the attacker's request for url; victim reads the plain
	// GET that follows and returns the verdict.
	attack func(url string) (*http.Request, error)
	judge  func(att, vic exchange) (verdict, why string)
}

func withHeader(method, name, value string) func(string) (*http.Request, error) {
	return func(url string) (*http.Request, error) {
		req, err := http.NewRequest(method, url, nil) //nolint:noctx // the caller attaches the context
		if err == nil && name != "" {
			req.Header.Set(name, value)
		}
		return req, err
	}
}

func unsafeIf(bad bool, yes, no string) (string, string) {
	if bad {
		return "unsafe", yes
	}
	return "safe", no
}

var probes = []probe{
	{"reflect-host", withHeader("GET", "X-Forwarded-Host", "evil.example"), func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "evil.example"), "the victim was served the attacker's host", "the victim got the origin's own host")
	}},
	{"reflect-url", withHeader("GET", "X-Original-URL", "/admin"), func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "/admin"), "the victim was served the page the attacker's header chose", "the victim got its own page")
	}},
	{"fat-get", func(url string) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, url, strings.NewReader("evil")) //nolint:noctx // the caller attaches the context
	}, func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "evil"), "the victim was served the answer to the attacker's body", "the attacker's body did not reach the victim")
	}},
	{"method-override", withHeader("GET", "X-HTTP-Method-Override", "DELETE"), func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200, "the victim got the error the attacker's header caused", "the victim got the page")
	}},
	{"big-header", withHeader("GET", "X-Padding", strings.Repeat("a", 6000)), func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200, "the victim got the error the attacker's header caused", "the victim got the page")
	}},
	{"cookie", withHeader("GET", "Cookie", "user=alice"), func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "a request without the cookie was served alice's page", "alice's page was not served to anyone else")
	}},
	{"auth", withHeader("GET", "Authorization", "Bearer alice"), func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "a request without credentials was served alice's account", "alice's account was not served to anyone else")
	}},
	{"set-cookie", withHeader("GET", "", ""), func(a, v exchange) (string, string) {
		return unsafeIf(v.SetCk != "" && v.SetCk == a.SetCk, "the second visitor was handed the first visitor's session cookie", "no session cookie was replayed")
	}},
	{"deception", withHeader("GET", "Cookie", "user=alice"), func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "alice's account page, sent with no caching headers, was served to another visitor", "the response without caching headers was not reused")
	}},
	{"range", withHeader("GET", "Range", "bytes=0-0"), func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200 || v.Body != "0123456789", "the victim got a partial or truncated response", "the victim got the whole object")
	}},
	{"head", withHeader("HEAD", "", ""), func(_, v exchange) (string, string) {
		return unsafeIf(v.Body != "0123456789", "a GET after a HEAD got no body", "the GET got its body")
	}},
}

func do(ctx context.Context, c *http.Client, req *http.Request) exchange {
	ex := exchange{Method: req.Method}
	for k, v := range req.Header {
		val := v[0]
		if len(val) > 40 {
			val = val[:40] + "..."
		}
		ex.Header = k + ": " + val
	}
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		ex.Body = "error: " + err.Error()
		return ex
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	ex.Status, ex.Body, ex.SetCk = resp.StatusCode, string(b), resp.Header.Get("Set-Cookie")
	return ex
}

// runProbes sends every probe through target and returns one row each, plus
// the amplification count.
func runProbes(ctx context.Context, c *http.Client, target, subject, tier string) []probeRow {
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	var out []probeRow
	for _, p := range probes {
		url := target + "/p/" + p.name + "/" + nonce
		if p.name == "deception" {
			url += "/x.css"
		}
		row := probeRow{Subject: subject, Tier: tier, Probe: p.name}
		areq, err := p.attack(url)
		if err != nil {
			row.Verdict, row.Why = "error", err.Error()
			out = append(out, row)
			continue
		}
		row.Attacker = do(ctx, c, areq)
		vreq, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // do attaches the context
		row.Victim = do(ctx, c, vreq)
		if row.Victim.Status == 0 {
			row.Verdict, row.Why = "error", "the victim's request failed"
		} else {
			row.Verdict, row.Why = p.judge(row.Attacker, row.Victim)
		}
		out = append(out, row)
	}

	// Client revalidation as an origin amplifier: one request stores the
	// page, then 50 ask for it with Cache-Control: no-cache. The figure is
	// how many of the 50 reached the origin.
	url := target + "/p/no-cache/" + nonce
	a := exchange{Method: "GET"}
	stored := ""
	if first, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); err == nil {
		if resp, err := c.Do(first); err == nil {
			a.Status, stored = resp.StatusCode, resp.Header.Get("X-Origin-Call")
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
	seen := map[string]bool{}
	var last exchange
	for range 50 {
		req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // do attaches the context
		req.Header.Set("Cache-Control", "no-cache")
		resp, err := c.Do(req.WithContext(ctx))
		if err != nil {
			continue
		}
		seen[resp.Header.Get("X-Origin-Call")] = true
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		last = exchange{Method: "GET", Header: "Cache-Control: no-cache", Status: resp.StatusCode}
	}
	delete(seen, "")
	delete(seen, stored) // the stored response replayed is not a new origin call
	out = append(out, probeRow{Subject: subject, Tier: tier, Probe: "no-cache-amplify",
		Verdict: strconv.Itoa(len(seen)) + " of 50", Why: "distinct origin responses among 50 requests that asked for revalidation",
		Attacker: a, Victim: last})

	// An origin that only ever answers 500: does any client get a 2xx?
	url = target + "/p/error-200/" + nonce
	ok := 0
	var lastErr exchange
	for range 20 {
		req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // do attaches the context
		lastErr = do(ctx, c, req)
		if lastErr.Status >= 200 && lastErr.Status < 300 {
			ok++
		}
	}
	v, why := unsafeIf(ok > 0, strconv.Itoa(ok)+" of 20 clients got a 2xx for a URL whose origin only answers 500", "every client saw the error")
	out = append(out, probeRow{Subject: subject, Tier: tier, Probe: "error-as-success", Verdict: v, Why: why, Victim: lastErr})
	return out
}

func probeCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	target := fs.String("target", "", "base URL of the cache under test")
	subject := fs.String("subject", "", "subject name recorded in each row")
	tier := fs.String("tier", "default", "config tier recorded in each row")
	outPath := fs.String("out", "", "append JSON lines to this file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || *subject == "" {
		return errors.New("compare: probe needs -target and -subject")
	}
	out := io.Writer(os.Stdout)
	if *outPath != "" {
		f, err := os.OpenFile(*outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the operator names the results file
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	c := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	r := &runner{client: c, target: strings.TrimRight(*target, "/")}
	if err := r.ready(ctx, 90*time.Second); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	for _, row := range runProbes(ctx, c, r.target, *subject, *tier) {
		if err := enc.Encode(row); err != nil {
			return err
		}
		log.Printf("%s/%s %-18s %-8s %s", *subject, *tier, row.Probe, row.Verdict, row.Why)
	}
	return nil
}
