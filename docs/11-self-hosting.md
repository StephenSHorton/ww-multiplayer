# Self-hosting a dedicated server

**Status:** built on branch `ww-online-ownerkey` (ww-online review ideas 7 and 22, handoff `archive/ww-online-review/REPORT.md`). Not yet run on a real VPS or in a real container; the live checks are in docs/TESTING.md "Dedicated server".

Usually the host's own app is the server: **Host a session** starts one on the host's machine, and friends join its address or join code. A **dedicated server** is the same server with no game attached, `ww-multiplayer server`, on an always-on machine (a VPS, a home server, a Raspberry Pi). It needs no disc image, no Dolphin and almost no resources.

> **You probably don't need this.** If your friends can reach your machine (the same home network, a shared Tailscale network, or a forwarded port), just press Host. A dedicated server is worth it when the session should stay up without the host, or when nobody's own machine can be reached.

## 1. What a dedicated server does and doesn't do

- **The transport is raw TCP**, one port (25565 by default), the same protocol as an app-hosted session. No HTTP, no WebSockets, no UDP. Anything that forwards a TCP port works; an HTTP reverse proxy (Caddy, nginx's `http` block) does not.
- **Plain multiplayer works**: every player's Link in everyone's game, chat, tunic colors, and the enemy mirror, which follows the lobby host's game (the host's app runs the enemy host).
- **The app's lobby needs an owner key** (§4). Without one the lobby on a dedicated server has no host: nobody can press Start. CLI players (`join`) play as before either way.
- **No co-op.** A co-op world lives in the hosting app's own server process. A dedicated server refuses co-op in the lobby's settings (`no_coop`, docs/10-lobby.md §3 "Owner key"), and the keyed host plays plain multiplayer.
- **No LAN discovery or join code of its own.** The server prints no join code; a player can still make one from its IPv4 address and port, or type the address.
- **Everything lives in memory.** A restart ends the lobby; the apps reconnect to the new one and join again.

## 2. Where to run it: Tailscale first

**Tailscale** (or another WireGuard mesh: Headscale, ZeroTier, Radmin VPN) is the easiest and the safest place:

1. Install Tailscale on the server machine and on every player's machine, all in one tailnet (or share the server machine with your friends' tailnets).
2. Run `ww-multiplayer server` on the server machine.
3. Players join its Tailscale IP (`100.x.y.z`) or its MagicDNS name, port 25565.

Nothing is exposed to the internet and no router changes are needed. Tailscale encrypts the traffic, which matters here: our protocol is not encrypted, so the owner key (§4) and the chat cross the network in the clear otherwise.

## 3. Port forwarding

Without a VPN the server's TCP port must be reachable from the internet:

- **A home server:** forward **TCP 25565** on the router to the machine. Only TCP; nothing uses UDP.
- **A VPS:** allow inbound TCP 25565 in the provider's firewall (its security group or cloud firewall) and in the machine's own (`ufw allow 25565/tcp`).
- **Windows:** the first start asks whether `ww-multiplayer.exe` may accept connections; allow it on the networks players come from.
- **Docker and ufw:** a published port (`-p 25565:25565`) bypasses ufw. To keep it to one address, publish on it: `-p 100.101.102.103:25565:25565` (a Tailscale IP).

Players then join the public address (or a join code made from it). Everything, the owner key included, crosses the internet unencrypted: prefer §2 when you can.

## 4. Running it, and the owner key

```
ww-multiplayer server [--port N] [--owner-key K]
```

- `--port N` (default 25565).
- `--owner-key K`, or the environment variable **`WW_OWNER_KEY`** (preferred: a flag shows in the machine's process list). 8 to 256 bytes; pick a long random one (`openssl rand -hex 16`). A key that is too short or too long stops the server at start with a message.
- Ctrl+C or SIGTERM (`docker stop`, `systemctl stop`) stops it cleanly; connected apps reconnect to the next one.

**The owner key** makes one player the lobby's host on a server none of them runs. Whoever should run the session pastes the key into the app's Join form, under **Advanced: dedicated server owner key**, and presses Join. That player becomes the host (Start, settings, kick) wherever they connect from; everyone else leaves the box empty. The app sends the key only to the address on that form and never saves it. A wrong key joins as a player and says so. Five wrong keys from one address lock it out for 10 minutes. The host role survives a reconnect. The details are in docs/10-lobby.md §3 "Owner key".

The key keeps a friend from taking the session by accident and slows down a stranger guessing it. It is not a password for the server: anyone who can reach the port can join as a player.

## 5. Health check: `status`

```
ww-multiplayer status <host[:port]|join-code> [--json] [--timeout D]
```

asks a server whether it is up without joining (the status probe: one `S` frame on the game port, one JSON answer, docs/10-lobby.md §1). It prints the server's version, protocol, player and lobby counts, its phase, whether it has an owner key, and its uptime, and exits:

| Exit | Meaning |
|---|---|
| 0 | a ww-multiplayer server of this build's protocol answered |
| 1 | unreachable: nothing listening, a firewall, a bad name, a timeout; or a usage error |
| 3 | something answered, but not a server this build can play with: an older ww-multiplayer (from before the probe), another protocol, or another program |

The server does not log a probe, so a health check every few seconds stays out of its log. The probe rides the game port on purpose: a separate HTTP port would be one more port to forward, and it could answer while the game port is the one that's broken.

## 6. Docker

The repo's `Dockerfile` builds the server alone (a static Go binary on a distroless image, user `nonroot`, a `HEALTHCHECK` that runs `status` against itself). The build context is an allowlist of the Go sources (`.dockerignore`), so no disc image or save can end up in it.

```
docker build -t ww-multiplayer-server --build-arg VERSION=v0.5.0 .
docker run -d --name ww-mp --restart unless-stopped -p 25565:25565 \
  -e WW_OWNER_KEY="$(openssl rand -hex 16)" ww-multiplayer-server
docker logs ww-mp          # the key isn't printed; read it from your shell or a .env file
docker ps                  # healthy once the probe answers
```

- **VERSION** must be the players' release tag. Players need the same protocol; `status` from a player's machine says whether it matches.
- **Keep the container port 25565**: the health check asks there. For another outside port change only the left side: `-p 7000:25565`.
- The image has no shell. `docker exec ww-mp /ww-multiplayer status 127.0.0.1:25565` runs a probe by hand.

## 7. Updating

Replace the binary (or rebuild the image with the new `VERSION`) when your players update: a server of another protocol turns them away at the hello with the version dialog. After the restart, apps reconnect on their own; the lobby starts empty, so the key holder joins again and presses Start.
