# Updates

[← Playing](playing.md) · [Docs index](README.md) · [Self-hosting a dedicated server →](11-self-hosting.md)

How the app finds and installs a new version.

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

---

[← Playing](playing.md) · [Docs index](README.md) · [Self-hosting a dedicated server →](11-self-hosting.md)
