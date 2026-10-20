package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The fake host Link, its linktex, zel_basic and the FULL body with its
// solid heap.
const (
	fgFakeLink    = uint32(0x80B00000)
	fgFakeLinktex = uint32(0x80B10000)
	fgFakeZel     = uint32(0x80B20000)
	fgFakeBody    = uint32(0x80C00000)
	fgFakeHeap    = uint32(0x80C10000)
	fgFakeHeapLo  = uint32(0x80C20000)
	fgFakeHeapHi  = uint32(0x80C40000)
)

// fgTicker runs f before every read of the REL words (the first read of
// each sample), so a value can move between samples.
type fgTicker struct {
	*mem.FakeMem
	n int
	f func(n int)
}

func (r *fgTicker) Read(a uint32, n int) ([]byte, error) {
	if a == mailbox.Mailbox+mailbox.OffRELMagic && r.f != nil {
		r.f(r.n)
		r.n++
	}
	return r.FakeMem.Read(a, n)
}

func f32b(vs ...float32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		mem.PutF32BE(b[4*i:], v)
	}
	return b
}

// fgFakeHost is an idle host in M_Dai room 1: the REL linked, Link with
// his pos/angles mirrored into the l_debug_* globals, a linktex, gear,
// no ship, no body.
func fgFakeHost(t *testing.T) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: 7, Frames: 1000}); err != nil {
		t.Fatal(err)
	}
	store(t, m, addr.CurStageName, []byte("M_Dai\x00\x00\x00"))
	store(t, m, addr.StayNo, []byte{1})
	store(t, m, addr.PlayerPtr, u32b(fgFakeLink))
	store(t, m, fgFakeLink+addr.ActorOffProcName, u16b(0xA9))
	pos := f32b(100, 200, 300)
	ang := []byte{0, 0, 0x40, 0, 0, 0}
	store(t, m, fgFakeLink+0x1F8, pos)
	store(t, m, fgFakeLink+0x204, ang)
	store(t, m, fgFakeLink+0x20C, ang)
	store(t, m, fgFakeLink+0x31D8, u32b(0x04))
	store(t, m, 0x803E440C, pos)
	store(t, m, 0x803F6F10, ang)
	store(t, m, 0x803F6F18, ang)
	store(t, m, fgFakeLink+0x338, u32b(fgFakeLinktex))
	store(t, m, fgFakeLinktex, bytes.Repeat([]byte{0xAB}, 0x20))
	store(t, m, 0x803F7710, u32b(fgFakeZel))
	store(t, m, fgFakeZel+0x41, []byte{1, 2, 3, 4, 5, 6})
	store(t, m, 0x803CA7E9, []byte{3})
	store(t, m, 0x803F772C, u32b(0x80900000))
	store(t, m, 0x803F6940, u32b(0x80900000))
	return m
}

// fgWithBody makes slot 0 a FULL LIVE body with its solid heap and
// shanm windows w.
func fgWithBody(t *testing.T, m *mem.FakeMem) {
	t.Helper()
	st := mailbox.RemoteStatus{}
	st.Bodies[0] = mailbox.RemoteBodyStatus{Actor: fgFakeBody, PID: 9, State: mailbox.RemoteBodyLive, Kind: mailbox.RemoteKindFull, Tries: 1}
	storeStatus(t, m, &st)
	store(t, m, fgFakeBody+fgActorOffHeap, u32b(fgFakeHeap))
	store(t, m, fgFakeHeap+addr.HeapOffStart, append(u32b(fgFakeHeapLo), u32b(fgFakeHeapHi)...))
	store(t, m, fgFakeBody+0x1F8, f32b(500, 0, 500))
}

// fgCapt takes n samples of m; blink runs the blink timer between them.
func fgCapt(t *testing.T, m *mem.FakeMem, n int, blink bool, each func(i int)) *fgCapture {
	t.Helper()
	regs, err := fgRegions(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fgTicker{FakeMem: m, f: func(i int) {
		if blink {
			store(t, m, 0x803F6F1E, []byte{byte(40 - i), 0, 0, 3})
		}
		if each != nil {
			each(i)
		}
	}}
	var ss []fgSample
	t0 := time.Now()
	for range n {
		s, err := fgReadSample(r, regs, 0, t0)
		if err != nil {
			t.Fatal(err)
		}
		ss = append(ss, s)
	}
	return fgBuild(ss, regs, 0)
}

// fgRow finds the printed row of name.
func fgRow(t *testing.T, out, name string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, name+" ") {
			return l
		}
	}
	t.Fatalf("no row %s in:\n%s", name, out)
	return ""
}

