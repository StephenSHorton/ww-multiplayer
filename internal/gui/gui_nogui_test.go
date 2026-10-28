//go:build !((windows || darwin) && desktop && production)

package gui

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// TestNoGUIStub is the build without the window: a native Linux build,
// and any build without -tags desktop,production. Run must refuse at once
// with ErrNoGUI (main then opens the TUI) without touching the controller.
func TestNoGUIStub(t *testing.T) {
	if Available() {
		t.Fatal("Available() in a build without the window")
	}
	ctrl := session.New(nil, session.Options{})
	err := Run(Options{Version: "dev", Controller: ctrl})
	if !errors.Is(err, ErrNoGUI) {
		t.Fatalf("Run = %v, want ErrNoGUI", err)
	}
	if st := ctrl.Snapshot(); st.Session != 0 {
		t.Fatalf("Run started a session: %+v", st)
	}
}
