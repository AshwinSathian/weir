package keys

import (
	"hash/maphash"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/store"
)

// Class says whether a request may be served from and stored in the cache.
type Class uint8

// Class values.
const (
	ClassCacheable Class = iota // GET or HEAD
	ClassPass                   // forwarded as received, never stored
)

// FwdReason is why a ClassPass request goes forward. The root package maps
// it to weir.FwdReason.
type FwdReason uint8

// FwdReason values.
const (
	FwdNone   FwdReason = iota
	FwdMethod           // the method is not cacheable
	FwdBypass           // a Bypass rule matched a GET or HEAD (FR-BYP-1)
)

// ClientConditionals are the client's preconditions, kept so the engine can
// answer 304 itself (FR-SRV-2) after they are dropped from the forwarded
// request (FR-FWD-1).
type ClientConditionals struct {
	IfNoneMatch     []string  // entity-tags as sent, or just "*"; nil when absent, malformed or over MaxKeyedHeaderBytes
	IfModifiedSince time.Time // zero when absent, repeated, not an HTTP-date, or If-None-Match is present
}

// Classified is a validated request with everything the engine needs to
// look it up and forward it (04 §3.1).
type Classified struct {
	Class      Class
	FwdReason  FwdReason // for ClassPass: FwdMethod or FwdBypass
	Head       bool      // client method was HEAD
	Range      bool      // request carried Range
	Authorized bool      // request carried Authorization
	Unsafe     bool      // unsafe or unknown method: invalidate on 2xx/3xx
	HasBody    bool      // the forwarded request carries a body (upload pool, FR-LIM-7)
	Unkeyed    bool      // the forward carries an unkeyed Forward.Allow field, Cache-Control or Pragma, or ForwardAll (FR-STO-12, T-31)
	Forwarded  Request   // the request the origin sees on a miss
	Primary    store.Key // zero for ClassPass
	Origin     string    // scheme://host[:port]
	TagPath    string    // path and query the URI tag covers: the rewritten ones, also for ClassPass
	TagQuery   string
	Partition  string // Origin + path, truncated to maxPartitionBytes
	PartitionH uint64 // maphash of Partition, per-process seed
	HostH      uint64 // maphash of the normalized host, per-process seed (FR-FAIR-1)
	ReqCC      httpcc.RequestDirectives
	Variants   []Dim // experiment dimensions the key and the forwarded header carry (T-35)
	Decision   any   // what Config.Assign returned alongside Variants; opaque to this package
	ClientCond ClientConditionals
	RangeValue string   // the Range value a hit may slice (FR-RNG-1); "" when absent or repeated
	HasIfRange bool     // request carried If-Range
	IfRange    string   // its value; "" when repeated, which never applies
	expHeader  string   // Config.ExpHeader, for pass
	expCookie  string   // Config.ExpCookie, for pass
	rangeHdr   []string // the client's Range lines, for AsRangePass
	ifRange    []string // the client's If-Range lines, for AsRangePass
	req        *Request // the client request, for AsBypass; never modified
}

// CanonicalHeader is the client's header with canonical keys, the one every
// decision in Classify read. Nil when the request was refused.
func (c *Classified) CanonicalHeader() http.Header {
	if c.req == nil {
		return nil
	}
	return c.req.Header
}

// URITag is the tag of the request's URI (04 §3.1). It is computed on use,
// not in Classify: a hit reads the tags the entry carries, so only stores,
// purges and invalidations pay for the SHA-256.
func (c *Classified) URITag() store.Tag { return TagURI(c.Origin, c.TagPath, c.TagQuery) }

// OriginTag is the tag of the request's origin; it is also the entry owner.
func (c *Classified) OriginTag() store.Tag { return TagOrigin(c.Origin) }

const maxPartitionBytes = 512

var partitionSeed = maphash.MakeSeed()

