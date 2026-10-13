package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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

// varyPad brings a variant's body to about 1 KiB.
var varyPad = strings.Repeat("x", 1000)

// varyN is how many values of the varied header the cardinality probe sends
// for one URL. A variable so the tests can send fewer.
var varyN = 20000

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
	name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/p/"), "/")
	switch name {
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
	case "cloak": // a query parser that also splits on ";", last value wins
		cacheable()
		q := "none"
		for _, kv := range strings.FieldsFunc(r.URL.RawQuery, func(c rune) bool { return c == '&' || c == ';' }) {
			if v, ok := strings.CutPrefix(kv, "q="); ok {
				q = v
			}
		}
		_, _ = fmt.Fprintf(w, "q=%s", q)
	case "pathnorm": // routes on the path exactly as sent: no cleaning, no decoding
		cacheable()
		raw := r.URL.EscapedPath()
		seg := strings.Split(strings.TrimPrefix(raw, "/p/pathnorm/"), "/")
		if len(seg) == 2 && seg[1] == "page" && !strings.ContainsAny(seg[0], "%.") {
			_, _ = io.WriteString(w, "page")
			return
		}
		_, _ = fmt.Fprintf(w, "other:%s", raw)
	case "meta": // rejects a header value with a byte outside printable ASCII, and says the error may be cached
		cacheable()
		for _, c := range []byte(r.Header.Get("X-Meta")) {
			if c < 0x20 || c >= 0x7f {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad header")
				return
			}
		}
		_, _ = io.WriteString(w, "page")
	case "vary": // one URL, one response per value of a request header
		cacheable()
		h.Set("Vary", "X-Variant")
		_, _ = fmt.Fprintf(w, "variant=%s\n%s", r.Header.Get("X-Variant"), varyPad)
	case "error-200": // always fails, slowly enough for clients to overlap
		t := time.NewTimer(100 * time.Millisecond)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			return
		}
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
	Figure   string   `json:"figure,omitempty"`
	Why      string   `json:"why"`
	Attacker exchange `json:"attacker"`
	Victim   exchange `json:"victim"`
}

type probe struct {
	name string
	// suffix is appended to /p/<name>/<nonce> to make the victim's URL.
	suffix string
	// attack sends the attacker's request or requests, given the victim's
	// URL, and returns the last exchange. The victim's plain GET follows
	// and judge reads it.
	attack func(ctx context.Context, c *http.Client, url string) exchange
	judge  func(att, vic exchange) (verdict, why string)
}

func withHeader(method, name, value string) func(context.Context, *http.Client, string) exchange {
	return func(ctx context.Context, c *http.Client, url string) exchange {
		req, err := http.NewRequest(method, url, nil) //nolint:noctx // do attaches the context
		if err != nil {
			return exchange{Body: "error: " + err.Error()}
		}
		if name != "" {
			req.Header.Set(name, value)
		}
		return do(ctx, c, req)
	}
}

// each sends a plain GET to every URL that variants derives from the
// victim's and returns the last exchange.
func each(variants func(url string) []string) func(context.Context, *http.Client, string) exchange {
	return func(ctx context.Context, c *http.Client, url string) exchange {
		var last exchange
		for _, u := range variants(url) {
			req, err := http.NewRequest(http.MethodGet, u, nil) //nolint:noctx // do attaches the context
			if err != nil {
				return exchange{Body: "error: " + err.Error()}
			}
			last = do(ctx, c, req)
			_, rest, _ := strings.Cut(u, "/p/")
			last.Header = "URL: /p/" + rest
		}
		return last
	}
}

