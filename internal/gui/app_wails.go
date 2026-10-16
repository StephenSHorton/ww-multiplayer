//go:build (windows || darwin) && desktop && production

package gui

import (
	"context"
	"path/filepath"
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
// session runs asks first, then leaves the session (the same teardown as
// Ctrl+C in the CLI). An error before the window opened (no WebView2, for
// one) leaves the console as it was, so the caller can fall back to the
// TUI in it.
func Run(o Options) error {
	assets, err := Frontend()
	if err != nil {
		return err
	}
	b := NewBridge(o)
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
			b.pump(ctx, func(ev EventView) { wruntime.EventsEmit(ctx, eventName, ev) })
		},
		OnBeforeClose: func(ctx context.Context) bool {
			if !b.running() {
				return false
			}
			ans, err := wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{
				Type:          wruntime.QuestionDialog,
				Title:         "Leave the session?",
				Message:       "Closing the window leaves the session for everyone you are playing with.",
				Buttons:       []string{"Leave", "Stay"},
				DefaultButton: "Stay",
				CancelButton:  "Stay",
			})
			if err != nil {
				return false
			}
			return ans != "Leave" && ans != "Yes" // Windows shows Yes / No
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

// webviewDataDir keeps WebView2's profile next to the app's config rather
// than in a folder named after the exe; "" is WebView2's default.
func webviewDataDir(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "webview")
}
