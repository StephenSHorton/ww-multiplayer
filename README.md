# Wind Waker Multiplayer

![splash](docs/img/splash.png)

![Two Dolphin windows showing each player's Link rendered in the other's game on Outset Island](docs/img/two-links-ingame.png)

Real-time visual multiplayer for The Legend of Zelda: The Wind Waker on Dolphin.
Each player runs `ww-multiplayer` alongside their own Dolphin instance — positions and
skeletal poses are shared over TCP so each side sees the other's real Link
walking around in-game. Runs on **Windows and macOS** (Apple Silicon + Intel).

## Status

What works today:
- **A desktop app** that does it all: first-run Setup patches your own
  disc and finds Dolphin, then a lobby where everyone gathers, and the
  host's **Start** launches each player's Dolphin with the patched game
- **Easy joining:** sessions on your network are listed with a Join
  button; anyone else pastes the host's short join code (`WW-30AG-09AX`)
  or address
- **IP addresses hidden on screen**, so streaming the app is safe
- **Each other's Link in your game**, at their real position with their
  real animations, each in that player's own tunic color (a player in
  casual clothes shows the casual shirt)
- **Shared enemies:** the host's Moblins, Bokoblins, ChuChus and Keese are the
  real ones, and a hit from any player counts
- **Other players have a body** those enemies can see and hit
- **Optional co-op worlds:** shared progress across everyone's saves
- **Built on a modding API:** multiplayer and co-op are mods on `wwapi`
  (`ww-multiplayer mods` lists them)

Known limits:
- Only Outset Island has been heavily tested. Stage / room transitions
  aren't gracefully handled yet: without co-op, your friend's Link just
  renders at the last known coords if they cross to a different stage
  (in a two-player co-op world it is hidden while they are elsewhere).
- Other enemy types still run separately on each player's game. Only the
  first two other players get bodies, and each player mirrors at most 4
  enemies at once. Shared enemies have not yet been tested over the
  internet.
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

You need a recent [Dolphin](https://dolphin-emu.org/) (the patched disc
needs 48 MB of memory, and the app sets this itself every time it starts
Dolphin) and your own legitimate copy of Wind Waker (NTSC-U, game ID `GZLE01`, `.iso`
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
prints it. This version can't play with v0.4.1 or older, so upgrade
together. The host also turns away a player whose built-in mods differ
from its own.

**After updating, your disc is patched again.** The app's first start
after an update re-patches it from the original disc Setup saved; if it
has none, it opens Setup to pick it ([Updates](#updates)). With the
command line, run `ww-multiplayer patch` again. Dolphin save states
taken on an older version are refused: load your in-game save and take a
new state. If you boot the disc from Dolphin's game list instead of from
the app, use Dolphin 2609 or newer, or turn on Dolphin's memory override
(MEM1 48 MB) yourself.

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
   patched ISO" picks one directly. A compressed `.rvz`, `.wia` or `.gcz`
   disc works too: the app first converts it to a temporary ISO with the
   DolphinTool that ships with your Dolphin, and removes that copy once
   the patched disc is written; when the compressed disc was already
   patched, the converted ISO is kept as the `…-multiplayer.iso`. (NKit
   images still need converting back to a full ISO with the NKit tool
   first.)
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
world, and which one to load. Under that panel's "Advanced: your game's
relays" each player, host or joiner, picks their own boss-kill rows and
stale-actor refresh (as `--coop-boss-kill` and `--coop-refresh`; empty is
the default: Gohma and Kalle Demos, no refresh). After Start a guided dialog walks the host
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
menu if you decline. Each player's Link is drawn in their chosen tunic
color in everyone's game; a player still in casual clothes shows up in
the casual shirt instead.

**Shared enemies are on by default.** The host's Moblins, Bokoblins and
ChuChus are the real ones: everyone else's game mirrors them, a hit from
any player counts, and those enemies see, chase and hit the other
players' bodies. Other enemy types still run separately on each game and
ignore the other players. If an enemy connection fails for good (a
refusal, an error), the log says "enemy sync off for this session:
<reason>; multiplayer is unaffected", and you keep seeing each other.
To turn it off, every player starts the app with `WW_ENEMY_SYNC=0` (the
whole enemy layer) or `WW_ENEMY_REMOTE=off` (bodies only); all players
set the same value. Known limits: [05-known-issues.md](docs/05-known-issues.md#shared-enemies).

**Reporting a bug.** The header's **Bug report** button saves one zip
with your newest session logs, Dolphin's own log (when Dolphin's logger
writes to a file) and the app's diagnostics; **Show folder** opens where it is. Every
file in it is redacted first: your home folder becomes `~`, IP addresses
and join codes are masked, and so are host names you joined or hosted on
when they carry a port or a dynamic-DNS, tunnel or tailnet name
(`•••.duckdns.org:25570`, `•••.ts.net`); tokens are removed. A plain
computer name with no dot (`my-pc:25565`) is not masked, so look before
posting. It never holds your disc,
your saves, save states or co-op worlds, and nothing is uploaded: attach
the zip to your issue. From a terminal, `ww-multiplayer bug-report`
writes the same zip (`--include FILE` adds a log you captured).

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

### Updates

The app checks for a new version when it opens: one HTTPS request to
GitHub's latest-release API, at most every 6 hours (the answer is kept
in the app's settings), never for a development build and never for a
prerelease. When a newer release is out, Home shows **"vX.Y.Z is out"**
with **Update**, **Release notes** and **Later**.

- **Update** downloads the release's file for your computer
  (`ww-multiplayer.exe` or `ww-multiplayer-macos.zip`), checks it against
  the release's `SHA256SUMS` before anything is replaced (a mismatch
  deletes it and changes nothing), puts it in place and restarts the app
  on it. Before the old version closes, the new one must run (it is
  started once with `version`); if it doesn't (antivirus blocked or removed
  it), the old one is put back and the banner says so. Windows: the
  running exe is renamed to `ww-multiplayer.exe.old` and removed at the
  next start. macOS: the `WW Multiplayer.app` bundle is replaced (only when
  the app runs from its `.app`). If the new version still fails to open
  after the restart, the old one is put back and opened, with a message.
- After the restart, if the new version's game code differs from your
  patched disc's, the app patches your vanilla disc again (the one Setup
  saved) with the Setup screen's progress bar; without a saved vanilla
  disc it opens Setup's game step. If the game code didn't change, your
  disc is kept. A re-patch that didn't finish (the window was closed, or
  Dolphin still had the disc open: close Dolphin first) runs again the
  next time the app opens: every start checks the disc.
