# What works and known limits

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
- Only Outset Island has been heavily tested. A friend in another stage
  is hidden in your game until they are back in yours (tested live on
  two Dolphins). A friend in another room of the same stage stays drawn
  where they really are.
- Other enemy types still run separately on each player's game. Only the
  first three other players get bodies, and each player mirrors at most 4
  enemies at once. Shared enemies have not yet been tested over the
  internet.
- Internet play needs a port-forward or a VPN such as Tailscale (see
  [Playing over the internet](online.md)); there's no built-in NAT traversal.
- macOS first-run requires a one-time step, the app's **Prepare Dolphin**
  button (or `scripts/setup-mac-dolphin.sh`): it copies
  `/Applications/Dolphin.app` to `~/Applications/Dolphin.app` and re-signs
  it without the hardened runtime, which lets AMFI permit `task_for_pid`
  against it. After that, no sudo is needed. **Remove the copy** (or
  `scripts/setup-mac-dolphin.sh --undo`) undoes it.
- The downloads aren't code-signed yet: Windows SmartScreen and macOS
  Gatekeeper warn the first time (see [Install](install.md)).

Active work is tracked in [GitHub issues](../../issues) — epics group related items, sub-issues are concrete tasks. `docs/06-history.md` is the session-by-session log of how the pipeline was built (precious for debugging).

---

[← Updates](updates.md) · [Docs home](README.md) · [Known issues →](05-known-issues.md)
