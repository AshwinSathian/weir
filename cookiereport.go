package weir

import (
	"cmp"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
)

const (
	cookieReportNames    = 32 // counters in the summary (FR-OBS-5)
	cookieReportPairs    = 32 // pairs read from one request
	cookieReportNameSize = 64 // longer names are not counted
)

// cookieReport counts the cookie names strict forwarding strips during the
// first Bypass.ReportStrippedCookies after New, then logs them once
// (FR-OBS-5, D31). It never reads a value. Memory is at most
// cookieReportNames names of cookieReportNameSize bytes (P5).
type cookieReport struct {
	done  atomic.Bool // set once the report is logged; the only cost after that
	keyed []string    // Key.Cookies: forwarded, so not stripped
	log   *slog.Logger

	mu       sync.Mutex
	deadline time.Time
	counts   []cookieCount // Space-Saving summary, at most cookieReportNames
}

type cookieCount struct {
	name string
	n    uint64
}

// newCookieReport returns nil when the report is disabled or when
// ForwardAll forwards every cookie.
func newCookieReport(c *Config) *cookieReport {
	if c.Bypass.ReportStrippedCookies < 0 || c.Forward.Mode == ForwardAll {
		return nil
	}
	return &cookieReport{keyed: c.Key.Cookies, log: c.Logger, deadline: time.Now().Add(c.Bypass.ReportStrippedCookies)}
}

// observe counts the unkeyed cookie names in the Cookie lines of one
// cacheable request. The first call with a Cookie line past the deadline
// logs the report, so no timer or goroutine is needed; an engine with no
// cookie traffic after the window never logs, and has little to say. A
// call that finds the lock held skips its request: the report is a sample
// (D31), and the hit path must not queue behind it.
func (r *cookieReport) observe(h http.Header) {
	if r == nil || r.done.Load() {
		return
	}
	// Classify strips whatever case the caller's key has, so read it the
	// same way. net/http headers are already canonical and cost a scan.
	lines := keys.CanonicalHeader(h)["Cookie"]
	if len(lines) == 0 || !r.mu.TryLock() {
		return
	}
	defer r.mu.Unlock()
	if r.done.Load() {
		return
	}
	if !time.Now().Before(r.deadline) {
		r.done.Store(true)
		r.report()
		return
	}
	pairs := 0
	for _, line := range lines {
		for pair := range strings.SplitSeq(line, ";") {
			if pairs++; pairs > cookieReportPairs {
				return
			}
			name, _, ok := strings.Cut(strings.Trim(pair, " \t"), "=")
			// Only token names are logged: a name is attacker-chosen
			// bytes, and a token holds no space, quote or control byte.
			if ok && len(name) <= cookieReportNameSize && isToken(name) && !slices.Contains(r.keyed, name) {
				r.count(name)
			}
		}
	}
}

// count is the Space-Saving update: a new name takes over the smallest
// counter and inherits its count, so a frequent name cannot be pushed out
// by a stream of distinct rare ones.
func (r *cookieReport) count(name string) {
	least := 0
	for i := range r.counts {
		if r.counts[i].name == name {
			r.counts[i].n++
			return
		}
		if r.counts[i].n < r.counts[least].n {
			least = i
		}
	}
	// Cloned so the summary does not pin the request's whole Cookie line.
	name = strings.Clone(name)
	if len(r.counts) < cookieReportNames {
		r.counts = append(r.counts, cookieCount{name, 1})
		return
	}
	r.counts[least] = cookieCount{name, r.counts[least].n + 1}
}

// report logs the names, most frequent first, and frees the summary.
func (r *cookieReport) report() {
	if len(r.counts) == 0 {
		return
	}
	slices.SortFunc(r.counts, func(a, b cookieCount) int {
		return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.name, b.name))
	})
	names := make([]string, len(r.counts))
	for i, c := range r.counts {
		names[i] = c.name
	}
	r.counts = nil
	r.log.Info("weir: cookies stripped from cacheable requests; add a name to Key.Cookies or Bypass.Cookies if responses depend on it", "names", names)
}
