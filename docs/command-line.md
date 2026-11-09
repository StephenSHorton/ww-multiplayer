# The terminal menu and command line

The same program is also a terminal menu and a command line (details
under [Playing](playing.md)):

```
.\ww-multiplayer.exe patch path\to\your-wind-waker.iso   # Windows
.\ww-multiplayer.exe host [name]
.\ww-multiplayer.exe join <host-ip> [name]
.\ww-multiplayer.exe tui                                 # the terminal menu
```

On macOS, in Terminal, `cd` into the unzipped `WW Multiplayer` folder and
clear the download quarantine once (`xattr -cr .`), then use
`./ww-multiplayer` the same way (it is a link to the app's own binary).
`./scripts/setup-mac-dolphin.sh` does what Prepare Dolphin does
(`--undo` does what Remove the copy does); boot the patched ISO in the
`~/Applications/Dolphin.app` copy.

`patch` writes `your-wind-waker-multiplayer.iso` next to the input and
leaves the original untouched; already-patched ISOs are detected and
skipped.

We don't ship a pre-patched ISO for legal reasons (it would be a derivative
of the entire Wind Waker DOL). The patcher contains only our injected code
plus a list of byte-edit records; your vanilla DOL is the source of all
original game-code bytes.

## Headless and debug commands

```bash
./ww-multiplayer.exe debug         # Print Link's position for 5 seconds (sanity check)
./ww-multiplayer.exe server        # Run a TCP server on :25565 with no UI
./ww-multiplayer.exe fake-client   # Connect a bot that walks in circles
./ww-multiplayer.exe mods          # List the built-in mods and their status
./ww-multiplayer.exe bug-report    # Save the logs as one redacted zip for an issue (--include FILE, --user-dir DIR, --out DIR)
./ww-multiplayer.exe help          # Full command list
```

Developer and research commands (dump, dolphin2, mp-local, the co-op and
tunic diagnostics, probes) live in a separate `wwdev` tool, not shipped in
releases: `go build -o wwdev.exe ./cmd/wwdev` (see `SETUP.md`).

---

[← Playing](playing.md) · [Docs home](README.md) · [Updates →](updates.md)
