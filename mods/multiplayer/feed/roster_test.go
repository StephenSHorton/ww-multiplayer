package feed

import (
	"context"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
)

// TestFeederViews: after a Feed, every drawn remote is a RemoteView with
// its link slot (or -1 beyond avatar.Cap), the smoothed position plus the
// render offset, the raw angle, and Live while its pose is fresh; the
// broadcast twin is not one.
func TestFeederViews(t *testing.T) {
	f, _, _ := newTestFeeder(t)
	f.offX = 7
	now := time.Unix(1000, 0)
	rs := []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 300, true, now)}
	self := remote(9, 5, true, now)
	self.Name = "Self"
	rs = append(rs, self)
	f.Feed(context.Background(), rs, now)
	vs := f.Views(rs, now)
	if len(vs) != 3 {
		t.Fatalf("%d views, want 3: %+v", len(vs), vs)
	}
	for i, v := range vs {
		id := byte(i + 1)
		wantSlot := i
		if i >= avatar.Cap {
			wantSlot = -1
		}
		if v.ID != id || v.Name != rs[i].Name || v.LinkSlot != wantSlot || v.RotY != 0x4000 {
			t.Errorf("view %d = %+v", i, v)
		}
		if v.Pos != [3]float32{float32(100*(i+1)) + 7, 0, 0} {
			t.Errorf("view %d pos = %v", i, v.Pos)
		}
		if v.Live != (wantSlot >= 0) {
			t.Errorf("view %d live = %v", i, v.Live)
		}
	}
	// A silent pose stream: frozen, not live.
	later := now.Add(f.freezeAfter + time.Millisecond)
	f.Feed(context.Background(), rs[:1], later)
	if vs := f.Views(rs[:1], later); len(vs) != 1 || vs[0].LinkSlot != 0 || vs[0].Live {
		t.Errorf("frozen views = %+v", vs)
	}
}

// TestRosterFor: one roster per (name, pid), shared by every caller;
// Snapshot copies.
func TestRosterFor(t *testing.T) {
	a, b, c := RosterFor("roster-test", 1), RosterFor("roster-test", 1), RosterFor("roster-test", 2)
	if a != b || a == c {
		t.Fatal("RosterFor keys")
	}
	if vs, at := a.Snapshot(); vs != nil || !at.IsZero() {
		t.Errorf("fresh roster = %v %v", vs, at)
	}
	now := time.Unix(5, 0)
	a.Publish([]RemoteView{{ID: 4, LinkSlot: 1}}, now)
	vs, at := b.Snapshot()
	if len(vs) != 1 || vs[0].ID != 4 || !at.Equal(now) {
		t.Errorf("snapshot = %v %v", vs, at)
	}
	vs[0].ID = 9
	if again, _ := a.Snapshot(); again[0].ID != 4 {
		t.Error("Snapshot shares its slice")
	}
	a.Publish(nil, now)
	if vs, _ := a.Snapshot(); len(vs) != 0 {
		t.Errorf("cleared = %v", vs)
	}
}
