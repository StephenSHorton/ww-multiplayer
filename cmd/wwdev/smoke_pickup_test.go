package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

func recipe(t *testing.T, name string) pickupRecipe {
	t.Helper()
	rs, err := parsePickupArg(name, true)
	if err != nil || len(rs) != 1 {
		t.Fatalf("parsePickupArg(%q): %v %v", name, rs, err)
	}
	return rs[0]
}

func TestPickupArgs(t *testing.T) {
	// --pickup without co-op after -- is refused, with the error smoke
	// turns into exit 2.
	_, err := parseSmokeArgs([]string{"--pickup", "drc-key3"})
	if !errors.Is(err, errPickupNeedsCoop) {
		t.Fatalf("no co-op: %v, want errPickupNeedsCoop", err)
	}
	_, err = parseSmokeArgs([]string{"--pickup", "drc-key3", "--", "--names", "x"})
	if !errors.Is(err, errPickupNeedsCoop) {
		t.Fatalf("mp-local args without --coop: %v", err)
	}
	o, err := parseSmokeArgs([]string{"--pickup", "DRC-key3, cave01-heart", "--", "--coop=new"})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.pickups) != 2 || o.pickups[0].Name != "drc-key3" || o.pickups[1].Name != "cave01-heart" {
		t.Fatalf("pickups %+v", o.pickups)
	}
	if r := o.pickups[0]; r.Table != addr.TableDRC || r.No != 3 || r.Derive != deriveKey || r.SyncTimeout != 30*time.Second || r.Stage != "M_NewD2" || r.Point != 9 || r.Room != 6 {
		t.Errorf("drc-key3 %+v", r)
	}
	if r := o.pickups[1]; r.Table != addr.TableSubdungeon || r.No != 5 || r.Derive != deriveLife || len(r.KillProcs) != 0 {
		t.Errorf("cave01-heart %+v", r)
	}
	if o, err := parseSmokeArgs([]string{"--", "--coop=new"}); err != nil || o.pickups != nil {
		t.Errorf("no --pickup: %+v %v", o.pickups, err)
	}
	for _, bad := range []string{"nope", "drc-key3,drc-key3", "drc-key3,"} {
		if _, err := parseSmokeArgs([]string{"--pickup", bad, "--", "--coop"}); err == nil || errors.Is(err, errPickupNeedsCoop) {
			t.Errorf("--pickup %q: %v, want a usage error", bad, err)
		}
	}
}

func TestPickupChildEnv(t *testing.T) {
	none := func(string) string { return "" }
	o := smokeOpts{pickups: []pickupRecipe{recipe(t, "drc-key3")}}
	if env := pickupChildEnv(o, []string{"A=1"}, none); !slices.Equal(env, []string{"A=1", "WW_ENEMY_SYNC=0"}) {
		t.Errorf("kills: %v", env)
	}
	if env := pickupChildEnv(o, []string{"A=1"}, func(string) string { return "1" }); len(env) != 1 {
		t.Errorf("WW_ENEMY_SYNC set by the user: %v", env)
	}
	o.pickups = []pickupRecipe{recipe(t, "cave01-heart")}
	if env := pickupChildEnv(o, nil, none); len(env) != 0 {
		t.Errorf("no kills: %v", env)
	}
}

