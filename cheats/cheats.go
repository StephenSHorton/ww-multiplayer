// Package cheats is the curated Gecko code pack shipped in the repo,
// GZLE01.ini, baked into both binaries so the released ww-multiplayer.exe
// (the app's Dolphin launch) and wwdev (dolphin2) can drop it into
// Dolphin user dirs without the source tree alongside them.
package cheats

import _ "embed"

// GZLE01 is cheats/GZLE01.ini.
//
//go:embed GZLE01.ini
var GZLE01 []byte
