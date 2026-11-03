# Server stress test: 4 and 8 synthetic players

Issues #13 (server stress beyond 2 clients) and #11 (N>2 players), wind#81.
The server and the co-op hub were run under 4 and 8 synthetic players for
32 minutes each. No Dolphin was used. Nothing broke: no drop, no panic,
no reconnect and no send error. The host's memory and goroutines stayed
flat, and every relay's p99 stayed under 7 ms on loopback.

## The command

```bash
go build -o wwdev ./cmd/wwdev
./wwdev stress --players 8 --duration 30m --out report.json   # progress on stderr every --every (30s)
./wwdev stress --players 4 --duration 30m --no-coop           # player connections only
./wwdev stress --players 8 --addr host:port                   # load a running server instead (no co-op unless it hosts one)
```

`stress` starts `wwdev stress-host` as a child process, so the host's CPU
and memory are measured apart from the players. The child runs a host's
server with its co-op hub over a fresh world in a temporary directory, as
the app hosts a session. It prints its own CPU time, peak RSS, Go heap,
goroutines, player connections and relay scoping counters every 5 s. The
exit code is 1 if the host exited early, or its log has a panic or (in a
`-race` build) a data race.

Each synthetic player has the three connections a real one has
(cmd/wwdev/stress.go):

- **pose**, as broadcast-pose: a 42-joint pose frame (2 KB) at 60 Hz. The
  frame carries its send time.
- **sync**, as puppet-sync: a position with its place trailer at 20 Hz,
  plus a chat line every 30 s. It receives the other players' poses, and
  each is timed from its send time (`network.Client.OnPose`).
- **co-op**: CO_HELLO bound to the world, then WORLD and PRESENCE.
  - It sends a SCOPED KILL bit every second. Each kill is timed to the
    other players in the same room when they read it. Kills replayed from
    the room's store on entry are counted separately.
  - Chest CLAIMs: an ask with 1 Hz renewals for 3 s, then a release. A
    quarter of the time the chest's bit is first sent as a GAIN.
    There are six chests per table, so players in one room contend.

Players hop to a random stage of four and a room of four every 20-60 s,
and send PRESENCE as they hop. A connection that drops is counted and
dialled again.

## Results (2026-11-02, branch wind81-stress on main ed33b6b, MacBook Pro M5 Max, loopback)

The two runs overlapped on the same Mac. Each had its own host process
and port.

| | 4 players (12 connections) | 8 players (24 connections) |
|---|---|---|
| Duration | 32 min | 32 min |
| Pose relay p50 / p99 / max | 0.1 / 2.2 / 25.5 ms (775k frames) | 0.2 / 2.4 / 34.6 ms (2.55M frames) |
| KILL relay p50 / p99 / max | 0.4 / 5.7 / 17.9 ms (4,944) | 0.5 / 5.4 / 28.4 ms (23,525) |
| CLAIM ask to verdict p50 / p99 | 0.4 / 6.7 ms (3,872) | 0.4 / 5.4 ms (7,744) |
| Heartbeat RTT p99 | 1.8 ms | 1.8 ms |
| Host CPU | 44 s, 0.023 cores | 126 s, 0.065 cores |
| Host peak RSS | 23.1 MB | 25.0 MB |
| Host Go heap first / last / max | 2.3 / 4.5 / 4.7 MB | 3.4 / 3.2 / 4.7 MB |
| Host goroutines | 37, flat | 69, flat |
| Upload per player | 120 KB/s | 120 KB/s |
| Pose download per player | 199 KB/s | 328 KB/s |
| Pose deliveries to puppet-sync held back by stage scoping | 58% | 65% |
| Drops, reconnects, send errors, panics | 0 | 0 |
| Stage hops / chats / chests opened | 192 / 249 / 280 | 383 / 496 / 506 |
| Claim grants / denies | 1,208 / 34 | 1,331 / 117 |

The raw reports are `docs/stress/2026-11-02-p4.json` and `-p8.json`.

What the numbers say:

- **No races or exhaustion.** Goroutines stayed at about three per
  connection the whole run. The heap went up and down with no growth, and
  there was no file-descriptor or port trouble with 24 connections. A `-race`
  build of wwdev (`go build -race -o wwdev ./cmd/wwdev`) ran 8 players
  for 90 s with no data race in the host or the players. The report's
  `data_races` counts any race in the host's log, and the exit code is 1
  if there is one.
- **The relay keeps its rate.** The server's relay count matches the
  poses the players read. Latency in the last 10 minutes was lower than
  in the first, so nothing builds up. As in the real game, each
  puppet-sync also gets its own player's pose back (puppet-sync drops it
  by name), so that echo is in the counts and the latency.
- **Relay scoping matters most for bandwidth.** A pose never goes to a
  pose connection (broadcasters draw no remotes). Of the deliveries to the
  puppet-sync connections, 65% were held back at 8 players because the
  receiver was in another stage. Without scoping each puppet-sync would
  download about 8 x 120 KB/s = 960 KB/s, against the 328 KB/s measured.
  A WAN host's upload is therefore the limit to watch, not the server's
  CPU. With all 8 players in one stage, a host would send about 7.7 MB/s.
- **Co-op scope works.** KILLs reached only the players in the same room,
  and a player entering a room got its stored kills. Chest claims
  contended correctly: denials went to the second asker, and
  `consumed` came back once a chest's GAIN landed.

Limits of the driver, not of the server:

- The 8-player run skipped 1,358 kills because a player ran out of its
  share of a room's 512 kill bits. It still sent 14,002.
- Loopback hides network latency. These numbers are the server's own
  cost. The relay over a real network adds the link's RTT.
- `claim_consumed` is counted per CLAIM answer, so a renewal of a consumed
  chest counts again.

## The live check

The card asked for a 3-Dolphin spot check if rig slots were free. It was
not repeated here: wind#16 already ran three and four remotes live on the
Mac rig (R3-1..4, the relay scoping checks RS1-RS7, docs/TESTING.md "Three
remotes and the soft reset"). The synthetic run adds the 4- and 8-player
load that no rig can reach.
