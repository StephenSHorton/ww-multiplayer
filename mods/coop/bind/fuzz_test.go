package bind

import (
	"bytes"
	"slices"
	"testing"
)

// Fuzz targets for the tag reader: mReserve is whatever the loaded file's
// card held, so Decode and Classify see arbitrary bytes. The seed corpora
// run as ordinary tests under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzDecode -fuzztime=20s ./mods/coop/bind/

func tagSeeds() [][]byte {
	good := sampleTag().Encode()
	newer := good
	newer[offLayout] = 2
	bent := good
	bent[offCRC] ^= 0xFF
	return [][]byte{good[:], newer[:], bent[:], make([]byte, TagSize), nil, []byte("WWCO"), bytes.Repeat([]byte{0xFF}, 0x50)}
}

func FuzzDecode(f *testing.F) {
	for _, seed := range tagSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tg, err := Decode(data)
		if err != nil {
			return
		}
		// A tag that decodes is a valid layout-1 tag and re-encodes to
		// exactly the bytes it came from.
		if tg.Layout != LayoutV1 || tg.Flags != 0 || tg.World.IsZero() || tg.Nonce.IsZero() {
			t.Fatalf("decoded an invalid tag %+v", tg)
		}
		if enc := tg.Encode(); !bytes.Equal(enc[:], data[:TagSize]) {
			t.Fatalf("re-encoded % X, decoded from % X", enc, data[:TagSize])
		}
	})
}

func FuzzClassify(f *testing.F) {
	for _, seed := range tagSeeds() {
		f.Add(seed, uint8(2), uint8(0), []byte("Link"), uint64(1), false, false)
	}
	f.Add(tagSeeds()[0], uint8(1), uint8(1), []byte("Tetra\x00Link"), uint64(0), true, true)
	f.Fuzz(func(t *testing.T, tag []byte, slot, clearCount uint8, name []byte, date uint64, withWorld, fresh bool) {
		var file File
		copy(file.Tag[:], tag)
		file.Slot, file.ClearCount, file.DateIPL = slot, clearCount, date
		copy(file.Name[:], name)
		var e Expect
		if withWorld {
			e.World = worldA
		}
		if fresh {
			e.Fresh = func(n Nonce) bool { return n == nonce1 }
		}

		v := Classify(file, e)
		if v != Classify(file, e) {
			t.Fatal("Classify is not deterministic")
		}
		ran := 0
		for i, c := range v.Checks {
			if c.Rule != Rule(i+1) {
				t.Fatalf("check %d is %s", i, c.Rule)
			}
			if c.Ran {
				ran++
			}
		}
		failed := v.Failed()
		switch v.State {
		case Unbound:
			if v.HasTag || len(failed) > 1 || (len(failed) == 1) == v.Empty {
				t.Fatalf("unbound with tag %v, failing %v, empty %v", v.HasTag, failed, v.Empty)
			}
		case Bound:
			// Bound means every rule that ran held, and that V1-V5 all ran
			// (V6 too when a world was given).
			want := 5
			if withWorld {
				want = 6
			}
			if !v.HasTag || len(failed) != 0 || ran != want || (withWorld && v.Tag.World != worldA) {
				t.Fatalf("bound with tag %v, failing %v, %d checks run", v.HasTag, failed, ran)
			}
			if v.Tag.Slot != slot || v.Tag.ClearCount != clearCount || v.Tag.NameHash != NameHash(file.Name[:]) {
				t.Fatalf("bound tag %+v doesn't match the file", v.Tag)
			}
			if date == 0 && !(fresh && v.Tag.Nonce == nonce1) {
				t.Fatal("bound a never-loaded file without a pending fresh bind")
			}
		case Leaked:
			if !v.HasTag || len(failed) == 0 || failed[0] == V1 || failed[len(failed)-1] == V6 {
				t.Fatalf("leaked failing %v", failed)
			}
		case Foreign:
			if v.HasTag && (!slices.Equal(failed, []Rule{V6}) || !withWorld) {
				t.Fatalf("foreign failing %v", failed)
			}
			if !v.HasTag && !slices.Equal(failed, []Rule{V1}) {
				t.Fatalf("foreign without a tag failing %v", failed)
			}
		default:
			t.Fatalf("state %d is no CO_HELLO fileState", v.State)
		}
	})
}
