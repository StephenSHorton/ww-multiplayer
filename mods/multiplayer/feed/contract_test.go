package feed

import (
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

type lines struct{ got []string }

func (l *lines) Log(_ log.Level, msg string) { l.got = append(l.got, msg) }

// TestContractGate: puppet-sync writes only into a game the strict
// contract check allows. A save state from before E1 (no WWMB header)
// is refused with the reason and the re-patch advice, logged once; the
// check runs again after ContractEvery, and the game running this
// version's mod again is "regained" (the caller re-arms then).
func TestContractGate(t *testing.T) {
	m := mem.NewFakeMem()
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	var l lines
	g := newContractGate(m, &l)
	t0 := time.Unix(1000, 0)
	if ok, regained := g.check(t0); !ok || regained {
		t.Fatalf("booted game: ok %v regained %v", ok, regained)
	}
	if len(l.got) != 1 || !strings.Contains(l.got[0], "writes allowed") {
		t.Errorf("allowed: logged %q", l.got)
	}

	// An old save state: no header. Not seen until ContractEvery passed.
	if err := m.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := g.check(t0.Add(time.Second)); !ok {
		t.Error("checked again before ContractEvery")
	}
	if ok, _ := g.check(t0.Add(ContractEvery)); ok {
		t.Fatal("old save state allowed")
	}
	if ok, _ := g.check(t0.Add(3 * ContractEvery)); ok {
		t.Fatal("old save state allowed on the next check")
	}
	if len(l.got) != 2 || !strings.Contains(l.got[1], "no WWMB header") || !strings.Contains(l.got[1], wwapi.RepatchAdvice) {
		t.Errorf("refusal logged %q: want it once, with the reason and the advice", l.got[1:])
	}

	if err := mailbox.WriteHeader(m, fakegame.ThisHeader()); err != nil {
		t.Fatal(err)
	}
	if ok, regained := g.check(t0.Add(5 * ContractEvery)); !ok || !regained {
		t.Errorf("back on this version's mod: ok %v regained %v", ok, regained)
	}

	// A game refused from the start becomes allowed: regained.
	vanilla := mem.NewFakeMem()
	if err := vanilla.Store(0x80000000, []byte("GZLE01")); err != nil {
		t.Fatal(err)
	}
	g = newContractGate(vanilla, &l)
	if ok, _ := g.check(t0); ok {
		t.Fatal("vanilla game allowed")
	}
	if err := fakegame.Boot(vanilla); err != nil {
		t.Fatal(err)
	}
	if ok, regained := g.check(t0.Add(ContractEvery)); !ok || !regained {
		t.Errorf("patched after a refusal: ok %v regained %v", ok, regained)
	}
}
