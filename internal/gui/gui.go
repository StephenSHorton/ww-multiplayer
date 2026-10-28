// Package gui is the desktop app's window (App PR 5, design §4.3): a Wails
// v2 window on Windows and macOS whose HTML/CSS/JS frontend (frontend/,
// embedded) drives a session.Controller through Bridge, the one Go type
// the frontend calls.
//
// The frontend reaches Go through one JS module (frontend/transport.js):
// in the window it calls Wails' bound Bridge methods and listens for the
// "ww:event" event; served by `ww-multiplayer ui --browser` (ServeBrowser,
// pure Go, every OS) it posts the same calls over HTTP and reads the same
// events as server-sent events. Both paths call Bridge's methods by name
// with JSON arguments, so the frontend can't tell them apart.
//
// Build tags: the Wails window is compiled only for windows or darwin with
// both `desktop` and `production` set (`go build -tags desktop,production`,
// what release.yml and scripts/build-mac.sh pass). Any other
// build (a plain `go build`, Linux) gets a stub
// whose Run returns ErrNoGUI, so the binary still works as the CLI and the
// TUI, never imports Wails, and needs no webkit or cgo for it. Wails v2
// itself refuses to start a window without `production` (its default build
// only shows "Wails applications will not build without the correct build
// tags"), which is why the stub, not Wails, answers for an untagged build.
package gui

import (
	"errors"
	"io"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// ErrNoGUI is Run's error in a build without the window (see the package
// comment); the caller falls back to the TUI.
var ErrNoGUI = errors.New("this build has no desktop window")

// Title is the window's title.
const Title = "Wind Waker Multiplayer"

// Options configures Run and ServeBrowser.
type Options struct {
	// Version is the app version the UI shows (main.version).
	Version string
	// Controller runs the sessions; the GUI drives it like the TUI does.
	Controller *session.Controller
	// ConfigPath is the app's config file (internal/config): the name,
	// color and recent hosts the forms start from and save. "" is
	// config.Path(), the app's own; when that has no directory either,
	// nothing is kept.
	ConfigPath string
	// Getenv reads the environment overrides config.WithEnv applies
	// (WW_COLOR, ...); nil is no overrides.
	Getenv func(string) string
	// PostPatch is the setup's patch.Options.PostPatch: the loaded mods'
	// post-patch steps, the ones the `patch` CLI runs. Co-op's
	// (stagetables.CacheForPatch) writes the line that becomes the
	// "Co-op tables built" check row. None skips them.
	PostPatch []func(w io.Writer, image string)
	// DolphinEnv is what the setup's Dolphin detection looks at; nil is
	// launch.Host (this machine).
	DolphinEnv func() launch.Env
	// HostIPs lists this machine's addresses for the host's share card
	// (package main's listHostIPs, what the CLI's host prints); nil lists
	// none.
	HostIPs func() []string
	// Mods is the app's mod runtime (package main's startApp): the Mods
	// page lists its mods and CallMod runs their commands. Nil: the page
	// lists none.
	Mods *wwapi.Runtime
	// ModFlag is what this run's mod flags (--mods, --no-mod) say about
	// a mod, for the page to say why it is on or off; nil says nothing.
	ModFlag func(id string) (on, set bool)
	// DevMods says this run lists the dev mods (--dev).
	DevMods bool
	// Args are this run's arguments (os.Args[1:]): an update starts the
	// new version with them. Nil is os.Args[1:].
	Args []string
}
