package write

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// beatMem is a FakeMem whose heartbeat moves by step on every after-th
// read of spawn_trigger (never when after is 0).
type beatMem struct {
	*mem.FakeMem
	mu    sync.Mutex
	reads int
	after int
	step  uint32
	hook  func(n int) // called with each spawn_trigger read's count
}

func newBeatMem(hb uint32, after int, step uint32) *beatMem {
	m := &beatMem{FakeMem: mem.NewFakeMem(), after: after, step: step}
	m.setBeat(hb)
	return m
}

func (m *beatMem) setBeat(hb uint32) {
	_ = m.Store(mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb))
}

func (m *beatMem) Read(a uint32, n int) ([]byte, error) {
	if a == mailbox.SpawnTrigger && n == 4 {
		m.mu.Lock()
		m.reads++
		r := m.reads
		if m.after > 0 && r%m.after == 0 {
			b, _ := m.FakeMem.Read(a, 4)
			m.setBeat(binary.BigEndian.Uint32(b) + m.step)
		}
		h := m.hook
		m.mu.Unlock()
		if h != nil {
			h(r)
		}
	}
	return m.FakeMem.Read(a, n)
}

func TestWaitBeat(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		hb      uint32 // in memory at the call
		from    uint32 // the snapshot's
		after   int
		step    uint32
		want    error
		wantHB  uint32
		message string
	}{
		{"moves", 10, 10, 3, 1, nil, 11, ""},
		{"moves by two", 10, 10, 2, 2, nil, 12, ""},
		{"went back since the snapshot", 9, 10, 0, 0, ErrAborted, 9, "back to 9"},
		{"ran ahead of the snapshot", 10 + 1000, 10, 0, 0, ErrAborted, 1010, "frames past the snapshot's"},
		{"jumped while polled", 10, 10, 2, 3, ErrAborted, 13, "jumped 3 frames"},
		{"went backwards while polled", 10, 10, 2, ^uint32(0), ErrAborted, 9, "went backwards"},
		{"stalled", 10, 10, 0, 0, ErrStall, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newBeatMem(c.hb, c.after, c.step)
			f := &FrameWriter{Mem: m, Breaker: NewBreaker(BreakerConfig{}), Timeout: 20 * time.Millisecond}
			hb, _, err := f.WaitBeat(context.Background(), Beat{Heartbeat: c.from, At: now})
			if !errors.Is(err, c.want) || c.want == nil && err != nil {
				t.Fatalf("WaitBeat = %d, %v: want %v", hb, err, c.want)
			}
			if hb != c.wantHB {
				t.Errorf("heartbeat %d, want %d", hb, c.wantHB)
			}
			if c.message != "" && !strings.Contains(err.Error(), c.message) {
				t.Errorf("error %q lacks %q", err, c.message)
			}
		})
	}
}

