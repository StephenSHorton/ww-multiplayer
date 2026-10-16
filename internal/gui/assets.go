package gui

import "embed"

// frontendFS is the UI: plain HTML/CSS/ES modules, no build step (design
// §4.2), styled after the game's pause menu (owner decision, 2026-10-16;
// CSS only, no game assets), with the site's fonts vendored under
// frontend/fonts (SIL OFL 1.1, frontend/fonts/OFL.txt) so the window works
// offline.
//
//go:embed frontend
var frontendFS embed.FS