func TestStickToward(t *testing.T) {
	// Camera at eye looking at the origin; Link at the origin. Forward f
	// is levelled center-eye, right r = (-f.z, f.x): looking along -Z,
	// right is +X; looking along +Z, right is -X; looking along -X,
	// right is -Z.
	o := vec{0, 0, 0}
	for _, c := range []struct {
		name         string
		eye, target  vec
		mag          float64
		mirror       bool
		wantX, wantY int8
	}{
		{"-Z cam, ahead", vec{0, 50, 100}, vec{0, 0, -100}, 100, false, 0, 100},
		{"-Z cam, right (+X)", vec{0, 50, 100}, vec{100, 0, 0}, 100, false, 100, 0},
		{"-Z cam, left (-X)", vec{0, 50, 100}, vec{-100, 0, 0}, 100, false, -100, 0},
		{"-Z cam, behind", vec{0, 50, 100}, vec{0, 0, 100}, 100, false, 0, -100},
		{"-Z cam, ahead-right", vec{0, 50, 100}, vec{100, 0, -100}, 100, false, 71, 71},
		{"+Z cam, world -X is right", vec{0, 0, -100}, vec{-100, 0, 0}, 100, false, 100, 0},
		{"-X cam, world -Z is right", vec{100, 0, 0}, vec{0, 0, -100}, 100, false, 100, 0},
		{"-X cam, world +Z is left", vec{100, 0, 0}, vec{0, 0, 100}, 100, false, -100, 0},
		{"mirror flips X only", vec{0, 50, 100}, vec{100, 0, -100}, 100, true, -71, 71},
		{"clamped", vec{0, 50, 100}, vec{0, 0, -100}, 200, false, 0, 127},
		{"far Y ignored", vec{0, 50, 100}, vec{0, 900, -100}, 40, false, 0, 40},
	} {
		x, y, ok := stickToward(o, c.target, c.eye, o, c.mag, c.mirror)
		if !ok || x != c.wantX || y != c.wantY {
			t.Errorf("%s: (%d, %d) %v, want (%d, %d)", c.name, x, y, ok, c.wantX, c.wantY)
		}
	}
	if _, _, ok := stickToward(o, vec{0, 30, 0}, vec{0, 50, 100}, o, 100, false); ok {
		t.Error("Link on the target: ok")
	}
	if _, _, ok := stickToward(o, vec{0, 0, 100}, vec{0, 500, 0}, o, 100, false); ok {
		t.Error("camera straight down: ok")
	}
}

func TestMirrorVote(t *testing.T) {
	// Camera forward +Z (f = (0, 1)); d sideways (-X is the camera's right).
	if v := mirrorVote(-1, 0, 0, 1, -30, 0); v != -1 {
		t.Errorf("moved as asked: %d", v)
	}
	if v := mirrorVote(-1, 0, 0, 1, 30, 0); v != 1 {
		t.Errorf("moved mirrored: %d", v)
	}
	if v := mirrorVote(-1, 0, 0, 1, -3, 0); v != 0 {
		t.Errorf("too small a move: %d", v)
	}
	if v := mirrorVote(0, 1, 0, 1, 0, 40); v != 0 {
		t.Errorf("straight ahead can't tell: %d", v)
	}
	// Diagonal ahead-left: mirrored is ahead-right.
	if v := mirrorVote(-0.707, 0.707, 0, 1, 20, 20); v != 1 {
		t.Errorf("diagonal mirrored: %d", v)
	}
}

func TestPickupYaw(t *testing.T) {
	o := vec{0, 0, 0}
	if y := yawToward(o, vec{0, 0, 100}); y != 0 {
		t.Errorf("+Z: 0x%04X", uint16(y))
	}
	if y := yawToward(o, vec{100, 0, 0}); y != 0x4000 {
		t.Errorf("+X: 0x%04X", uint16(y))
	}
	if d := yawDiffDeg(-32768, 32767); d > 0.01 {
		t.Errorf("wrap: %f", d)
	}
	if d := yawDiffDeg(0, 0x4000); d != 90 {
		t.Errorf("quarter: %f", d)
	}
	c := pickupChest{Pos: vec{10, 100, 20}, Yaw: 0x4000}
	if f := chestFront(c, 150, 10); xzDist(f, vec{160, 0, 20}) > 0.01 || f[1] != 110 {
		t.Errorf("front: %s", f)
	}
	if f := chestFront(pickupChest{}, -150, 0); xzDist(f, vec{0, 0, -150}) > 0.01 {
		t.Errorf("behind: %s", f)
	}
}

func tbox(pid uint32, no int, ft uint32, pos vec) actors.Actor {
	return actors.Actor{PID: pid, Addr: 0x80800000 + pid*0x1000, Proc: int16(mailbox.RefreshProcTbox), InitState: 2,
		Params: uint32(no)<<mailbox.RefreshTboxBitShift | ft, Pos: [3]float32(pos), HomeRoom: -1}
}

