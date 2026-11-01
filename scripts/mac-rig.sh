#!/bin/bash
# mac-rig.sh: the macOS test rig (docs/mac-rig.md). One rig = one folder
# with its own Dolphin user dirs, so many agents can run Dolphins on one Mac.
#
#   scripts/mac-rig.sh setup <rig>          make $WW_SHARED/rigs/<rig>/{u1,u2} from the fixtures, hotkeys on the keyboard
#   scripts/mac-rig.sh pair <rig> [save]    launch two Dolphins (u1, u2) from the Mac start save; writes pair.pids
#   scripts/mac-rig.sh one <rig> [save]     launch one Dolphin on u1; writes u1.pid
#   scripts/mac-rig.sh mp-local <rig> [args] run wwdev mp-local on the pair (MP_LOCAL_PIDS pinned, USER_DIR_1/2 = the rig's u1/u2); blocks
#   scripts/mac-rig.sh stop <rig>           end this rig's Dolphins by recorded PID (never by name)
#   scripts/mac-rig.sh shot <pid> <out.png> screenshot one Dolphin's render window
#   scripts/mac-rig.sh fps <pid>...         game frames per second over 5 s (full speed = 30)
#   scripts/mac-rig.sh recapture <rig> [out] make a Mac start save (wwdev auto-recapture, unattended)
#
# Take an Irori lease ww/dolphin-slot-<n> before `pair`/`one`; release it after `stop`.
# Env: WW_SHARED (default ~/ww-shared), WWDEV (default $WW_SHARED/bin/wwdev),
# DOLPHIN_APP (default ~/Applications/Dolphin.app, the copy scripts/setup-mac-dolphin.sh makes).
set -euo pipefail

WW_SHARED=${WW_SHARED:-$HOME/ww-shared}
WWDEV=${WWDEV:-$WW_SHARED/bin/wwdev}
DOLPHIN_APP=${DOLPHIN_APP:-$HOME/Applications/Dolphin.app}
ISO=${ISO_PATH:-$WW_SHARED/disc/WW-mp.iso}
SAVE_DEFAULT=$WW_SHARED/fixtures/start-mac-CAC6980B.sav  # REL 0xCAC6980B (main since #289); start-mac-A9DE0F1E.sav and -1CE30685.sav stay for older builds
[ -f "$SAVE_DEFAULT" ] || SAVE_DEFAULT=none

die() { echo "mac-rig: $*" >&2; exit 1; }
rigdir() { [ -n "${1:-}" ] || die "name a rig"; echo "$WW_SHARED/rigs/$1"; }

# Hotkeys on the Mac keyboard, so a synthetic Shift+F1 saves state 1
# (a controller-bound hotkey device ignores it).
keyboard_hotkeys() {
	local ini=$1/Config/Hotkeys.ini
	[ -f "$ini" ] || { mkdir -p "$1/Config"; printf '[Hotkeys]\n' >"$ini"; }
	python3 - "$ini" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
def put(key, val):
    global s
    if re.search(r'(?m)^' + re.escape(key) + r' = ', s):
        s = re.sub(r'(?m)^' + re.escape(key) + r' = .*$', key + ' = ' + val, s)
    else:
        s = s.replace('[Hotkeys]\n', '[Hotkeys]\n' + key + ' = ' + val + '\n', 1)
put('Device', 'Quartz/0/Keyboard & Mouse')
put('Save State/Save State Slot 1', '@(Shift+F1)')
put('Load State/Load State Slot 1', 'F1')
open(p, 'w').write(s)
PY
}

# launch_on <userdir> <save|none>: one Dolphin on that user dir (48 MB MEM1,
# no panic popups, dev log); prints its PID, found by its own -u argument.
launch_on() {
	local u=$1 save=$2 pid=""
	pgrep -f -- "-u $u " >/dev/null && die "a Dolphin already runs on $u (stop it first)"
	rm -rf "$u/Logs"
	local args=(-u "$u" -b -e "$ISO" -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000
		-C Dolphin.Interface.UsePanicHandlers=False -C Dolphin.Interface.ConfirmStop=False
		-C Logger.Options.WriteToFile=True -C Logger.Options.Verbosity=4 -C Logger.Logs.OSREPORT=True
		-C Logger.Logs.MI=True -C Logger.Logs.MASTER=True)
	[ "$save" = none ] || args+=("--save_state=$save")
	/usr/bin/open -n -a "$DOLPHIN_APP" --args "${args[@]}"
	for _ in $(seq 1 60); do pid=$(pgrep -f -- "-u $u " | head -1 || true); [ -n "$pid" ] && break; sleep 0.25; done
	[ -n "$pid" ] || die "no Dolphin appeared on $u"
	echo "$pid"
}

env_for() {
	local r; r=$(rigdir "$1")
	export DOLPHIN_EXE=$DOLPHIN_APP/Contents/MacOS/Dolphin ISO_PATH=$ISO USER_DIR_1=$r/u1 USER_DIR_2=$r/u2 WW_DOLPHIN_DEVLOG=1
}

