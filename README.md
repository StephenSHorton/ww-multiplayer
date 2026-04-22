# Wind Waker Multiplayer

![splash](docs/img/splash.png)

Real-time visual multiplayer for The Legend of Zelda: The Wind Waker on Dolphin.
Each player runs `ww.exe` alongside their own Dolphin instance — positions and
skeletal poses are shared over TCP so each side sees the other's real Link
walking around in-game.

## Status

What works today:
- Reading your own Link's position + skeletal pose from a running Dolphin
- Hosting / joining a TCP session and exchanging positions + poses
- **Rendering each other's Link in-game** — your friend's Link walks around
  on Outset (or wherever you are) at their actual world coords, with their
  real animations, at ~50 ms latency on LAN

Known limits:
- Only Outset Island has been heavily tested. Stage / room transitions
  aren't gracefully handled yet (your friend's Link just renders at the
  last known coords if they cross to a different room).
- Local LAN tested. Internet play would work but firewall / NAT traversal
  isn't included.
- Windows only (Dolphin process memory access uses Win32 APIs).

See `docs/06-roadmap.md` for the full feature/known-issue list.

## Quick start (end users)

1. Download `ww.exe` from the [latest release](../../releases).
2. Patch your own legitimate copy of Wind Waker (NTSC-U, game ID `GZLE01`,
   `.iso` or `.ciso` works):
   ```
   ww.exe patch path\to\your-wind-waker.iso
   ```
   This produces `your-wind-waker-multiplayer.iso` next to the input. Your
   original is left untouched. Already-patched ISOs are detected and skipped.
3. Both players: boot the patched ISO in Dolphin and load a save (any save —
   saves don't have to match, and the other player's Link only appears once
   their session connects).
4. Host runs `ww.exe host` and shares the printed LAN IP.
5. Joiner runs `ww.exe join <host-ip>` (you can also pass a custom name:
   `ww.exe join 192.168.1.42 Alice`).
6. You should see each other's Link walking around in-game within a second
   or two. Ctrl+C in either terminal cleanly shuts down and hides Link #2.

For internet play the host just needs a reachable IP (port-forward :25565
or use a VPN / relay); the rest of the flow is identical.

We don't ship a pre-patched ISO for legal reasons (it would be a derivative
of the entire Wind Waker DOL). The patcher contains only our injected code
plus a list of byte-edit records; your vanilla DOL is the source of all
original game-code bytes.

## Requirements

- Windows
- [Dolphin emulator](https://dolphin-emu.org/) — recent stable release
- Your own legitimate copy of The Wind Waker (NTSC-U, game ID `GZLE01`)
- [Go 1.21+](https://go.dev/dl/) — only if building from source

## Building from source

```bash
git clone https://github.com/StephenSHorton/ww-multiplayer.git
cd ww-multiplayer
go build -o ww.exe .
```

The compiled C-side blob is checked in as `internal/inject/blob.go` so plain
Go builds work without the C toolchain. To rebuild the injected C code, see
`SETUP.md` (devkitPPC + Freighter), then run:

```bash
cd inject && python build.py        # rebuilds patched.dol
cd .. && python scripts/extract_blob.py  # regenerates blob.go
```

### Headless / debug commands

```bash
./ww.exe debug         # Print Link's position for 5 seconds (sanity check)
./ww.exe server        # Run a TCP server on :25565 with no UI
./ww.exe fake-client   # Connect a bot that walks in circles
./ww.exe help          # Full command list
```

## Contributing / hacking

- `docs/01-architecture.md` — how the pieces fit together
- `docs/06-roadmap.md` — what's next
- `SETUP.md` — what you need to install if you want to work on the C
  injection side (devkitPPC, Freighter, wit, etc.)

## License

[MIT](LICENSE) — do whatever you want with it, no warranty, not liable.

This project does not include or distribute any Nintendo IP. The patcher
splices our own injected code into your own legitimately-acquired Wind
Waker disc image. You are responsible for the legality of your own ISO
in your jurisdiction.
