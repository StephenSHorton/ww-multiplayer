# Headless and debug commands

[← Releases, signing and the website](releasing.md) · [Docs index](README.md) · [Testing →](TESTING.md)

The commands that run without a window, for testing and bug reports.

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
releases: `go build -o wwdev.exe ./cmd/wwdev` (see [dev-setup.md](dev-setup.md)).

---

[← Releases, signing and the website](releasing.md) · [Docs index](README.md) · [Testing →](TESTING.md)
