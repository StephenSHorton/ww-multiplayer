#!/bin/bash
# mac-rig.sh: the macOS test rig (docs/mac-rig.md). One rig = one folder
# with its own Dolphin user dirs, so many agents can run Dolphins on one Mac.
#
#   scripts/mac-rig.sh setup <rig>          make $WW_SHARED/rigs/<rig>/{u1,u2} from the fixtures, hotkeys on the keyboard
#   scripts/mac-rig.sh pair <rig> [save]    launch two Dolphins (wwdev dolphin2) from the Mac start save; writes pair.pids
#   scripts/mac-rig.sh one <rig> [save]     launch one Dolphin on u1; writes u1.pid
#   scripts/mac-rig.sh mp-local <rig> [args] run wwdev mp-local on the pair (MP_LOCAL_PIDS pinned); blocks
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
SAVE_DEFAULT=$WW_SHARED/fixtures/start-mac-1CE30685.sav

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
	r=$(rigdir "${1:-}"); env_for "$1"; save=${2:-$SAVE_DEFAULT}
	export SAVE_STATE=$save WW_DEV_LOGS=$r/logs
	(cd "$r" && "$WWDEV" dolphin2) | tee "$r/dolphin2.out"
	grep -o 'Launched Dolphin [12] (pid [0-9]*' "$r/dolphin2.out" | awk '{print $5}' | paste -sd, - >"$r/pair.pids"
	echo "pair pids: $(cat "$r/pair.pids") (in $r/pair.pids)"
	;;
one)
	r=$(rigdir "${1:-}"); save=${2:-$SAVE_DEFAULT}
	args=(-u "$r/u1" -b -e "$ISO" -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000
		-C Dolphin.Interface.UsePanicHandlers=False -C Dolphin.Interface.ConfirmStop=False
		-C Logger.Options.WriteToFile=True -C Logger.Options.Verbosity=4 -C Logger.Logs.OSREPORT=True -C Logger.Logs.MASTER=True)
	[ "$save" = none ] || args+=("--save_state=$save")
	/usr/bin/open -n -a "$DOLPHIN_APP" --args "${args[@]}"
	for _ in $(seq 1 40); do pid=$(pgrep -f -- "-u $r/u1 " | head -1 || true); [ -n "$pid" ] && break; sleep 0.25; done
	[ -n "${pid:-}" ] || die "no Dolphin appeared"
	echo "$pid" >"$r/u1.pid"; echo "pid $pid (in $r/u1.pid)"
	;;
mp-local)
	r=$(rigdir "${1:-}"); shift
	[ -f "$r/pair.pids" ] || die "no $r/pair.pids: run pair first"
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