// Classify validates r and builds its keys and forwarded request (04 §3.1).
// r and its Header are never modified; the forwarded request of a
// cacheable request has a fresh Header map.
func Classify(r *Request, c *Config) (Classified, error) {
	// INV-1, T-15: library callers may send keys in any case; the origin
	// would see a "x-custom" that every keyed and Vary read below misses.
	if !canonicalKeys(r.Header) {
		cr := *r
		cr.Header = CanonicalHeader(r.Header)
		r = &cr
	}
	host, err := Validate(r, c)
	if err != nil {
		return Classified{}, err
	}
	h := r.Header
	path := r.Path
	if c.NormalizePath {
		path = normalizePath(path)
	}
	query := rewriteQuery(r.RawQuery, c)
	// One allocation for both strings: Origin is the front of Partition.
	partition := r.Scheme + "://" + host + path
	origin := partition[:len(r.Scheme)+len("://")+len(host)]

	// One map read per field: this runs on every request, hits included.
	rng, ifRange, inm := h["Range"], h["If-Range"], h["If-None-Match"]
	out := Classified{
		Head:       r.Method == http.MethodHead,
		req:        r,
		Range:      len(rng) > 0,
		rangeHdr:   rng,
		RangeValue: single(rng),
		HasIfRange: len(ifRange) > 0,
		IfRange:    single(ifRange),
		ifRange:    ifRange,
		Authorized: len(h["Authorization"]) > 0,
		expHeader:  c.ExpHeader,
		expCookie:  c.ExpCookie,
		Origin:     origin,
		TagPath:    path,
		TagQuery:   query,
		Partition:  partition,
		ReqCC:      httpcc.ParseRequest(h),
	}
	if len(out.Partition) > maxPartitionBytes {
		out.Partition = out.Partition[:maxPartitionBytes]
	}
	out.PartitionH = maphash.String(partitionSeed, out.Partition)
	out.HostH = maphash.String(partitionSeed, host)
	if !c.HonorRevalidation {
		// FR-SRV-8, D5: only no-store and only-if-cached change behavior.
		out.ReqCC = httpcc.RequestDirectives{NoStore: out.ReqCC.NoStore, OnlyIfCached: out.ReqCC.OnlyIfCached}
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions, http.MethodTrace:
		return pass(out, r, host), nil
	default:
		out.Unsafe = true
		return pass(out, r, host), nil
	}
	if bypassed(h, c) { // FR-BYP-1
		out = pass(out, r, host)
		out.FwdReason = FwdBypass
		return out, nil
	}

	cookies := dropCookie(keyedCookies(h["Cookie"], c), c.ExpCookie)
	var dims []Dim
	if c.Assign != nil {
		dims, out.Decision = c.Assign(host, path, h["Cookie"])
	}
	// FR-FWD-4: a HEAD miss is fetched as GET so the response can be stored.
	// T-5: the body of a fat GET never reaches the origin.
	fh, unkeyed := forwardHeader(h, c, cookies, dims)
	if len(fh["Cookie"]) == 0 {
		// INV-1: under ForwardAll a Connection option can name Cookie, and
		// a cookie the origin never sees must not split the key.
		cookies = nil
	}
	out.Forwarded = Request{Method: http.MethodGet, Scheme: r.Scheme, Host: host, Path: path, RawQuery: query, Header: fh}
	out.Unkeyed = unkeyed
	out.Variants = dims
	out.Primary = PrimaryKey(&KeyInput{Method: http.MethodGet, Scheme: r.Scheme, Host: host, Path: path, Query: query,
		Headers: keyedHeaders(fh, c), CookieNames: c.Cookies, Cookies: cookies, Variants: dims})
	out.ClientCond = ClientConditionals{IfNoneMatch: parseIfNoneMatch(inm, c)}
	// RFC 9110 §13.1.3: If-Modified-Since is ignored whenever If-None-Match
	// is present, even when that field is malformed or over the limit.
	if ims := h["If-Modified-Since"]; len(ims) == 1 && inm == nil {
		if t, err := http.ParseTime(ims[0]); err == nil {
			out.ClientCond.IfModifiedSince = t
		}
	}
	return out, nil
}

// AsRangePass turns a cacheable Range request that no entry answers into a
// pass-through (FR-SRV-5): the forwarded request gains the client's Range
// and If-Range lines, so the origin decides between 206 and 200 (RFC 9110
// §13.1.5). A HEAD stays a GET (FR-FWD-4); the engine drops the body.
// T-7: the cacheable forward itself never carries Range.
func (c *Classified) AsRangePass() *Classified {
	out := *c
	out.Class, out.FwdReason = ClassPass, FwdNone
	out.Forwarded.Header = c.Forwarded.Header.Clone()
	out.Forwarded.Header["Range"] = slices.Clone(c.rangeHdr)
	if len(c.ifRange) > 0 {
		out.Forwarded.Header["If-Range"] = slices.Clone(c.ifRange)
	}
	return &out
}

