# Wind Waker Multiplayer

![splash](docs/img/splash.png)

![Two Dolphin windows showing each player's Link rendered in the other's game on Outset Island](docs/img/two-links-ingame.png)

Real-time visual multiplayer for The Legend of Zelda: The Wind Waker on Dolphin.
Each player runs `ww-multiplayer` alongside their own Dolphin instance — positions and
skeletal poses are shared over TCP so each side sees the other's real Link
walking around in-game. Runs on **Windows and macOS** (Apple Silicon + Intel).

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
- Internet play needs a port-forward or a VPN such as Tailscale (see
  [Playing](#playing)); there's no built-in NAT traversal.
- macOS first-run requires a one-time step, the app's **Prepare Dolphin**
  button (or `scripts/setup-mac-dolphin.sh`): it copies
  `/Applications/Dolphin.app` to `~/Applications/Dolphin.app` and re-signs
  it without the hardened runtime, which lets AMFI permit `task_for_pid`
  against it. After that, no sudo is needed.
- The downloads aren't code-signed yet: Windows SmartScreen and macOS
  Gatekeeper warn the first time (see [Quick start](#quick-start-end-users)).

Active work is tracked in [GitHub issues](../../issues) — epics group related items, sub-issues are concrete tasks. `docs/06-history.md` is the session-by-session log of how the pipeline was built (precious for debugging).

## Quick start (end users)

You need [Dolphin](https://dolphin-emu.org/) (a recent stable build) and
your own legitimate copy of Wind Waker (NTSC-U, game ID `GZLE01`, `.iso`
or `.ciso`). **Download the app, double-click it**, and it walks you
through the rest.

### Windows

1. Download `ww-multiplayer.exe` from the [latest release](../../releases)
   and double-click it. A console window may flash for a moment, then the
   app window opens.
2. If Windows shows **"Windows protected your PC"** (SmartScreen: the exe
   isn't code-signed yet), click **More info**, then **Run anyway**.
3. The first time, the app opens on **Setup** (see [Playing](#playing)):
   your name and tunic color, your Wind Waker disc (the app writes a
   patched copy next to it; your original is never changed) and Dolphin
   (found for you, or Browse…).
4. **Host**, or **Join**: a session on your network shows under Join with
   a Join button; otherwise paste the host's join code (`WW-30AG-09AX`)
   or address. Everyone meets in the
   lobby; when the others are Ready, the host presses **Start** and each
   player's Dolphin starts with the patched game. Load any save (saves
   don't have to match). If Windows asks, allow `ww-multiplayer.exe`
   through the firewall.

The window needs Microsoft's WebView2 runtime, which Windows 11 and
current Windows 10 have; without it the app offers to install it.

### macOS (Apple Silicon or Intel, macOS 12 or later)

1. Download `ww-multiplayer-macos.zip` from the
   [latest release](../../releases) and open it (Safari usually unzips it
   for you). You get a `WW Multiplayer` folder with `WW Multiplayer.app`,
   a `ww-multiplayer` command for the terminal, `scripts/setup-mac-dolphin.sh`
   and `LICENSE`. Move the folder somewhere permanent, such as
   Applications.
2. Double-click **`WW Multiplayer.app`**. It isn't notarized by Apple yet,
   so the first time macOS says it can't verify the app: **Control-click
   (right-click) the app → Open → Open**. On macOS 15 and later, after
   that first warning go to **System Settings → Privacy & Security** and
   click **Open Anyway**.
3. **Setup**, as on Windows, plus **Prepare Dolphin**: press it once, and
   again after every Dolphin update (the app reminds you). It copies
   `/Applications/Dolphin.app` to `~/Applications/Dolphin.app` and re-signs
   the copy without the hardened runtime, so the app may read its memory;
   your original Dolphin is left alone. No sudo.
4. **Host** or **Join**, then **Start** from the lobby, as on Windows.

The app is ad-hoc signed with the `com.apple.security.cs.debugger`
entitlement: once Dolphin's hardened runtime is off (Prepare Dolphin),
AMFI lets it attach with `task_for_pid` from your normal user account.

### Co-op is optional

Plain multiplayer (seeing each other's Link) needs nothing more. Co-op
worlds, which share story progress between players' saves, are off unless
the host turns them on in the lobby; see [Playing](#playing).

### Advanced: the terminal menu and command line

The same program is also a terminal menu and a command line (details
under [Playing](#playing)):

```
.\ww-multiplayer.exe patch path\to\your-wind-waker.iso   # Windows
.\ww-multiplayer.exe host [name]
.\ww-multiplayer.exe join <host-ip> [name]
.\ww-multiplayer.exe tui                                 # the terminal menu
```

On macOS, in Terminal, `cd` into the unzipped `WW Multiplayer` folder and
clear the download quarantine once (`xattr -cr .`), then use
`./ww-multiplayer` the same way (it is a link to the app's own binary).
`./scripts/setup-mac-dolphin.sh` does what Prepare Dolphin does; boot the
patched ISO in the `~/Applications/Dolphin.app` copy.

`patch` writes `your-wind-waker-multiplayer.iso` next to the input and
leaves the original untouched; already-patched ISOs are detected and
skipped.

We don't ship a pre-patched ISO for legal reasons (it would be a derivative
of the entire Wind Waker DOL). The patcher contains only our injected code
plus a list of byte-edit records; your vanilla DOL is the source of all
original game-code bytes.

## Playing

**Both players must run the same version.** `ww-multiplayer version`
prints it. v0.3.0 and later can't play with v0.2.x, so upgrade together.

**The app.** Run `ww-multiplayer` with no arguments — double-click
`ww-multiplayer.exe` on Windows or `WW Multiplayer.app` on macOS — and the
app window opens. The first time, it opens on **Setup**, three steps you
can each skip and come back to with the **Setup** button:

1. **Your name and tunic color** (a palette, or your own color), with a
   preview of your Link.
2. **Your game:** choose your own Wind Waker disc image (`.iso` or
   `.ciso`, GZLE01). The app patches it as `ww-multiplayer patch` does,
   with a progress bar, writing `…-multiplayer.iso` next to it (your
   original is never changed) and building the co-op stage tables. A disc
   that is already patched is used as it is, and "I already have a
   patched ISO" picks one directly.
3. **Dolphin:** the app finds it (a running Dolphin first, then
   `DOLPHIN_EXE`, the usual install folders and `Desktop\Dolphin-x64`);
   Browse… if it didn't. On macOS a **Prepare Dolphin** button does what
   `scripts/setup-mac-dolphin.sh` does, and the app offers to refresh that
   copy when Dolphin was updated.

Setup opens again by itself while one of those is missing, or when the
patched disc or Dolphin has moved. Then pick Host (on port 25565
or the one you type) or Join: sessions hosted on your network (the same
LAN, maybe a Radmin VPN network: not verified yet) are listed there with
a Join button, or paste the host's join code (like `WW-30AG-09AX`: the
host's address and port, shortened, with a check letter that catches a
typo) or type its address. Everyone meets in the
app's lobby, which lists each player's name, color and ready flag, with
the session log and chat. Any color can be picked, even one someone else
wears; without a pick you wear classic green. IP addresses are hidden on
screen (shown as `•••.•••.•••.42`), so streaming the app never shows one:
the host's share card has Show and Copy per address, and the **Show IP
addresses** switch at the top of the window shows them all (it is saved).
A join code holds the address, so it is hidden the same way
(`WW-••••-••••`; Copy code copies the real one), and so are the addresses
of the sessions listed on your network.
**Starting the game:** everyone but the host presses Ready, then the
host presses Start (or "Start anyway" to go without waiting). Each app
then starts that player's Dolphin with the patched disc, or plays on the
one already running on that player's Dolphin user folder; tick **I'll
start Dolphin myself** in the lobby to start Dolphin yourself (the app
plays on it once it runs). Every player's row shows their Dolphin (Not
running, Launching…, Title screen, In game), and other players' Links
appear from the title screen on. The Session screen then shows the
players with their Dolphin and whether they are in your area, a minimap
and chat. If Dolphin runs another game or an unpatched disc, the app
says what to boot. Leave stops the multiplayer and keeps Dolphin open.
**Co-op worlds (shared progress) are optional.** In the lobby the host picks
Off, New world (founded from the file the host loads after Start) or
Continue (a world on the host's computer), with dry run, merging, live
switches and the co-op tables' disc under Advanced. Everyone's window then
shows the three files on their memory card, whether each is bound to that
world, and which one to load. After Start a guided dialog walks the host
through founding a new world; every co-op question is a dialog (Bind / Not
now to bind a file, Remove the tag / Leave it for a stale tag), and a file
bound to another world can have its tag removed from the app (in game
memory only: save in game to keep it off the card); a file far behind the world
or needing a merge gets advice cards, including "Ask the host to allow
merging". In game the co-op panel shows where co-op is, with Refresh area,
Warp to partner, Pause co-op writes and Copy status, and the log has a
Co-op sync tab. Save in game after a bind so your card keeps the tag. On Windows the window
needs Microsoft's WebView2 runtime (Windows 11 and current Windows 10
have it); without it the app offers to install it, and opens the terminal
menu if you decline. In-game tunic colors arrive in a later update too.

**The terminal menu.** `ww-multiplayer tui` opens the menu that no
arguments used to open. Pick Host or Join, enter a player name (and the
host's address, to join), and the dashboard shows who's connected, the
host's IP to share, a minimap and chat. IP addresses are hidden there
too; **Ctrl+R** shows or hides them. **Each player needs a different
name**: two players with the same name can't see each other. The menu
always hosts on port 25565; use the command line for another port.

**The command line** does the same thing:

```
ww-multiplayer host [name] [--port N]       # default port 25565
ww-multiplayer join <host> [name]           # <host> is an IP, hostname or join code
ww-multiplayer join 192.168.1.42:26000      # host used --port 26000
ww-multiplayer join "[2001:db8::42]:26000"  # IPv6: brackets when giving a port
ww-multiplayer join WW-30AG-09AX            # a join code from the app (it holds the address and port; a typo is refused)
```

(`.\ww-multiplayer.exe` on Windows, `./ww-multiplayer` on macOS.) Keep the
quotes around a bracketed IPv6 address: macOS's default shell (zsh)
otherwise reads the brackets as a filename pattern and refuses to run the
command. A bare IPv6 address without a port, such as
`join 2001:db8::42`, needs no quotes.

**Over the internet.** The joiner has to reach the host's port (TCP 25565
unless the host passed `--port`). Either forward that TCP port on the
host's router to the host's PC and have the joiner use the host's public
IP, or put both machines on a VPN such as [Tailscale](https://tailscale.com)
and join the host's Tailscale IP (`100.x.y.z`) — no port-forwarding
needed. A Windows host should allow `ww-multiplayer.exe` through the
firewall when Windows asks.

## Requirements

- Windows 10+, or macOS 12+ (Apple Silicon or Intel)
- [Dolphin emulator](https://dolphin-emu.org/) — recent stable release
- Your own legitimate copy of The Wind Waker (NTSC-U, game ID `GZLE01`)
- [Go 1.21+](https://go.dev/dl/) — only if building from source. macOS
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
`SETUP.md` (devkitPPC + Freighter), then run:

```bash
cd inject && python build.py        # rebuilds patched.dol (devkitPPC from $DEVKITPPC)
cd .. && python scripts/extract_blob.py  # regenerates blob.go
go build -o ww-multiplayer.exe ./cmd/ww-multiplayer
./ww-multiplayer.exe patch <vanilla.iso|ciso> <out.iso>   # a disc with the new blob
```

### Releases, code signing and the website

Pushing a `v*` tag runs `.github/workflows/release.yml`: it builds
`ww-multiplayer.exe` (Windows, cross-built on Linux) and
`ww-multiplayer-macos.zip` (`scripts/build-mac.sh` on macOS: a universal
`WW Multiplayer.app` with an `Info.plist` carrying the tag's version,
the icon, and `LSMinimumSystemVersion` 12.0, zipped with `ditto` so the
bundle and its signature survive), writes `SHA256SUMS` over both, and
publishes one GitHub Release (a tag with a `-` is a prerelease). Running
the workflow by hand (workflow_dispatch) is a dry run: it builds both and
keeps them as workflow artifacts, but publishes nothing. `build.yml` also
builds and zips the macOS app on every push and PR, as a packaging smoke
test.

The Windows exe embeds `cmd/ww-multiplayer/ww_windows.syso`: the icon (`docs/img/icon.ico`)
and an application manifest (`packaging/ww-multiplayer.exe.manifest`:
per-monitor DPI awareness, Common Controls 6, `asInvoker`). It stays a
console-subsystem program, so one exe is the app, the terminal menu and
the CLI. After changing the icon or the manifest, run `go generate ./cmd/ww-multiplayer`
with [rsrc](https://github.com/akavel/rsrc) on `PATH`; that also rebuilds
the macOS icon `docs/img/icon.icns` (`tools/mkicns`).

**Code signing is optional and off for now.** Each signing step in
release.yml runs only when its repository secrets exist, so a fork (or
this repo today) builds the unsigned assets with nothing failing:

| Secrets | What they turn on |
|---|---|
| `WINDOWS_CERTIFICATE_PFX` (base64 .pfx), `WINDOWS_CERTIFICATE_PASSWORD` | Authenticode-signs the exe (osslsigncode, signtool's Linux equivalent, with an RFC 3161 timestamp) |
| `MACOS_CERTIFICATE_P12` (base64 .p12), `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGN_IDENTITY` | Signs the app with a Developer ID identity, hardened runtime and the cs.debugger entitlement, instead of ad-hoc |
| the three above plus `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` | Also notarizes the zip and staples the app |

Without them, users see the SmartScreen and Gatekeeper prompts described
in [Quick start](#quick-start-end-users). macOS players need Prepare
Dolphin either way: Dolphin's own hardened runtime is what blocks
`task_for_pid`.

**The website** (`site/`, GitHub Pages) describes the latest release, so
`.github/workflows/pages.yml` deploys it when a release is published
(release.yml starts it after publishing a non-prerelease; a release
published by hand triggers it too), always from the latest release's
tag. A `site/` change merged to `main` goes live with the next release;
run the Pages workflow by hand, optionally with a `ref`, to redeploy
sooner.

### Headless / debug commands

```bash
./ww-multiplayer.exe debug         # Print Link's position for 5 seconds (sanity check)
./ww-multiplayer.exe server        # Run a TCP server on :25565 with no UI
./ww-multiplayer.exe fake-client   # Connect a bot that walks in circles
./ww-multiplayer.exe help          # Full command list
```

## Contributing / hacking

- `docs/01-architecture.md` — how the pieces fit together
- `docs/06-history.md` — session-by-session log of what was built and why
- `docs/api/00-design.md` — the approved plan for the modding API, with multiplayer and co-op as mods
- [GitHub issues](../../issues) — what's next, organized by epic
- `SETUP.md` — what you need to install if you want to work on the C
  injection side (devkitPPC, Freighter, etc.)

## License

[MIT](LICENSE) — do whatever you want with it, no warranty, not liable.

This project does not include or distribute any Nintendo IP. The patcher
splices our own injected code into your own legitimately-acquired Wind
Waker disc image. You are responsible for the legality of your own ISO
in your jurisdiction.
