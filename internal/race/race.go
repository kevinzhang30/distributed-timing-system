// Package race turns a stream of decoded race packets into race state:
// per-lane splits, finish times, and standings. It is pure logic (no I/O) so
// it can be tested deterministically and reused by the node and scoreboard.
package race

import (
	"fmt"
	"slices"
	"time"

	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

// Touch is one wall touch recorded for a lane.
type Touch struct {
	Seq       uint32
	DistanceM uint16
	Elapsed   time.Duration
}

// Lane is everything known about one lane in one race.
type Lane struct {
	Lane     uint8
	Touches  []Touch // sorted by distance
	Finished bool
	Final    time.Duration

	seen map[uint32]bool
}

// Distance is how far the lane has been recorded swimming.
func (l *Lane) Distance() uint16 {
	if len(l.Touches) == 0 {
		return 0
	}
	return l.Touches[len(l.Touches)-1].DistanceM
}

// Race is the state of one race.
type Race struct {
	ID      uint32
	Event   string
	StartTS int64 // starter clock, unix ns; 0 if START not yet seen
	Lanes   map[uint8]*Lane
}

// Result describes what applying a packet did, for logging and display.
type Result int

const (
	Ignored   Result = iota // not a race event (heartbeat, NACK, sync)
	Duplicate               // already applied
	Started                 // first START for this race
	Touched                 // new split
	Finished                // new finish
)

// Tracker holds the state of every race it has seen.
type Tracker struct {
	races map[uint32]*Race
}

func NewTracker() *Tracker {
	return &Tracker{races: make(map[uint32]*Race)}
}

// Race returns the race with the given id, or nil.
func (t *Tracker) Race(id uint32) *Race { return t.races[id] }

func (t *Tracker) race(id uint32) *Race {
	r := t.races[id]
	if r == nil {
		r = &Race{ID: id, Lanes: make(map[uint8]*Lane)}
		t.races[id] = r
	}
	return r
}

// Apply records a packet. It is idempotent: replaying a packet (a UDP
// duplicate, or a retransmission after a NACK) returns Duplicate and changes
// nothing. Packets may arrive in any order.
func (t *Tracker) Apply(p wire.Packet) (*Race, Result) {
	switch pl := p.Payload.(type) {
	case wire.Start:
		r := t.race(p.RaceID)
		if r.StartTS != 0 {
			return r, Duplicate
		}
		r.StartTS, r.Event = pl.StartTS, pl.Event
		return r, Started

	case wire.Split:
		return t.touch(p, pl.DistanceM, pl.Elapsed, false)
	case wire.Finish:
		return t.touch(p, pl.DistanceM, pl.Elapsed, true)
	}
	return nil, Ignored
}

func (t *Tracker) touch(p wire.Packet, dist uint16, elapsed time.Duration, finish bool) (*Race, Result) {
	r := t.race(p.RaceID)
	l := r.Lanes[p.Lane]
	if l == nil {
		l = &Lane{Lane: p.Lane, seen: make(map[uint32]bool)}
		r.Lanes[p.Lane] = l
	}
	if l.seen[p.Seq] {
		return r, Duplicate
	}
	l.seen[p.Seq] = true

	// Insert in distance order: a retransmitted early split can arrive after later ones.
	tc := Touch{Seq: p.Seq, DistanceM: dist, Elapsed: elapsed}
	i, _ := slices.BinarySearchFunc(l.Touches, tc, func(a, b Touch) int { return int(a.DistanceM) - int(b.DistanceM) })
	l.Touches = slices.Insert(l.Touches, i, tc)

	if finish {
		l.Finished, l.Final = true, elapsed
		return r, Finished
	}
	return r, Touched
}

// Standings orders lanes: finishers by final time, then swimmers still racing
// by distance covered (furthest first), ties broken by lane number.
func (r *Race) Standings() []*Lane {
	out := make([]*Lane, 0, len(r.Lanes))
	for _, l := range r.Lanes {
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b *Lane) int {
		switch {
		case a.Finished && b.Finished:
			if a.Final != b.Final {
				return cmpDur(a.Final, b.Final)
			}
		case a.Finished != b.Finished:
			if a.Finished {
				return -1
			}
			return 1
		default:
			if a.Distance() != b.Distance() {
				return int(b.Distance()) - int(a.Distance())
			}
		}
		return int(a.Lane) - int(b.Lane)
	})
	return out
}

// Place returns the 1-based finishing place of a lane, or 0 if it hasn't finished.
// Lanes with identical times share a place.
func (r *Race) Place(lane uint8) int {
	l := r.Lanes[lane]
	if l == nil || !l.Finished {
		return 0
	}
	place := 1
	for _, o := range r.Lanes {
		if o.Finished && o.Final < l.Final {
			place++
		}
	}
	return place
}

// Done reports whether every lane that has reported anything has finished.
func (r *Race) Done() bool {
	if len(r.Lanes) == 0 {
		return false
	}
	for _, l := range r.Lanes {
		if !l.Finished {
			return false
		}
	}
	return true
}

// FormatTime renders a duration the way swim results do: "25.81", "1:02.34".
// Times are truncated (not rounded) to hundredths, matching touchpad rules.
func FormatTime(d time.Duration) string {
	if d < 0 {
		return "-" + FormatTime(-d)
	}
	cs := int64(d / (10 * time.Millisecond)) // centiseconds
	m, s, c := cs/6000, (cs/100)%60, cs%100
	if m > 0 {
		return fmt.Sprintf("%d:%02d.%02d", m, s, c)
	}
	return fmt.Sprintf("%d.%02d", s, c)
}

func cmpDur(a, b time.Duration) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
