# Playing

**Both players must run the same version.** `ww-multiplayer version`
prints it. This version can't play with v0.5.0 or older, so upgrade
together. The host also turns away a player whose built-in mods differ
from its own.

**After updating, your disc is patched again.** The app's first start
after an update re-patches it from the original disc Setup saved; if it
has none, it opens Setup to pick it ([Updates](updates.md)). With the
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
LAN or Tailscale network) are listed there with
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
bound to another world can have its tag removed from the app; a file far behind the world
or needing a merge gets advice cards, including "Ask the host to allow
merging". In game the co-op panel shows where co-op is, with Refresh area,
Warp to partner, Pause co-op writes and Copy status, and the log has a
Co-op sync tab. A bind, or a removed tag, reaches your memory card without an
in-game save: once Dolphin is closed the app writes the tag into the card's GCI
file, after a backup and a read-back check (a raw `.raw` card still needs an
in-game save). On Windows the window
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
set the same value. Known limits: [05-known-issues.md](05-known-issues.md#shared-enemies).

**Reporting a bug.** The **Bug report** button in Settings' Help card saves one zip
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

**Over the internet:** see [Playing over the internet](online.md).

---

[← Install and first run](install.md) · [Docs home](README.md) · [The terminal menu and command line →](command-line.md)
