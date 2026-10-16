#!/bin/bash
# Builds the macOS release artifacts:
#   1. A universal binary (`ww-multiplayer`) that runs natively on both
#      Apple Silicon and Intel Macs — produced by lipo'ing per-arch builds,
#      then ad-hoc signed with the `com.apple.security.cs.debugger`
#      entitlement so AMFI permits task_for_pid against Dolphin.
#   2. A `WW Multiplayer.app` bundle whose executable is the same binary:
#      double-clicked, it opens the app window (no arguments = the GUI;
#      internal/gui, Wails v2, compiled in by -tags desktop,production).
#      No sudo prompt (works against a non-hardened Dolphin — see
#      scripts/setup-mac-dolphin.sh).
#   3. `ww-multiplayer-macos.tar.gz` (the release download): both of the
#      above plus scripts/setup-mac-dolphin.sh and LICENSE.
#
# Outputs into ./dist/. cgo is required (mach_vm_* lives in C) so this
# script must run on macOS — Linux/Windows runners can't cross-build it.
#
# Usage: scripts/build-mac.sh [version]
#   version   string baked into `--version` output (default: "dev")

set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DIST="$ROOT/dist"
ENT="$ROOT/scripts/cs-debugger.entitlements"
rm -rf "$DIST"
mkdir -p "$DIST"

# Per-arch builds. Apple Silicon hosts cross-build amd64 by pointing cgo
# at the macOS SDK with an x86_64 target triple — clang/lipo support both.
SDK="$(xcrun --sdk macosx --show-sdk-path)"
LDFLAGS="-s -w -X main.version=${VERSION}"
# desktop,production compile the app window in (Wails v2 won't start a
# window without production; without the tags the binary is CLI + TUI).
# Wails v2's darwin frontend uses UTType (any macOS 11+ SDK) but its cgo
# LDFLAGS don't link UniformTypeIdentifiers; `wails build` adds this
# itself, so a plain `go build` must too or the link fails on
# _OBJC_CLASS_$_UTType. (Vet and test don't link.)
WAILS_LDFLAGS="-framework UniformTypeIdentifiers -mmacosx-version-min=10.13"

echo "==> Building arm64..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    CGO_LDFLAGS="$WAILS_LDFLAGS" \
    go build -trimpath -tags desktop,production -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-arm64" .

echo "==> Building amd64..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    CC="clang -target x86_64-apple-macos11 -isysroot $SDK" \
    CGO_LDFLAGS="$WAILS_LDFLAGS" \
    go build -trimpath -tags desktop,production -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-amd64" .

echo "==> Combining into universal binary..."
lipo -create \
    -output "$DIST/ww-multiplayer" \
    "$DIST/ww-multiplayer-arm64" \
    "$DIST/ww-multiplayer-amd64"
rm "$DIST/ww-multiplayer-arm64" "$DIST/ww-multiplayer-amd64"
file "$DIST/ww-multiplayer"

# Ad-hoc signing the binary with cs.debugger lets AMFI permit
# task_for_pid against non-hardened processes (which is what Dolphin
# becomes after running setup-mac-dolphin.sh once). Without this, even
# sudo can't read Dolphin's memory under SIP.
echo "==> Code-signing with cs.debugger entitlement..."
codesign -s - --entitlements "$ENT" --force "$DIST/ww-multiplayer"
codesign -d --entitlements - "$DIST/ww-multiplayer" 2>&1 | grep -A1 "cs.debugger" || true

echo "==> Building WW Multiplayer.app bundle..."
APP="$DIST/WW Multiplayer.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# The binary is the bundle's executable: Finder runs it with no arguments,
# which opens the app window. (It used to be an AppleScript launcher that
# opened the TUI in Terminal; `ww-multiplayer tui` still does that from a
# shell.)
cp "$DIST/ww-multiplayer" "$APP/Contents/MacOS/ww-multiplayer"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>ww-multiplayer</string>
    <key>CFBundleIdentifier</key>
    <string>com.stephenhorton.ww-multiplayer</string>
    <key>CFBundleName</key>
    <string>WW Multiplayer</string>
    <key>CFBundleDisplayName</key>
    <string>WW Multiplayer</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSMinimumSystemVersion</key>
    <string>12.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

# Sign the bundle (its executable gets the cs.debugger entitlement too, so
# the window's sessions can read Dolphin's memory).
codesign -s - --entitlements "$ENT" --force "$APP"
codesign -d --entitlements - "$APP" 2>&1 | grep -A1 "cs.debugger" || true

# The README's first-run steps run ./scripts/setup-mac-dolphin.sh from
# the untarred folder, so ship it at that same relative path, plus the
# MIT notice that has to travel with the binaries.
mkdir -p "$DIST/scripts"
cp "$ROOT/scripts/setup-mac-dolphin.sh" "$DIST/scripts/setup-mac-dolphin.sh"
chmod +x "$DIST/scripts/setup-mac-dolphin.sh"
cp "$ROOT/LICENSE" "$DIST/LICENSE"

# Tarball for release upload (preserves +x bits across download).
TARBALL="$DIST/ww-multiplayer-macos.tar.gz"
( cd "$DIST" && tar -czf "$TARBALL" "ww-multiplayer" "WW Multiplayer.app" \
    "scripts/setup-mac-dolphin.sh" "LICENSE" )
LISTING="$(tar -tzf "$TARBALL")"
for f in "ww-multiplayer" "scripts/setup-mac-dolphin.sh" "LICENSE"; do
    grep -qx "$f" <<<"$LISTING" || { echo "ERROR: $TARBALL is missing $f"; exit 1; }
done

echo
echo "==> Built:"
echo "    $DIST/ww-multiplayer       (universal binary, signed with cs.debugger)"
echo "    $DIST/WW Multiplayer.app   (the app window; Finder-clickable)"
echo "    $TARBALL"
echo "      (also carries scripts/setup-mac-dolphin.sh and LICENSE)"
echo
echo "==> First-time macOS setup: run scripts/setup-mac-dolphin.sh once"
echo "    to copy /Applications/Dolphin.app to ~/Applications/ and strip"
echo "    the hardened runtime so this binary can read its memory."
