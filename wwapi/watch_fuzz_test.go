package wwapi_test

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
)

// FuzzWatcherStep: any pair of snapshots (save bytes, table, stage)
// diffs without a panic, and every event names a valid slot, switch or
// event byte.
func FuzzWatcherStep(f *testing.F) {
	f.Add([]byte{0}, []byte{1}, int8(0), int8(0), "sea", "sea", true)
	f.Add([]byte{0xFF, 0x00}, []byte{0x27, 0x80}, int8(-1), int8(13), "Name", "M_NewD2", false)
	f.Fuzz(func(t *testing.T, a, b []byte, ta, tb int8, sa, sb string, beat bool) {
		prev, cur := new(snapshot), new(snapshot)
		// Spread the fuzz bytes over the whole save, so every region
		// sees changes.
		for i := range prev.Save {
			if len(a) > 0 {
				prev.Save[i] = a[i%len(a)] ^ byte(i>>4)
			}
			if len(b) > 0 {
				cur.Save[i] = b[i%len(b)] ^ byte(i>>4)
			}
		}
		prev.Table, cur.Table = ta, tb
		prev.StageName, cur.StageName = sa, sb
		var w wwapi.Watcher
		w.Step(prev, gate.Status{Heartbeat: beat})
		for _, e := range w.Step(cur, gate.Status{}) {
			switch e := e.(type) {
			case events.ItemGained:
				if e.Slot < 0 || e.Slot >= 21 || e.Item == 0xFF || e.Item == e.Prev {
					t.Errorf("bad %+v", e)
				}
			case events.SwitchSet:
				if e.Switch < 0 || e.Switch >= 0xF0 {
					t.Errorf("bad %+v", e)
				}
			case events.EventBitSet:
				if e.ID>>8 >= 0x79 || e.ID&0xFF == 0 {
					t.Errorf("bad %+v", e)
				}
			}
		}
	})
}
