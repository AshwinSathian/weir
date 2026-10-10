package valkey

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// fakeEp stands in for the two epoch scripts and WAIT. It records every call
// and answers with what the test queues.
type fakeEp struct {
	mu       sync.Mutex
	writes   []scriptCall
	reads    []scriptCall
	waits    [][2]int64
	writeErr []error   // popped one per evalWrite; nil entries succeed
	readRes  [][]int64 // popped one per evalRead
	readErr  error
	waitErr  error
	onWrite  func() // runs inside the first evalWrite (a context that ends mid-call)
	seedVal  []byte // what meta.seed holds; the first seed call stores its argument
	seedErr  error
	seeds    int // seed calls
}

type scriptCall struct {
	keys, args []string
}

func (f *fakeClient) evalWrite(_ context.Context, keys, args []string) error {
	f.ep.mu.Lock()
	defer f.ep.mu.Unlock()
	f.ep.writes = append(f.ep.writes, scriptCall{keys, args})
	if f.ep.onWrite != nil {
		f.ep.onWrite()
	}
	if len(f.ep.writeErr) == 0 {
		return nil
	}
	err := f.ep.writeErr[0]
	f.ep.writeErr = f.ep.writeErr[1:]
	return err
}

func (f *fakeClient) evalRead(_ context.Context, keys, args []string) ([]int64, error) {
	f.ep.mu.Lock()
	defer f.ep.mu.Unlock()
	f.ep.reads = append(f.ep.reads, scriptCall{keys, args})
	if f.ep.readErr != nil {
		return nil, f.ep.readErr
	}
	if len(f.ep.readRes) == 0 {
		return nil, nil
	}
	r := f.ep.readRes[0]
	f.ep.readRes = f.ep.readRes[1:]
	return r, nil
}

func (f *fakeClient) evalWriteWait(_ context.Context, keys, args []string, replicas, ms int64) error {
	f.ep.mu.Lock()
	defer f.ep.mu.Unlock()
	f.ep.writes = append(f.ep.writes, scriptCall{keys, args})
	f.ep.waits = append(f.ep.waits, [2]int64{replicas, ms})
	if f.ep.waitErr != nil {
		return f.ep.waitErr
	}
	if len(f.ep.writeErr) == 0 {
		return nil
	}
	err := f.ep.writeErr[0]
	f.ep.writeErr = f.ep.writeErr[1:]
	return err
}

func (f *fakeClient) seed(_ context.Context, _ string, fresh []byte) ([]byte, error) {
	f.ep.mu.Lock()
	defer f.ep.mu.Unlock()
	f.ep.seeds++
	if f.ep.seedErr != nil {
		return nil, f.ep.seedErr
	}
	if f.ep.seedVal == nil {
		f.ep.seedVal = slices.Clone(fresh)
	}
	return slices.Clone(f.ep.seedVal), nil
}

func (f *fakeClient) counts() (writes, reads, waits int) {
	f.ep.mu.Lock()
	defer f.ep.mu.Unlock()
	return len(f.ep.writes), len(f.ep.reads), len(f.ep.waits)
}

func tag(n int) store.Tag {
	var tg store.Tag
	copy(tg[:], "valkey-test-tag-"+strconv.Itoa(n))
	return tg
}

func hardAt(at time.Time) store.Epoch { return store.Epoch{At: at, Mode: store.EpochHard} }

// T-29, E-3: epoch times become whole Unix seconds. An At rounds up and a
// since rounds down, so a conversion can only purge more, never less.
func TestEpochSecondsRounding(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name  string
		at    time.Time
		since time.Time
		wantA int64
		wantS int64
	}{
		{"whole second is exact", base, base, 1_800_000_000, 1_800_000_000},
		{"At rounds up", base.Add(time.Nanosecond), base.Add(time.Nanosecond), 1_800_000_001, 1_800_000_000},
		{"just under the next second", base.Add(999 * time.Millisecond), base.Add(999 * time.Millisecond), 1_800_000_001, 1_800_000_000},
		{"zero time clamps to 1 and 0", time.Time{}, time.Time{}, 1, 0},
		{"far future saturates", time.Unix(1<<40, 0), time.Unix(1<<40, 0), math.MaxUint32, 1 << 40},
		{"before 1970 clamps", time.Unix(-5, 1), time.Unix(-5, 1), 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := epochSeconds(tc.at); got != tc.wantA {
				t.Errorf("epochSeconds = %d, want %d", got, tc.wantA)
			}
			if got := sinceSeconds(tc.since); got != tc.wantS {
				t.Errorf("sinceSeconds = %d, want %d", got, tc.wantS)
			}
		})
	}
}

