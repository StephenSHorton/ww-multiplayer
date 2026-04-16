# Wind Waker Multiplayer

![splash](docs/img/splash.png)

Real-time visual multiplayer for The Legend of Zelda: The Wind Waker on Dolphin.
Each player runs the TUI alongside their own Dolphin instance — positions are
shared over TCP so (eventually) you can see each other's Link sailing around.

## Status: work in progress

What works today:
- Reading your own Link's position from a running Dolphin in real time
- Hosting / joining a TCP session and exchanging positions with other players

What does **not** work yet:
- Actually rendering the other player's Link in your game world. Spawning a
  second player actor at runtime is blocked on a Dolphin/GameCube memory
  layout problem — see `docs/05-known-issues.md` and `docs/06-roadmap.md`.

So right now this is more of a "shared map dot" tool than a true multiplayer
experience. Star the repo if you want to follow along.

## Requirements

- Windows (only platform tested so far)
- [Dolphin emulator](https://dolphin-emu.org/) with The Wind Waker (NTSC-U,
  game ID `GZLE01`) running and a save loaded
- [Go 1.21+](https://go.dev/dl/) to build the client

## Install & run

```bash
git clone https://github.com/StephenSHorton/ww-multiplayer.git
cd ww-multiplayer
go build -o ww.exe .
./ww.exe
```

The TUI walks you through host vs join, IP, and player name.

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

TBD.
