#!/bin/bash
# One-time macOS setup: copy /Applications/Dolphin.app to ~/Applications/
# and re-sign it without the hardened runtime so ww-multiplayer can read
# its emulated GameCube RAM.
#
# Why this is needed:
#   - Stock Dolphin.app is signed with the hardened runtime + JIT
#     entitlement (required for the JIT to work under macOS code signing).
#   - AMFI refuses task_for_pid against hardened-runtime processes unless
#     the caller has com.apple.security.cs.debugger trusted by Apple.
#   - The fix: re-sign Dolphin without hardened runtime. AMFI then
#     accepts our ad-hoc cs.debugger-entitled binary as a valid debugger.
#   - We can't re-sign /Applications/Dolphin.app in place because of the
#     com.apple.provenance xattr (Gatekeeper lock); instead we copy it
#     to ~/Applications/ where xattrs are user-writable.
#
# Run this once after installing or updating Dolphin. After that, launch
# Wind Waker from ~/Applications/Dolphin.app (the script prints the
# command at the end). The original /Applications/Dolphin.app is left
# untouched.

set -euo pipefail

SRC="/Applications/Dolphin.app"
DST="$HOME/Applications/Dolphin.app"

if [ ! -d "$SRC" ]; then
    echo "ERROR: $SRC not found. Install Dolphin first."
    exit 1
fi

if [ -d "$DST" ]; then
    echo "==> Removing existing $DST..."
    rm -rf "$DST"
fi

echo "==> Copying $SRC -> $DST..."
mkdir -p "$(dirname "$DST")"
cp -R "$SRC" "$DST"

echo "==> Stripping com.apple.provenance and other locking xattrs..."
xattr -cr "$DST" 2>/dev/null || true

echo "==> Re-signing main Dolphin executable without hardened runtime..."
codesign --force --sign - "$DST/Contents/MacOS/Dolphin"

echo "==> Verifying..."
FLAGS=$(codesign -d -vvv "$DST/Contents/MacOS/Dolphin" 2>&1 | grep "flags=" || true)
echo "    $FLAGS"
if echo "$FLAGS" | grep -q "runtime"; then
    echo "ERROR: hardened runtime still set — re-sign failed."
    exit 1
fi

cat <<EOF

==> Done. Use this Dolphin from now on:
    $DST/Contents/MacOS/Dolphin -e <patched-iso>

The original $SRC is unchanged.
EOF