// T-29, 05 §7: a zero or absurd At cannot wrap to 2106 or to 1970. The
// script gets a value in [1, 2^32-1].
func TestSaturatingWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"zero At", time.Time{}, "1"},
		{"far future", time.Unix(1<<50, 0), strconv.FormatUint(math.MaxUint32, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cl := connected(t, nil)
			if err := s.SetEpoch(t.Context(), store.TagGlobal(), hardAt(tc.at)); err != nil {
				t.Fatal(err)
			}
			if got := cl.ep.writes[0].args[argAt]; got != tc.want {
				t.Fatalf("at argument = %q, want %q", got, tc.want)
			}
		})
	}
}

// 05 §7: every epoch key is declared in KEYS, all behind the {e} hash tag,
// on every call (cluster mode), and the layout matches the notes.
func TestEpochKeysDeclaredInKEYS(t *testing.T) {
	s, cl := connected(t, func(c *Config) { c.Prefix = "p"; c.HashTag = "h" })
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); err != nil {
		t.Fatal(err)
	}
	cl.ep.readRes = [][]int64{{}}
	if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []string{"p:{h}:hardidx", "p:{h}:global", "p:{h}:sketch:soft", "p:{h}:sketch:invalid", "p:{h}:newest", "p:{h}:meta"}
	for name, call := range map[string]scriptCall{"write": cl.ep.writes[0], "read": cl.ep.reads[0]} {
		if !equal(call.keys, want) {
			t.Errorf("%s script KEYS = %q, want %q", name, call.keys, want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every tag takes every mode once the sketch exists (E-7); only an unknown
// mode is refused, with ErrUnavailable and nothing sent (T-29: refusing is
// safe because Purge reports it, silently dropping the epoch is not).
func TestSetEpochModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tag     store.Tag
		mode    store.EpochMode
		refused bool
	}{
		{"soft on a URI tag", tag(1), store.EpochSoft, false},
		{"invalid on a URI tag", tag(1), store.EpochInvalid, false},
		{"soft on the global tag", store.TagGlobal(), store.EpochSoft, false},
		{"invalid on the global tag", store.TagGlobal(), store.EpochInvalid, false},
		{"hard on a URI tag", tag(1), store.EpochHard, false},
		{"zero mode", store.TagGlobal(), 0, true},
		{"mode past hard", store.TagGlobal(), store.EpochHard + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cl := connected(t, nil)
			err := s.SetEpoch(t.Context(), tc.tag, store.Epoch{At: time.Now(), Mode: tc.mode})
			w, _, _ := cl.counts()
			if tc.refused {
				if !errors.Is(err, store.ErrUnavailable) || w != 0 {
					t.Fatalf("SetEpoch = %v after %d writes, want ErrUnavailable and none", err, w)
				}
				return
			}
			if err != nil || w != 1 {
				t.Fatalf("SetEpoch = %v after %d writes, want success in one script call", err, w)
			}
		})
	}
}

// 05 §7 (Timeouts): scripts are idempotent, so the store retries SetEpoch
// once itself after a network error, and only then.
func TestSetEpochRetriesOnceOnNetworkError(t *testing.T) {
	srv := &valkey.ValkeyError{}
	for _, tc := range []struct {
		name       string
		errs       []error
		wantWrites int
		wantErr    bool
	}{
		{"first write fails on the network", []error{io.EOF}, 2, false},
		{"both writes fail on the network", []error{io.EOF, io.EOF}, 2, true},
		{"server error is final", []error{srv}, 1, true},
		{"closing client is final", []error{valkey.ErrClosing}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cl := connected(t, nil)
			cl.ep.writeErr = tc.errs
			err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now()))
			if w, _, _ := cl.counts(); w != tc.wantWrites {
				t.Errorf("script calls = %d, want %d", w, tc.wantWrites)
			}
			if tc.wantErr != (err != nil) || (err != nil && !errors.Is(err, store.ErrUnavailable)) {
				t.Errorf("err = %v, want error=%v wrapping ErrUnavailable", err, tc.wantErr)
			}
		})
	}
	t.Run("ended context is not retried", func(t *testing.T) {
		s, cl := connected(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cl.ep.writeErr = []error{io.EOF, io.EOF}
		cl.ep.onWrite = cancel // the context ends during the first call
		_ = s.SetEpoch(ctx, tag(1), hardAt(time.Now()))
		if w, _, _ := cl.counts(); w != 1 {
			t.Errorf("script calls = %d, want exactly 1", w)
		}
	})
}

