# Source this: shared settings for the wind#20 macOS rig scripts.
# Ported from the PC's archive/rig-scripts-2026-10-27 (boot1k.sh, driveg.sh,
# healer.sh, l6-b2-cli.ps1). Override any of these before sourcing.
: "${WW_REPO:=$HOME/projects/ww-mp-wind20}"          # where ./wwdev and ./ww-multiplayer are built
: "${WW_SHARED:=$HOME/ww-shared}"
: "${ISO_PATH:=$WW_SHARED/disc/WW-mp.iso}"           # stage-hop checks starts against this; never leave it unset
: "${RIG_DIR:=$HOME/ww-rigs/wind20}"                 # this card's own copies of the fixture user dirs + logs
: "${DOLPHIN_BIN:=/Applications/Dolphin.app/Contents/MacOS/Dolphin}"
export WW_REPO WW_SHARED ISO_PATH RIG_DIR DOLPHIN_BIN
export WW_LISTEN_HOST=127.0.0.1                      # loopback only: nobody is awake to click a firewall dialog
mkdir -p "$RIG_DIR/logs" "$RIG_DIR/pids"