- Updates never run during a session: the banner comes back after
  **Leave**. While an update downloads, Host, Join, Start and patching
  wait. Should a session start anyway, the app restarts on the new version
  after **Leave** (or **Restart** on the banner). No network means no
  banner and no message; **Check now** says "Couldn't reach GitHub".
- The version pill in the header opens **Check for updates** (on by
  default; turn it off to never ask GitHub) and **Check now**.
- The builds are unsigned (owner decision), so an update asks nothing
  new: the same SmartScreen / Gatekeeper notes as the first download apply.

The CLI's `host`, `join` and `version` and the terminal menu print one
line when a newer release is out (`Update available: vX.Y.Z (you have
...). Download it from ...`) and never download anything. They keep their
own cache (`<user cache dir>/ww-multiplayer/update-check.json`, not the
app's settings); `WW_NO_UPDATE_CHECK=1` turns their check off. The
terminal menu also follows the app's **Check for updates** switch.
`WW_UPDATE_URL` points every check at another release API (tests use a
fake server on 127.0.0.1).

## Requirements

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
publishes one GitHub Release (a tag with a `-` is a prerelease). The
release's body is `docs/release-notes/<tag>.md` with GitHub's generated
"What's Changed" list after it; write and merge that file before tagging
(a tag without one, or with a draft, fails before anything is built;
`docs/release-notes/README.md`). `tools/check_game_files` runs on the
repo and on every asset before it is published, and fails the release
if any holds a Nintendo game file or a save: by name, by header, and by
scanning each file whole (the exe and the app binary in the macOS zip
too) for an archive, compressed data, a model, a disc or save header
inside it. Running
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
./ww-multiplayer.exe mods          # List the built-in mods and their status
./ww-multiplayer.exe bug-report    # Save the logs as one redacted zip for an issue (--include FILE, --user-dir DIR, --out DIR)
./ww-multiplayer.exe help          # Full command list
```

Developer and research commands (dump, dolphin2, mp-local, the co-op and
tunic diagnostics, probes) live in a separate `wwdev` tool, not shipped in
releases: `go build -o wwdev.exe ./cmd/wwdev` (see `SETUP.md`).

## Contributing / hacking

- `docs/01-architecture.md` — how the pieces fit together
- `docs/06-history.md` — session-by-session log of what was built and why
- The modding API (`wwapi/`): design in `docs/api/00-design.md`, mods in
  `docs/api/02-mods.md`, a walkthrough in `docs/api/03-writing-a-mod.md`;
  multiplayer and co-op are mods under `mods/`
- `go build -o wwdev.exe ./cmd/wwdev` — developer tools (dump, dolphin2,
  mp-local, co-op and tunic diagnostics, probes), not shipped in releases
- `docs/TESTING.md` — the checks to run before pushing
- [GitHub issues](../../issues) — what's next, organized by epic
- `SETUP.md` — what you need to install if you want to work on the C
  injection side (devkitPPC, Freighter, etc.)

## License

[MIT](LICENSE) — do whatever you want with it, no warranty, not liable.

This project does not include or distribute any Nintendo IP. The patcher
splices our own injected code into your own legitimately-acquired Wind
Waker disc image. You are responsible for the legality of your own ISO
in your jurisdiction.