// The retry also covers the combined write-and-WAIT call.
func TestSetEpochWaitPathRetriesOnce(t *testing.T) {
	s, cl := connected(t, func(c *Config) { c.HardEpochWait = 50 * time.Millisecond })
	cl.ep.writeErr = []error{io.EOF}
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); err != nil {
		t.Fatal(err)
	}
	if w, _, _ := cl.counts(); w != 2 {
		t.Fatalf("calls = %d, want 2", w)
	}
}

// 05 §7 (Replication): with HardEpochWait a hard write and its WAIT go out
// as one call (one connection), and only a hard write waits.
func TestHardEpochWaitIssuesWait(t *testing.T) {
	cfg := func(c *Config) { c.HardEpochWait = 50 * time.Millisecond }
	t.Run("hard on a tag", func(t *testing.T) {
		s, cl := connected(t, cfg)
		if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); err != nil {
			t.Fatal(err)
		}
		if len(cl.ep.waits) != 1 || cl.ep.waits[0] != [2]int64{1, 50} {
			t.Fatalf("WAIT calls = %v, want one WAIT 1 50", cl.ep.waits)
		}
		if w, _, _ := cl.counts(); w != 1 {
			t.Fatalf("script calls = %d, want the write and WAIT in one", w)
		}
	})
	t.Run("hard on the global tag", func(t *testing.T) {
		s, cl := connected(t, cfg)
		if err := s.SetEpoch(t.Context(), store.TagGlobal(), hardAt(time.Now())); err != nil {
			t.Fatal(err)
		}
		if _, _, w := cl.counts(); w != 1 {
			t.Fatalf("WAIT calls = %d, want 1", w)
		}
	})
	t.Run("soft global epoch does not wait", func(t *testing.T) {
		s, cl := connected(t, cfg)
		if err := s.SetEpoch(t.Context(), store.TagGlobal(), store.Epoch{At: time.Now(), Mode: store.EpochSoft}); err != nil {
			t.Fatal(err)
		}
		if _, _, w := cl.counts(); w != 0 {
			t.Fatalf("WAIT calls = %d, want 0", w)
		}
	})
	t.Run("off by default", func(t *testing.T) {
		s, cl := connected(t, nil)
		if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); err != nil {
			t.Fatal(err)
		}
		if _, _, w := cl.counts(); w != 0 {
			t.Fatalf("WAIT calls = %d, want 0", w)
		}
	})
	t.Run("a failed WAIT is reported", func(t *testing.T) {
		s, cl := connected(t, cfg)
		cl.ep.waitErr = io.EOF
		if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("SetEpoch = %v, want ErrUnavailable", err)
		}
	})
}

// E-10, 05 §7: GET newest answers "no epochs" with no script when newest is
// older than since minus the skew; newer, equal within skew, absent or
// unreadable goes to the script.
func TestNewestEpochFastPath(t *testing.T) {
	since := time.Unix(1_800_000_100, 0)
	newestKey := "weir:{e}:newest"
	for _, tc := range []struct {
		name   string
		newest string // "" means absent
		skew   time.Duration
		script bool
	}{
		{"older than since and skew", "1800000050", time.Second, false},
		{"older than since, skew reaches it", "1800000099", time.Second, true},
		{"equal to since", "1800000100", time.Second, true},
		{"newer than since", "1800000200", time.Second, true},
		{"skew of zero, one second older", "1800000099", 0, false},
		{"absent is never no epochs", "", time.Second, true},
		{"garbage goes to the script", "x", time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, cl := connected(t, func(c *Config) {
				if tc.skew == 0 {
					c.NoClockSkew = true
				} else {
					c.MaxClockSkew = tc.skew
				}
			})
			if tc.newest != "" {
				cl.kv.vals = map[string][]byte{newestKey: []byte(tc.newest)}
			}
			cl.ep.readRes = [][]int64{{}}
			_, ok, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, since)
			if err != nil || ok {
				t.Fatalf("NewestEpoch = %v, %v; want no epoch", ok, err)
			}
			if _, r, _ := cl.counts(); (r == 1) != tc.script {
				t.Fatalf("script calls = %d, want script=%v", r, tc.script)
			}
		})
	}
	t.Run("an unreadable newest key is an error", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.kv.err = io.EOF
		if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, since); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
	t.Run("no tags needs no round trip", func(t *testing.T) {
		s, cl := connected(t, nil)
		if _, ok, err := s.NewestEpoch(t.Context(), nil, since); ok || err != nil {
			t.Fatalf("NewestEpoch = %v, %v", ok, err)
		}
		if _, r, _ := cl.counts(); r != 0 || len(cl.kv.vals) != 0 {
			t.Fatal("no tags still called the server")
		}
	})
}

