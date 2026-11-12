// Command lane simulates one lane's touchpad. It waits for START on the race
// multicast group, then sends a SPLIT at every 25m wall and a FINISH at the end.
//
//	--swimmer bot     a simulated swimmer races at --pace
//	--swimmer manual  press SPACE to touch the wall yourself (q to quit)
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/kevinzhang30/distributed-timing-system/internal/mcast"
	"github.com/kevinzhang30/distributed-timing-system/internal/race"
	"github.com/kevinzhang30/distributed-timing-system/internal/swimmer"
	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

// logf always ends lines with \r\n so output stays aligned when the terminal
// is in raw mode (manual swimmer), where \n alone doesn't return the cursor.
func logf(format string, args ...any) {
	fmt.Printf(time.Now().Format("15:04:05.000")+" "+format+"\r\n", args...)
}

func fatalf(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}

func main() {
	group := flag.String("group", mcast.DefaultGroup, "multicast group address")
	iface := flag.String("iface", "", "interface to receive multicast on (empty = OS default)")
	laneNum := flag.Uint("lane", 0, "lane number (1-255)")
	mode := flag.String("swimmer", "bot", "bot or manual")
	pace := flag.Duration("pace", 0, "bot: time per 25m (0 = random between 13s and 15s)")
	seed := flag.Uint64("seed", 0, "bot: random seed (0 = random)")
	flag.Parse()

	if *laneNum < 1 || *laneNum > 255 {
		fatalf("--lane must be 1-255 (0 is the starter)")
	}
	if *mode != "bot" && *mode != "manual" {
		fatalf("--swimmer must be bot or manual")
	}
	if *seed == 0 {
		*seed = rand.Uint64()
	}
	rng := rand.New(rand.NewPCG(*seed, uint64(*laneNum)))
	if *pace == 0 {
		*pace = 13*time.Second + time.Duration(rng.Int64N(int64(2*time.Second)))
	}

	recv, err := mcast.Listen(*group, *iface)
	if err != nil {
		fatalf("listen %s: %v", *group, err)
	}
	send, err := mcast.Dial(*group)
	if err != nil {
		fatalf("dial %s: %v", *group, err)
	}

	l := &lane{num: uint8(*laneNum), conn: send, rng: rng, profile: swimmer.DefaultProfile(*pace)}

	if *mode == "manual" {
		l.touches = make(chan struct{})
		restore := readKeys(l.touches)
		defer restore()
		logf("lane %d ready (manual): SPACE = touch the wall, q = quit", l.num)
	} else {
		logf("lane %d ready (bot, pace %s/25m)", l.num, race.FormatTime(*pace))
	}

	starts := make(chan wire.Packet)
	go readStarts(recv, starts)

	// One race at a time. A START for a new race id cancels the race in
	// progress (a false start / restart); repeats of the same START are ignored.
	var current uint32
	cancel := context.CancelFunc(func() {})
	for p := range starts {
		if p.RaceID == current {
			continue
		}
		cancel()
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		current = p.RaceID
		go l.run(ctx, p.RaceID, p.Payload.(wire.Start))
	}
}

type lane struct {
	num     uint8
	conn    *net.UDPConn
	rng     *rand.Rand
	profile swimmer.Profile
	touches chan struct{} // non-nil in manual mode
}

// readStarts forwards every START packet; everything else on the group
// (other lanes' splits) is ignored.
func readStarts(conn *net.UDPConn, out chan<- wire.Packet) {
	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			fatalf("read: %v", err)
		}
		p, err := wire.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		if _, ok := p.Payload.(wire.Start); ok {
			out <- p
		}
	}
}

func (l *lane) run(ctx context.Context, raceID uint32, st wire.Start) {
	dist, err := swimmer.ParseDistance(st.Event)
	if err != nil {
		logf("race %d: %v — ignoring", raceID, err)
		return
	}
	start := time.Unix(0, st.StartTS)
	logf("race %d: %s — GO", raceID, st.Event)

	// TODO(clock sync): elapsed is measured against the starter's timestamp
	// using this machine's clock, so it's only exact when both run on one
	// host. The clock-sync milestone adds the measured offset here.
	elapsed := func() time.Duration { return time.Since(start) }

	var schedule []time.Duration
	if l.touches == nil {
		if schedule, err = l.profile.Splits(dist, l.rng); err != nil {
			logf("race %d: %v", raceID, err)
			return
		}
	}

	var seq uint32
	lengths := dist / swimmer.LengthM
	for i := range lengths {
		if l.touches == nil {
			// Wait until this wall touch is due. Waiting on an absolute time
			// (start + split) rather than sleeping per length avoids drift.
			t := time.NewTimer(time.Until(start.Add(schedule[i])))
			select {
			case <-ctx.Done():
				t.Stop()
				logf("race %d: cancelled", raceID)
				return
			case <-t.C:
			}
		} else {
			select {
			case <-ctx.Done():
				logf("race %d: cancelled", raceID)
				return
			case <-l.touches:
			}
		}

		d := uint16((i + 1) * swimmer.LengthM)
		e := elapsed()
		seq++
		var pl wire.Payload = wire.Split{DistanceM: d, Elapsed: e}
		label := "split "
		if i == lengths-1 {
			pl, label = wire.Finish{DistanceM: d, Elapsed: e}, "FINISH"
		}
		if err := l.send(raceID, seq, pl); err != nil {
			logf("race %d: send %dm: %v", raceID, d, err)
			continue
		}
		logf("race %d: %s %4dm  %s", raceID, label, d, race.FormatTime(e))
	}
}

func (l *lane) send(raceID, seq uint32, pl wire.Payload) error {
	b, err := wire.Marshal(wire.Packet{Lane: l.num, RaceID: raceID, Seq: seq, Payload: pl})
	if err != nil {
		return err
	}
	_, err = l.conn.Write(b)
	return err
}

// readKeys puts the terminal in raw mode so single keypresses arrive without
// Enter. SPACE sends a touch (dropped if no race is running); q or Ctrl-C quits.
func readKeys(touches chan<- struct{}) (restore func()) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fatalf("--swimmer manual needs an interactive terminal")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		fatalf("raw mode: %v", err)
	}
	restore = func() { _ = term.Restore(fd, old) }

	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				restore()
				os.Exit(0)
			}
			switch buf[0] {
			case ' ':
				select {
				case touches <- struct{}{}:
				default: // no race running; ignore
				}
			case 'q', 3: // 3 = Ctrl-C, which raw mode delivers as a byte instead of a signal
				restore()
				fmt.Print("\r\n")
				os.Exit(0)
			}
		}
	}()
	return restore
}
