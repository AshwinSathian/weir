package weir

import (
	"bytes"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/AshwinSathian/weir/internal/experiments"
	"github.com/AshwinSathian/weir/internal/keys"
)

// ExperimentsConfig turns on A/B bucketing at the cache (docs/10 §3). The
// zero value turns it off. Each matching experiment adds a (experiment,
// variant) dimension to the cache key and sets Header on the forwarded
// request, so the origin sees exactly what was keyed (INV-1). A client's own
// Header is always discarded (T-35).
type ExperimentsConfig struct {
	Cookie      string   // assignment cookie name; "": "weir_v"
	SigningKeys [][]byte // each at least 32 bytes; the first signs, all verify (rotation)
	Header      string   // forwarded variant header; "": "Weir-Variant"; no underscore
	MaxActive   int      // experiments that may apply to one request; 0: 4; at most 8
	Consent     ExperimentConsent
	Experiments []ExperimentConfig

	// DownstreamKeysOnVariant declares that every cache in front of Weir keys
	// on the variant (Weir-Variant, or the experiment cookie). Only then do
	// matched responses keep public, s-maxage and CDN-Cache-Control; by
	// default they are rewritten to private (E7, T-36).
	DownstreamKeysOnVariant bool
}

// ExperimentConsent gates bucketing (E6). With Cookie set and the cookie
// absent or lacking Value, a visitor gets the control variant, no cookie is
// set and no exposure is recorded. With the zero value, visitors are
// bucketed without a gate and New logs a warning.
type ExperimentConsent struct {
	Cookie string // consent cookie name, for example "cmp_consent"
	Value  string // an element of its value, for example "analytics=yes"; elements are split on '&', '|' and ','
}

// ExperimentConfig is one static experiment. The first variant is the
// control arm, served when consent is missing.
type ExperimentConfig struct {
	Name     string
	Salt     string // decorrelates experiments; change it to reshuffle visitors
	Winner   string // a variant name ends the experiment for all visitors; "" keeps it running
	Host     string // normalized, lower case; "" matches any host
	Path     string // exact path, or a prefix when it ends in "*"; "" matches any
	Variants []VariantConfig
}

// VariantConfig is one arm of an experiment. Weight is relative to the other arms.
type VariantConfig struct {
	Name   string
	Weight int
}

func (x *ExperimentsConfig) enabled() bool {
	return len(x.Experiments) > 0 || len(x.SigningKeys) > 0
}

func (x *ExperimentsConfig) applyDefaults() {
	if !x.enabled() {
		return
	}
	if x.Cookie == "" {
		x.Cookie = "weir_v"
	}
	if x.Header == "" {
		x.Header = "Weir-Variant"
	}
	x.Header = http.CanonicalHeaderKey(x.Header) // map keys of a forwarded request are canonical
	orInt(&x.MaxActive, 4)
}

// internal converts to the validated internal form, detached from the
// caller's slices.
func (x *ExperimentsConfig) internal() experiments.Config {
	out := experiments.Config{
		CookieName: x.Cookie, Header: x.Header, MaxActive: x.MaxActive,
		Consent: experiments.Consent{Cookie: x.Consent.Cookie, Value: x.Consent.Value},
	}
	for _, k := range x.SigningKeys {
		out.SigningKeys = append(out.SigningKeys, bytes.Clone(k))
	}
	for _, e := range x.Experiments {
		ie := experiments.Experiment{Name: e.Name, Salt: e.Salt, Winner: e.Winner, Host: e.Host, Path: e.Path}
		for _, v := range e.Variants {
			ie.Variants = append(ie.Variants, experiments.Variant{Name: v.Name, Weight: v.Weight})
		}
		out.Experiments = append(out.Experiments, ie)
	}
	return out
}

// validateExperiments checks the block at load (FR-LCY-1, E5). The
// variant product limit follows Key.MaxVariants (T-15, FR-KEY-10).
func (c *Config) validateExperiments() error {
	x := &c.Experiments
	if !x.enabled() {
		return nil
	}
	ic := x.internal()
	if err := ic.Validate(c.Key.MaxVariants); err != nil {
		return invalid("Experiments", strings.TrimPrefix(err.Error(), experiments.ErrConfig.Error()+": "))
	}
	// T-35, INV-1: the cookie carries the visitor id, which is no key input.
	named := func(n string) bool { return strings.EqualFold(n, x.Cookie) }
	if slices.ContainsFunc(c.Key.Cookies, named) || slices.ContainsFunc(c.Bypass.Cookies, named) {
		return invalid("Key.Cookies", "names the experiment cookie "+x.Cookie)
	}
	// One owner for the header: the engine sets it, never the client or an allowlist.
	for _, h := range [][]string{c.Key.Headers, c.Forward.Allow, c.Bypass.Headers} {
		if slices.ContainsFunc(h, func(n string) bool { return strings.EqualFold(strings.ReplaceAll(n, "_", "-"), x.Header) }) {
			return invalid("Experiments.Header", "also named in Key.Headers, Forward.Allow or Bypass.Headers")
		}
	}
	return nil
}