func TestWaitBeatStops(t *testing.T) {
	m := newBeatMem(10, 0, 0)
	b := NewBreaker(BreakerConfig{})
	f := &FrameWriter{Mem: m, Breaker: b, Timeout: time.Hour}
	m.hook = func(n int) {
		if n == 5 {
			b.TurnOff("test")
		}
	}
	if _, _, err := f.WaitBeat(context.Background(), Beat{Heartbeat: 10, At: time.Now()}); !errors.Is(err, ErrOff) {
		t.Fatalf("Breaker off: %v, want ErrOff", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.hook = func(n int) {
		if n == 15 {
			cancel()
		}
	}
	f.Breaker = NewBreaker(BreakerConfig{})
	if _, _, err := f.WaitBeat(ctx, Beat{Heartbeat: 10, At: time.Now()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx cancelled: %v", err)
	}
}

func TestBeatLead(t *testing.T) {
	if BeatLead(-time.Second) != beatSlack || BeatLead(0) != beatSlack {
		t.Errorf("BeatLead(<=0) = %d, want %d", BeatLead(0), beatSlack)
	}
	if got := BeatLead(time.Second); got != beatSlack+MaxBeatRate {
		t.Errorf("BeatLead(1s) = %d", got)
	}
}

func putStage(t *testing.T, m *mem.FakeMem, name string, enable, table int8) {
	t.Helper()
	b := make([]byte, int(addr.NextStageEnable-addr.CurStageName)+1)
	copy(b[:addr.StageNameLen-1], name)
	b[len(b)-1] = byte(enable)
	if err := m.Store(addr.CurStageName, b); err != nil {
		t.Fatal(err)
	}
	if err := m.Store(addr.DanStageNo, []byte{byte(table)}); err != nil {
		t.Fatal(err)
	}
}

func TestReadStageSince(t *testing.T) {
	m := mem.NewFakeMem()
	putStage(t, m, "sea", 0, 0)
	was, err := ReadStage(m)
	if err != nil || was != (Stage{Name: "sea", NextEnable: 0, Table: 0}) {
		t.Fatalf("ReadStage = %+v, %v", was, err)
	}
	if err := was.Since(was); err != nil {
		t.Errorf("unchanged: %v", err)
	}
	for _, c := range []struct {
		name  string
		s     Stage
		write string
	}{
		{"stage", Stage{Name: "M_NewD2"}, "the stage changed"},
		{"enable", Stage{Name: "sea", NextEnable: 1}, "mNextStage.mEnable went from 0 to 1"},
		{"table", Stage{Name: "sea", Table: 3}, "the loaded save table went from 0 to 3"},
	} {
		putStage(t, m, c.s.Name, c.s.NextEnable, c.s.Table)
		now, err := ReadStage(m)
		if err != nil || now != c.s {
			t.Fatalf("%s: ReadStage = %+v, %v", c.name, now, err)
		}
		if err := now.Since(was); !errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), c.write) {
			t.Errorf("%s: Since = %v", c.name, err)
		}
	}
}

func TestCheckIdle(t *testing.T) {
	m := mem.NewFakeMem()
	if err := CheckIdle(m); err != nil {
		t.Fatalf("idle: %v", err)
	}
	for _, a := range []uint32{addr.EvtMode, addr.EvtOrderCount, addr.MenuPause, addr.MiniGameType, addr.PlayerInfoBufStageNo} {
		m := mem.NewFakeMem()
		_ = m.Store(a, []byte{1})
		if err := CheckIdle(m); !errors.Is(err, ErrAborted) {
			t.Errorf("0x%08X set: %v", a, err)
		}
	}
}

// listGuard is a Guard over an Allowlist of whole bytes, with closed
// addresses and a refusal hook.
type listGuard struct {
	l      Allowlist[struct{}]
	closed map[uint32]bool
	refuse func(a uint32, old, val byte) bool
}

func (g *listGuard) Mask(a uint32) byte { return g.l.Mask(a) }

func (g *listGuard) Ready(a uint32) error {
	if g.closed[a] {
		return fmt.Errorf("%w: test", ErrClosed)
	}
	return nil
}

func (g *listGuard) Check(a uint32, old, val byte) error {
	if g.l.Mask(a) == 0 {
		return errors.New("not in the allowlist")
	}
	if g.refuse != nil && g.refuse(a, old, val) {
		return errors.New("refused")
	}
	return nil
}

func wholeGuard(t *testing.T, rs ...Range) *listGuard {
	t.Helper()
	g := &listGuard{closed: map[uint32]bool{}}
	for _, r := range rs {
		mask := make([]byte, r.Len)
		for i := range mask {
			mask[i] = 0xFF
		}
		if err := g.l.Add(Span[struct{}]{Start: r.Addr, Mask: mask, Rules: make([]struct{}, r.Len), Name: r.String()}); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestWriteFields(t *testing.T) {
	req := StageRequest{Name: "sea", Point: 1, Room: 44, Layer: -1}
	fs := req.Fields()
	newWriter := func() (*FrameWriter, *beatMem) {
		m := newBeatMem(10, 2, 1)
		return &FrameWriter{Mem: m, Breaker: NewBreaker(BreakerConfig{}), Timeout: 50 * time.Millisecond}, m
	}
	from := &Beat{Heartbeat: 10, At: time.Now()}
	g := wholeGuard(t, Range{Addr: addr.NextStage, Len: addr.NextStageSize})

	t.Run("writes", func(t *testing.T) {
		f, m := newWriter()
		m.Record(true)
		res, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from}, fs)
		if err != nil || res.Beat != 11 {
			t.Fatalf("WriteFields = %+v, %v", res, err)
		}
		if ws := m.Writes(); len(ws) != 2 || ws[0].Addr != addr.NextStage || ws[1].Addr != addr.NextStage+addr.NextOffEnable {
			t.Errorf("writes %+v: want mNextStage, then mEnable (mWipe already 0)", ws)
		}
		if res.Already != 1 {
			t.Errorf("Already %d, want mWipe's 1", res.Already)
		}
		b := req.Bytes()
		got, _ := m.Read(addr.NextStage, addr.StageSize)
		if string(got) != string(b[:]) {
			t.Errorf("mNextStage % X, want % X", got, b)
		}
	})
	t.Run("outside the allowlist", func(t *testing.T) {
		f, m := newWriter()
		m.Record(true)
		res, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from}, []Field{{Addr: addr.NextStage, Data: []byte{'s'}}, {Addr: mailbox.WarpSeq, Data: []byte{1}}})
		if err == nil || res.Refused != 1 || len(m.Writes()) != 0 {
			t.Errorf("WriteFields = %+v, %v, %d writes: want a refusal and nothing written", res, err, len(m.Writes()))
		}
	})
	t.Run("closed", func(t *testing.T) {
		f, m := newWriter()
		m.Record(true)
		cg := wholeGuard(t, Range{Addr: addr.NextStage, Len: addr.NextStageSize})
		cg.closed[addr.NextStageEnable] = true
		res, err := f.WriteFields(context.Background(), Batch{Guard: cg, From: from}, fs)
		if !errors.Is(err, ErrClosed) || res.Closed != addr.StageSize+2 || len(m.Writes()) != 0 {
			t.Errorf("WriteFields = %+v, %v", res, err)
		}
	})
	t.Run("no snapshot", func(t *testing.T) {
		f, _ := newWriter()
		var waited error
		_, err := f.WriteFields(context.Background(), Batch{Guard: g, Waited: func(err error) { waited = err }}, fs)
		if !errors.Is(err, ErrAborted) || waited != err {
			t.Errorf("WriteFields = %v, Waited %v", err, waited)
		}
	})
	t.Run("recheck aborts", func(t *testing.T) {
		f, m := newWriter()
		m.Record(true)
		var waited error
		abort := fmt.Errorf("%w: test", ErrAborted)
		_, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from, Recheck: func() error { return abort }, Waited: func(err error) { waited = err }}, fs)
		if err != abort || waited != abort || len(m.Writes()) != 0 {
			t.Errorf("WriteFields = %v, Waited %v", err, waited)
		}
	})
	t.Run("refused after the beat", func(t *testing.T) {
		f, m := newWriter()
		m.Record(true)
		rg := wholeGuard(t, Range{Addr: addr.NextStage, Len: addr.NextStageSize})
		var beaten bool
		m.hook = func(n int) { beaten = beaten || n >= 2 }
		rg.refuse = func(a uint32, old, val byte) bool { return beaten && a == addr.NextStageEnable }
		res, err := f.WriteFields(context.Background(), Batch{Guard: rg, From: from}, fs)
		if err == nil || res.Refused != 1 || len(m.Writes()) != 1 {
			t.Errorf("WriteFields = %+v, %v, %d writes: want mNextStage written, mEnable refused", res, err, len(m.Writes()))
		}
	})
	t.Run("bad fields", func(t *testing.T) {
		f, _ := newWriter()
		if _, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from}, []Field{{Addr: addr.NextStage}}); err == nil {
			t.Error("an empty field was taken")
		}
		if _, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from}, []Field{{Addr: 0x10, Data: []byte{1}}}); !errors.Is(err, mem.ErrOutOfRange) {
			t.Errorf("a field outside MEM1: %v", err)
		}
		if res, err := f.WriteFields(context.Background(), Batch{Guard: g, From: from}, nil); err != nil || res.Beat != 0 {
			t.Errorf("no fields: %+v, %v", res, err)
		}
	})
}

