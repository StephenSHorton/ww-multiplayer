#!/bin/bash
# Builds the macOS release download, ww-multiplayer-macos.zip:
#
#   WW Multiplayer/                 (the folder the zip unpacks to)
#     WW Multiplayer.app            the app: double-click it. Its executable,
#                                   Contents/MacOS/ww-multiplayer, is a
#                                   universal binary (Apple Silicon + Intel)
#                                   with the app window compiled in
#                                   (-tags desktop,production; internal/gui,
#                                   Wails v2). No arguments = the window.
#     ww-multiplayer                symlink to that binary, for the terminal
#                                   menu (`tui`) and the CLI
#     scripts/setup-mac-dolphin.sh  the one-time Dolphin step, for CLI users
#                                   (the app's Setup has a Prepare Dolphin
#                                   button that does the same)
#     LICENSE
#
# The zip is made with `ditto -c -k --keepParent`, which keeps the bundle
# structure, the symlink, the +x bits and the code signature intact (a
# plain `zip` can drop extended attributes and resource forks).
#
# Signing: by default the bundle is ad-hoc signed with the
# com.apple.security.cs.debugger entitlement (scripts/cs-debugger.entitlements)
# so AMFI permits task_for_pid against a Dolphin whose hardened runtime has
# been stripped (Prepare Dolphin / setup-mac-dolphin.sh). Set
# MACOS_SIGN_IDENTITY to a "Developer ID Application: ..." identity in the
# keychain and it signs with that instead, with the hardened runtime and a
# secure timestamp, ready for notarization (release.yml does that only
# when its signing secrets exist; see the README's "Code signing").
#
# Outputs into ./dist/. cgo is required (mach_vm_* lives in C) so this
# script must run on macOS: Linux/Windows runners can't cross-build it.
#
# Usage: scripts/build-mac.sh [version]
#   version   string baked into `ww-multiplayer version` (default: "dev");
#             a tag like v0.4.0 or v0.4.0-rc1 also sets the bundle's
#             CFBundleShortVersionString (0.4.0). Anything else gives 0.0.0.

set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

APP_NAME="WW Multiplayer"
BUNDLE_ID="com.stephenhorton.ww-multiplayer"
SIGN_IDENTITY="${MACOS_SIGN_IDENTITY:--}"
# The oldest macOS the app runs on. Go 1.26 (go.mod) needs macOS 12
# Monterey, so this one value sets the cgo deployment target, the linker's
# -mmacosx-version-min and the bundle's LSMinimumSystemVersion together.
MACOS_MIN="12.0"

DIST="$ROOT/dist"
STAGE="$DIST/$APP_NAME"
APP="$STAGE/$APP_NAME.app"
ZIP="$DIST/ww-multiplayer-macos.zip"
ENT="$ROOT/scripts/cs-debugger.entitlements"
rm -rf "$DIST"
mkdir -p "$DIST"

# CFBundleShortVersionString must be numeric (major.minor.patch): v0.4.0
# and v0.4.0-rc1 give 0.4.0, a branch name or "dev" gives 0.0.0.
PLIST_VERSION="0.0.0"
if [[ "$VERSION" =~ ^v?([0-9]+(\.[0-9]+){0,2}) ]]; then
    PLIST_VERSION="${BASH_REMATCH[1]}"
fi

# Per-arch builds. Apple Silicon hosts cross-build amd64 by pointing cgo
# at the macOS SDK with an x86_64 target triple: clang/lipo support both.
SDK="$(xcrun --sdk macosx --show-sdk-path)"
LDFLAGS="-s -w -X main.version=${VERSION}"
export MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN"
# desktop,production compile the app window in (Wails v2 won't start a
# window without production; without the tags the binary is CLI + TUI).
# Wails v2's darwin frontend uses UTType (any macOS 11+ SDK) but its cgo
# LDFLAGS don't link UniformTypeIdentifiers; `wails build` adds this
# itself, so a plain `go build` must too or the link fails on
# _OBJC_CLASS_$_UTType. (Vet and test don't link.)
WAILS_LDFLAGS="-framework UniformTypeIdentifiers -mmacosx-version-min=${MACOS_MIN}"

echo "==> Building arm64..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    CGO_LDFLAGS="$WAILS_LDFLAGS" \
    go build -trimpath -tags desktop,production -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-arm64" ./cmd/ww-multiplayer

echo "==> Building amd64..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 \
    SDKROOT="$SDK" \
    CC="clang -target x86_64-apple-macos${MACOS_MIN} -isysroot $SDK" \
    CGO_LDFLAGS="$WAILS_LDFLAGS" \
    go build -trimpath -tags desktop,production -ldflags="$LDFLAGS" -o "$DIST/ww-multiplayer-amd64" ./cmd/ww-multiplayer

