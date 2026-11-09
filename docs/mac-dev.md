# Developing on macOS

How to build, check and test ww-multiplayer on a Mac. SETUP.md covers the
external pieces (disc, Dolphin, devkitPPC). This page covers what is
different on macOS. Running Dolphin rigs on a Mac (patching the disc,
two-Dolphin loopback, how many Dolphins fit) is in docs/mac-rig.md.

## Verified desk (2026-11-01)

| | |
|---|---|
| Machine | Apple M5 Max, 18 cores, 128 GB, macOS 26.6.2 (25G83) |
| Go | go1.27.1 darwin/arm64 (go.mod asks for 1.26.2 or newer) |
| Tools | Xcode (cgo), git 2.54, gh 2.101, python 3.14, Homebrew |
| Dolphin | 2603-342 in /Applications, plus the re-signed copy in ~/Applications |
| Commit | main c1278b3 |

Every line of CLAUDE.md's "Checks (run locally - no CI)" block passed on
this machine, including the `-race` run and the macOS-only lines.

## Toolchain

- Go from go.mod's `go` line or newer (`brew install go`).
- The Xcode command-line tools: the macOS build uses cgo (memory access,
  the Wails window), and so does `go test -race`.
- `gh`, signed in to an account that can push to the repo.
- devkitPPC and `freighter` only if you rebuild the injected C/REL blob
  (`inject/`). The Go side, `patch`, and every test use the committed
  `internal/inject/blob.go`, so most work never needs them. devkitPro's
  macOS installer needs an admin password.

## One clone, many worktrees

When several agents share one Mac, keep one clone at
`~/projects/ww-multiplayer` and give each piece of work its own worktree:

```bash
git -C ~/projects/ww-multiplayer fetch
git -C ~/projects/ww-multiplayer worktree add ~/projects/ww-mp-<card> -b <branch> origin/main
```

They all share Go's default build cache (`~/Library/Caches/go-build`),
so you don't need to set `GOCACHE`. Remove the worktree after the merge.

## Checks

Run CLAUDE.md's whole block, including the macOS lines:

```bash
go vet ./... && go vet -tags desktop,production ./... && go test -count=1 ./... && go build -o /tmp/wwdev ./cmd/wwdev
./scripts/build-mac.sh local && go run ./tools/check_game_files dist/ww-multiplayer-macos.zip
```

`build-mac.sh` writes `dist/WW Multiplayer/WW Multiplayer.app` (ad-hoc
signed with `cs.debugger`, which lets it read Dolphin's memory) and
`dist/ww-multiplayer-macos.zip`, the same way a release does.

**grep gotcha (agents in Claude Code):** in a Claude Code shell, `grep`
can be a shell function that runs ugrep with `-I`, and `-I` skips binary
files. The manifest line,
`grep -q 'requestedExecutionLevel level="asInvoker"' /tmp/ww-multiplayer.exe`,
then fails even though the exe is fine. Run that line with
`command grep` (or `/usr/bin/grep`).

## Dolphin on macOS

Stock Dolphin is signed with the hardened runtime, and macOS won't let
another process read its memory. Run `scripts/setup-mac-dolphin.sh` once
after you install or update Dolphin. It copies Dolphin to
`~/Applications/Dolphin.app` and re-signs it without the hardened runtime
(bundle id `org.dolphin-emu.dolphin-unhardened`). Use that copy from then
on (the app's Setup has the same step: Prepare Dolphin). To undo it,
`scripts/setup-mac-dolphin.sh --undo` (or Remove the copy in Setup)
unregisters the copy from LaunchServices and deletes it; the original in
`/Applications` is never touched. Both refuse a `~/Applications/Dolphin.app`
that is a stock Dolphin (hardened, with Dolphin's own bundle id) rather
than a prepared copy. To check it:

```bash
codesign -d -vvv ~/Applications/Dolphin.app/Contents/MacOS/Dolphin 2>&1 | command grep flags=
# expect flags=0x2(adhoc), with no "runtime"
```

Dolphin before 2609 doesn't size MEM1 from the disc. The app and
`wwdev dolphin2` pass the 48 MB override on the command line, so a 2603
build works when you launch it through them.

---

[← Releases, code signing and the website](releasing.md) · [Docs home](README.md) · [The macOS test rig →](mac-rig.md)