func TestAllowlist(t *testing.T) {
	var l Allowlist[int]
	add := func(start uint32, mask []byte, name string) error {
		rules := make([]int, len(mask))
		for i := range rules {
			rules[i] = i + 1
		}
		return l.Add(Span[int]{Start: start, Mask: mask, Rules: rules, Name: name})
	}
	base := uint32(addr.MEM1Base) + 0x1000
	if err := add(base+16, []byte{0xFF, 0, 0x0F}, "b"); err != nil {
		t.Fatal(err)
	}
	if err := add(base, []byte{1, 2}, "a"); err != nil {
		t.Fatal(err)
	}
	if err := add(base+32, []byte{0, 0}, "empty"); err != nil || len(l.Spans()) != 2 {
		t.Fatalf("an all-zero span: %v, %d spans", err, len(l.Spans()))
	}
	for _, c := range []struct {
		start uint32
		n     int
	}{{base + 1, 1}, {base + 15, 2}, {base + 18, 4}, {base, 1}} {
		mask := make([]byte, c.n)
		mask[0] = 1
		if err := add(c.start, mask, "overlap"); err == nil {
			t.Errorf("a span at 0x%08X (+%d) overlapping was added", c.start, c.n)
		}
	}
	if err := l.Add(Span[int]{Start: base + 40, Mask: []byte{1}, Name: "short"}); err == nil {
		t.Error("a span with no rules was added")
	}
	if s := l.Spans(); s[0].Name != "a" || s[1].Name != "b" {
		t.Errorf("spans out of order: %+v", s)
	}
	for _, c := range []struct {
		a    uint32
		name string
		rule int
		ok   bool
		mask byte
	}{
		{base - 1, "", 0, false, 0},
		{base, "a", 1, true, 1},
		{base + 1, "a", 2, true, 2},
		{base + 2, "", 0, false, 0},
		{base + 16, "b", 1, true, 0xFF},
		{base + 17, "b", 2, false, 0},
		{base + 18, "b", 3, true, 0x0F},
		{base + 19, "", 0, false, 0},
	} {
		name, r, ok := l.At(c.a)
		if name != c.name || r != c.rule || ok != c.ok || l.Mask(c.a) != c.mask {
			t.Errorf("0x%08X: At = %q, %d, %v, Mask 0x%02X; want %q, %d, %v, 0x%02X", c.a, name, r, ok, l.Mask(c.a), c.name, c.rule, c.ok, c.mask)
		}
	}
	if !l.Allows(base, 2) || l.Allows(base, 3) || l.Allows(base+16, 3) || l.Allows(0, 1) {
		t.Error("Allows")
	}
	if rs := l.Ranges(); len(rs) != 2 || rs[0] != (Range{Addr: base, Len: 2}) || rs[1] != (Range{Addr: base + 16, Len: 3}) {
		t.Errorf("Ranges = %v", rs)
	}
}

