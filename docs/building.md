# Requirements and building from source

[← What is left for co-op](coop-remaining.md) · [Docs index](README.md) · [Developer setup →](dev-setup.md)

What you need to play, and how to build the app yourself.

- Windows 10+, or macOS 12+ (Apple Silicon or Intel)
- A recent [Dolphin emulator](https://dolphin-emu.org/). The patched disc
  needs 48 MB of memory, and the app sets this itself every time it
  starts Dolphin
- Your own legitimate copy of The Wind Waker (NTSC-U, game ID `GZLE01`)
- [Go 1.26.2+](https://go.dev/dl/) (the `go` line in `go.mod`) — only if building from source. macOS
  builds also need the Xcode command-line tools (`xcode-select --install`)
  because the Mach VM bridge uses cgo.

## Building from source

```bash
git clone https://github.com/StephenSHorton/ww-multiplayer.git
cd ww-multiplayer
# Windows (run on Windows), with the app window
go build -tags desktop,production -o ww-multiplayer.exe ./cmd/ww-multiplayer
# Windows, cross-compiled from Linux or macOS (no cgo needed, window included)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -o ww-multiplayer.exe ./cmd/ww-multiplayer
# macOS — universal WW Multiplayer.app, zipped (the script passes the tags)
./scripts/build-mac.sh dev
# outputs dist/WW Multiplayer/ (the .app, a ww-multiplayer symlink, the
# setup script, LICENSE) and dist/ww-multiplayer-macos.zip
```

`-tags desktop,production` compiles in the app window (Wails v2,
`internal/gui`). A plain `go build` leaves it out and is the CLI and the
terminal menu only: no arguments then opens the terminal menu, as before.
`ww-multiplayer ui --browser` serves the app's UI to your browser on
`127.0.0.1` instead of a window (development and testing; Ctrl+C stops
it).

Linux isn't a supported player platform yet. `go build ./cmd/ww-multiplayer` and
`go test ./...` work on Linux, and the resulting binary can `patch` an ISO
or run a headless `server`, but anything that reads Dolphin's memory fails
with "dolphin process memory access is not implemented on linux".

The compiled C-side blob is checked in as `internal/inject/blob.go` so plain
Go builds work without the C toolchain. To rebuild the injected C code, see
[dev-setup.md](dev-setup.md) (devkitPPC + Freighter), then run:

```bash
cd inject && python build.py        # rebuilds patched.dol (devkitPPC from $DEVKITPPC)
cd .. && python scripts/extract_blob.py  # regenerates blob.go
go build -o ww-multiplayer.exe ./cmd/ww-multiplayer
./ww-multiplayer.exe patch <vanilla.iso|ciso> <out.iso>   # a disc with the new blob
```

---

[← What is left for co-op](coop-remaining.md) · [Docs index](README.md) · [Developer setup →](dev-setup.md)
