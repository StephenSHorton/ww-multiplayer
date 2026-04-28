#!/bin/bash
# Builds the macOS release artifacts:
#   1. A universal binary (`ww-multiplayer`) that runs natively on both
#      Apple Silicon and Intel Macs — produced by lipo'ing per-arch builds.
#   2. A minimal `WW Multiplayer.app` bundle that double-clicks open into
#      Terminal.app and `sudo`-runs the binary, since reading Dolphin's
#      process memory needs root on macOS (task_for_pid is gated by SIP).
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
rm -rf "$DIST"
mkdir -p "$DIST"

# Per-arch builds. Apple Silicon hosts cross-build amd64 by pointing cgo
# at the macOS SDK with an x86_64 target triple — clang/lipo support both.
SDK="$(xcrun --sdk macosx --show-sdk-path)"
LDFLAGS="-s -w -X main.version=${VERSION}"

echo "==> Building arm64..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    go build -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-arm64" .

echo "==> Building amd64..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    CC="clang -target x86_64-apple-macos11 -isysroot $SDK" \
    go build -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-amd64" .

echo "==> Combining into universal binary..."
lipo -create \
    -output "$DIST/ww-multiplayer" \
    "$DIST/ww-multiplayer-arm64" \
    "$DIST/ww-multiplayer-amd64"
rm "$DIST/ww-multiplayer-arm64" "$DIST/ww-multiplayer-amd64"
file "$DIST/ww-multiplayer"

echo "==> Building WW Multiplayer.app bundle..."
APP="$DIST/WW Multiplayer.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$DIST/ww-multiplayer" "$APP/Contents/Resources/ww-multiplayer"

# launcher is the bundle's CFBundleExecutable. Finder runs it on
# double-click; we then hand off to Terminal.app so the TUI has a real
# tty. `sudo` is required because task_for_pid is denied without
# either root or the com.apple.security.cs.debugger entitlement
# (which itself requires Apple Developer signing).
cat > "$APP/Contents/MacOS/launcher" <<'LAUNCHER'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
BIN="$DIR/../Resources/ww-multiplayer"
osascript <<APPLESCRIPT
tell application "Terminal"
    activate
    do script "echo 'Wind Waker Multiplayer requires sudo to read Dolphin process memory on macOS.' && sudo '$BIN'"
end tell
APPLESCRIPT
LAUNCHER
chmod +x "$APP/Contents/MacOS/launcher"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>launcher</string>
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
    <string>11.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

# Tarball for release upload (preserves +x bits across download).
TARBALL="$DIST/ww-multiplayer-macos.tar.gz"
( cd "$DIST" && tar -czf "$TARBALL" "ww-multiplayer" "WW Multiplayer.app" )

echo
echo "==> Built:"
echo "    $DIST/ww-multiplayer       (universal binary; run with 'sudo')"
echo "    $DIST/WW Multiplayer.app   (Finder-clickable wrapper)"
echo "    $TARBALL"
