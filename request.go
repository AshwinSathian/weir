package weir

import (
	"io"
	"net/http"
	"time"
)

// Request is one client request as the adapter received it (01 §4).
type Request struct {
	Method   string        // exactly as received; methods are case-sensitive
	Scheme   string        // "http" or "https"
	Host     string        // authority (host[:port]) the client addressed
	Path     string        // origin-form path, still percent-encoded, as received
	RawQuery string        // query without the leading '?', as received
	Header   http.Header   // never mutated by Weir
	Body     io.ReadCloser // nil for GET and HEAD; passed through for other methods
	// Client names who sent the request, for Limiter.MaxPerClient: any
	// string that is equal for one client and different for another, usually
	// the remote address. Weir never keys on it, never puts it in an event,
	// and never sends it to the origin with a cacheable fetch, whose response
	// other clients share. A request that is passed through (unsafe methods,
	// bypass) carries it in the Request the Origin receives, so an Origin can
	// tell the origin server who asked. Empty: the request is never capped.
	Client string
}

// Response is a response from an Origin or from Serve. The receiver of a
// Response from Serve owns Body and must close it.
type Response struct {
	StatusCode int
	Header     http.Header
	// Body is never nil on responses returned by Serve. An Origin's body may
	// also implement Trailer() http.Header, valid once Read has returned
	// io.EOF; the body of a streamed response from Serve then does too, and
	// weirhttp.WriteResponse sends those trailers. Stored responses have none.
	Body io.ReadCloser
	// Cache describes how Serve produced the response. Serve sets it; an
	// Origin's value is ignored.
	Cache CacheInfo
}

// CacheInfo describes how Serve produced a response. It feeds the
// Cache-Status header (FR-SRV-9) and events.
type CacheInfo struct {
	Hit       bool          // answered without contacting the origin for this request
	Fwd       FwdReason     // why the request went forward; FwdNone when Hit
	Stale     StaleReason   // non-zero when a stale response was served
	FwdStatus int           // origin status when Fwd != FwdNone (304 on revalidation)
	Stored    bool          // the forwarded response was stored
	Collapsed bool          // this request waited on another request's flight
	TTL       time.Duration // remaining freshness at send time; negative when stale
	// Detail is a short label for the Cache-Status detail parameter. Serve
	// sets it to one of two values: "negative" (a cached origin error
	// answered) or "hit-for-miss" (the URI is known to be unstorable, so the
	// request skipped coalescing). It is empty otherwise. When it is empty
	// and Stale is not StaleNone, the Cache-Status header carries the stale
	// reason instead: "stale-while-revalidate", "stale-if-error", "shed",
	// "circuit-open" or "coalesce-timeout". New values may be added in a
	// minor release, so treat an unknown value as informational.
	Detail string
}

// FwdReason is why a request went to the origin. Values mirror the fwd
// parameter of RFC 9211 §2.2.
type FwdReason uint8

// FwdReason values.
const (
	FwdNone     FwdReason = iota // not forwarded
	FwdBypass                    // a bypass rule matched (fwd=bypass)
	FwdMethod                    // the method is not cacheable (fwd=method)
	FwdURIMiss                   // no stored response for the URI (fwd=uri-miss)
	FwdVaryMiss                  // stored responses exist, none match Vary (fwd=vary-miss)
	FwdRequest                   // the request's directives forced forwarding (fwd=request)
	FwdStale                     // the stored response was stale (fwd=stale)
)

// StaleReason is why a stale response was served.
type StaleReason uint8

// StaleReason values.
const (
	StaleNone            StaleReason = iota // the response was not stale
	StaleWhileRevalidate                    // within stale-while-revalidate
	StaleIfError                            // the origin failed, within stale-if-error
	StaleShed                               // the limiter shed the fetch
	StaleCircuitOpen                        // the breaker was open
	StaleCoalesceTimeout                    // the wait on another request's flight timed out
)
