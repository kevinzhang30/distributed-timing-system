# distributed-timing-system

[![test](https://github.com/kevinzhang30/distributed-timing-system/actions/workflows/test.yml/badge.svg)](https://github.com/kevinzhang30/distributed-timing-system/actions/workflows/test.yml)

A distributed swim meet timing system in Go. Lane timers broadcast splits over UDP multicast; a 3-node results cluster stores them in a replicated write-ahead log over a custom TCP protocol and survives leader failure; a terminal scoreboard follows races live.

No hardware required — timers, swimmers, and scoreboards are all simulated Go processes.

## Status

🚧 **In active development.** The full design is specified in [docs/PROTOCOL.md](docs/PROTOCOL.md); progress is tracked in [docs/PLAN.md](docs/PLAN.md).

| Milestone | Status |
|---|---|
| Binary wire format (all packet types), unit + fuzz tests | ✅ Done |
| UDP multicast transport, `starter`, `dump` packet inspector | ✅ Done |
| Simulated lanes (bot pace model + manual spacebar mode), single results node | ✅ Done |
| NACK-based loss recovery over UDP | 🔨 Next |
| NTP-style clock sync | ⏳ Planned |
| Write-ahead log + TCP framing + terminal scoreboard | ⏳ Planned |
| 3-node replication, leader election, failover | ⏳ Planned |

### Try what works today

Run a race on one machine (each command in its own terminal):

```bash
go run ./cmd/node                                    # results node: live splits + final results table
go run ./cmd/lane --lane 1                           # bot swimmer (random pace ~13-15s per 25m)
go run ./cmd/lane --lane 2 --pace 1s                 # fast bot, for a quick demo
go run ./cmd/lane --lane 3 --swimmer manual          # you: press SPACE at each wall
go run ./cmd/starter --race 1 --event "100 free"     # GO
```

Sample node output (bots at ~1s/25m):

```
RACE 1 · 100 free
────────────────────────────────────────────
PLACE  LANE   TIME       SPLITS
1st    3      3.91       0.86 0.97 1.02 1.05
2nd    1      3.96       0.89 0.99 1.01 1.05
3rd    2      4.17       0.94 1.04 1.07 1.10
────────────────────────────────────────────
duplicates dropped so far: 2
```

`go run ./cmd/dump -x` shows every packet on the wire, with raw bytes.

```bash
# tests, including a fuzz test of the packet decoder
go test ./...
go test ./internal/wire -run '^$' -fuzz FuzzUnmarshal -fuzztime 30s
```

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
| `dump` | Debug tool: prints every decoded multicast packet (`-x` for raw bytes) | UDP multicast receive |

## Concepts demonstrated

- **UDP & IP multicast** — group membership, connectionless broadcast to many receivers
- **Reliability over UDP** — per-lane sequence numbers, gap detection, NACK-based retransmission, de-duplication
- **Clock synchronization** — NTP-style offset estimation so splits from different machines are comparable
- **Custom TCP protocol** — length-prefixed binary framing, partial reads, deadlines, heartbeats, reconnects
- **Distributed storage** — write-ahead log, leader-based replication, majority commit, failover, follower catch-up
- **Concurrency** — goroutine-per-connection, channels, `context` cancellation

## Target demo

When complete (see [Status](#status)), the full system runs like this:

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
go run ./cmd/starter --event "100 free" --race 1
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
