package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

// TestReconcileM5Status: Status.Puzzle says what /status shows of the room
// puzzles: whether they are granted and judged by a table, where Link is
// with his room's zone, and the partner's switches set, refused as not
// latching here (each once), and the room edges dropped.
func TestReconcileM5Status(t *testing.T) {
	here := world.Place{Stage: drc.Name, Table: drc.Table, Room: 1}
	t.Run("written, refused and dropped", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		st := rr.r.Status().Puzzle
		if want := (PuzzleStatus{Granted: true, Tables: true, Place: here, Zone: rr.slot(1)}); st != want {
			t.Fatalf("armed: %+v, want %+v", st, want)
		}
		for range 2 { // the refusal is counted once
			rr.send(danMsg(t, drc, danOK, danTimed), zoneMsg(t, drc, 1, zoneOK, zoneOK2), edgeMsg(t, drc, 1, roomOK))
			rr.settleM5()
		}
		if st := rr.r.Status().Puzzle; st.Applied != 4 || st.Refused != 1 || st.Dropped != 0 {
			t.Fatalf("after the stores: %+v\n%s", st, rr.log)
		}

		// An edge that waits out its ticks while an event runs is dropped,
		// and so is one for a room Link is not in.
		rr.g.SetEvent(1, 0)
		rr.ticks(2)
		rr.send(edgeMsg(t, drc, 1, roomOK2))
		rr.ticks(edgeTicks + 2)
		rr.g.SetEvent(0, 0)
		rr.send(edgeMsg(t, drc, 0, roomOK2))
		rr.settleM5()
		if st := rr.r.Status().Puzzle; st.Applied != 4 || st.Dropped != 2 {
			t.Fatalf("after the edges: %+v\n%s", st, rr.log)
		}

		rr.enter(2, 1)
		rr.settleM5()
		st = rr.r.Status().Puzzle
		if st.Place != (world.Place{Stage: drc.Name, Table: drc.Table, Room: 2}) || st.Zone != rr.slot(2) {
			t.Fatalf("in room 2: %+v", st)
		}
		rr.audit()
	})
	t.Run("not granted", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, proto.FeatureImage)
		rr.send(danMsg(t, drc, danOK))
		rr.settleM5()
		if st := rr.r.Status().Puzzle; st.Granted || !st.Tables || st.Applied != 0 || st.Place != here {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("no tables", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true}, m5Features)
		rr.send(danMsg(t, drc, danTimed))
		rr.settleM5()
		if st := rr.r.Status().Puzzle; !st.Granted || st.Tables || st.Applied != 0 || st.Refused != 0 {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("nowhere off the game screen", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.g.EnterFileSelect()
		rr.settleM5()
		if st := rr.r.Status().Puzzle; st.Place != world.Nowhere || st.Zone != -1 {
			t.Fatalf("on file select: %+v", st)
		}
	})
}
