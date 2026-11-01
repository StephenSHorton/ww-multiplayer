# Source this: shared settings for the wind#20 macOS rig scripts.
# Ported from the PC's rig scripts (driveg.sh, healer.sh, l6-b2-cli.ps1). Booting is
# flint's scripts/mac-rig.sh (setup/pair/stop/shot), never a direct Dolphin exec.
: "${WW_BIN:=$HOME/ww-shared/bin}"                   # the entitled wwdev + ww-multiplayer (docs/mac-rig.md: attach needs the entitlement)
: "${WW_SHARED:=$HOME/ww-shared}"
: "${ISO_PATH:=$WW_SHARED/disc/WW-mp.iso}"           # stage-hop checks starts against this; never leave it unset
: "${RIG:=wind20}"                                    # the mac-rig.sh rig name
: "${RIG_DIR:=$WW_SHARED/rigs/$RIG}"                  # its u1/u2 user dirs (mac-rig.sh setup) + our logs/pids
export WW_BIN WW_SHARED ISO_PATH RIG RIG_DIR
export WW_LISTEN_HOST=127.0.0.1                      # loopback only: nobody is awake to click a firewall dialog
mkdir -p "$RIG_DIR/logs" "$RIG_DIR/pids"
