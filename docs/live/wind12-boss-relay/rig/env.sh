# Sourced by every wind#12 rig script. Override any of these in the environment.
# Paths follow WW-MAC-RULES.md; wind#3 (docs/mac-rig.md) is the authority if they differ.
: "${WW:=$HOME/projects/ww-mp-wind12}"                 # this worktree (wwdev is built here)
: "${WWDEV:=$HOME/ww-shared/bin/wwdev}"                # entitled build (docs/mac-rig.md)
: "${WWSHARED:=$HOME/ww-shared}"
: "${ISO:=$WWSHARED/disc/WW-mp.iso}"                   # patched disc (never the vanilla CISO)
: "${FIXTURES:=$WWSHARED/fixtures}"                    # shared originals: read-only, never launch on them
: "${DOLPHIN_APP:=$HOME/Applications/Dolphin.app}"     # re-signed copy (scripts/setup-mac-dolphin.sh)
: "${RUN:=$HOME/ww-shared/runs/wind12}"                # per-run user dirs, logs, pids
: "${START_SAV:=}"                                     # optional -s state (start-totg-1CE30685.sav), empty = cold boot
export WW WWDEV WWSHARED ISO FIXTURES DOLPHIN_APP RUN START_SAV
mkdir -p "$RUN"