func TestFGExpr(t *testing.T) {
	e, err := parseFGExpr("0x803CA754*+0x2F34*+0x04:8")
	if err != nil {
		t.Fatal(err)
	}
	if e.Base != 0x803CA754 || e.Len != 8 || len(e.Ops) != 4 || !e.Ops[0].Deref || e.Ops[1].Add != 0x2F34 || !e.Ops[2].Deref || e.Ops[3].Add != 4 {
		t.Fatalf("parsed %+v", e)
	}
	m := fgFakeHost(t)
	store(t, m, fgFakeLink+0x2F34, u32b(0x80B30000))
	a, why := e.resolve(m, map[uint32]uint32{})
	if why != "" || a != 0x80B30004 {
		t.Fatalf("resolve = 0x%08X %q", a, why)
	}
	// The ship pointer is 0: unresolved.
	s, _ := parseFGExpr("0x803CA75C*+0x1F8:0xC")
	if _, why := s.resolve(m, map[uint32]uint32{}); !strings.HasPrefix(why, "unresolved") {
		t.Fatalf("ship resolve: %q", why)
	}
	for _, bad := range []string{"0x803CA754", "0x803CA754:0", "zz:4", "0x80000000*-4:4", "0x80000000+:4"} {
		if _, err := parseFGExpr(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, err := fgRegions(nil); err != nil {
		t.Fatal(err)
	}
	rest, tr, err := fgSplitTracks([]string{"x.json", "--track", "brk=0x803CA754*+0x2F34*+0x04:8", "--track=a=0x80000000:4", "--slot", "1"})
	if err != nil || len(tr) != 2 || tr[0].Name != "brk" || strings.Join(rest, " ") != "x.json --slot 1" {
		t.Fatalf("split: %v %v %v", rest, tr, err)
	}
}

// TestFGDiff: two idle dumps pass; a create with the body in place passes;
// each kind of fault gets its verdict and exit code.
func TestFGDiff(t *testing.T) {
	diff := func(a, b, base *fgCapture) (string, int) {
		var w bytes.Buffer
		code := fgDiff(&w, a, b, base)
		return w.String(), code
	}
	idle := func() *fgCapture { return fgCapt(t, fgFakeHost(t), 6, true, nil) }

	out, code := diff(idle(), idle(), nil)
	if code != 0 || !strings.Contains(out, "FAIL 0 MASKED 0 INVALID 0") {
		t.Fatalf("idle/idle code %d:\n%s", code, out)
	}
	if r := fgRow(t, out, "ship_pos"); !strings.Contains(r, "PASS") || !strings.Contains(r, "no ship") {
		t.Errorf("ship: %s", r)
	}
	if r := fgRow(t, out, "matanm"); !strings.Contains(r, "blink still running") {
		t.Errorf("matanm: %s", r)
	}
	if r := fgRow(t, out, "debug_keep_pos"); !strings.Contains(r, "PASS") || !strings.Contains(r, "(100.00,200.00,300.00)") {
		t.Errorf("keep_pos: %s", r)
	}
	if !strings.Contains(out, "a (before): stage \"M_Dai\" room 1") {
		t.Errorf("meta:\n%s", out)
	}

	// A create: before -> after, everything put back.
	withBody := func(f func(m *mem.FakeMem)) *fgCapture {
		m := fgFakeHost(t)
		fgWithBody(t, m)
		if f != nil {
			f(m)
		}
		return fgCapt(t, m, 6, true, nil)
	}
	out, code = diff(idle(), withBody(nil), nil)
	if code != 0 || !strings.Contains(out, "b (after)") {
		t.Fatalf("create code %d:\n%s", code, out)
	}

	// cur_heap left inside the body's solid heap: FAIL.
	out, code = diff(idle(), withBody(func(m *mem.FakeMem) { store(t, m, 0x803F772C, u32b(fgFakeHeapLo+0x10)) }), nil)
	if code != 1 || !strings.Contains(fgRow(t, out, "cur_heap"), "FAIL") {
		t.Fatalf("cur_heap code %d:\n%s", code, out)
	}

	// A STATIC global not put back: FAIL, a missing restore.
	out, code = diff(idle(), withBody(func(m *mem.FakeMem) { store(t, m, 0x803CA7E9, []byte{7}) }), nil)
	if r := fgRow(t, out, "direction"); code != 1 || !strings.Contains(r, "FAIL") || !strings.Contains(r, "missing restore") {
		t.Fatalf("direction code %d: %s", code, r)
	}

	// The body's position leaked into l_debug_keep_pos: FAIL.
	out, code = diff(idle(), withBody(func(m *mem.FakeMem) { store(t, m, 0x803E440C, f32b(500, 0, 500)) }), nil)
	if r := fgRow(t, out, "debug_keep_pos"); code != 1 || !strings.Contains(r, "body's own field") {
		t.Fatalf("keep_pos leak code %d: %s", code, r)
	}

	// The blink timer stuck mid-count after the create: FAIL.
	m := fgFakeHost(t)
	fgWithBody(t, m)
	store(t, m, 0x803F6F1E, []byte{17, 0, 0, 3})
	out, code = diff(idle(), fgCapt(t, m, 6, false, nil), nil)
	if r := fgRow(t, out, "matanm"); code != 1 || !strings.Contains(r, "blink frozen in b") {
		t.Fatalf("matanm code %d: %s", code, r)
	}
	// Resting at 0 between blinks: MASKED, not FAIL.
	store(t, m, 0x803F6F1E, []byte{0, 0, 0, 0})
	out, code = diff(idle(), fgCapt(t, m, 6, false, nil), nil)
	if r := fgRow(t, out, "matanm"); code != 0 || !strings.Contains(r, "resting at 0") {
		t.Fatalf("matanm rest code %d: %s", code, r)
	}

	// solidHeapErrors +1: a stage load, INVALID.
	out, code = diff(idle(), withBody(func(m *mem.FakeMem) { store(t, m, addr.SolidHeapErrors, u32b(1)) }), nil)
	if r := fgRow(t, out, "heap_counters"); code != 2 || !strings.Contains(r, "INVALID") {
		t.Fatalf("counters code %d: %s", code, r)
	}
	// GameHeapErrors +1: FAIL.
	out, code = diff(idle(), withBody(func(m *mem.FakeMem) { store(t, m, addr.GameHeapErrors, u32b(1)) }), nil)
	if r := fgRow(t, out, "heap_counters"); code != 1 || !strings.Contains(r, "game 0->1") {
		t.Fatalf("game errors code %d: %s", code, r)
	}

	// The host moving in the before dump: INVALID, redo.
	hm := fgFakeHost(t)
	busy := fgCapt(t, hm, 6, true, func(i int) { store(t, hm, fgFakeLink+0x31D8, u32b(uint32(4+i%2))) })
	out, code = diff(busy, withBody(nil), nil)
	if r := fgRow(t, out, "link_cur_proc"); code != 2 || !strings.Contains(r, "host not idle") {
		t.Fatalf("busy code %d: %s", code, r)
	}

	// The host dropped after the create (mCurProc changed): FAIL; a
	// base dump where the byte moved makes it MASKED instead.
	dropped := withBody(func(m *mem.FakeMem) { store(t, m, fgFakeLink+0x31D8, u32b(0x2B)) })
	out, code = diff(idle(), dropped, nil)
	if r := fgRow(t, out, "link_cur_proc"); code != 1 || !strings.Contains(r, "host dropped") {
		t.Fatalf("dropped code %d: %s", code, r)
	}
	nm := fgFakeHost(t)
	noise := fgCapt(t, nm, 6, true, func(i int) { store(t, nm, fgFakeLink+0x31DB, []byte{byte(i)}) })
	out, _ = diff(idle(), dropped, noise)
	if r := fgRow(t, out, "link_cur_proc"); !strings.Contains(r, "MASKED") {
		t.Fatalf("masked: %s", r)
	}

	// A delete: after -> del; a cur_heap sample inside the deleted body's
	// actor (taken from the after dump) FAILs.
	del := fgCapt(t, func() *mem.FakeMem {
		m := fgFakeHost(t)
		store(t, m, 0x803F772C, u32b(fgFakeBody+0x100))
		return m
	}(), 6, true, nil)
	out, code = diff(withBody(nil), del, nil)
	if r := fgRow(t, out, "cur_heap"); code != 1 || !strings.Contains(r, "del sample") || !strings.Contains(out, "b (del)") {
		t.Fatalf("delete code %d: %s\n%s", code, r, out)
	}

	// A region missing from one capture (an edited file or another
	// table) cannot pass unseen: INVALID; a --track one is only noted;
	// a region with no class is INVALID too.
	short := idle()
	short.Regions = slices.DeleteFunc(short.Regions, func(d fgDump) bool { return d.Name == "direction" })
	short.Regions = append(short.Regions, fgDump{Name: "mytrack", Class: fgInfo})
	out, code = diff(idle(), short, nil)
	if r := fgRow(t, out, "direction"); code != 2 || !strings.Contains(r, "INVALID") || !strings.Contains(r, "missing in b") {
		t.Fatalf("missing code %d: %s", code, r)
	}
	if r := fgRow(t, out, "mytrack"); !strings.Contains(r, "INFO") || !strings.Contains(r, "missing in a") {
		t.Fatalf("missing track: %s", r)
	}
	nc, nc2 := idle(), idle()
	nc.Regions[0].Class, nc2.Regions[0].Class = "", ""
	out, code = diff(nc, nc2, nil)
	if r := fgRow(t, out, nc.Regions[0].Name); code != 2 || !strings.Contains(r, "unknown class") {
		t.Fatalf("no class code %d: %s", code, r)
	}
}

// TestFGSnapJSON: snap over a fake game writes a capture that loads back
// to the same verdicts.
func TestFGSnapJSON(t *testing.T) {
	m := fgFakeHost(t)
	regs, err := fgRegions([]fgRegion{{Name: "brk", Expr: "0x803CA754*+0x2F34*+0x04:8", Class: fgInfo}})
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	c, err := fgSnap(context.Background(), &w, m, regs, fgSnapOpts{Samples: 3, Wait: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "zel_gear") || !strings.Contains(w.String(), "brk") {
		t.Fatalf("snap output:\n%s", w.String())
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := fgLoad(p)
	if err != nil {
		t.Fatal(err)
	}
	if d := back.region("zel_gear"); d == nil || !bytes.Equal(d.First, []byte{1, 2, 3, 4, 5, 6}) || uint32(d.Addr) != fgFakeZel+0x41 {
		t.Fatalf("zel_gear back: %+v", d)
	}
	if d := back.region("brk"); d == nil || d.resolved() || !strings.HasPrefix(d.Why, "unresolved") {
		t.Fatalf("brk back: %+v", d)
	}
	var out bytes.Buffer
	// No blink in a 3-sample fake: matanm MASKED (before/before), not FAIL.
	if code := fgDiff(&out, back, back, nil); code != 0 {
		t.Fatalf("self diff code %d:\n%s", code, out.String())
	}
}

// TestFGWatch: the watch sees the create and the delete of slot 0's
// FULL body and compares the window before each with the window after.
func TestFGWatch(t *testing.T) {
	old := fgWindow
	fgWindow = 8 * time.Millisecond
	defer func() { fgWindow = old }()
	m := fgFakeHost(t)
	regs, err := fgRegions(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := func(state, kind uint8, actor uint32) {
		st := mailbox.RemoteStatus{}
		st.Bodies[0] = mailbox.RemoteBodyStatus{Actor: actor, State: state, Kind: kind}
		storeStatus(t, m, &st)
	}
	r := &fgTicker{FakeMem: m, f: func(i int) {
		store(t, m, 0x803F6F1E, []byte{byte(i), 0, 0, 3})
		switch {
		case i == 20:
			set(mailbox.RemoteBodyQueued, mailbox.RemoteKindFull, 0)
		case i == 22:
			fgWithBody(t, m)
		case i == 60:
			set(mailbox.RemoteBodyDying, mailbox.RemoteKindFull, fgFakeBody)
		case i == 62:
			set(mailbox.RemoteBodyEmpty, 0, 0)
		case i >= 100:
			cancel()
		}
	}}
	var w, csv bytes.Buffer
	code, err := fgWatch(ctx, &w, &csv, r, regs, 0, 0, 2*time.Millisecond, time.Millisecond)
	out := w.String()
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v:\n%s", code, err, out)
	}
	for _, want := range []string{"baseline:", "EVENT create slot 0 kind FULL", "verdict create: PASS", "EVENT delete slot 0 kind FULL", "verdict delete: PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(csv.String(), "t_ms,state,kind,err,evt_exception,") || strings.Count(csv.String(), "\n") < 50 {
		t.Errorf("csv:\n%.300s", csv.String())
	}
}

// TestFGModal: the linktex header is judged by its usual value, so the
// tunic swap caught mid-draw passes and a changed usual value fails.
func TestFGModal(t *testing.T) {
	capt := func(usual, odd byte, nOdd int) *fgCapture {
		c := fgCapt(t, fgFakeHost(t), 6, true, nil)
		d := c.region("host_linktex")
		if d == nil || d.Class != fgModal {
			t.Fatalf("host_linktex: %+v", d)
		}
		for i := range d.Samples {
			v := bytes.Repeat([]byte{usual}, d.Size)
			if i < nOdd {
				v[0] = odd
			}
			d.Samples[i] = v
		}
		d.stats()
		return c
	}
	diff := func(a, b *fgCapture) (string, int) {
		var w bytes.Buffer
		code := fgDiff(&w, a, b, nil)
		return w.String(), code
	}
	out, code := diff(capt(0x0E, 0, 0), capt(0x0E, 0x08, 1))
	if r := fgRow(t, out, "host_linktex"); code != 0 || !strings.Contains(r, "PASS") || !strings.Contains(r, "mid-draw") {
		t.Errorf("mid-draw code %d: %s", code, r)
	}
	out, code = diff(capt(0x0E, 0, 0), capt(0x08, 0, 0))
	if r := fgRow(t, out, "host_linktex"); code != 1 || !strings.Contains(r, "FAIL") {
		t.Errorf("changed code %d: %s", code, r)
	}
	out, code = diff(capt(0x0E, 0, 0), capt(0x0E, 0x08, 3))
	if r := fgRow(t, out, "host_linktex"); code != 2 || !strings.Contains(r, "INVALID") {
		t.Errorf("no usual value code %d: %s", code, r)
	}
}

// TestFGBlinkWorst: a blink timer resting at 0 in a does not hide one
// stuck mid-count in b.
func TestFGBlinkWorst(t *testing.T) {
	ma := fgFakeHost(t)
	fgWithBody(t, ma)
	store(t, ma, 0x803F6F1E, []byte{0, 0, 0, 0})
	a := fgCapt(t, ma, 6, false, nil) // after: resting at 0
	mb := fgFakeHost(t)
	store(t, mb, 0x803F6F1E, []byte{37, 0, 0, 3})
	b := fgCapt(t, mb, 6, false, nil) // del: stuck at 37
	var w bytes.Buffer
	code := fgDiff(&w, a, b, nil)
	if r := fgRow(t, w.String(), "matanm"); code != 1 || !strings.Contains(r, "blink frozen in b") {
		t.Fatalf("code %d: %s", code, r)
	}
}

// TestFGSinceAhead: a --since count past the live shanm windows is an
// error, not a wrapped rise that passes the wait at once.
func TestFGSinceAhead(t *testing.T) {
	m := fgFakeHost(t)
	_, err := fgWait(context.Background(), io.Discard, m, fgSnapOpts{Wait: "full", Timeout: time.Second, Since: 99, HasSince: true})
	if err == nil || !strings.Contains(err.Error(), "past the live") {
		t.Fatalf("err %v", err)
	}
}
