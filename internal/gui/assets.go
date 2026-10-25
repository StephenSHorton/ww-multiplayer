package gui

import "embed"

// frontendFS is the UI: plain HTML/CSS/ES modules, no build step (design
// §4.2), styled after the game (the pause menu's item slots and SAVE
// button, owner decision 2026-10-16, on a Great Sea sidebar with light and
// dark themes since 2026-10-25; CSS only, no game assets), with the
// site's fonts vendored under
// frontend/fonts (SIL OFL 1.1, frontend/fonts/OFL.txt) so the window works
// offline.
//
//go:embed frontend
var frontendFS embed.FS