// rawGet writes a GET by hand, with one header line net/http would refuse
// to send, and reads the response.
func rawGet(ctx context.Context, rawURL, headerLine string) exchange {
	ex := exchange{Method: "GET", Header: strconv.QuoteToASCII(headerLine)}
	u, err := url.Parse(rawURL)
	if err != nil {
		ex.Body = "error: " + err.Error()
		return ex
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", u.Host)
	if err != nil {
		ex.Body = "error: " + err.Error()
		return ex
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n%s\r\n\r\n", u.RequestURI(), u.Host, headerLine); err != nil {
		ex.Body = "error: " + err.Error()
		return ex
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		ex.Body = "error: " + err.Error()
		return ex
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	ex.Status, ex.Body = resp.StatusCode, string(b)
	return ex
}

func unsafeIf(bad bool, yes, no string) (string, string) {
	if bad {
		return "unsafe", yes
	}
	return "safe", no
}

var probes = []probe{
	{name: "reflect-host", attack: withHeader("GET", "X-Forwarded-Host", "evil.example"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "evil.example"), "the victim was served the attacker's host", "the victim got the origin's own host")
	}},
	{name: "reflect-url", attack: withHeader("GET", "X-Original-URL", "/admin"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "/admin"), "the victim was served the page the attacker's header chose", "the victim got its own page")
	}},
	{name: "fat-get", attack: func(ctx context.Context, c *http.Client, url string) exchange {
		req, err := http.NewRequest(http.MethodGet, url, strings.NewReader("evil")) //nolint:noctx // do attaches the context
		if err != nil {
			return exchange{Body: "error: " + err.Error()}
		}
		return do(ctx, c, req)
	}, judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "evil"), "the victim was served the answer to the attacker's body", "the attacker's body did not reach the victim")
	}},
	{name: "method-override", attack: withHeader("GET", "X-HTTP-Method-Override", "DELETE"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200, "the victim got the error the attacker's header caused", "the victim got the page")
	}},
	{name: "big-header", attack: withHeader("GET", "X-Padding", strings.Repeat("a", 6000)), judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200, "the victim got the error the attacker's header caused", "the victim got the page")
	}},
	{name: "cookie", attack: withHeader("GET", "Cookie", "user=alice"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "a request without the cookie was served alice's page", "alice's page was not served to anyone else")
	}},
	{name: "auth", attack: withHeader("GET", "Authorization", "Bearer alice"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "a request without credentials was served alice's account", "alice's account was not served to anyone else")
	}},
	{name: "set-cookie", attack: withHeader("GET", "", ""), judge: func(a, v exchange) (string, string) {
		return unsafeIf(v.SetCk != "" && v.SetCk == a.SetCk, "the second visitor was handed the first visitor's session cookie", "no session cookie was replayed")
	}},
	{name: "deception", suffix: "/x.css", attack: withHeader("GET", "Cookie", "user=alice"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "alice"), "alice's account page, sent with no caching headers, was served to another visitor", "the response without caching headers was not reused")
	}},
	{name: "range", attack: withHeader("GET", "Range", "bytes=0-0"), judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200 || v.Body != "0123456789", "the victim got a partial or truncated response", "the victim got the whole object")
	}},
	{name: "head", attack: withHeader("HEAD", "", ""), judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Body != "0123456789", "a GET after a HEAD got no body", "the GET got its body")
	}},
	// Parameter cloaking: the origin splits the query on ";" as well as "&"
	// and lets the last value win. A cache that drops, sorts or dedupes
	// parameters before it builds its key can file the attacker's URL
	// under the victim's.
	{name: "cloak", suffix: "?q=page", attack: each(func(u string) []string {
		return []string{u + "&q=evil", u + ";q=evil", u + "&utm_content=1;q=evil", strings.Replace(u, "?q=page", "?q=evil&q=page;q=evil", 1)}
	}), judge: func(_, v exchange) (string, string) {
		return unsafeIf(strings.Contains(v.Body, "evil"), "the victim was served the answer to the attacker's cloaked parameter", "the victim got the answer to its own query")
	}},
	// Path normalization: the origin routes on the path exactly as sent. A
	// cache that cleans or decodes the path for its key, and forwards what
	// it received, stores the attacker's page under the victim's URL.
	{name: "pathnorm", suffix: "/page", attack: each(func(u string) []string {
		base := strings.TrimSuffix(u, "/page")
		return []string{base + "//page", base + "/./page", base + "/x/../page", base + "/%70age"}
	}), judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Body != "page", "the victim was served the page of a differently spelled path", "the victim got its own page")
	}},
	// A meta character in a header (Nguyen et al. 2019, "HMC"): the origin
	// rejects the value with an error it marks cacheable. Sent twice by
	// hand, with a control byte and with a byte above ASCII.
	{name: "meta", attack: func(ctx context.Context, _ *http.Client, u string) exchange {
		rawGet(ctx, u, "X-Meta: a\x07b")
		return rawGet(ctx, u, "X-Meta: a\xffb")
	}, judge: func(_, v exchange) (string, string) {
		return unsafeIf(v.Status != 200, "the victim got the error the attacker's header caused", "the victim got the page")
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
		url := target + "/p/" + p.name + "/" + nonce + p.suffix
		row := probeRow{Subject: subject, Tier: tier, Probe: p.name}
		row.Attacker = p.attack(ctx, c, url)
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

	// An origin that only ever answers 500, asked by 20 clients at once,
	// three times: does any client get a 2xx? The clients overlap on
	// purpose. Asked one after another, as in the first round, the probe
	// never reached a cache that hands the waiters of a failed fetch an
	// empty 200 (darkweak/souin#876).
	url = target + "/p/error-200/" + nonce
	ok, empty := 0, 0
	var shown exchange
	for range 3 {
		got := make([]exchange, 20)
		var wg sync.WaitGroup
		for i := range got {
			wg.Go(func() {
				req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // do attaches the context
				got[i] = do(ctx, c, req)
			})
		}
		wg.Wait()
		for _, g := range got {
			if g.Status/100 == 2 {
				ok++
				shown = g
				if g.Body == "" {
					empty++
				}
			} else if ok == 0 {
				shown = g
			}
		}
	}
	v, why := unsafeIf(ok > 0, strconv.Itoa(ok)+" of 60 concurrent clients got a 2xx ("+strconv.Itoa(empty)+" with an empty body) for a URL whose origin only answers 500", "every client saw the error")
	out = append(out, probeRow{Subject: subject, Tier: tier, Probe: "error-as-success", Verdict: v, Why: why, Victim: shown})
	return append(out, varyProbe(ctx, c, target+"/p/vary/"+nonce, subject, tier))
}

// varyProbe sends varyN values of a header the origin varies on, all for
// one URL, then asks again for the first and the last 100 of them and for a
// value never sent. The verdict is whether every answer was the asker's own
// variant. The figure is how many of the 200 came from the cache: what a
// client can make one URL hold. probes.sh reads the container's memory
// before and after.
func varyProbe(ctx context.Context, c *http.Client, url, subject, tier string) probeRow {
	type answer struct {
		status     int
		call, body string
	}
	get := func(variant string) answer {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return answer{}
		}
		req.Header.Set("X-Variant", variant)
		resp, err := c.Do(req)
		if err != nil {
			return answer{}
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		first, _, _ := strings.Cut(string(b), "\n")
		return answer{resp.StatusCode, resp.Header.Get("X-Origin-Call"), first}
	}
	calls := make([]string, varyN)
	next := make(chan int)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for i := range next {
				calls[i] = get("v" + strconv.Itoa(i)).call
			}
		})
	}
	for i := range varyN {
		next <- i
	}
	close(next)
	wg.Wait()

	sample := min(100, varyN/2)
	kept, wrong, failed := 0, 0, 0
	vic := exchange{Method: "GET", Header: "X-Variant: victim"}
	for i := range varyN {
		if i >= sample && i < varyN-sample {
			continue
		}
		a := get("v" + strconv.Itoa(i))
		switch {
		case a.status != http.StatusOK:
			failed++
		case a.body != "variant=v"+strconv.Itoa(i):
			wrong++
			vic.Status, vic.Body, vic.Header = a.status, a.body, "X-Variant: v"+strconv.Itoa(i)
		case a.call == calls[i]:
			kept++
		}
	}
	if a := get("victim"); wrong == 0 {
		vic.Status, vic.Body = a.status, a.body
		if a.status == http.StatusOK && a.body != "variant=victim" {
			wrong++
		}
	}
	row := probeRow{Subject: subject, Tier: tier, Probe: "vary-cardinality", Victim: vic,
		Figure: strconv.Itoa(kept) + " of " + strconv.Itoa(2*sample)}
	row.Verdict, row.Why = unsafeIf(wrong > 0, "a request was served another value's variant",
		"every answer was the asker's own variant; "+row.Figure+" sampled variants of "+strconv.Itoa(varyN)+" were still served from the cache, "+strconv.Itoa(failed)+" requests failed")
	return row
}

func probeCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	target := fs.String("target", "", "base URL of the cache under test")
	subject := fs.String("subject", "", "subject name recorded in each row")
	tier := fs.String("tier", "default", "config tier recorded in each row")
	outPath := fs.String("out", "", "append JSON lines to this file (default: stdout)")
	readyOnly := fs.Bool("ready-only", false, "wait until the subject answers, then stop")
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
	r := &runner{client: c, target: strings.TrimRight(*target, "/"), want: -1}
	if err := r.ready(ctx); err != nil || *readyOnly {
		return err
	}
	enc := json.NewEncoder(out)
	for _, row := range runProbes(ctx, c, r.target, *subject, *tier) {
		if err := enc.Encode(row); err != nil {
			return err
		}
		log.Printf("%s/%s %-18s %-8s %s | attacker %d %q | victim %d %q", *subject, *tier, row.Probe, row.Verdict, row.Why, row.Attacker.Status, row.Attacker.Body, row.Victim.Status, row.Victim.Body)
	}
	return nil
}