func TestDisjoint(t *testing.T) {
	a := []Range{{Addr: 0x80000000, Len: 4}, {Addr: 0x80000010, Len: 4}}
	b := []Range{{Addr: 0x80000004, Len: 12}}
	c := []Range{{Addr: 0x80000013, Len: 1}}
	if err := Disjoint(a, b); err != nil {
		t.Errorf("a, b: %v", err)
	}
	if err := Disjoint(a, b, c); err == nil || !strings.Contains(err.Error(), "set 0") || !strings.Contains(err.Error(), "set 2") {
		t.Errorf("a, b, c: %v", err)
	}
	if err := Disjoint(append(a, a[0])); err != nil {
		t.Errorf("one set overlapping itself: %v", err)
	}
	if (Range{Addr: 0x80000000}).Overlaps(Range{Addr: 0x80000000, Len: 1}) {
		t.Error("an empty range overlaps")
	}
	if !(Range{Addr: 0xFFFFFFFF, Len: 1}).Overlaps(Range{Addr: 0xFFFFFFF0, Len: 0x20}) {
		t.Error("ranges at the top of the address space")
	}
}

func TestStageRequest(t *testing.T) {
	h := StageRequest{Name: "M_NewD2", Point: 255, Room: 63, Layer: MaxStageLayer}
	if err := h.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	b := h.Bytes()
	if string(b[:7]) != "M_NewD2" || b[7] != 0 || binary.BigEndian.Uint16(b[addr.StageOffPoint:]) != 255 ||
		b[addr.StageOffRoomNo] != 63 || b[addr.StageOffLayer] != MaxStageLayer {
		t.Errorf("Bytes % X", b)
	}
	fs := h.Fields()
	if len(fs) != 3 || fs[2].Addr != addr.NextStageEnable || fs[2].Data[0] != 1 || fs[1].Data[0] != 0 {
		t.Errorf("Fields %+v: want the stage, mWipe 0, then mEnable 1 last", fs)
	}
	for _, bad := range []StageRequest{
		{}, {Name: "toolong8"}, {Name: "sea", Point: -1}, {Name: "sea", Point: 256},
		{Name: "sea", Room: -1}, {Name: "sea", Room: addr.RoomCount}, {Name: "sea", Layer: -2},
		{Name: "sea", Layer: MaxStageLayer + 1}, {Name: "s a"}, {Name: "s\x00a"},
	} {
		if bad.Check() == nil {
			t.Errorf("%+v passed Check", bad)
		}
	}
	if s := h.String(); s != "M_NewD2 by point 255 into room 63, layer 11" {
		t.Errorf("String = %q", s)
	}
}
