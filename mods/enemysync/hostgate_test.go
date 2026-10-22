package enemysync

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// levelLog records each line with its level.
type levelLog struct{ lines []levelLine }

type levelLine struct {
	level log.Level
	msg   string
}

func (l *levelLog) Log(level log.Level, msg string) { l.lines = append(l.lines, levelLine{level, msg}) }

func (l *levelLog) count(level log.Level) int {
	n := 0
	for _, x := range l.lines {
		if x.level == level {
			n++
		}
	}
	return n
}

// TestHostGateLogsTransition: a REL of another build gates the host's
// hits with one Warn in checkREL's words, not one per check; the build
// linked again is one OK line, and the gate opens.
func TestHostGateLogsTransition(t *testing.T) {
	m := mem.NewFakeMem()
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	setREL := func(r mailbox.REL) {
		t.Helper()
		if err := mailbox.WriteREL(gameWriter{m}, r); err != nil {
			t.Fatal(err)
		}
	}
	other := inject.RELBuildID ^ 0xFFFF
	setREL(mailbox.REL{Linked: true, BuildID: other, Frames: 1})
	rec := &levelLog{}
	g := hostGate{rep: rec}
	now := time.Unix(1000, 0)
	for i := 0; i < 5; i++ {
		if g.check(m, now) {
			t.Fatal("gate open with another build's REL")
		}
		now = now.Add(ContractEvery)
	}
	if n := len(rec.lines); n != 1 || rec.count(log.Warn) != 1 {
		t.Fatalf("mismatch logged %d lines (%v), want one Warn", n, rec.lines)
	}
	if msg := rec.lines[0].msg; !strings.Contains(msg, "the linked REL module is build") || !strings.Contains(msg, "host hits wait") {
		t.Errorf("Warn %q, want the build mismatch and that hits wait", msg)
	}

	setREL(mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 2})
	for i := 0; i < 5; i++ {
		if !g.check(m, now) {
			t.Fatal("gate shut with this build's REL")
		}
		now = now.Add(ContractEvery)
	}
	if n := len(rec.lines); n != 2 || rec.count(log.OK) != 1 {
		t.Fatalf("recovery logged %v, want one OK line after the Warn", rec.lines)
	}

	setREL(mailbox.REL{})
	g.check(m, now)
	g.check(m, now.Add(ContractEvery))
	if n := len(rec.lines); n != 3 || !strings.Contains(rec.lines[2].msg, "not linked") {
		t.Fatalf("unlinked logged %v, want one more Warn saying not linked", rec.lines)
	}
}

// TestRemoteStepLabel: the start-of-session lines name a step preset, the
// raw flags otherwise, and "off" for no block.
func TestRemoteStepLabel(t *testing.T) {
	step := func(k int) uint16 {
		var f uint16
		for i := 0; i <= k; i++ {
			f |= remoteSteps[i]
		}
		return f
	}
	for _, c := range []struct {
		cfg  RemoteConfig
		want string
	}{
		{RemoteConfig{}, "off"},
		{RemoteConfig{On: true, Flags: step(6)}, "step6"},
		{RemoteConfig{On: true, Flags: step(7)}, "step7"},
		{RemoteConfig{On: true}, "step0"},
		{RemoteConfig{On: true, Flags: mailbox.RemoteFlagApply}, fmt.Sprintf("flags 0x%04X", mailbox.RemoteFlagApply)},
	} {
		if got := remoteStepLabel(c.cfg); got != c.want {
			t.Errorf("remoteStepLabel(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}
