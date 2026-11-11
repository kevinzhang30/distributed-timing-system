# Build Plan

Two weekends. Weekend 1 ends with a complete, demoable single-node system; weekend 2 makes it distributed. Each milestone ends with something you can run and see.

## Weekend 1 — UDP, reliability, single node

### Sat AM — Wire format + multicast hello world
- [ ] `internal/wire`: header + packet encode/decode with `encoding/binary`
- [ ] Unit tests: round-trip every packet type; reject bad magic / short buffers
- [ ] `cmd/starter` sends `START`; a throwaway listener joins `239.0.0.42:9999` and prints it
- **Done when:** two terminals, one sends, one prints the decoded packet.
- Gotcha: on macOS multicast may need an explicit interface (`net.ListenMulticastUDP("udp4", ifi, addr)`); try `lo0` or `en0`.

### Sat PM — Simulated lanes + basic node
- [ ] `internal/swimmer`: pace model (base pace per 25m ± random variation, slight fade)
- [ ] `cmd/lane --swimmer bot`: wait for `START`, emit `SPLIT` every 25m, `FINISH` at the end
- [ ] `cmd/lane --swimmer manual`: spacebar = touch the wall
- [ ] `cmd/node` (single node, no replication yet): receives packets, prints per-lane results
- **Done when:** a 3-lane 100 free runs and the node prints correct final times.

### Sun AM — Reliability over UDP
- [ ] `--drop 0.2` flag in lanes (randomly skip sends)
- [ ] Lane retransmit buffer + `HEARTBEAT`
- [ ] Node: gap detection, `NACK` with backoff, de-dup
- [ ] Counters: sent / lost / recovered / duplicates
- **Done when:** with `--drop 0.2`, final results match a run with no loss. Write a test that proves it.

### Sun PM — Clock sync + WAL + scoreboard
- [ ] Starter sync responder; lane sync loop (keep lowest-RTT of last 8)
- [ ] `--skew 150ms` flag; verify sync cancels it
- [ ] `internal/wal`: append with length + CRC32, fsync, replay, truncate torn tail
- [ ] Node writes every accepted packet to the WAL; restart replays it
- [ ] TCP framing (`internal/frame`) + `SUBSCRIBE`/`ENTRY` on the client port
- [ ] `cmd/board`: terminal UI (`bubbletea`) with lane bars, splits, placings
- **Done when:** run a race with loss + skew, kill and restart the node, board still shows the full correct result.

## Weekend 2 — Distributed

### Sat AM — Peer connections
- [ ] Node dials every peer on `7000+N`, `HELLO`, auto-reconnect with backoff
- [ ] Read deadlines + 300 ms heartbeats; log peers going up/down
- **Done when:** start 3 nodes, kill one, the others notice within 1.5 s; restart it, they reconnect.

### Sat PM — Leader election
- [ ] Roles, terms, persisted `term`/`voted_for`
- [ ] `VOTE_REQ`/`VOTE_RESP`, id-staggered election timeouts
- [ ] Leader appends `NOOP` on election
- **Done when:** kill the leader repeatedly; exactly one new leader each time, terms only go up.

### Sun AM — Log replication
- [ ] `APPEND` with `prev_index`/`prev_term` consistency check
- [ ] Per-follower `next_index`/`match_index`; back off on reject
- [ ] Majority commit (current-term rule); followers apply `commit_index`
- [ ] Only the leader ingests UDP; new leader NACKs lanes from its last committed seq
- **Done when:** `kill -9` the leader mid-race; final results identical to an undisturbed run.

### Sun PM — Board failover + polish
- [ ] Board handles `NOT_LEADER`, reconnects to the new leader, resubscribes from last index
- [ ] Dim uncommitted / bright committed splits
- [ ] `scripts/demo.sh` to launch everything in tmux panes
- [ ] Record the demo GIF (`vhs` or asciinema) for the README
- **Done when:** the full chaos demo in the README works end to end.

## Stretch goals
- Full Raft (randomized timeouts, log compaction/snapshots)
- Jepsen-style test: random kills + partitions, assert no committed result is ever lost
- Multi-event meet with heat/event results queryable from the board
- Benchmark: splits/sec through the cluster, p99 commit latency