// E-5: the global tag is read from its hash only when the lookup names it;
// the script receives the other tags raw, in order, after three fixed
// arguments.
func TestNewestEpochScriptArguments(t *testing.T) {
	s, cl := connected(t, nil)
	g, a, b := store.TagGlobal(), tag(1), tag(2)
	cl.ep.readRes = [][]int64{{}, {}}
	since := time.Unix(1_800_000_000, 5)
	for _, tags := range [][]store.Tag{{g, a, b}, {a, b}} {
		if _, _, err := s.NewestEpoch(t.Context(), tags, since); err != nil {
			t.Fatal(err)
		}
	}
	r := cl.ep.reads
	if r[0].args[argSince] != "1800000000" || r[0].args[argSkew] != "1" || r[0].args[argHasGlobal] != "1" {
		t.Errorf("fixed arguments = %q", r[0].args[:3])
	}
	if got := r[0].args[argFirstTag:]; len(got) != 2*tagStride || got[0] != string(a[:]) || got[tagStride] != string(b[:]) {
		t.Errorf("tag arguments = %d, want the two non-global tags", len(got)/tagStride)
	}
	if r[1].args[argHasGlobal] != "0" || len(r[1].args) != argFirstTag+2*tagStride {
		t.Errorf("without the global tag: hasGlobal=%q, %d args", r[1].args[argHasGlobal], len(r[1].args))
	}
}

// E-3: the script reply becomes an Epoch: mode first, then At.
func TestNewestEpochReply(t *testing.T) {
	s, cl := connected(t, nil)
	cl.ep.readRes = [][]int64{{int64(store.EpochHard), 1_800_000_000}, {1, 2, 3}}
	ep, ok, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0))
	if err != nil || !ok || ep.Mode != store.EpochHard || !ep.At.Equal(time.Unix(1_800_000_000, 0)) {
		t.Fatalf("NewestEpoch = %+v, %v, %v", ep, ok, err)
	}
	if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0)); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("malformed reply: err = %v, want ErrUnavailable", err)
	}
}

// 05 §7 (loss detection): a script that reports loss makes the store write a
// global hard epoch at server time, then look again once. A second loss is an
// error, not a loop.
func TestLossRepairIsOneWriteAndOneRetry(t *testing.T) {
	t.Run("repaired", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{lossReply}, {int64(store.EpochHard), 1_800_000_000}}
		ep, ok, err := s.NewestEpoch(t.Context(), []store.Tag{store.TagGlobal()}, time.Unix(1, 0))
		if err != nil || !ok || ep.Mode != store.EpochHard {
			t.Fatalf("NewestEpoch = %+v, %v, %v", ep, ok, err)
		}
		w, r, _ := cl.counts()
		if w != 1 || r != 2 {
			t.Fatalf("writes = %d, reads = %d; want 1 and 2", w, r)
		}
		args := cl.ep.writes[0].args
		g := store.TagGlobal()
		if args[argMode] != "3" || args[argGlobal] != "1" || args[argTag] != string(g[:]) || args[argServerNow] != "1" {
			t.Errorf("repair arguments = mode %q global %q serverNow %q, want a global hard write at server time",
				args[argMode], args[argGlobal], args[argServerNow])
		}
	})
	t.Run("loss again after repair", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{lossReply}, {lossReply}, {lossReply}}
		_, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0))
		if !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if w, r, _ := cl.counts(); w != 1 || r != 2 {
			t.Fatalf("writes = %d, reads = %d; want 1 and 2", w, r)
		}
	})
	t.Run("repair failure is reported", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{lossReply}}
		cl.ep.writeErr = []error{&valkey.ValkeyError{}}
		if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0)); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
}

// A closed store and an unreachable one fail the epoch calls with
// ErrUnavailable and never reach a script.
func TestEpochCallsHonorConnectPath(t *testing.T) {
	s := newFake(t, nil, &fakeDialer{err: errors.New("connection refused")})
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(time.Now())); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("SetEpoch = %v", err)
	}
	if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Now()); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("NewestEpoch = %v", err)
	}
}
