package gui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
)

// TestApplyPatchLineConvert: a converted disc's lines (internal/patch
// convert.go) move the view through "convert" to "patch".
func TestApplyPatchLineConvert(t *testing.T) {
	v := PatchView{}
	for _, l := range []string{
		"Wind Waker Multiplayer patcher",
		"  input : C:\\g\\ww.rvz",
		"  output: C:\\g\\ww-multiplayer.iso",
		"  convert: RVZ image -> plain ISO with C:\\Dolphin\\DolphinTool.exe",
	} {
		applyPatchLine(&v, l)
	}
	if v.Stage != "convert" || v.Converted != "RVZ" || v.Percent != 5 {
		t.Fatalf("converting: %+v", v)
	}
	// DolphinTool's own lines, and a free-space note, leave it there.
	for _, l := range []string{"  DolphinTool: 50%", "  convert: (free space in C:\\g unknown: x; trying anyway)"} {
		applyPatchLine(&v, l)
	}
	if v.Stage != "convert" || v.Converted != "RVZ" {
		t.Fatalf("tool lines: %+v", v)
	}
	applyPatchLine(&v, "  convert: done, patching the ISO it wrote (removed afterwards)")
	if v.Stage != "patch" || v.Percent != convertTo {
		t.Fatalf("converted: %+v", v)
	}
	applyPatchLine(&v, "  REL   : module 0x1 (x.rel) -> wwrel build 0x0, RELS.arc at 0x0 (+0 bytes)")
	if v.Percent != 86 || len(v.Lines) != 8 {
		t.Errorf("patched: %+v", v)
	}
	if creepFrom(5, convertTo, 0, convertCreep) != 5 || creepFrom(5, convertTo, time.Hour, convertCreep) > convertTo ||
		creepFrom(convertTo, 85, time.Hour, patchCreep) > 85 || creepFrom(convertTo, 85, patchCreep, patchCreep) <= convertTo {
		t.Error("creepFrom out of range")
	}
}

func TestPatchErrorConvert(t *testing.T) {
	nt := &patch.NoDolphinToolError{Format: &inject.UnsupportedFormatError{Format: "WIA"}, Dolphin: `C:\D\Dolphin.exe`}
	if s := patchError(fmt.Errorf("x: %w", nt)); !strings.Contains(s, "compressed WIA image") || !strings.Contains(s, `none beside C:\D\Dolphin.exe`) || !strings.Contains(s, "Convert File") {
		t.Errorf("no tool: %s", s)
	}
	nt.Dolphin = ""
	if s := patchError(nt); !strings.Contains(s, "no Dolphin was found yet") || !strings.Contains(s, "Set up Dolphin in the next step") {
		t.Errorf("no Dolphin: %s", s)
	}
	low := fmt.Errorf("%w: converting needs about 2.8 GB free in C:\\g and it has 1.0 GB", patch.ErrLowSpace)
	if s := patchError(low); !strings.HasPrefix(s, "There isn't enough free space next to your disc to convert it (converting needs about 2.8 GB") || strings.Contains(s, "not enough free space to convert") {
		t.Errorf("low space: %s", s)
	}
}

// converterSpy records the Converter Run was given.
type converterSpy struct {
	fakePatcher
	got []*patch.Converter
}

func (c *converterSpy) run(ctx context.Context, in, out string, w io.Writer, o patch.Options) (patch.Result, error) {
	c.got = append(c.got, o.Converter)
	return c.fakePatcher.run(ctx, in, out, w, o)
}

// TestBridgePatchConverter: an RVZ disc gets the DolphinTool beside the
// saved Dolphin, else beside the detected one; an ISO gets none.
func TestBridgePatchConverter(t *testing.T) {
	spy := &converterSpy{}
	b, _, _ := patchBridge(t, &spy.fakePatcher)
	b.patchRun = spy.run
	b.getenv = func(string) string { return "" }
	home, cfgDir := t.TempDir(), t.TempDir()
	b.denv = fakeEnv(home, cfgDir)
	dir := t.TempDir()
	rvz := filepath.Join(dir, "ww.rvz")
	if err := os.WriteFile(rvz, append([]byte("RVZ\x01"), make([]byte, 0x400)...), 0o644); err != nil {
		t.Fatal(err)
	}
	iso := touch(t, filepath.Join(dir, "ww.iso"))

	patchOnce := func(in string) *patch.Converter {
		t.Helper()
		if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
			t.Fatal(err)
		}
		waitPatchDone(t, b)
		return spy.got[len(spy.got)-1]
	}
	if c := patchOnce(iso); c != nil {
		t.Errorf("ISO got a converter: %+v", c)
	}
	// Nothing saved or found: a converter with no tool and no Dolphin.
	if c := patchOnce(rvz); c == nil || c.Tool.Path != "" || c.Dolphin != "" {
		t.Errorf("no Dolphin: %+v", c)
	}
	// The detected Dolphin (the Desktop zip build) and its DolphinTool.
	desk := touch(t, filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe"))
	deskTool := touch(t, filepath.Join(home, "Desktop", "Dolphin-x64", "DolphinTool.exe"))
	if c := patchOnce(rvz); c == nil || c.Dolphin != desk || c.Tool.Path != deskTool {
		t.Errorf("detected: %+v", c)
	}
	// The saved Dolphin wins, even with no DolphinTool beside it.
	saved := touch(t, filepath.Join(t.TempDir(), "Dolphin.exe"))
	b.mu.Lock()
	b.cfg.Dolphin.Exe = saved
	b.mu.Unlock()
	if c := patchOnce(rvz); c == nil || c.Dolphin != saved || c.Tool.Path != "" {
		t.Errorf("saved: %+v", c)
	}
}

// TestBridgeStartPatchPatchedRVZ: "I already have a patched ISO" with an
// RVZ says to choose it the other way.
func TestBridgeStartPatchPatchedRVZ(t *testing.T) {
	b, _, _ := patchBridge(t, &fakePatcher{})
	rvz := filepath.Join(t.TempDir(), "ww.rvz")
	if err := os.WriteFile(rvz, append([]byte("RVZ\x01"), make([]byte, 0x400)...), 0o644); err != nil {
		t.Fatal(err)
	}
	err := b.StartPatch(PatchRequest{Path: rvz, Patched: true})
	if err == nil || !strings.Contains(err.Error(), "compressed RVZ image: choose it with “Choose your disc”") {
		t.Errorf("patched RVZ: %v", err)
	}
}
