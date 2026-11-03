package ops_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops/opstest"
)

func setup(t *testing.T) (*mem.FakeMem, *opstest.C, *ops.Queue) {
	t.Helper()
	m := mem.NewFakeMem()
	return m, opstest.New(m), ops.New(m)
}

// mustResult runs frames until t has a result.
func mustResult(t *testing.T, c *opstest.C, q *ops.Queue, tk ops.Ticket) ops.Result {
	t.Helper()
	for i := 0; i < 20; i++ {
		r, done, err := q.Result(tk)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return r
		}
		c.Frame()
	}
	t.Fatalf("ticket %d never ran", tk.N)
	return 0
}

func TestNoRuntime(t *testing.T) {
	m, _, q := setup(t)
	if err := q.Ready(); !errors.Is(err, ops.ErrNoRuntime) {
		t.Fatalf("Ready before boot = %v", err)
	}
	if _, err := q.Submit(ops.OpEcho, 0, 0, 0); !errors.Is(err, ops.ErrNoRuntime) {
		t.Fatalf("Submit before boot = %v", err)
	}
	// Another layout is no runtime either.
	_ = m.Store(mailbox.OpsAddr, []byte{'W', 'W', 'A', 'P', 0, 9})
	if err := q.Ready(); !errors.Is(err, ops.ErrNoRuntime) {
		t.Fatalf("Ready on layout 9 = %v", err)
	}
}

