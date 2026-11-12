package race

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

func split(lane uint8, seq uint32, dist uint16, sec float64) wire.Packet {
	return wire.Packet{Lane: lane, RaceID: 1, Seq: seq, Payload: wire.Split{DistanceM: dist, Elapsed: secs(sec)}}
}

func finish(lane uint8, seq uint32, dist uint16, sec float64) wire.Packet {
	return wire.Packet{Lane: lane, RaceID: 1, Seq: seq, Payload: wire.Finish{DistanceM: dist, Elapsed: secs(sec)}}
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// laneRace is a 50m race for one lane: start, 25m split, finish.
func laneRace(lane uint8, s25, s50 float64) []wire.Packet {
	return []wire.Packet{split(lane, 1, 25, s25), finish(lane, 2, 50, s50)}
}

func TestApplyStartAndDuplicates(t *testing.T) {
	tr := NewTracker()
	start := wire.Packet{RaceID: 1, Seq: 1, Payload: wire.Start{StartTS: 99, Event: "50 free"}}

	if r, res := tr.Apply(start); res != Started || r.Event != "50 free" || r.StartTS != 99 {
		t.Fatalf("first START: res=%v race=%+v", res, r)
	}
	if _, res := tr.Apply(start); res != Duplicate {
		t.Fatalf("repeated START: got %v, want Duplicate", res)
	}

	p := split(3, 1, 25, 12.0)
	if _, res := tr.Apply(p); res != Touched {
		t.Fatalf("first split: got %v", res)
	}
	if _, res := tr.Apply(p); res != Duplicate {
		t.Fatalf("repeated split: got %v, want Duplicate", res)
	}
	if n := len(tr.Race(1).Lanes[3].Touches); n != 1 {
		t.Fatalf("duplicate was recorded: %d touches", n)
	}

	if _, res := tr.Apply(wire.Packet{Lane: 3, RaceID: 1, Payload: wire.Heartbeat{LatestSeq: 1}}); res != Ignored {
		t.Fatalf("heartbeat: got %v, want Ignored", res)
	}
}

// Whatever order packets arrive in, and however many duplicates, the final
// state must be identical. This is the property NACK recovery will rely on.
func TestApplyOrderIndependent(t *testing.T) {
	var all []wire.Packet
	all = append(all, wire.Packet{RaceID: 1, Seq: 1, Payload: wire.Start{StartTS: 1, Event: "100 free"}})
	for lane := uint8(1); lane <= 4; lane++ {
		base := 13.0 + float64(lane)*0.3
		all = append(all,
			split(lane, 1, 25, base),
			split(lane, 2, 50, base*2.05),
			split(lane, 3, 75, base*3.15),
			finish(lane, 4, 100, base*4.3),
		)
	}

	want := NewTracker()
	for _, p := range all {
		want.Apply(p)
	}

	rng := rand.New(rand.NewPCG(3, 4))
	for trial := range 50 {
		shuffled := append([]wire.Packet(nil), all...)
		// add random duplicates, then shuffle
		for range 10 {
			shuffled = append(shuffled, all[rng.IntN(len(all))])
		}
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got := NewTracker()
		for _, p := range shuffled {
			got.Apply(p)
		}
		gr, wr := got.Race(1), want.Race(1)
		for lane, wl := range wr.Lanes {
			gl := gr.Lanes[lane]
			if gl.Final != wl.Final || len(gl.Touches) != len(wl.Touches) {
				t.Fatalf("trial %d lane %d: got %+v want %+v", trial, lane, gl, wl)
			}
			for i := range wl.Touches {
				if gl.Touches[i] != wl.Touches[i] {
					t.Fatalf("trial %d lane %d touch %d: got %+v want %+v", trial, lane, i, gl.Touches[i], wl.Touches[i])
				}
			}
		}
		if !gr.Done() {
			t.Fatalf("trial %d: race not done", trial)
		}
	}
}

func TestStandingsAndPlace(t *testing.T) {
	tr := NewTracker()
	for _, p := range laneRace(4, 12.1, 26.50) { // 2nd
		tr.Apply(p)
	}
	for _, p := range laneRace(5, 11.9, 25.80) { // 1st
		tr.Apply(p)
	}
	for _, p := range laneRace(2, 12.5, 26.50) { // tie with lane 4
		tr.Apply(p)
	}
	tr.Apply(split(6, 1, 25, 13.0)) // still swimming
	tr.Apply(split(3, 1, 25, 14.0)) // still swimming, same distance as lane 6

	r := tr.Race(1)
	var order []uint8
	for _, l := range r.Standings() {
		order = append(order, l.Lane)
	}
	want := []uint8{5, 2, 4, 3, 6} // finishers by time (tie → lane order), then racing lanes by lane
	if len(order) != len(want) {
		t.Fatalf("standings %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("standings %v, want %v", order, want)
		}
	}

	for lane, place := range map[uint8]int{5: 1, 4: 2, 2: 2, 6: 0, 9: 0} {
		if got := r.Place(lane); got != place {
			t.Errorf("Place(%d) = %d, want %d", lane, got, place)
		}
	}
	if r.Done() {
		t.Error("Done() with lanes still racing")
	}
}

func TestFormatTime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "0.00",
		secs(9.5):                     "9.50",
		secs(25.819):                  "25.81", // truncated, not rounded
		secs(59.999):                  "59.99",
		secs(60):                      "1:00.00",
		secs(62.34):                   "1:02.34",
		15*time.Minute + secs(30.123): "15:30.12",
		-secs(1.5):                    "-1.50",
	}
	for in, want := range cases {
		if got := FormatTime(in); got != want {
			t.Errorf("FormatTime(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestOutOfOrderSplitsSortedByDistance(t *testing.T) {
	tr := NewTracker()
	tr.Apply(split(1, 3, 75, 40))
	tr.Apply(split(1, 1, 25, 13))
	tr.Apply(split(1, 2, 50, 27))
	l := tr.Race(1).Lanes[1]
	for i, d := range []uint16{25, 50, 75} {
		if l.Touches[i].DistanceM != d {
			t.Fatalf("touches not sorted by distance: %+v", l.Touches)
		}
	}
	if l.Distance() != 75 {
		t.Fatalf("Distance() = %d, want 75", l.Distance())
	}
}
