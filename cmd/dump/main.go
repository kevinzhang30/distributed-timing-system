// Command dump joins the race multicast group and prints every decoded packet,
// like a protocol-aware tcpdump. Useful for debugging every later milestone.
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kevinzhang30/distributed-timing-system/internal/mcast"
	"github.com/kevinzhang30/distributed-timing-system/internal/wire"
)

func main() {
	group := flag.String("group", mcast.DefaultGroup, "multicast group address")
	iface := flag.String("iface", "", "network interface to join on (e.g. en0, lo0); empty = OS default")
	hexdump := flag.Bool("x", false, "also print raw bytes")
	flag.Parse()

	conn, err := mcast.Listen(*group, *iface)
	if err != nil {
		log.Fatalf("listen %s: %v", *group, err)
	}
	defer conn.Close()
	log.Printf("listening on %s", *group)

	buf := make([]byte, 65535) // max UDP payload; anything larger is truncated by the kernel
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		ts := time.Now().Format("15:04:05.000")
		p, err := wire.Unmarshal(buf[:n])
		if err != nil {
			fmt.Printf("%s %-21s  BAD (%d bytes): %v\n", ts, from, n, err)
			continue
		}
		fmt.Printf("%s %-21s  lane=%d race=%d seq=%d %-9s %s\n",
			ts, from, p.Lane, p.RaceID, p.Seq, p.Payload.Type(), describe(p.Payload))
		if *hexdump {
			fmt.Printf("    % x\n", buf[:n])
		}
	}
}

func describe(pl wire.Payload) string {
	switch v := pl.(type) {
	case wire.Start:
		return fmt.Sprintf("event=%q start=%s", v.Event, time.Unix(0, v.StartTS).Format("15:04:05.000"))
	case wire.Split:
		return fmt.Sprintf("%dm %s", v.DistanceM, v.Elapsed)
	case wire.Finish:
		return fmt.Sprintf("%dm %s", v.DistanceM, v.Elapsed)
	case wire.Heartbeat:
		return fmt.Sprintf("latest_seq=%d", v.LatestSeq)
	case wire.Nack:
		return fmt.Sprintf("resend %d..%d", v.FromSeq, v.ToSeq)
	}
	return fmt.Sprintf("%+v", pl)
}
