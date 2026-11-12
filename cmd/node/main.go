// Command node is a results node. In this milestone it is a single node: it
// joins the race multicast group, records every split and finish (dropping
// duplicates), and prints live results. Replication arrives in weekend 2.
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kevinzhang30/distributed-timing-system/internal/mcast"
	"github.com/kevinzhang30/distributed-timing-system/internal/race"
	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

func main() {
	group := flag.String("group", mcast.DefaultGroup, "multicast group address")
	iface := flag.String("iface", "", "interface to receive multicast on (empty = OS default)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	conn, err := mcast.Listen(*group, *iface)
	if err != nil {
		log.Fatalf("listen %s: %v", *group, err)
	}
	log.Printf("node listening on %s", *group)

	tracker := race.NewTracker()
	reported := map[uint32]bool{} // races whose final results have been printed
	var bad, dups int

	buf := make([]byte, 65535)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		p, err := wire.Unmarshal(buf[:n])
		if err != nil {
			bad++
			log.Printf("dropped malformed packet from %s: %v (total %d)", from, err, bad)
			continue
		}

		r, res := tracker.Apply(p)
		switch res {
		case race.Started:
			log.Printf("race %d: %s — started", r.ID, r.Event)
		case race.Touched:
			s := p.Payload.(wire.Split)
			log.Printf("race %d: lane %d  %4dm  %s", r.ID, p.Lane, s.DistanceM, race.FormatTime(s.Elapsed))
		case race.Finished:
			f := p.Payload.(wire.Finish)
			log.Printf("race %d: lane %d  %4dm  %s  FINISH (%s)", r.ID, p.Lane, f.DistanceM,
				race.FormatTime(f.Elapsed), ordinal(r.Place(p.Lane)))
		case race.Duplicate:
			dups++
		}

		if r != nil && r.Done() && !reported[r.ID] {
			reported[r.ID] = true
			fmt.Print(resultsTable(r, dups))
		}
	}
}

func resultsTable(r *race.Race, dups int) string {
	var b strings.Builder
	title := fmt.Sprintf("RACE %d · %s", r.ID, r.Event)
	if r.Event == "" {
		title += "(event unknown: START not received)"
	}
	fmt.Fprintf(&b, "\n%s\n%s\n", title, strings.Repeat("─", 44))
	fmt.Fprintf(&b, "%-6s %-6s %-10s %s\n", "PLACE", "LANE", "TIME", "SPLITS")
	for _, l := range r.Standings() {
		var splits []string
		var prev time.Duration
		for _, t := range l.Touches {
			splits = append(splits, race.FormatTime(t.Elapsed-prev))
			prev = t.Elapsed
		}
		fmt.Fprintf(&b, "%-6s %-6d %-10s %s\n", ordinal(r.Place(l.Lane)), l.Lane, race.FormatTime(l.Final), strings.Join(splits, " "))
	}
	fmt.Fprintf(&b, "%s\nduplicates dropped so far: %d\n\n", strings.Repeat("─", 44), dups)
	return b.String()
}

func ordinal(n int) string {
	if n <= 0 {
		return "-"
	}
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