// AsBypass turns a cacheable request into a pass-through forwarded as
// received (FR-MODE-3, FR-FWD-3): method, path, query, headers and body as
// the client sent them, hop-by-hop fields removed. Nothing is stored, so
// INV-1 does not apply; tags and partition stay those of the cacheable form.
func (c *Classified) AsBypass() *Classified {
	out := pass(*c, c.req, c.Forwarded.Host)
	out.FwdReason = FwdNone
	return &out
}

// pass fills the forwarded request of a ClassPass request: path, query and
// body as received, hop-by-hop fields removed (FR-FWD-3). Its URI tag still
// uses the rewritten path and query, so invalidation hits the URI a GET is
// stored under (04 §3.1).
func pass(out Classified, r *Request, host string) Classified {
	out.Class, out.FwdReason = ClassPass, FwdMethod
	h := r.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	DropHopByHop(h, r.Header["Connection"])
	deleteVariantHeader(h, out.expHeader) // T-35: a client cannot pick a variant on any path
	if out.expCookie != "" {
		stripCookie(h, out.expCookie) // the visitor id is for Weir, not the origin
	}
	out.Forwarded = Request{Method: r.Method, Scheme: r.Scheme, Host: host, Path: r.Path, RawQuery: r.RawQuery,
		Header: h, Body: r.Body}
	// net/http gives every bodyless request http.NoBody (FR-LIM-7: main pool).
	out.HasBody = r.Body != nil && r.Body != http.NoBody && r.Header.Get("Content-Length") != "0"
	return out
}

// parseIfNoneMatch returns the entity-tags of the If-None-Match lines
// (RFC 9110 §13.1.2), or nil when any element is malformed: ignoring the
// field makes the engine send the full response, never a wrong 304. Lines
// over MaxKeyedHeaderBytes combined are ignored too, which bounds the
// result (P5).
func parseIfNoneMatch(lines []string, c *Config) []string {
	if combinedLen(lines) > c.MaxKeyedHeaderBytes {
		return nil
	}
	if len(lines) == 1 && trimOWS(lines[0]) == "*" {
		return []string{"*"}
	}
	var tags []string
	for _, line := range lines {
		for i := 0; i < len(line); {
			if c := line[i]; c == ' ' || c == '\t' || c == ',' {
				i++
				continue
			}
			start := i
			if len(line)-i >= 2 && line[i] == 'W' && line[i+1] == '/' {
				i += 2
			}
			if i >= len(line) || line[i] != '"' {
				return nil
			}
			i++
			for i < len(line) && line[i] != '"' {
				if !etagc(line[i]) {
					return nil
				}
				i++
			}
			if i >= len(line) {
				return nil
			}
			i++
			tags = append(tags, line[start:i])
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			if i < len(line) && line[i] != ',' {
				return nil
			}
		}
	}
	return tags
}

// etagc reports an RFC 9110 §8.8.3 etagc byte.
func etagc(c byte) bool {
	return c == 0x21 || c >= 0x23 && c != 0x7f
}

func trimOWS(s string) string {
	for s != "" && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for s != "" && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// single returns the only line of a header, or "" for none or several: a
// repeated Range is ignored (RFC 9110 §14.2) and a repeated If-Range never
// applies, so both read as "serve the full 200" (FR-RNG-3).
func single(lines []string) string {
	if len(lines) != 1 {
		return ""
	}
	return lines[0]
}

// dropCookie removes the experiment cookie from the keyed cookies. New
// rejects a Key.Cookies entry that names it; this covers a Config built
// without New (T-35, INV-1).
func dropCookie(cs []Cookie, name string) []Cookie {
	if name == "" {
		return cs
	}
	if !slices.ContainsFunc(cs, func(c Cookie) bool { return strings.EqualFold(c.Name, name) }) {
		return cs
	}
	return slices.DeleteFunc(slices.Clone(cs), func(c Cookie) bool { return strings.EqualFold(c.Name, name) })
}
