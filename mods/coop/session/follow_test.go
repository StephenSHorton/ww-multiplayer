package coopsession

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// tieTo ties the loaded file to world w with a genuine tag (it passes V1
// to V5: its slot, clear count and name hash are the file's, and the file
// was saved and loaded back), as a bind of an earlier session left it.
func (cg *coGame) tieTo(w bind.WorldID) {
	cg.t.Helper()
	f, err := bind.ReadFile(cg.m)
	if err != nil {
		cg.t.Fatal(err)
	}
	tag := bind.Tag{Layout: bind.LayoutV1, World: w, Nonce: bind.Nonce{0x77, 1, 2, 3}, Slot: f.Slot, ClearCount: f.ClearCount, NameHash: bind.NameHash(f.Name[:])}
	b := tag.Encode()
	if err := cg.m.Store(addr.Reserve, b[:]); err != nil {
		cg.t.Fatal(err)
	}
}

// followNotes collects a joiner's one-line outcomes.
type followNotes struct {
	mu   sync.Mutex
	list []string
}

func (n *followNotes) add(outcome, world string) {
	n.mu.Lock()
	n.list = append(n.list, outcome+" "+world)
	n.mu.Unlock()
}

func (n *followNotes) String() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.list, ", ")
}

// TestJoinerFollowsHost: each joiner row of docs/coop-just-works.md §5,
// through a real hub and the app joiner's flags (yes, follow, the host's
// world from GO): nothing is asked, the file binds, and the outcome is
// one line.
func TestJoinerFollowsHost(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server")
	}
	other := bind.WorldID{0xAA, 0xBB, 1, 2, 3, 4, 5, 6}
	// A bit of shared progress the world lacks (TestRetryBindsAfterMerge's).
	ahead := func(g *coGame) {
		or, _ := coShareable()
		for _, off := range or {
			if m := world.SharedMask(off); g.get(off)&m != m {
				g.put(off, g.get(off)|(m&^g.get(off)&-(m&^g.get(off))))
				return
			}
		}
		g.t.Fatal("no shareable bit to set")
	}
	for _, tc := range []struct {
		name     string
		hostGame func(*coGame)
		join     func(*coGame)
		merge    bool
		armed    bool
		binds    int    // tag writes: a clear is one, a bind another
		notes    string // the outcomes, in order
		cleared  bool
	}{
		{name: "untied, same progress", armed: true, binds: 1, notes: "joined"},
		{name: "tied to another world", join: func(g *coGame) { g.tieTo(other) }, armed: true, binds: 2, notes: "cleared, followed", cleared: true},
		{name: "a leaked tag (the bind replaces it)", join: func(g *coGame) { g.leakTag(other) }, armed: true, binds: 1, notes: "joined"},
		{name: "ahead of the world, merge on", join: ahead, merge: true, armed: true, binds: 1, notes: "joined"},
		{name: "ahead of the world, merge off (a host's choice)", join: ahead, armed: false},
		// Far behind (a world past a milestone) takes the same path as
		// ahead: the preflight needs the merge feature, and with it asks
		// to bind. The milestones are outside the default policy these
		// fakes run, so replica's preflight tests cover the verdict.
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ga := newCoGame(t, "Link", coMine{life: 12})
			if tc.hostGame != nil {
				tc.hostGame(ga)
			}
			gb := newCoGame(t, "Tetra", coMine{life: 12})
			if tc.join != nil {
				tc.join(gb)
			}
			gb.m.Record(true)
			h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
				worlds: t.TempDir(), backups: t.TempDir(), userDirs: []string{coUserDir(t)},
				openMem: func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
				tick:    func() { ga.g.Frames(3) },
			}}
			h.start(Flags{on: true, world: coopWorldNew, merge: tc.merge, yes: true})
			defer h.stop()

			// The app joiner: GO names the world; the questions are not asked.
			log := &coLog{}
			notes := &followNotes{}
			jcs := startCoopSession(log, Flags{on: true, yes: true, follow: true}, coopEnv{
				backups: t.TempDir(), userDirs: []string{coUserDir(t)}, tick: func() { gb.g.Frames(3) },
				input: strings.NewReader(""),
			})
			jcs.world = h.cs.world
			jcs.notice = notes.add
			b := newCoPlayer(t, ctx, h, jcs, gb, "Tetra", log, &coAudit{}, 3)
			deadline := time.Now().Add(15 * time.Second)
			for b.s.r.Status().State != replica.StateArmed {
				if time.Now().After(deadline) {
					if tc.armed {
						t.Fatalf("not armed: %+v\nnotes %s\n%s", b.s.r.Status(), notes, log)
					}
					break
				}
				b.step()
				time.Sleep(2 * time.Millisecond)
			}
			for range 30 {
				b.step()
			}
			st := b.s.r.Status()
			if tc.armed != (st.State == replica.StateArmed) {
				t.Fatalf("armed %v, want %v: %+v\n%s", st.State == replica.StateArmed, tc.armed, st, log)
			}
			if log.has("refused") || log.has("did not accept the bind") {
				t.Errorf("the host refused something:\n%s", log)
			}
			ws := b.tagWrites(t)
			if len(ws) != tc.binds {
				t.Fatalf("%d tag writes, want %d: %+v\n%s", len(ws), tc.binds, ws, log)
			}
			if tc.cleared {
				if !bytes.Equal(ws[0].Data, make([]byte, bind.TagSize)) {
					t.Errorf("the first write is not the clear: %x", ws[0].Data)
				}
				// The other world's file was never needed on this PC, and
				// stays out of it.
			}
			if got := notes.String(); got != strings.ReplaceAll(strings.ReplaceAll(tc.notes, "cleared", "cleared "+h.cs.world.String()), "followed", "followed "+h.cs.world.String()) &&
				!noteMatches(got, tc.notes) {
				t.Errorf("notices %q, want %q", got, tc.notes)
			}
		})
	}
}

// noteMatches compares notices "outcome world" with the outcomes alone.
func noteMatches(got, want string) bool {
	var outs []string
	for _, n := range strings.Split(got, ", ") {
		if n == "" {
			continue
		}
		o, _, _ := strings.Cut(n, " ")
		outs = append(outs, o)
	}
	return strings.Join(outs, ", ") == want
}
