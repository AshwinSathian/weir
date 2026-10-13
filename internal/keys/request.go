package keys

import (
	"errors"
	"io"
	"net/http"
)

// Request mirrors weir.Request field for field; keys cannot import weir
// (04 §3.1), so the root package converts with a field copy.
type Request struct {
	Method   string
	Scheme   string
	Host     string
	Path     string
	RawQuery string
	Header   http.Header // canonical keys, as net/http builds them; nil is treated as empty
	Body     io.ReadCloser
	Client   string // who sent the request; never keyed, forwarded only on a pass-through
	// PassThrough marks the forwarded request of a ClassPass request for the
	// Origin; ignored on input.
	PassThrough bool
}

// Config is the compiled key configuration. Limits arrive with the root
// package's defaults already applied.
type Config struct {
	MaxPathBytes        int
	MaxQueryBytes       int
	MaxQueryParams      int
	MaxKeyedHeaderBytes int

	QueryDrop      []string // exact names or "prefix*"
	QueryKeep      []string // empty keeps all
	QuerySort      bool
	NormalizePath  bool
	Headers        []string // Key.Headers, canonical names, keyed in this order
	Cookies        []string // forwarded in this order
	AcceptEncoding []string // lowercase tokens, most preferred first

	ForwardAll        bool     // Forward.Mode == ForwardAll
	Allow             []string // Forward.Allow, canonical header names
	NoTraceHeaders    bool
	BypassHeaders     []string // Bypass.Headers, canonical names
	BypassCookies     []string // Bypass.Cookies
	HonorRevalidation bool     // Client.HonorRevalidation

	// Experiments (10 §4 steps 3 and 4). ExpHeader is deleted from every
	// forwarded request, spelled with '_' or '-' and in any case, and set
	// from the assignment on a cacheable one. ExpCookie never reaches the
	// origin. Assign receives the normalized host, the validated path and
	// the client's Cookie lines, and returns the key dimensions in config
	// order; its second result is handed back in Classified.Decision.
	ExpHeader string
	ExpCookie string
	Assign    func(host, path string, cookieLines []string) (dims []Dim, decision any)
}

// Dim is one experiment dimension of the primary key: the variant a
// request is keyed and forwarded under (10 §4 step 4).
type Dim struct {
	Experiment, Variant string
}

// ErrUpgrade reports a CONNECT or protocol upgrade request (FR-UPG-1). The
// root package maps it to weir.ErrUpgradeNotSupported.
var ErrUpgrade = errors.New("weir: connect and protocol upgrades not supported")

// RequestError reports why a request failed validation. Reason is one of the
// Reason constants and is safe as a metric label (04 §9).
type RequestError struct {
	Reason string
}

// Error implements error.
func (e *RequestError) Error() string { return "weir: invalid request: " + e.Reason }

// Reason values for RequestError (04 §3.1 step 1).
const (
	ReasonScheme      = "scheme"
	ReasonHost        = "host"
	ReasonPath        = "path"
	ReasonPathEscape  = "path-escape"
	ReasonQuery       = "query"
	ReasonQueryParams = "query-params"
	ReasonMethod      = "method"
)