cmd=${1:-}; shift || true
case "$cmd" in
setup)
	r=$(rigdir "${1:-}")
	[ -d "$WW_SHARED/fixtures/dolphin-user-3" ] || die "no fixtures in $WW_SHARED/fixtures"
	mkdir -p "$r"
	for i in 1 2; do
		[ -d "$r/u$i" ] && die "$r/u$i exists (stop the rig and remove it to start over)"
		cp -R "$WW_SHARED/fixtures/dolphin-user-$((i + 2))" "$r/u$i"
		rm -rf "$r/u$i/StateSaves/"* "$r/u$i/Logs"
		keyboard_hotkeys "$r/u$i"
	done
	echo "rig $r ready (u1, u2)"
	;;
pair)
	# Launches both Dolphins itself: wwdev dolphin2 adopts any running Dolphin
	# as Dolphin 1, which on a shared Mac is another agent's.
	r=$(rigdir "${1:-}"); save=${2:-$SAVE_DEFAULT}
	p1=$(launch_on "$r/u1" "$save"); p2=$(launch_on "$r/u2" "$save")
	[ -n "$p1" ] && [ -n "$p2" ] && [ "$p1" != "$p2" ] || die "pair: got pids '$p1' '$p2'"
	echo "$p1,$p2" >"$r/pair.pids"; echo "pair pids: $p1,$p2 (in $r/pair.pids)"
	;;
one)
	r=$(rigdir "${1:-}"); save=${2:-$SAVE_DEFAULT}
	pid=$(launch_on "$r/u1" "$save")
	echo "$pid" >"$r/u1.pid"; echo "pid $pid (in $r/u1.pid)"
	;;
mp-local)
	r=$(rigdir "${1:-}"); env_for "$1"; shift
	[ -f "$r/pair.pids" ] || die "no $r/pair.pids: run pair first"
	# env_for exports USER_DIR_1/2 as this rig's u1/u2: --coop reads, backs up
	# and binds the cards there, never the default Dolphin folders.
	# With --coop, the stage tables come from --coop-iso, else from the disc
	# `patch` last wrote (a per-user record any agent's patch run moves), so
	# name the rig's disc unless the caller did. The rig's cards are copies
	# (setup), and nobody is at a TTY to answer the bind prompt: add --yes.
	coop=0 iso=0 yes=0
	for a in "$@"; do
		case "$a" in
		--) break ;;
		--coop | --coop=*) coop=1 ;;
		--coop-iso | --coop-iso=*) iso=1 ;;
		--yes) yes=1 ;;
		esac
	done
	if [ "$coop" = 1 ]; then
		# prepended: anything after a "--" goes to the pipelines untouched
		[ "$yes" = 1 ] || set -- --yes "$@"
		[ "$iso" = 1 ] || set -- --coop-iso "$ISO" "$@"
	fi
	cd "$r" && MP_LOCAL_PIDS=$(cat "$r/pair.pids") WW_DEV_LOGS=$r/logs exec "$WWDEV" mp-local "$@"
	;;
stop)
	r=$(rigdir "${1:-}")
	for f in "$r/pair.pids" "$r/u1.pid"; do
		[ -f "$f" ] || continue
		for pid in $(tr ',' ' ' <"$f"); do
			if ps -o args= -p "$pid" 2>/dev/null | grep -qF -- "$r/u"; then kill "$pid" && echo "stopped $pid"; fi
		done
		rm -f "$f"
	done
	;;
shot)
	pid=${1:-}; out=${2:-}; [ -n "$pid" ] && [ -n "$out" ] || die "usage: shot <pid> <out.png>"
	wid=$(swift - "$pid" 2>/dev/null <<'SW'
import CoreGraphics
let pid = Int32(CommandLine.arguments[1])!
let l = CGWindowListCopyWindowInfo([.optionAll], kCGNullWindowID) as! [[String: Any]]
var best = 0, area = 0.0
for w in l where (w[kCGWindowOwnerPID as String] as? Int32) == pid && (w[kCGWindowLayer as String] as? Int) == 0 {
  let name = (w[kCGWindowName as String] as? String) ?? ""
  if !name.contains(" | ") { continue } // the render window: "Dolphin <ver> | JIT... | <game>"
  if let b = w[kCGWindowBounds as String] as? [String: Double], let n = w[kCGWindowNumber as String] as? Int {
    let a = (b["Width"] ?? 0) * (b["Height"] ?? 0); if a > area { area = a; best = n } } }
print(best)
SW
	)
	[ -n "$wid" ] && [ "$wid" != 0 ] || die "no render window for pid $pid (Screen Recording permission?)"
	screencapture -x -o -l "$wid" "$out" && echo "$out"
	;;
fps)
	[ $# -gt 0 ] || die "usage: fps <pid>..."
	t=$(mktemp)
	for p in "$@"; do echo "$p $(WW_DOLPHIN_PID=$p "$WWDEV" peek 0x80412F00 | awk '{print $2}')"; done >"$t"
	sleep 5
	while read -r p a; do b=$(WW_DOLPHIN_PID=$p "$WWDEV" peek 0x80412F00 | awk '{print $2}'); echo "pid $p: $(( ($(printf %d "$b") - $(printf %d "$a")) / 5 )) fps"; done <"$t"
	rm -f "$t"
	;;
recapture)
	r=$(rigdir "${1:-}"); env_for "$1"; out=${2:-$r/start-mac.sav}
	cd "$r" && "$WWDEV" auto-recapture "$out"
	;;
*)
	sed -n '2,15p' "$0"; exit 2
	;;
esac