func TestFindChest(t *testing.T) {
	w := actors.Walk{Actors: []actors.Actor{
		tbox(1, 2, 0, vec{0, 0, 0}),
		tbox(2, 3, 1, vec{-2552.7, 2850, -1317.6}),
		{PID: 3, Proc: 0x00BE, InitState: 2, Params: 3 << 7},                         // a Bokoblin, not a chest
		{PID: 4, Proc: int16(mailbox.RefreshProcTbox), InitState: 3, Params: 3 << 7}, // leaving
	}}
	cs := chestsOf(&w)
	if len(cs) != 2 {
		t.Fatalf("chests %+v", cs)
	}
	c, why := findChest(cs, 3, nil)
	if why != "" || c.PID != 2 || c.FuncType != 1 {
		t.Errorf("chest 3: %+v %q", c, why)
	}
	if _, why := findChest(cs, 7, nil); !strings.Contains(why, "no chest 7") || !strings.Contains(why, "chest 2 (pid 1") {
		t.Errorf("missing: %q", why)
	}
	w.Actors = append(w.Actors, tbox(5, 3, 0, vec{500, 0, 500}))
	cs = chestsOf(&w)
	if _, why := findChest(cs, 3, nil); !strings.Contains(why, "2 chests have number 3") {
		t.Errorf("two without a hint: %q", why)
	}
	if c, why := findChest(cs, 3, &vec{400, 0, 400}); why != "" || c.PID != 5 {
		t.Errorf("two with a hint: %+v %q", c, why)
	}
}

func u16(m *mem.FakeMem, a uint32, v uint16) { _ = m.Store(a, []byte{byte(v >> 8), byte(v)}) }

func TestReadPickupVals(t *testing.T) {
	r := recipe(t, "drc-key3")
	m := mem.NewFakeMem()
	u16(m, addr.MaxLife, 80)
	u16(m, addr.Life, 72)
	u16(m, addr.Rupee, 200)
	saved, _ := addr.SavedMemBitAddr(addr.TableDRC)
	_ = m.Store(saved, []byte{0, 0, 0, 0x08}) // tbox 3 in the saved slot
	_ = m.Store(saved+addr.MemBitOffKeyNum, []byte{1})
	_ = m.Store(addr.DanStageNo, []byte{addr.TableHyrule})

	v, err := readPickupVals(m, r)
	if err != nil {
		t.Fatal(err)
	}
	if v.Live || !v.Flag || v.Key != 1 || v.MaxLife != 80 || v.Life != 72 || v.Rupee != 200 {
		t.Errorf("saved: %+v", v)
	}

	// In DRC: the live memBit, and the HUD's pending key.
	_ = m.Store(addr.DanStageNo, []byte{addr.TableDRC})
	_ = m.Store(addr.LiveKeyNum, []byte{2})
	u16(m, addr.ItemKeyNumCount, 1)
	v, err = readPickupVals(m, r)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Live || v.Flag || v.Key != 2 || v.KeyPend != 1 || v.keyEff() != 3 {
		t.Errorf("live: %+v", v)
	}
	_ = m.Store(addr.LiveMemBit, []byte{0, 0, 0, 0x08})
	if v, _ := readPickupVals(m, r); !v.Flag {
		t.Errorf("live flag: %+v", v)
	}
}