func TestEchoAndEvents(t *testing.T) {
	_, c, q := setup(t)
	c.Boot(nil)
	if err := q.Ready(); err != nil {
		t.Fatal(err)
	}
	tk, err := q.Submit(ops.OpEcho, 1, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r, done, _ := q.Result(tk); done || r != 0 {
		t.Fatalf("result before a frame: %v %v", r, done)
	}
	if r := mustResult(t, c, q, tk); r != ops.ROK {
		t.Fatalf("echo = %v", r)
	}
	evs, bad, err := q.Events()
	if err != nil || bad != 0 {
		t.Fatal(err, bad)
	}
	if len(evs) != 3 || evs[0].Kind != ops.EvBoot || evs[1].Kind != ops.EvEcho || evs[2].Kind != ops.EvCmd {
		t.Fatalf("events = %+v", evs)
	}
	if e := evs[1]; e.A != 1 || e.B != 2 || e.C != 3 {
		t.Errorf("echo event = %+v", e)
	}
	op, seq, r, a, n, ok := evs[2].CmdEvent()
	if !ok || op != ops.OpEcho || seq != tk.Seq || r != ops.ROK || a != 1 || n != tk.N {
		t.Errorf("cmd event = %d %d %v %d %d %v", op, seq, r, a, n, ok)
	}
	if evs, _, _ := q.Events(); len(evs) != 0 {
		t.Errorf("second drain = %+v", evs)
	}
}

func TestResultsAndCounters(t *testing.T) {
	m, c, q := setup(t)
	c.Boot(func(a, b, cc uint32) uint8 {
		if a == 7 {
			return mailbox.OpsROK
		}
		return mailbox.OpsRMismatch
	})
	cases := []struct {
		op   uint8
		a    uint32
		want ops.Result
	}{
		{ops.OpKill, 7, ops.ROK},
		{ops.OpKill, 8, ops.RMismatch},
		{30, 0, ops.RUnknown},
		{200, 0, ops.RUnknown},
	}
	for _, k := range cases {
		tk, err := q.Submit(k.op, k.a, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r := mustResult(t, c, q, tk); r != k.want {
			t.Errorf("op %d a %d = %v, want %v", k.op, k.a, r, k.want)
		}
	}
	// A torn entry (check byte wrong) is refused unrun.
	tk, _ := q.Submit(ops.OpKill, 7, 0, 0)
	e := mailbox.OpsAddr + mailbox.OpsCmdRingOff + (tk.N%mailbox.OpsRing)*mailbox.OpsEntrySize
	_ = m.Store(e+mailbox.OpsCA, []byte{0, 0, 0, 9})
	if r := mustResult(t, c, q, tk); r != ops.RBadCheck {
		t.Errorf("torn = %v", r)
	}
	s, err := q.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Done != 5 || s.Refused != 4 || s.Unknown != 2 || s.BadCheck != 1 || s.EventsDropped != 0 {
		t.Errorf("stats = %+v", s)
	}
}

func TestFullQueueAndPerFrame(t *testing.T) {
	_, c, q := setup(t)
	c.Boot(nil)
	var tks []ops.Ticket
	for i := 0; i < mailbox.OpsRing; i++ {
		tk, err := q.Submit(ops.OpKill, uint32(i), 0, 0)
		if err != nil {
			t.Fatal(i, err)
		}
		tks = append(tks, tk)
	}
	if _, err := q.Submit(ops.OpKill, 99, 0, 0); !errors.Is(err, ops.ErrFull) {
		t.Fatalf("17th submit = %v", err)
	}
	c.Frame()
	ran := 0
	for _, tk := range tks {
		if _, done, _ := q.Result(tk); done {
			ran++
		}
	}
	if ran != mailbox.OpsCmdPerFrame {
		t.Fatalf("one frame ran %d commands, want %d", ran, mailbox.OpsCmdPerFrame)
	}
	if _, err := q.Submit(ops.OpKill, 99, 0, 0); err != nil {
		t.Fatalf("submit after a frame = %v", err)
	}
	// The first ticket's entry is reused by the 17th command once it runs.
	for i := 0; i < 20; i++ {
		c.Frame()
	}
	if _, _, err := q.Result(tks[0]); !errors.Is(err, ops.ErrLost) {
		t.Errorf("result of an overwritten entry = %v", err)
	}
}

func TestEventRingDropsAndWraps(t *testing.T) {
	_, c, q := setup(t)
	c.Boot(nil) // one boot event
	for i := 0; i < mailbox.OpsRing+3; i++ {
		c.Push(ops.EvModFirst, uint32(i), 0, 0)
	}
	evs, _, err := q.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != mailbox.OpsRing || evs[0].Kind != ops.EvBoot || evs[len(evs)-1].A != mailbox.OpsRing-2 {
		t.Fatalf("drained %d events, last %+v", len(evs), evs[len(evs)-1])
	}
	if s, _ := q.Stats(); s.EventsDropped != 4 {
		t.Errorf("dropped = %d, want 4", s.EventsDropped)
	}
	// Past a wrap the entries are reused in order.
	for round := 0; round < 3; round++ {
		for i := 0; i < 10; i++ {
			c.Push(ops.EvModFirst+1, uint32(round*10+i), 0, 0)
		}
		evs, bad, err := q.Events()
		if err != nil || bad != 0 || len(evs) != 10 || evs[0].A != uint32(round*10) || evs[9].A != uint32(round*10+9) {
			t.Fatalf("round %d: %d events, bad %d, %v", round, len(evs), bad, err)
		}
	}
}

func TestRebootUnderCommand(t *testing.T) {
	_, c, q := setup(t)
	c.Boot(nil)
	tk, err := q.Submit(ops.OpEcho, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.Boot(nil)
	if _, _, err := q.Result(tk); !errors.Is(err, ops.ErrReset) {
		t.Errorf("result after a reboot = %v", err)
	}
	// The event reader follows the cleared ring.
	evs, _, err := q.Events()
	if err != nil || len(evs) != 1 || evs[0].Kind != ops.EvBoot {
		t.Errorf("events after a reboot = %+v %v", evs, err)
	}
}

func TestWaitAndRun(t *testing.T) {
	_, c, q := setup(t)
	c.Boot(nil)
	q.Poll = time.Millisecond
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				c.Frame()
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if r, err := q.Run(ctx, ops.OpEcho, 5, 6, 7); err != nil || r != ops.ROK {
		t.Fatalf("Run = %v %v", r, err)
	}
	if r, err := q.Run(ctx, ops.OpKill, 1, 2, 3); err != nil || r != ops.RNotFound {
		t.Fatalf("Run kill = %v %v", r, err)
	}
	short, cancel2 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel2()
	_, c2, q2 := setup(t)
	c2.Boot(nil)
	if _, err := q2.Run(short, ops.OpEcho, 0, 0, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run with no frames = %v", err)
	}
}

func TestResultString(t *testing.T) {
	if ops.ROK.String() != "ok" || ops.RNotFound.String() != "not_found" || ops.Result(42).String() != "result42" {
		t.Error("Result.String")
	}
}
