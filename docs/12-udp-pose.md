# 12. UDP pose transport (wind#91, issue #7)

[← The lobby protocol](10-lobby.md) · [Docs index](README.md) · [Co-op that just works →](coop-just-works.md)

The wind#82 netem run (issue #15) showed that packet loss makes a remote Link
stop and then jump. Positions and poses ride the player's TCP connection, so
one lost segment holds back every frame behind it until TCP retransmits it.
Each frame is a full snapshot, so a newer frame makes an older one useless.
This change sends them as UDP datagrams with sequence numbers. The receiver
keeps the newest frame and drops stale ones. Everything else stays on TCP:
the handshake, player list, chat, pings, co-op, enemy sync, projectile events,
and the lobby.

It is behind `WW_UDP=1` (default off) on both the server and the clients.
Without the flag, nothing changes on the wire.

## Negotiation

- The client sets `CapUDP` (hello caps bit 2) when `WW_UDP` is on.
- A server started with `WW_UDP` on also binds a UDP socket. It uses the TCP
  port + 1 (25566 for the default 25565) on the same host, or an
  OS-assigned port if that bind fails. If UDP can't be bound at all, the
  server logs why and serves TCP alone.
  UDP 25565 is never used: LAN discovery's beacons bind it with SO_REUSEADDR
  (internal/lan), and a second socket there would split unicast delivery.
- When a `CapUDP` hello reaches a server with UDP up, the welcome gets a
  tail: `[udpOffset:2 BE][token:8]`. The offset is the UDP port minus the
  TCP port, mod 2^16. Old readers ignore the bytes after the version.
- The client sends UDP to the host it dialed, at the dialed port + offset.
  It never uses an absolute port, so a proxy (`wwdev netem`) or a port
  forward that maps both ports the same way stays in the path.

## Datagrams

Every datagram starts `['W' 'U'][ver=1][kind]`.

| kind | direction | after the header |
| --- | --- | --- |
| 1 FRAME | client → server | `[token:8][seq:4][posLen:1][position + place trailer][pose payload, may be empty]` |
| 1 FRAME | server → client | `[seq:4][senderID:1][posLen:1][position + place trailer][pose payload, may be empty]` |
| 2 PING | client → server | `[token:8][nonce:8]` |
| 3 PONG | server → client | `[nonce:8]` |

- The position and place use the TCP bytes (`Serialize`, `AppendPlace`).
  The pose is `SerializePose`'s payload. One frame carries what the TCP
  path sends as a MsgPosition followed by a MsgPose, so a pose never loses
  its position. The server checks it as it checks the TCP frames:
  `DeserializePosition`, `ParsePlace` and `DeserializePose` with their
  caps.
- A datagram stays under 1200 bytes. A frame that doesn't fit goes over TCP
  that once. In practice that is a raw (unquantized) pose; a quantized
  42-joint pose with position, place and face is about 1.1 KB.
- `seq` is a u32 per client connection, starting at 1 and counting frames
  sent. The server relays the sender's seq unchanged.

## Sequence and drop policy

- **Server ingress:** per sender, a FRAME with seq ≤ the last one taken is
  dropped. If seq has fallen back by more than 4096, the sender restarted
  and the frame is taken.
- **Receiver:** per remote, the newest frame wins. It updates `Position`,
  the pose, `Place` and `LastPose`. A frame that comes late but is at most
  8 behind the newest is still inserted, in seq order, into `Recent`, the
  playout buffer (feed/playout.go), so the playout has one more point
  between its neighbours. Anything older, or a seq already held, is
  dropped.
- `Sample.Seq` is the playout's clock. A UDP frame's sample gets the
  wire seq plus a per-remote offset, so a lost frame leaves a gap and the
  playout interpolates across it. A TCP sample still counts up by one past
  the newest sample. The offset is set again on the first UDP frame after
  a TCP sample, so `Seq` keeps increasing when a remote switches paths.

## Binding, liveness and fallback

- **Bind:** the client sends a PING every 250 ms until a PONG answers, then
  one a second. A PING or FRAME whose token matches a live player binds
  that player's UDP address to its source. If the source changes (a NAT
  rebinding), the newest one wins. The token is checked on every
  datagram, so nobody can send poses under another player's ID. The
  per-second PING also keeps the NAT mapping open for puppet-sync, which
  otherwise sends nothing.
- **Client upstream:** UDP is up while a PONG has come within the last
  3 s. Until then, or after that, `SendFrame` sends over TCP as before.
- **Server downstream:** a receiver gets UDP relays while its PING or FRAME
  has come within the last 3 s. Otherwise it gets the TCP relays as
  before.
- A refused or unreachable UDP socket (ICMP) only marks UDP down. It never
  ends the session.
- Mixing is fine: a UDP sender's frames reach TCP receivers as MsgPosition
  + MsgPose, and a TCP sender's frames reach UDP receivers over TCP.

## Server bookkeeping

A UDP FRAME does the bookkeeping the TCP MsgPosition/MsgPose cases do:
`Position`, `where` (relay scoping), `posed` and `lobby.playersChanged`,
the scope counters, and `relaysTo` (no self-echo). Positions go to every
receiver. The pose goes only where `relayPose` would send it, so a receiver
out of scope gets the FRAME without its pose. With `Wire` set, `[wire]`
counts UDP frames as `udp-pose` / `udp-pos` streams.

## Not in scope

NAT traversal: the host still needs a port forward (now TCP and UDP) or a
VPN (docs/11-self-hosting.md). There is no forward error correction or
redundancy (sending the last two frames per datagram). Proposing default-on
waits for the live and netem record.

---

[← The lobby protocol](10-lobby.md) · [Docs index](README.md) · [Co-op that just works →](coop-just-works.md)
