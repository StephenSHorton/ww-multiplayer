package coopsession

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

type offRecorder struct {
	mu  sync.Mutex
	why []string
}

func (o *offRecorder) TurnOff(why string) {
	o.mu.Lock()
	o.why = append(o.why, why)
	o.mu.Unlock()
}

func (o *offRecorder) n() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.why)
}

// TestCoopContract: co-op writes only into a game the strict contract
// check (Phase E1) allows. contractErr refuses a game without this
// version's WWMB header with the reason and the re-patch advice, and
// guardContract turns the replica's writes off when an old save state is
// loaded mid-session, saying so once.
func TestCoopContract(t *testing.T) {
	m := mem.NewFakeMem()
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	if err := contractErr(m); err != nil {
		t.Fatalf("booted game refused: %v", err)
	}

	off := &offRecorder{}
	l := &coLog{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		guardContract(ctx, m, off, l, 5*time.Millisecond)
	}()
	time.Sleep(30 * time.Millisecond)
	if off.n() != 0 {
		t.Fatalf("allowed game: writes turned off %v", off.why)
	}
	// An old blob's save state: no WWMB header.
	if err := m.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for off.n() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if off.n() < 2 {
		t.Fatalf("refused game: TurnOff %d times, want it kept off", off.n())
	}
	if !strings.Contains(off.why[0], "no WWMB header") || !strings.Contains(off.why[0], wwapi.RepatchAdvice) {
		t.Errorf("TurnOff why %q: want the reason and the re-patch advice", off.why[0])
	}
	if n := l.count("no WWMB header"); n != 1 {
		t.Errorf("refusal logged %d times, want once", n)
	}
	if err := contractErr(m); !errors.Is(err, wwapi.ErrContract) {
		t.Errorf("contractErr: %v, want ErrContract", err)
	}
}
