package engine

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
)

// TestPlayerViewVitals: a remote's vitals come from its pose trailer
// (wind#55); an older sender's shorter trailer gives none, and no error.
func TestPlayerViewVitals(t *testing.T) {
	v := vissync.Vitals{Life: 5, MaxLife: 0x30, Magic: 9, MaxMagic: 0x10, Rupees: 120}
	full := append(make([]byte, vissync.VitalsOffset), vissync.EncodeVitals(v, true)...)
	pv := playerView(network.RemotePlayer{ID: 2, Name: "Tetra", FaceState: full})
	want := session.Vitals{Life: 5, MaxLife: 0x30, Magic: 9, MaxMagic: 0x10, Rupees: 120}
	if !pv.HasVitals || pv.Vitals != want {
		t.Fatalf("64-byte trailer: got %+v %v, want %+v", pv.Vitals, pv.HasVitals, want)
	}
	for _, n := range []int{0, 16, 19, 59} {
		if pv := playerView(network.RemotePlayer{ID: 2, Name: "Tetra", FaceState: full[:n]}); pv.HasVitals {
			t.Errorf("%d-byte trailer: vitals %+v from an older sender", n, pv.Vitals)
		}
	}
}

// TestLiveStateLocalVitals: SetLocalVitals reaches LocalVitals; a failed
// read clears them, and so does a new session.
func TestLiveStateLocalVitals(t *testing.T) {
	ls := &LiveState{}
	if _, ok := ls.LocalVitals(); ok {
		t.Fatal("vitals before any read")
	}
	LiveStateVitalsSetter(ls)(vissync.Vitals{Life: 12, MaxLife: 12, Rupees: 3}, true)
	if v, ok := ls.LocalVitals(); !ok || v != (session.Vitals{Life: 12, MaxLife: 12, Rupees: 3}) {
		t.Fatalf("got %+v %v", v, ok)
	}
	ls.SetLocalVitals(vissync.Vitals{}, false)
	if _, ok := ls.LocalVitals(); ok {
		t.Error("a failed read kept the old vitals")
	}
	ls.SetLocalVitals(vissync.Vitals{Life: 1, MaxLife: 12}, true)
	ls.Reset()
	if _, ok := ls.LocalVitals(); ok {
		t.Error("vitals survived Reset")
	}
	if LiveStateVitalsSetter(nil) != nil {
		t.Error("nil LiveState gave a setter")
	}
}
