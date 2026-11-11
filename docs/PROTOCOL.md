# Protocol Spec

All integers are **big-endian** (network byte order). Times are `int64` nanoseconds.

## 1. Addresses and ports

| What | Address |
|---|---|
| Race multicast group | `239.0.0.42:9999` (administratively scoped, stays on the LAN) |
| Starter clock-sync (unicast UDP) | `starter:9100` |
| Node `N` peer port (TCP, node↔node) | `7000 + N` |
| Node `N` client port (TCP, board→node) | `8000 + N` |

Lanes receive NACKs on the same UDP socket they send from; nodes learn it from the packet's source address.

## 2. UDP race packets (multicast)

### Header (20 bytes)

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 2 | `magic` | `0x5354` — drop anything else |
| 2 | 1 | `version` | `1` |
| 3 | 1 | `type` | see below |
| 4 | 1 | `lane` | `0` = starter |
| 5 | 3 | reserved | zero |
| 8 | 4 | `race_id` | set by starter, increments each race |
| 12 | 4 | `seq` | per (race, lane), starts at 1, +1 per packet (heartbeats excluded) |
| 16 | 4 | `payload_len` | bytes after header |

### Types

| Type | Name | Sender | Payload |
|---|---|---|---|
| `1` | `START` | starter | `start_ts i64` (starter clock), `event_len u8`, `event []byte` |
| `2` | `SPLIT` | lane | `distance_m u16`, `elapsed i64` |
| `3` | `FINISH` | lane | `distance_m u16`, `elapsed i64` |
| `4` | `HEARTBEAT` | lane | `latest_seq u32` (no seq of its own) |
| `5` | `NACK` | leader node → lane (unicast) | `from_seq u32`, `to_seq u32` |

`elapsed` = touch time − start time, both in the **starter's clock** (see §3).

### Reliability rules

1. **Lanes** keep every packet they sent for the current race in a retransmit buffer.
2. **Lanes** send `HEARTBEAT` every 500 ms with their latest seq. This catches *tail loss* (the last packet dropped, so no later packet reveals the gap).
3. **The leader** tracks `next_expected[race][lane]`. On a gap (or a heartbeat with `latest_seq >= next_expected`), it waits 20 ms for reordering, then unicasts `NACK{from, to}`. It retries with backoff (20 → 40 → 80 ms, cap 500 ms) until filled.
4. **Lanes** answer a NACK by resending the buffered packets unicast to the requester.
5. Duplicates (same race, lane, seq) are dropped.
6. **Failover:** a new leader reads its log to find the last committed seq per lane and NACKs everything after it. The lanes' buffers make leader changes lossless.

## 3. Clock sync (unicast UDP, lane ↔ starter)

The starter's clock is the reference. Each lane runs NTP-style sync every 2 s:

```
lane  t1 ──── SYNC_REQ{t1} ────▶ starter  t2
lane  t4 ◀─ SYNC_RESP{t1,t2,t3} ─ starter  t3

offset = ((t2 - t1) + (t3 - t4)) / 2      # starter_clock ≈ lane_clock + offset
rtt    = (t4 - t1) - (t3 - t2)
```

Keep the last 8 samples and use the offset from the sample with the **lowest RTT** (least queueing noise). Packets: `type 10 SYNC_REQ {t1}`, `type 11 SYNC_RESP {t1,t2,t3}`, with the same 20-byte header (race_id/seq = 0).

`--skew 150ms` on a lane adds a fake constant to its local clock; sync should cancel it.

## 4. TCP framing (all TCP connections)

```
┌──────────────┬──────────┬───────────────────┐
│ length u32   │ type u8  │ payload           │
└──────────────┴──────────┴───────────────────┘
  length = 1 + len(payload); reject if > 1 MiB
```

Read with `io.ReadFull` — a single `Read` can return a partial frame. Every connection sets read deadlines; a peer silent for 1.5 s is considered dead.

## 5. Results cluster (node ↔ node)

Leader-based log replication — a deliberately small subset of Raft.

### Log entry

| Field | Type |
|---|---|
| `index` | u64 (1-based, contiguous) |
| `term` | u64 |
| `race_id`, `lane`, `seq` | u32, u8, u32 |
| `kind` | u8 (`START`, `SPLIT`, `FINISH`, `NOOP`) |
| `distance_m`, `elapsed` | u16, i64 |

On disk (`data/node-N/wal.log`): `[len u32][crc32 u32][entry bytes]` per record, `fsync` after each batch. On startup, replay the file; if the last record is truncated or fails its CRC (crash mid-write), truncate it.

### Peer messages

| Type | Name | Payload |
|---|---|---|
| `1` | `HELLO` | `node_id u8` |
| `2` | `APPEND` | `term, leader_id, prev_index, prev_term, commit_index, entries[]` (empty = heartbeat, every 300 ms) |
| `3` | `APPEND_ACK` | `term, success bool, match_index` |
| `4` | `VOTE_REQ` | `term, candidate_id, last_index, last_term` |
| `5` | `VOTE_RESP` | `term, granted bool` |

### Rules

- **Roles:** follower, candidate, leader. Everyone starts as follower.
- **Election:** a follower that hears no `APPEND` for `1500 ms + id × 300 ms` becomes candidate: `term++`, votes for itself, sends `VOTE_REQ`. The id stagger makes lower ids try first and avoids split votes without randomness.
- **Granting a vote:** grant iff (a) haven't voted in this term, and (b) candidate's `(last_term, last_index)` ≥ mine. Persist `term` and `voted_for` to `data/node-N/state` before replying.
- **Winning:** majority (2 of 3) → leader. Immediately append a `NOOP` entry in the new term (this is what lets earlier-term entries commit safely).
- **Replication:** the leader ingests UDP, appends to its WAL, sends `APPEND` to followers. A follower rejects if it doesn't have `prev_index` with `prev_term`; the leader then decrements `next_index` for that follower and retries (this is also how a restarted node catches up).
- **Commit:** an entry is committed once a majority has it **and** it is from the current term. Only committed entries count as official results.
- **Any message with a higher term** → step down to follower and adopt that term.
- Only the leader ingests UDP and sends NACKs; followers ignore race packets.

## 6. Scoreboard ↔ node (client port)

| Type | Name | Direction | Payload |
|---|---|---|---|
| `20` | `SUBSCRIBE` | board → node | `from_index u64` |
| `21` | `ENTRY` | node → board | `entry`, `committed bool` |
| `22` | `COMMIT` | node → board | `commit_index u64` |
| `23` | `NOT_LEADER` | node → board | `leader_id u8` (0 = unknown) |

The board connects to any node. Non-leaders reply `NOT_LEADER`; the board redirects. On disconnect, the board tries the other nodes and resubscribes from its last seen index, so it never misses or double-counts an entry. Uncommitted splits are drawn dim, committed ones bright.