echo "==> Building $APP_NAME.app (version $VERSION, bundle $PLIST_VERSION)..."
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
lipo -create \
    -output "$APP/Contents/MacOS/ww-multiplayer" \
    "$DIST/ww-multiplayer-arm64" \
    "$DIST/ww-multiplayer-amd64"
rm "$DIST/ww-multiplayer-arm64" "$DIST/ww-multiplayer-amd64"
file "$APP/Contents/MacOS/ww-multiplayer"

# docs/img/icon.icns is generated from docs/img/icon.ico (go generate;
# tools/mkicns), so Windows and macOS show the same icon.
cp "$ROOT/docs/img/icon.icns" "$APP/Contents/Resources/icon.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleExecutable</key>
    <string>ww-multiplayer</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIconFile</key>
    <string>icon.icns</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${PLIST_VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${PLIST_VERSION}</string>
    <key>CFBundleGetInfoString</key>
    <string>${APP_NAME} ${VERSION}</string>
    <key>LSApplicationCategoryType</key>
    <string>public.app-category.games</string>
    <key>LSMinimumSystemVersion</key>
    <string>${MACOS_MIN}</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSHumanReadableCopyright</key>
    <string>MIT licensed. Not affiliated with Nintendo.</string>
</dict>
</plist>
PLIST
plutil -lint "$APP/Contents/Info.plist"

# Sign the executable, then the bundle (no --deep needed: one binary).
# Both carry cs.debugger, so the window's sessions can read Dolphin's
# memory. A Developer ID identity adds the hardened runtime and a
# timestamp, which notarization requires.
SIGN_FLAGS=(--force --sign "$SIGN_IDENTITY" --entitlements "$ENT")
if [ "$SIGN_IDENTITY" != "-" ]; then
    echo "==> Signing with a Developer ID identity (hardened runtime)..."
    SIGN_FLAGS+=(--options runtime --timestamp)
else
    echo "==> Ad-hoc signing (MACOS_SIGN_IDENTITY not set)..."
fi
codesign "${SIGN_FLAGS[@]}" "$APP/Contents/MacOS/ww-multiplayer"
codesign "${SIGN_FLAGS[@]}" "$APP"
codesign --verify --strict --verbose=2 "$APP"
ENTS="$(codesign -d --entitlements - "$APP" 2>&1)"
[[ "$ENTS" == *cs.debugger* ]] \
    || { echo "ERROR: $APP lacks the cs.debugger entitlement"; exit 1; }

# CLI users get a top-level `ww-multiplayer` (a symlink into the bundle,
# so the download carries one copy of the binary), the Dolphin script at
# the path the README uses, and the MIT notice.
ln -s "$APP_NAME.app/Contents/MacOS/ww-multiplayer" "$STAGE/ww-multiplayer"
mkdir -p "$STAGE/scripts"
cp "$ROOT/scripts/setup-mac-dolphin.sh" "$STAGE/scripts/setup-mac-dolphin.sh"
chmod +x "$STAGE/scripts/setup-mac-dolphin.sh"
cp "$ROOT/LICENSE" "$STAGE/LICENSE"

echo "==> Zipping with ditto..."
ditto -c -k --keepParent "$STAGE" "$ZIP"

# Unpack the zip as a user would and check what they get: the bundle's
# layout and signature survive, and the symlink runs the binary.
CHECK="$(mktemp -d)"
trap 'rm -rf "$CHECK"' EXIT
ditto -x -k "$ZIP" "$CHECK"
U="$CHECK/$APP_NAME"
for f in "$APP_NAME.app/Contents/Info.plist" \
         "$APP_NAME.app/Contents/MacOS/ww-multiplayer" \
         "$APP_NAME.app/Contents/Resources/icon.icns" \
         "scripts/setup-mac-dolphin.sh" "LICENSE"; do
    [ -e "$U/$f" ] || { echo "ERROR: $ZIP is missing $f"; exit 1; }
done
[ -L "$U/ww-multiplayer" ] || { echo "ERROR: $ZIP lost the ww-multiplayer symlink"; exit 1; }
[ -x "$U/scripts/setup-mac-dolphin.sh" ] || { echo "ERROR: setup-mac-dolphin.sh lost its +x bit"; exit 1; }
codesign --verify --strict "$U/$APP_NAME.app"
"$U/ww-multiplayer" version

echo
echo "==> Built:"
echo "    $APP   (the app; signed with cs.debugger)"
echo "    $ZIP"
echo "      (unpacks to \"$APP_NAME/\": the app, a ww-multiplayer CLI symlink,"
echo "       scripts/setup-mac-dolphin.sh and LICENSE)"
