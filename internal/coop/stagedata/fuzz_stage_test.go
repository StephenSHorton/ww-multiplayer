package stagedata

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// Fuzz targets for the DZx reader, the l_objectName reader and the stage
// loader over both archives, in the style of fuzz_test.go. The seed
// corpora are built in code (no game data) and run as ordinary tests; to
// fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzParseDZx -fuzztime=30s ./internal/coop/stagedata/

func FuzzParseDZx(f *testing.F) {
	sample := sampleDZx()
	overlap := stagetest.DZx(stagetest.Actors("ACTR", make([]stagetest.Actor, 4)...), stagetest.Chunk{Tag: "ACT0", Count: 4})
	binary.BigEndian.PutUint32(overlap[4+0xC+8:], binary.BigEndian.Uint32(overlap[4+8:]))
	for _, seed := range [][]byte{
		sample,
		sample[:len(sample)/2],
		overlap,
		stagetest.DZx(),
		stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(3, int(StageDungeon), true))),
		stagetest.DZx(stagetest.Actors("DOOR", stagetest.Actor{Name: "door12AB", Scale: [3]uint8{1, 2, 3}})),
		stagetest.DZx(stagetest.Chunk{Tag: "ACTR", Count: -1}, stagetest.Chunk{Tag: "STAG", Count: 0}),
		nil, {0, 0, 0, 1}, {0xFF, 0xFF, 0xFF, 0xFF},
	} {
		f.Add(seed)
	}
	byTag := make(map[string]placeTag, len(placeTags))
	for _, pt := range placeTags {
		byTag[pt.tag] = pt
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := ParseDZx(b)
		if err != nil {
			return
		}
		if dzxHeaderSize+len(d.Chunks)*dzxChunkSize > len(b) || len(d.Actors)*actorEntrySize > len(b) {
			t.Fatalf("%d chunks and %d entries from %d bytes", len(d.Chunks), len(d.Actors), len(b))
		}
		for _, a := range d.Actors {
			pt, ok := byTag[a.Tag]
			c, _ := d.Chunk(a.Tag)
			if !ok || a.Layer != pt.layer || a.Scaled != pt.scaled || a.Index >= c.Count || len(a.Name) > 8 ||
				(!a.Scaled && a.Scale != [3]uint8{}) {
				t.Fatalf("entry %+v (chunk %+v)", a, c)
			}
		}
		// The entries and STAG, written out again with stagetest, parse
		// back the same: the field offsets agree both ways.
		var chunks []stagetest.Chunk
		if d.Stag != nil {
			s := *d.Stag
			chunks = append(chunks, stagetest.StagChunk(stagetest.Stag{
				Near: s.Near, Far: s.Far, CameraType: s.CameraType, Prop: s.Prop, ParticleScene: s.ParticleScene,
				TypeAndSchbit: s.TypeAndSchbit, SchbitAndFar: s.SchbitAndFar, Rest: s.Rest,
			}))
		}
		for _, pt := range placeTags {
			var as []stagetest.Actor
			for _, a := range d.Actors {
				if a.Tag == pt.tag {
					as = append(as, stagetest.Actor{Name: a.Name, Params: a.Params, Pos: a.Pos, Angle: a.Angle, SetID: a.SetID, Scale: a.Scale})
				}
			}
			if as != nil {
				chunks = append(chunks, stagetest.Actors(pt.tag, as...))
			}
		}
		again, err := ParseDZx(stagetest.DZx(chunks...))
		if err != nil {
			t.Fatalf("rebuilt file: %v", err)
		}
		if !slices.EqualFunc(again.Actors, d.Actors, sameActor) || (again.Stag == nil) != (d.Stag == nil) ||
			(d.Stag != nil && !sameStag(*again.Stag, *d.Stag)) {
			t.Fatalf("round trip:\n%+v\n%+v\nbecame\n%+v\n%+v", d.Stag, d.Actors, again.Stag, again.Actors)
		}
	})
}