func TestPickupLogScan(t *testing.T) {
	r := recipe(t, "drc-key3")
	if got := pickupSetPhrase(r); got != "co-op: set t03 DRC chest 3 (" {
		t.Errorf("set phrase %q", got)
	}
	if got := pickupDerivePhrase(r); got != "co-op: wrote saved mKeyNum 0x803C5014 " {
		t.Errorf("derive phrase %q", got)
	}
	if got := pickupSetPhrase(recipe(t, "cave01-heart")); got != "co-op: set t12 subdungeon chest 5 (" {
		t.Errorf("heart set phrase %q (wind88: set t12 subdungeon chest 5 (0x803C513B |= 0x20))", got)
	}
	lines := []string{
		"[coop-B] co-op: dry-run: would set t03 DRC chest 3 (0x803C4FF7 |= 0x08)",
		"[coop-A] co-op: set t03 DRC chest 3 (0x803C5383 |= 0x08)",
		"[coop-B] co-op: set t03 DRC chest 30 (0x803C4FF4 |= 0x40)",
		"[coop-B] co-op: PlayerB:   combat: on; 0 partner kill bit(s) held; 0 written to zones, 0 light-shrunk, 0 held, 0 refused, 0 verify failures",
		"co-op: host:   PlayerB (bound): in sea (table 0, room 11); puzzles granted; 0 scoped bit(s) refused by the host's tables",
		"[coop-B] co-op: PlayerB:   room puzzles: on; 0 partner switch(es) set, 0 refused as not latching here, 0 room edge(s) dropped",
		"[coop-B] co-op: PlayerB: armed; world 07bb at seq 2; writing; 64 bytes written, 0 escapes, 0 verify failures",
		"WARN: Can't reach 127.0.0.1:25640 (connect: connection refused)",
	}
	l := scanPickupLog(lines, r)
	if l.SetLine != "" || l.DeriveLine != "" || len(l.Bad) != 0 {
		t.Fatalf("nothing should match: %+v", l)
	}
	lines = append(lines,
		"[12:00:01] [coop-B] co-op: set t03 DRC chest 3 (0x803C4FF7 |= 0x08)",
		"[coop-B] co-op: wrote saved mKeyNum 0x803C5014 00 -> 01: small keys, table 3: 0 -> 1",
		"[coop-B] co-op: verify failed: 0x803C4FF7: the game stored over it",
		"[coop-B] co-op: PlayerB: armed; 3 bytes written, 0 escapes, 2 verify failures",
		"[coop-B] co-op: placed kills: on; 0 pending, 10 refused",
		"co-op: host: refused PlayerB's chest claim",
	)
	l = scanPickupLog(lines, r)
	if !strings.Contains(l.SetLine, "chest 3 (") || !strings.Contains(l.DeriveLine, "00 -> 01") {
		t.Errorf("lines: %+v", l)
	}
	if len(l.Bad) != 4 {
		t.Errorf("bad lines %d, want 4: %q", len(l.Bad), l.Bad)
	}
}

// goodKeyObs is a drc-key3 run where everything went right.
func goodKeyObs() pickupObs {
	return pickupObs{
		A0:      pickupVals{Live: true, Key: 0, MaxLife: 80},
		A1:      pickupVals{Live: true, Flag: true, Key: 1, MaxLife: 80},
		AKeyMax: 1,
		B0:      pickupVals{Key: 0, Life: 80, Rupee: 200, MaxLife: 80},
		B1:      pickupVals{Flag: true, Key: 1, Life: 80, Rupee: 200, MaxLife: 80},
		BKeyMax: 1, BFlagAt: 2.5,
		Log: pickupLog{Available: true, Path: "mp-local.log",
			SetLine:    "[coop-B] co-op: set t03 DRC chest 3 (0x803C4FF7 |= 0x08)",
			DeriveLine: "[coop-B] co-op: wrote saved mKeyNum 0x803C5014 00 -> 01"},
	}
}

func verdicts(t *testing.T, r pickupRecipe, o pickupObs) map[string]smokeCheck {
	t.Helper()
	cs, rig := pickupVerdicts(r, o)
	if rig != "" {
		t.Fatalf("rig failure: %s", rig)
	}
	out := map[string]smokeCheck{}
	for _, c := range cs {
		out[strings.TrimPrefix(c.Name, "pickup "+r.Name+" ")] = c
	}
	return out
}

