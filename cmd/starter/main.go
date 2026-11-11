// Command starter fires the race start signal over UDP multicast.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/kevinzhang30/distributed-timing-system/internal/mcast"
	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

func main() {
	group := flag.String("group", mcast.DefaultGroup, "multicast group address")
	race := flag.Uint("race", 1, "race id")
	event := flag.String("event", "100 free", "event name")
	repeat := flag.Int("repeat", 3, "send START this many times (same seq; receivers de-dup)")
	flag.Parse()

	conn, err := mcast.Dial(*group)
	if err != nil {
		log.Fatalf("dial %s: %v", *group, err)
	}
	defer conn.Close()

	// The start timestamp is taken once: every copy carries the same instant,
	// so a duplicate is byte-identical and harmless.
	pkt := wire.Packet{
		Lane:    0,
		RaceID:  uint32(*race),
		Seq:     1,
		Payload: wire.Start{StartTS: time.Now().UnixNano(), Event: *event},
	}
	b, err := wire.Marshal(pkt)
	if err != nil {
		log.Fatal(err)
	}

	// START has no NACK path yet (lanes NACK the leader, not the reverse), so
	// cheap redundancy covers a single lost packet until the starter gets a
	// retransmit buffer of its own.
	for i := 0; i < *repeat; i++ {
		if _, err := conn.Write(b); err != nil {
			log.Fatalf("send: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	log.Printf("START race=%d event=%q sent x%d to %s (%d bytes)", *race, *event, *repeat, *group, len(b))
}