// sameActor compares entries with the floats as bits, so a NaN position
// equals itself.
func sameActor(a, b Actor) bool {
	for i := range a.Pos {
		if math.Float32bits(a.Pos[i]) != math.Float32bits(b.Pos[i]) {
			return false
		}
	}
	a.Pos, b.Pos = [3]float32{}, [3]float32{}
	return a == b
}

func sameStag(a, b Stag) bool {
	if math.Float32bits(a.Near) != math.Float32bits(b.Near) || math.Float32bits(a.Far) != math.Float32bits(b.Far) {
		return false
	}
	a.Near, a.Far, b.Near, b.Far = 0, 0, 0, 0
	return a == b
}

func FuzzReadObjectNames(f *testing.F) {
	good := stagetest.MainDOL(sampleNames()...)
	for _, seed := range [][]byte{
		good,
		stagetest.MainDOL(),
		stagetest.MainDOL(stagetest.ObjectName{Name: "door12AB"}),
		stagetest.DOL(ObjectNameAddr, stagetest.ObjectNameTable(sampleNames()...)[:0x100]),
		good[:len(good)/2], good[:0x100], nil,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, dol []byte) {
		o, err := ReadObjectNames(dol)
		if err != nil {
			return
		}
		list := o.List()
		if o.Len() != ObjectNameCount || len(list) != ObjectNameCount || list[len(list)-1].Name != objectNameEnd {
			t.Fatalf("%d entries, last %+v", o.Len(), list[len(list)-1])
		}
		for i, e := range list {
			if len(e.Name) == 0 || len(e.Name) >= objectNameLen || !printable(e.Name) || e.Proc < 0 || e.Proc >= ProcMaxNum {
				t.Fatalf("entry %d: %+v", i, e)
			}
			// Lookup finds the first entry of the name.
			k := slices.IndexFunc(list, func(x ObjectName) bool { return x.Name == e.Name })
			if got, ok := o.Lookup(e.Name); !ok || got != list[k] || k > i {
				t.Fatalf("Lookup(%q) = %+v, %v; the first entry of it is %d, %+v", e.Name, got, ok, k, list[k])
			}
		}
	})
}

// FuzzLoad loads a disc holding one stage: the fuzzed Stage.arc and one
// fuzzed room archive.
func FuzzLoad(f *testing.F) {
	s := stagetest.Stage{
		Name: "fz",
		DZS:  stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(4, int(StageDungeon), true)), act("ACTR", "Bk", 1)),
	}
	room := stagetest.Room{No: 3, DZR: sampleDZx()}
	z := s
	z.Yaz0 = true
	for _, seed := range [][2][]byte{
		{s.StageArc(), s.RoomArc(room)},
		{z.StageArc(), z.RoomArc(room)},
		{s.StageArc(), s.RoomArc(stagetest.Room{No: 3})},
		{s.StageArc(), nil},
		{stagetest.Stage{Name: "fz"}.StageArc(), s.RoomArc(room)},
		{nil, nil},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, stageArc, roomArc []byte) {
		d := new(stagetest.Disc)
		d.Add("res/Stage/fz/Stage.arc", stageArc)
		d.Add("res/Stage/fz/Room3.arc", roomArc)
		stages, err := Load(d)
		if err != nil {
			return
		}
		if len(stages) != 1 {
			t.Fatalf("%d stages", len(stages))
		}
		st := stages[0]
		if st.DZS == nil || st.DZS.Stag == nil || st.Stag != *st.DZS.Stag || st.SaveTable != st.Stag.SaveTable() ||
			len(st.Rooms) != 1 || st.Rooms[0].No != 3 {
			t.Fatalf("stage %+v", st)
		}
		if n := len(st.DZS.Actors) + len(st.Rooms[0].Actors()); len(st.All()) != n {
			t.Fatalf("All() = %d entries, the files have %d", len(st.All()), n)
		}
		for _, l := range st.Layers() {
			if l < 0 || l >= NumLayers || len(st.OnLayer(l)) > len(st.All()) {
				t.Fatalf("layer %d", l)
			}
		}
	})
}