func TestPickupVerdicts(t *testing.T) {
	r := recipe(t, "drc-key3")
	cs := verdicts(t, r, goodKeyObs())
	for _, n := range []string{"opened on A", "A once", "B flag", "B derive", "B own state"} {
		if c, ok := cs[n]; !ok || !c.Pass {
			t.Errorf("all good: %s %+v", n, c)
		}
	}

	for _, c := range []struct {
		name   string
		edit   func(o *pickupObs)
		check  string
		pass   bool
		detail string
	}{
		{"never set on B", func(o *pickupObs) { o.B1.Flag, o.BFlagAt = false, -1 }, "B flag", false, "false -> false"},
		{"set on B, no log line", func(o *pickupObs) { o.Log.SetLine = "" }, "B flag", false, "no \"co-op: set t03 DRC chest 3 (\" line"},
		{"set on B, logs off", func(o *pickupObs) { o.Log = pickupLog{Path: "logs are off"} }, "B flag", true, "not read"},
		{"A doubled", func(o *pickupObs) { o.A1.Key, o.AKeyMax = 2, 2 }, "A once", false, "double"},
		{"A doubled for a moment", func(o *pickupObs) { o.AKeyMax = 2 }, "A once", false, "double"},
		{"A's +1 still pending", func(o *pickupObs) { o.A1.Key, o.A1.KeyPend = 0, 1 }, "A once", true, "0 -> 1"},
		{"A unchanged", func(o *pickupObs) { o.A1.Key = 0 }, "A once", false, "want 1"},
		{"B doubled", func(o *pickupObs) { o.B1.Key, o.BKeyMax = 2, 2 }, "B derive", false, "double"},
		{"B derive by log only", func(o *pickupObs) { o.B1.Key, o.BKeyMax = 0, 0 }, "B derive", true, "log:"},
		{"B derive nowhere", func(o *pickupObs) { o.B1.Key, o.BKeyMax, o.Log.DeriveLine = 0, 0, "" }, "B derive", false, "want 1"},
		{"B life moved", func(o *pickupObs) { o.B1.Life = 76 }, "B own state", false, "life 80 -> 76"},
		{"B rupees moved", func(o *pickupObs) { o.B1.Rupee = 201 }, "B own state", false, "rupees 200 -> 201"},
		{"a verify failure", func(o *pickupObs) { o.Log.Bad = []string{"co-op: verify failed: x"} }, "B own state", false, "verify failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := goodKeyObs()
			c.edit(&o)
			got := verdicts(t, r, o)[c.check]
			if got.Pass != c.pass || !strings.Contains(got.Detail, c.detail) {
				t.Errorf("%s: pass %v %q, want pass %v containing %q", c.check, got.Pass, got.Detail, c.pass, c.detail)
			}
		})
	}

	// The drive never opened it: a rig failure, nothing else judged.
	o := goodKeyObs()
	o.A1.Flag = false
	cs2, rig := pickupVerdicts(r, o)
	if !strings.Contains(rig, "the drive did not open the chest") || len(cs2) != 1 || cs2[0].Pass {
		t.Errorf("not opened: %q %+v", rig, cs2)
	}

	// Already set on A before the drive: a skip.
	if why := pickupPreSkip(r, true); !strings.Contains(why, "already has table 3 tbox 3") {
		t.Errorf("pre-set: %q", why)
	}
	if why := pickupPreSkip(r, false); why != "" {
		t.Errorf("clear: %q", why)
	}
}

func TestPickupVerdictsLife(t *testing.T) {
	r := recipe(t, "cave01-heart")
	o := pickupObs{
		A0:      pickupVals{Live: true, MaxLife: 20, Life: 20},
		A1:      pickupVals{Live: true, Flag: true, MaxLife: 21, Life: 20},
		ALifMax: 21,
		B0:      pickupVals{MaxLife: 20, Life: 20, Rupee: 50},
		B1:      pickupVals{Flag: true, MaxLife: 20, LifePnd: 1, Life: 20, Rupee: 50},
		BLifMax: 21, BFlagAt: 1,
		Log: pickupLog{Available: true, SetLine: "[coop-B] co-op: set t12 subdungeon chest 5 (0x803C513B |= 0x20)"},
	}
	cs := verdicts(t, r, o)
	for _, n := range []string{"opened on A", "A once", "B flag", "B derive", "B own state"} {
		if !cs[n].Pass {
			t.Errorf("heart: %s %+v", n, cs[n])
		}
	}
	o.A1.LifePnd = 1 // A's own derive added a second quarter
	if c := verdicts(t, r, o)["A once"]; c.Pass || !strings.Contains(c.Detail, "double") {
		t.Errorf("heart double: %+v", c)
	}
}

func TestItemGetOver(t *testing.T) {
	for _, c := range []struct {
		name     string
		saw      bool
		idle     bool
		run      int
		since    time.Duration
		flag     bool
		wantOver bool
	}{
		{"lid opening, idle counted before the event", false, true, 30, 300 * time.Millisecond, false, false},
		{"in the event", true, false, 0, 2 * time.Second, true, false},
		{"idle 1 s after the event", true, true, 10, 5 * time.Second, true, false},
		{"idle 2 s after the event", true, true, 20, 6 * time.Second, true, true},
		{"no event, flag set, 6 s idle", false, true, 60, 6 * time.Second, true, true},
		{"no event, flag clear", false, true, 60, 10 * time.Second, false, false},
	} {
		if got := itemGetOver(c.saw, c.idle, c.run, c.since, c.flag); got != c.wantOver {
			t.Errorf("%s: %v, want %v", c.name, got, c.wantOver)
		}
	}
}
