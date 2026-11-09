package feed

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestXScaleSampler: the hack's scale comes from the Dolphin's own user
// folder and render size, is measured again every xscaleEvery, and a
// setting change shows up.
func TestXScaleSampler(t *testing.T) {
	t.Setenv("WW_TAG_XSCALE", "")
	dir := t.TempDir()
	gfx := filepath.Join(dir, "Config", "GFX.ini")
	if err := os.MkdirAll(filepath.Dir(gfx), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(gfx, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[Settings]\nAspectRatio = 3\nwideScreenHack = True\n")
	procs := 0
	s := &xscaleSampler{pid: 7, scale: 1,
		proc: func(pid uint32) (string, []string, bool) { procs++; return dir, []string{"-u", dir}, pid == 7 },
		size: func(uint32) (int, int, bool) { return 3440, 1440, true }}
	t0 := time.Unix(1000, 0)
	want := (4.0 / 3.0) / (3440.0 / 1440.0)
	if got := s.Scale(t0, log.Discard{}); math.Abs(got-want) > 1e-9 {
		t.Fatalf("stretch 3440x1440: %v, want %v", got, want)
	}
	write("[Settings]\nAspectRatio = 3\nwideScreenHack = False\n")
	if got := s.Scale(t0.Add(time.Second), log.Discard{}); math.Abs(got-want) > 1e-9 {
		t.Fatalf("measured again before xscaleEvery: %v", got)
	}
	if got := s.Scale(t0.Add(xscaleEvery), log.Discard{}); got != 1 {
		t.Fatalf("hack turned off: %v", got)
	}
	if procs != 1 {
		t.Fatalf("process looked up %d times, want once", procs)
	}
	t.Setenv("WW_TAG_XSCALE", "0.5")
	if got := s.Scale(t0.Add(2*xscaleEvery), log.Discard{}); got != 0.5 {
		t.Fatalf("WW_TAG_XSCALE: %v", got)
	}
}

// TestXScaleSamplerProcRetry: a Dolphin whose process can't be looked up
// is looked up again only every xscaleProcRetry, not every tick.
func TestXScaleSamplerProcRetry(t *testing.T) {
	t.Setenv("WW_TAG_XSCALE", "")
	procs := 0
	s := &xscaleSampler{pid: 7, scale: 1,
		proc: func(uint32) (string, []string, bool) { procs++; return "", nil, false },
		size: func(uint32) (int, int, bool) { return 0, 0, false }}
	t0 := time.Unix(1000, 0)
	for i := 0; i < 10; i++ {
		if got := s.Scale(t0.Add(time.Duration(i)*xscaleEvery), log.Discard{}); got != 1 {
			t.Fatalf("unknown Dolphin: %v", got)
		}
	}
	if procs != 1 {
		t.Fatalf("looked up %d times in %v, want 1", procs, 9*xscaleEvery)
	}
	s.Scale(t0.Add(xscaleProcRetry+xscaleEvery), log.Discard{})
	if procs != 2 {
		t.Fatalf("looked up %d times after the retry wait, want 2", procs)
	}
}

// TestXScaleSamplerNative: in native widescreen (block mode 2) the scale is
// 0 whatever the hack says; back in mode 1 the hack's scale returns.
func TestXScaleSamplerNative(t *testing.T) {
	t.Setenv("WW_TAG_XSCALE", "")
	dir := t.TempDir()
	gfx := filepath.Join(dir, "Config", "GFX.ini")
	if err := os.MkdirAll(filepath.Dir(gfx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gfx, []byte("[Settings]\nAspectRatio = 1\nwideScreenHack = True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := mem.NewFakeMem()
	blk := make([]byte, mailbox.WsSize)
	mem.PutU32BE(blk[mailbox.WsMagicOff:], mailbox.WsMagic)
	blk[mailbox.WsModeOff] = mailbox.WsModeNative
	_ = m.Store(mailbox.WsAddr, blk)
	s := newXScaleSampler(7, m)
	s.proc = func(uint32) (string, []string, bool) { return dir, nil, true }
	s.size = func(uint32) (int, int, bool) { return 0, 0, false }
	t0 := time.Unix(1000, 0)
	if got := s.Scale(t0, log.Discard{}); got != 0 {
		t.Fatalf("mode 2: %v, want 0", got)
	}
	if b := (mailbox.VisBlock{XScale: 0}).Bytes(); mem.U32BE(b[mailbox.VisFlagsOff-mailbox.VisGoStart:])&mailbox.VisFlagsXScaleMask != 0 {
		t.Fatal("XScale 0 must encode as none")
	}
	_ = m.Store(mailbox.WsAddr+mailbox.WsModeOff, []byte{mailbox.WsModeClip})
	if got := s.Scale(t0.Add(xscaleEvery), log.Discard{}); got != 0.75 {
		t.Fatalf("mode 1 with the hack at 16:9: %v, want 0.75", got)
	}
}
