# Getting started

[Docs index](README.md) · [Playing →](playing.md)

From download to your first session in a few minutes. Back to the [project README](../README.md).

You need a recent [Dolphin](https://dolphin-emu.org/) (the patched disc
needs 48 MB of memory, and the app sets this itself every time it starts
Dolphin) and your own legitimate copy of Wind Waker (NTSC-U, game ID `GZLE01`, `.iso`,
`.ciso`, `.rvz`, `.wia` or `.gcz`). **Download the app, double-click it**, and it walks you
through the rest.

## Windows

1. Download `ww-multiplayer.exe` from the [latest release](https://github.com/StephenSHorton/ww-multiplayer/releases)
   and double-click it. A console window may flash for a moment, then the
   app window opens.
2. If Windows shows **"Windows protected your PC"** (SmartScreen: the exe
   isn't code-signed yet), click **More info**, then **Run anyway**.
3. The first time, the app opens on **Setup** (see [Playing](playing.md)):
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

## macOS (Apple Silicon or Intel, macOS 12 or later)

1. Download `ww-multiplayer-macos.zip` from the
   [latest release](https://github.com/StephenSHorton/ww-multiplayer/releases) and open it (Safari usually unzips it
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
   your original Dolphin is left alone. No sudo. Setup explains this
   before it starts. To undo it, press **Remove the copy** on the same
   step: it unregisters the copy from macOS and deletes it, and the app
   goes back to the original. If `~/Applications/Dolphin.app` is a
   Dolphin you installed there yourself (not a prepared copy), the app
   leaves it alone and asks you to move it first.
4. **Host** or **Join**, then **Start** from the lobby, as on Windows.

The app is ad-hoc signed with the `com.apple.security.cs.debugger`
entitlement: once Dolphin's hardened runtime is off (Prepare Dolphin),
AMFI lets it attach with `task_for_pid` from your normal user account.

## Co-op is optional

Plain multiplayer (seeing each other's Link) needs nothing more. Co-op
worlds, which share story progress between players' saves, are off unless
the host turns them on in the lobby; see [Playing](playing.md).

## Advanced: the terminal menu and command line

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

---

[Docs index](README.md) · [Playing →](playing.md)
