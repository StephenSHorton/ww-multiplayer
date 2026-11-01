#!/usr/bin/env bash
# boot.sh <user-N>: boot one Dolphin on this card's copy of fixture user-N with the MEM1
# override and file logs (as the PC's boot1k.sh), press Start through the title,
# screenshot, and print the PID (also kept in $RIG_DIR/pids/dolphin-<user-N>).
# Take an Irori ww/dolphin-slot-N lease first (a two-Dolphin pair takes one slot).
set -euo pipefail; . "$(dirname "$0")/env.sh"
UD="$RIG_DIR/dolphin-$1"; [ -d "$UD" ] || { echo "run fresh-user.sh $1 first"; exit 2; }
"$DOLPHIN_BIN" -u "$UD" -e "$ISO_PATH" \
  -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000 \
  -C Logger.Options.WriteToFile=True -C Logger.Options.WriteToConsole=False -C Logger.Options.Verbosity=4 \
  -C Logger.Logs.OSREPORT=True -C Logger.Logs.OSREPORT_HLE=True -C Logger.Logs.MI=True -C Logger.Logs.MASTER=True \
  -C Logger.Logs.CP=True -C Logger.Logs.VIDEO=True \
  -C Dolphin.Interface.UsePanicHandlers=False -C Dolphin.Interface.ConfirmStop=False \
  >"$RIG_DIR/logs/dolphin-$1.stdout" 2>&1 &
pid=$!; echo $pid > "$RIG_DIR/pids/dolphin-$1"; sleep 20
cd "$WW_REPO"
for i in $(seq 1 9); do WW_DOLPHIN_PID=$pid ./wwdev input 0x1000 0 0 100 >/dev/null 2>&1 || true; sleep 0.7; done; sleep 3
WW_DOLPHIN_PID=$pid ./wwdev screenshot "$RIG_DIR/logs/boot-$1.png" >/dev/null 2>&1 || true
echo $pid
