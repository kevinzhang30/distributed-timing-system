# distributed-timing-system

A distributed swim meet timing system in Go. Lane timers broadcast splits over UDP multicast; a 3-node results cluster stores them in a replicated write-ahead log over a custom TCP protocol and survives leader failure; a terminal scoreboard follows races live.

No hardware required — timers, swimmers, and scoreboards are all simulated Go processes.

## Architecture

```
 lane timers ──UDP multicast──▶ results cluster (3 Go nodes) ──TCP──▶ terminal scoreboard
 (simulated)                      leader + 2 followers                (Go TUI)
                                  replicate over TCP
                                  write-ahead log on disk
```

| Component | Role | Networking |
|---|---|---|
| `starter` | Fires the race start signal | UDP multicast send |
| `lane` | Simulates one lane's touchpad: emits 25m splits and finish touch | UDP multicast send, NACK handling, clock sync |
| `node` | Results cluster member: ingests splits, appends to WAL, replicates | UDP multicast receive, TCP server/client, heartbeats |
| `board` | Terminal scoreboard: shows the live race and results | TCP client, leader discovery, reconnect |

## Concepts demonstrated

- **UDP & IP multicast** — group membership, connectionless broadcast to many receivers
- **Reliability over UDP** — per-lane sequence numbers, gap detection, NACK-based retransmission, de-duplication
- **Clock synchronization** — NTP-style offset estimation so splits from different machines are comparable
- **Custom TCP protocol** — length-prefixed binary framing, partial reads, deadlines, heartbeats, reconnects
- **Distributed storage** — write-ahead log, leader-based replication, majority commit, failover, follower catch-up
- **Concurrency** — goroutine-per-connection, channels, `context` cancellation

## Demo

```bash
# terminal 1-3: results cluster
go run ./cmd/node --id 1 --peers 2,3
go run ./cmd/node --id 2 --peers 1,3
go run ./cmd/node --id 3 --peers 1,2

# terminal 4: scoreboard
go run ./cmd/board

# terminals 5-7: lanes (bot swimmers, or --swimmer manual to press space at the wall)
go run ./cmd/lane --lane 1 --swimmer bot
go run ./cmd/lane --lane 2 --swimmer bot
go run ./cmd/lane --lane 3 --swimmer manual

# start the race
go run ./cmd/starter --event "100 free" --heat 1
```

Then break things:

```bash
go run ./cmd/lane --lane 2 --drop 0.2 --skew 150ms   # 20% packet loss + clock skew
kill -9 <leader pid>                                   # leader fails mid-race; a follower takes over
go run ./cmd/node --id 1 --peers 2,3                   # restarted node catches up from the log
```

Final results stay correct through all of it.

## Docs

- [Protocol spec](docs/PROTOCOL.md) — packet formats, TCP framing, replication rules
- [Build plan](docs/PLAN.md) — two-weekend milestone plan
