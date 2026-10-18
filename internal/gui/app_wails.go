//go:build (windows || darwin) && desktop && production

package gui

import (
	"context"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// eventName is the Wails event the frontend's transport listens on.
const eventName = "ww:event"

// Available reports whether this build has the desktop window.
func Available() bool { return true }

// Run opens the window and returns when it has closed. Closing it while a
// session runs or a disc is being patched asks first (Bridge.beforeClose),
// then leaves the session (the same teardown as Ctrl+C in the CLI) and
// stops the patch. An error before the window opened (no WebView2, for
// one) leaves the console as it was, so the caller can fall back to the
// TUI in it.
func Run(o Options) error {
	assets, err := Frontend()
	if err != nil {
		return err
	}
	b := NewBridge(o)
	// Last: an update's new version starts once this window is gone.
	defer b.relaunchNow()
	defer b.close()

	hidden := hideConsole()
	var opened atomic.Bool
	opts := &options.App{
		Title:            Title,
		Width:            1100,
		Height:           790,
		MinWidth:         760,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 0x05, G: 0x2c, B: 0x5e, A: 0xff}, // --sea-dark
		AssetServer:      &assetserver.Options{Assets: assets},
		Bind:             []interface{}{b},
		OnStartup: func(ctx context.Context) {
			opened.Store(true)
			if hidden {
				freeConsole()
			}
			b.setDialogs(wailsDialogs{ctx})
			b.setDesktop(func(s string) error { return wruntime.ClipboardSetText(ctx, s) },
				func(u string) error { wruntime.BrowserOpenURL(ctx, u); return nil })
			b.setQuit(func() { wruntime.Quit(ctx) }, false)
			b.pump(ctx, func(ev EventView) { wruntime.EventsEmit(ctx, eventName, ev) })
		},
		OnBeforeClose: func(ctx context.Context) bool {
			return b.beforeClose(func(q string) bool { return askClose(ctx, q) }, func() { wruntime.Quit(ctx) })
		},
		OnShutdown: func(context.Context) { b.close() },
		Windows: &windows.Options{
			WebviewUserDataPath: webviewDataDir(b.cfgPath),
		},
	}
	err = wails.Run(opts)
	if err != nil && hidden && !opened.Load() {
		showConsole()
	}
	return err
}

// askClose asks one of beforeClose's questions with a native dialog and
// says whether the answer is to close anyway.
func askClose(ctx context.Context, q string) bool {
	o := wruntime.MessageDialogOptions{Type: wruntime.QuestionDialog}
	closeWord := ""
	switch q {
	case askStopPatch:
		o.Title = "Stop patching?"
		o.Message = "Your disc is still being patched. Close anyway? The patch stops. If your disc is already being written, " +
			"the window stays open until that is finished (a few seconds to a few minutes), then the new disc is removed and the app closes."
		o.Buttons, o.DefaultButton, o.CancelButton = []string{"Close", "Keep patching"}, "Keep patching", "Keep patching"
		closeWord = "Close"
	case askLeaveSession:
		o.Title = "Leave the session?"
		o.Message = "Closing the window leaves the session for everyone you are playing with."
		o.Buttons, o.DefaultButton, o.CancelButton = []string{"Leave", "Stay"}, "Stay", "Stay"
		closeWord = "Leave"
	}
	ans, err := wruntime.MessageDialog(ctx, o)
	if err != nil {
		// As before: a failed "Stop patching?" keeps the patch; a failed
		// "Leave the session?" closes.
		return q == askLeaveSession
	}
	return ans == closeWord || ans == "Yes" // Windows shows Yes / No
}

// webviewDataDir keeps WebView2's profile next to the app's config rather
// than in a folder named after the exe; "" is WebView2's default.
func webviewDataDir(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "webview")
}

// wailsDialogs is the window's native file dialogs (Bridge.PickFile).
type wailsDialogs struct{ ctx context.Context }

func (d wailsDialogs) Open(kind, title, dir string) (string, error) {
	o := wruntime.OpenDialogOptions{Title: title, DefaultDirectory: dir}
	switch kind {
	case "userdir":
		o.CanCreateDirectories = true
		return wruntime.OpenDirectoryDialog(d.ctx, o)
	case "iso":
		o.Filters = []wruntime.FileFilter{
			{DisplayName: "Wind Waker disc image (*.iso, *.ciso)", Pattern: "*.iso;*.ciso;*.gcm"},
			{DisplayName: "All files", Pattern: "*.*"},
		}
	case "dolphin":
		if runtime.GOOS == "darwin" {
			o.Filters = []wruntime.FileFilter{{DisplayName: "Dolphin (Dolphin.app)", Pattern: "*.app"}}
		} else {
			o.Filters = []wruntime.FileFilter{{DisplayName: "Dolphin (Dolphin.exe)", Pattern: "*.exe"}}
		}
	}
	return wruntime.OpenFileDialog(d.ctx, o)
}
