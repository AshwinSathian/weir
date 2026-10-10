package valkey

import (
	"math"
	"strconv"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Epoch key slots in Store.ekeys, and so in the KEYS of both scripts.
const (
	keyHardIdx = iota
	keyGlobal
	keySketchSoft
	keySketchInvalid
	keyNewest
	keyMeta
	numEpochKeys
)

// Argument positions of the write script (scripts.go).
const (
	argMode = iota
	argGlobal
	argTag
	argAt
	argMargin
	argMaxHard
	argServerNow
	argPos1
	argPos2
	argSeedID
)

// Argument positions of the read script, before the tag list.
const (
	argSince = iota
	argSkew
	argHasGlobal
	argReadSeed
	argFirstTag
)

// tagStride is the number of arguments per non-global tag in the read
// script: raw tag, two cell positions, and '1' for a shared tag (E-12).
const tagStride = 4

// Read script replies that are not an epoch: meta, its version or a plane is
// absent (loss, 05 §7), or the stored seed differs from the caller's.
const (
	lossReply = -1
	seedReply = -2
)

// fieldSeed is the meta field holding the sketch seed.
const fieldSeed = "seed"

// epochKeys lays out the six epoch keys. All carry the hash tag, so one
// script touches one slot (05 §7).
func epochKeys(prefix, hashTag string) []string {
	base := prefix + ":{" + hashTag + "}:"
	k := make([]string, numEpochKeys)
	k[keyHardIdx] = base + "hardidx"
	k[keyGlobal] = base + "global"
	k[keySketchSoft] = base + "sketch:soft"
	k[keySketchInvalid] = base + "sketch:invalid"
	k[keyNewest] = base + "newest"
	k[keyMeta] = base + "meta"
	return k
}

// epochSeconds is At as the whole Unix seconds stored on the server: rounded
// up, so rounding only purges more (E-8), and clamped to [1, 2^32-1] so a
// zero time or an absurd one cannot wrap (05 §7, T-29).
func epochSeconds(at time.Time) int64 {
	s := at.Unix()
	if at.Nanosecond() > 0 {
		s++
	}
	return min(max(s, 1), math.MaxUint32)
}

// sinceSeconds is a lookup time rounded down, so rounding only matches more.
func sinceSeconds(since time.Time) int64 { return max(since.Unix(), 0) }

// wholeSeconds rounds a duration up to whole seconds.
func wholeSeconds(d time.Duration) int64 { return int64((d + time.Second - 1) / time.Second) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// writeArgs builds the write script's arguments. A zero at with serverNow
// false still sends 1 (epochSeconds), never zero.
func (s *Store) writeArgs(mode store.EpochMode, global bool, tag store.Tag, at time.Time, serverNow bool) []string {
	a := make([]string, argSeedID+1)
	a[argMode] = itoa(int64(mode))
	a[argGlobal] = "0"
	if global {
		a[argGlobal] = "1"
	}
	a[argTag] = string(tag[:])
	a[argAt] = itoa(epochSeconds(at))
	a[argMargin] = itoa(wholeSeconds(s.cfg.MaxRetention + s.cfg.MaxClockSkew + time.Second))
	a[argMaxHard] = strconv.Itoa(s.cfg.MaxHardEpochs)
	a[argServerNow] = "0"
	a[argPos1], a[argPos2] = "0", "0"
	if serverNow {
		a[argServerNow] = "1"
	}
	return a
}