// experimentsKeys wires the assignment into keys.Classify; it does nothing
// when experiments are off.
func (e *Engine) experimentsKeys() {
	x := &e.cfg.Experiments
	if !x.enabled() {
		return
	}
	ic := x.internal()
	e.kcfg.ExpHeader, e.kcfg.ExpCookie = x.Header, x.Cookie
	rnd := e.cfg.Rand
	assign := func(host, path string, cookieLines []string, rnd func() float64) ([]keys.Dim, any) {
		r := ic.Assign(host, path, cookieLines, rnd)
		if len(r.Picks) == 0 {
			return nil, nil
		}
		dims := make([]keys.Dim, len(r.Picks))
		for i, p := range r.Picks {
			dims[i] = keys.Dim{Experiment: p.Experiment, Variant: p.Variant}
		}
		return dims, r
	}
	e.kcfg.Assign = func(host, path string, cookieLines []string) ([]keys.Dim, any) {
		return assign(host, path, cookieLines, rnd)
	}
	// Warm has no visitor: no cookie and no Rand, so every experiment without
	// a winner gets its control variant and nothing is minted or burned.
	e.kcfgWarm = e.kcfg
	e.kcfgWarm.Assign = func(host, path string, _ []string) ([]keys.Dim, any) {
		dims, _ := assign(host, path, nil, nil)
		return dims, nil
	}
	e.kcfgPurge = e.kcfg
	e.kcfgPurge.Assign = nil
	if len(x.Experiments) > 0 && !ic.Consent.Configured() {
		e.cfg.Logger.Warn("weir: Experiments has no Consent gate; visitors are bucketed and cookied without a consent signal")
	}
}

const expCookieMaxAge = "7776000" // 90 days, not sliding (10 §3)

// experimentResponse finishes a response to a request that matched an
// experiment (10 §4 steps 5 and 6): it adds the assignment cookie when one
// was created or changed, and rewrites the headers downstream caches read
// (E7, T-36). It runs after the entry is built and stored, on a header map
// of its own, so the stored entry is never touched (P4). Responses to
// unmatched requests, and errors, pass through.
func (e *Engine) experimentResponse(c *keys.Classified, resp *Response) *Response {
	res, ok := c.Decision.(experiments.Result)
	if !ok || resp == nil || len(res.Picks) == 0 {
		return resp
	}
	x := &e.cfg.Experiments
	h := maps.Clone(resp.Header)
	if h == nil {
		h = http.Header{}
	}
	resp.Header = h
	if res.Changed {
		if v, ok := res.Cookie.Encode(x.SigningKeys[0]); ok {
			sc := x.Cookie + "=" + v + "; Path=/; Max-Age=" + expCookieMaxAge + "; HttpOnly; SameSite=Lax"
			if c.Forwarded.Scheme == "https" {
				sc += "; Secure"
			}
			old := h["Set-Cookie"]
			h["Set-Cookie"] = append(old[:len(old):len(old)], sc)
		}
	}
	if !x.DownstreamKeysOnVariant {
		h["Cache-Control"] = []string{privateCacheControl(h["Cache-Control"])}
		delete(h, "Cdn-Cache-Control")
	}
	return resp
}

// privateCacheControl drops public and s-maxage from Cache-Control lines and
// makes sure an unqualified private is present. Directives are split on
// commas outside quotes; anything else is kept as sent.
func privateCacheControl(lines []string) string {
	var kept []string
	private := false
	for _, l := range lines {
		start, quoted := 0, false
		emit := func(end int) {
			d := strings.TrimSpace(l[start:end])
			if d == "" {
				return
			}
			name, _, _ := strings.Cut(d, "=")
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "public", "s-maxage":
				return
			case "private":
				private = private || d == name // a field-qualified private is not the broad one
			}
			kept = append(kept, d)
		}
		for i := 0; i < len(l); i++ {
			switch l[i] {
			case '"':
				quoted = !quoted
			case '\\':
				if quoted {
					i++
				}
			case ',':
				if !quoted {
					emit(i)
					start = i + 1
				}
			}
		}
		emit(len(l))
	}
	if !private {
		kept = append(kept, "private")
	}
	return strings.Join(kept, ", ")
}
