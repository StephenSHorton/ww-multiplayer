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

# Both bundles share the same CFBundleIdentifier upstream
# (org.dolphin-emu.dolphin), so LaunchServices treats them as a single
# app and silently re-routes any launch of the user copy to whichever
# is currently registered as canonical (typically /Applications). The
# child process then runs out of the hardened-runtime binary at
# /Applications and AMFI denies task_for_pid against it. Giving the
# user copy a distinct bundle ID breaks the LS dedup tie.
NEW_ID="org.dolphin-emu.dolphin-unhardened"
echo "==> Setting CFBundleIdentifier to $NEW_ID (avoids LaunchServices tie with /Applications)..."
plutil -replace CFBundleIdentifier -string "$NEW_ID" "$DST/Contents/Info.plist"

echo "==> Re-signing main Dolphin executable without hardened runtime..."
codesign --force --sign - "$DST/Contents/MacOS/Dolphin"

# Force LaunchServices to re-register the user copy under its new
# bundle ID so future bundle-ID lookups don't silently fall back to
# /Applications.
echo "==> Registering with LaunchServices..."
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
"$LSREGISTER" -f "$DST" || true

echo "==> Verifying..."
FLAGS=$(codesign -d -vvv "$DST/Contents/MacOS/Dolphin" 2>&1 | grep "flags=" || true)
echo "    $FLAGS"
if echo "$FLAGS" | grep -q "runtime"; then
    echo "ERROR: hardened runtime still set — re-sign failed."
    exit 1
fi
ACTUAL_ID=$(plutil -extract CFBundleIdentifier raw "$DST/Contents/Info.plist")
if [ "$ACTUAL_ID" != "$NEW_ID" ]; then
    echo "ERROR: bundle ID is $ACTUAL_ID, expected $NEW_ID"
    exit 1
fi

cat <<EOF

==> Done. Use this Dolphin from now on:
    $DST/Contents/MacOS/Dolphin -e <patched-iso>

The original $SRC is unchanged.
EOF
