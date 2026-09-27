package weir

import (
	"strconv"
	"time"
)

var fwdNames = [...]string{
	FwdBypass:   "bypass",
	FwdMethod:   "method",
	FwdURIMiss:  "uri-miss",
	FwdVaryMiss: "vary-miss",
	FwdRequest:  "request",
	FwdStale:    "stale",
}

var staleDetails = [...]string{
	StaleWhileRevalidate: "stale-while-revalidate",
	StaleIfError:         "stale-if-error",
	StaleShed:            "shed",
	StaleCircuitOpen:     "circuit-open",
	StaleCoalesceTimeout: "coalesce-timeout",
}

// cacheStatus builds this cache's RFC 9211 member from ci. The key
// parameter is never emitted (FR-SRV-9, T-27). No fmt: it runs on every hit.
func cacheStatus(name string, ci CacheInfo) string {
	b := make([]byte, 0, 64)
	b = append(b, name...)
	if ci.Hit {
		b = append(b, "; hit; ttl="...)
		b = strconv.AppendInt(b, int64(ci.TTL/time.Second), 10)
	} else if int(ci.Fwd) < len(fwdNames) && fwdNames[ci.Fwd] != "" {
		b = append(b, "; fwd="...)
		b = append(b, fwdNames[ci.Fwd]...)
		if ci.FwdStatus != 0 {
			b = append(b, "; fwd-status="...)
			b = strconv.AppendInt(b, int64(ci.FwdStatus), 10)
		}
		if ci.Stored {
			b = append(b, "; stored"...)
		}
	}
	if ci.Collapsed {
		b = append(b, "; collapsed"...)
	}
	detail := ci.Detail
	if detail == "" && int(ci.Stale) < len(staleDetails) {
		detail = staleDetails[ci.Stale]
	}
	if detail != "" {
		b = append(b, "; detail="...)
		b = append(b, detail...)
	}
	return string(b)
}
