package weir

import (
	"testing"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// FR-SRV-2, RFC 9110 §13.1.3: HTTP-dates have whole-second resolution, so a
// stored Date or Last-Modified with sub-second time must not read as later
// than a client date naming the same second.
func TestClientIMSWholeSecond(t *testing.T) {
	sec := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		ent  store.Entry
		ims  time.Time
		want bool
	}{
		{"Date with fraction, same second", store.Entry{Status: 200, Date: sec.Add(700 * time.Millisecond)}, sec, true},
		{"Last-Modified with fraction, same second", store.Entry{Status: 200, LastModified: sec.Add(700 * time.Millisecond)}, sec, true},
		{"later second still modified", store.Entry{Status: 200, Date: sec.Add(time.Second)}, sec, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cc := &keys.ClientConditionals{IfModifiedSince: tc.ims}
			if got := clientNotModified(cc, &tc.ent); got != tc.want {
				t.Errorf("clientNotModified = %v, want %v", got, tc.want)
			}
		})
	}
}
