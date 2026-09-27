package weir

import "testing"

// FR-OBS-1, 04 §9.2: exporters use EventKind strings as metric labels, so every kind
// has a distinct name.
func TestEventKindString(t *testing.T) {
	seen := map[string]EventKind{}
	for k := EventKind(1); k < evCount; k++ {
		s := k.String()
		if s == "" || s == "unknown" {
			t.Errorf("EventKind(%d) has no name", k)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("EventKind(%d) and EventKind(%d) share name %q", prev, k, s)
		}
		seen[s] = k
	}
	if got := EventKind(0).String(); got != "unknown" {
		t.Errorf("EventKind(0).String() = %q, want unknown", got)
	}
}

type recorder []Event

func (r *recorder) Observe(ev Event) { *r = append(*r, ev) }

// 04 §9.1: a nil Observer is valid and emit must not panic on it (NFR-2).
func TestEmit(t *testing.T) {
	t.Run("nil observer is a no-op", func(*testing.T) {
		emit(nil, Event{Kind: EvRequest})
	})
	t.Run("event reaches the observer", func(t *testing.T) {
		var r recorder
		emit(&r, Event{Kind: EvShed, Reason: "queue-full"})
		if len(r) != 1 || r[0].Kind != EvShed || r[0].Reason != "queue-full" {
			t.Errorf("got %+v", r)
		}
	})
}
