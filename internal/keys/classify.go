package keys

import (
	"hash/maphash"
	"net/http"
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
// it to weir.FwdReason. Bypass rules add FwdBypass (M7-03).
type FwdReason uint8

// FwdReason values.
const (
	FwdNone   FwdReason = iota
	FwdMethod           // the method is not cacheable
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
	FwdReason  FwdReason // FwdMethod for ClassPass
	Head       bool      // client method was HEAD
	Range      bool      // request carried Range
	Authorized bool      // request carried Authorization
	Unsafe     bool      // unsafe or unknown method: invalidate on 2xx/3xx
	HasBody    bool      // the forwarded request carries a body (upload pool, FR-LIM-7)
	Forwarded  Request   // the request the origin sees on a miss
	Primary    store.Key // zero for ClassPass
	URITag     store.Tag
	OriginTag  store.Tag
	Origin     string // scheme://host[:port]
	Partition  string // Origin + path, truncated to maxPartitionBytes
	PartitionH uint64 // maphash of Partition, per-process seed
	ReqCC      httpcc.RequestDirectives
	ClientCond ClientConditionals
}

const maxPartitionBytes = 512

var partitionSeed = maphash.MakeSeed()

// Classify validates r and builds its keys and forwarded request (04 §3.1).
// r and its Header are never modified; the forwarded request of a
// cacheable request has a fresh Header map.
func Classify(r *Request, c *Config) (Classified, error) {
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
	origin := r.Scheme + "://" + host

	out := Classified{
		Head:       r.Method == http.MethodHead,
		Range:      len(h["Range"]) > 0,
		Authorized: len(h["Authorization"]) > 0,
		URITag:     TagURI(origin, path, query),
		OriginTag:  TagOrigin(origin),
		Origin:     origin,
		Partition:  origin + path,
		ReqCC:      httpcc.ParseRequest(h),
	}
	if len(out.Partition) > maxPartitionBytes {
		out.Partition = out.Partition[:maxPartitionBytes]
	}
	out.PartitionH = maphash.String(partitionSeed, out.Partition)
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

	cookies := keyedCookies(h["Cookie"], c)
	// FR-FWD-4: a HEAD miss is fetched as GET so the response can be stored.
	// T-5: the body of a fat GET never reaches the origin.
	out.Forwarded = Request{Method: http.MethodGet, Scheme: r.Scheme, Host: host, Path: path, RawQuery: query,
		Header: forwardHeader(h, c, cookies)}
	out.Primary = PrimaryKey(&KeyInput{Method: http.MethodGet, Scheme: r.Scheme, Host: host, Path: path, Query: query,
		CookieNames: c.Cookies, Cookies: cookies})
	out.ClientCond = ClientConditionals{IfNoneMatch: parseIfNoneMatch(h["If-None-Match"], c)}
	// RFC 9110 §13.1.3: If-Modified-Since is ignored whenever If-None-Match
	// is present, even when that field is malformed or over the limit.
	if ims := h["If-Modified-Since"]; len(ims) == 1 && h["If-None-Match"] == nil {
		if t, err := http.ParseTime(ims[0]); err == nil {
			out.ClientCond.IfModifiedSince = t
		}
	}
	return out, nil
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
