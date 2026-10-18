package rels_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// puppetModule is d_a_rectangle.rel's module ID (proc 0xB5,
// PROC_RECTANGLE), the module the puppet REL takes over.
const puppetModule = 0x58

// The disc's RELS.arc with a dummy REL in place of module 0x58: it
// parses, the module's entry reads back as the dummy, the profile list's
// relocation against the module points where asked and nothing else in
// it changed, every other entry is byte for byte as stored on the disc,
// and the growth is the dummy's stored size plus padding at most.
func TestPatchRELSArcVanilla(t *testing.T) {
	arc := vanillatest.ReadFile(t, vanillatest.RELSArc)
	before, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	// A dummy REL of the puppet's stored size (ww-online's is 87 KB raw,
	// 35 KB Yaz0) that doesn't compress, so the growth is a real one.
	text := make([]byte, 0x8000)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range text {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		text[i] = byte(x)
	}
	dummy, err := (&rel.File{
		ID: puppetModule, Version: 3, Align: 8, BSSAlign: 1,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 4}, Unresolved: rel.Symbol{Section: 1, Offset: 8},
		Sections: []rel.Section{{}, {Offset: 1, Exec: true, Data: text}, {Offset: 1, Data: make([]byte, 0x100)}},
	}).Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	const profileSection, profileOffset = 2, 0x40

	out, err := rels.PatchRELSArc(arc, puppetModule, dummy, profileSection, profileOffset, rels.RELSArcMaxGrowth)
	if err != nil {
		t.Fatal(err)
	}
	after, err := stage.ParseArchive(out)
	if err != nil {
		t.Fatalf("the patched RELS.arc doesn't parse: %v", err)
	}
	bf, af := before.Files(), after.Files()
	if len(bf) != len(af) {
		t.Fatalf("%d files, the disc has %d", len(af), len(bf))
	}
	storedDummy := stage.EncodeYaz0(dummy)
	replaced, lists := "", ""
	for i := range bf {
		b, a := bf[i], af[i]
		if a.Path != b.Path || a.Index != b.Index || a.ID != b.ID || a.Attr != b.Attr {
			t.Errorf("entry %d: %+v, the disc has %+v", i, a, b)
		}
		was, err := before.Read(b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := after.Read(a)
		if err != nil {
			t.Fatalf("%s: %v", a.Path, err)
		}
		switch id := binary.BigEndian.Uint32(was); id {
		case puppetModule:
			replaced = a.Path
			if !bytes.Equal(got, dummy) || a.Size != uint32(len(storedDummy)) {
				t.Errorf("%s: %d bytes stored as %d, want the dummy (%d, %d stored)", a.Path, len(got), a.Size, len(dummy), len(storedDummy))
			}
		case rels.ProfileListModule:
			lists = a.Path
			lf, err := rel.Parse(got)
			if err != nil {
				t.Fatalf("%s: %v", a.Path, err)
			}
			r, err := lf.SoleReloc(puppetModule)
			if err != nil || r.Type != rel.RelocAddr32 || r.TargetSection != profileSection || r.Addend != profileOffset {
				t.Errorf("%s: relocation against 0x%x = %+v, %v", a.Path, puppetModule, r, err)
			}
			diff := 0
			for k := range was {
				if len(got) != len(was) || got[k] != was[k] {
					diff++
				}
			}
			if len(got) != len(was) || diff == 0 || diff > 8 {
				t.Errorf("%s: %d bytes differ (%d, the disc's %d), want the one entry", a.Path, diff, len(got), len(was))
			}
		default:
			// Stored bytes identical: compare through the archives' data.
			if !bytes.Equal(got, was) || a.Size != b.Size {
				t.Errorf("%s changed (%d bytes stored, was %d)", a.Path, a.Size, b.Size)
			}
		}
	}
	if replaced != "mmem/d_a_rectangle.rel" || lists != "mmem/f_pc_profile_lst.rel" {
		t.Errorf("replaced %q, profile list %q", replaced, lists)
	}
	growth := len(out) - len(arc)
	t.Logf("RELS.arc %d -> %d bytes (+%d); the dummy is %d bytes, %d stored", len(arc), len(out), growth, len(dummy), len(storedDummy))
	if growth <= 0 || growth > len(storedDummy)+0x20 {
		t.Errorf("grew by %d, want between 1 and %d", growth, len(storedDummy)+0x20)
	}
	// A guard just under the growth refuses.
	if _, err := rels.PatchRELSArc(arc, puppetModule, dummy, profileSection, profileOffset, growth-1); !errors.Is(err, rels.ErrRELSArcGrowth) {
		t.Errorf("with the guard one byte under the growth: %v", err)
	}
}

// The real pipeline end to end on the vanilla disc, in memory: this
// build's module into the disc's RELS.arc (rels.PatchISOWithREL's path,
// Module.PatchISOInMemory). The output parses as a disc, its RELS.arc at
// the offset the result gives parses, module 0x58 reads back as our REL
// (ID 0x58, prolog section and offset the Layout's), the profile list's
// relocation against it points at the profile, every other entry is
// byte for byte the disc's, and the output patched again is
// ErrAlreadyPatched. Nothing is written to disk. (This holds the
// decompressed disc and its patched copy: about 3 GB.)
func TestPatchISOWithRELVanilla(t *testing.T) {
	m := rels.Blob()
	if m.Empty() {
		t.Skip("this blob has no REL module (RELELF is empty)")
	}
	p := vanillatest.Path()
	if p == "" {
		t.Skipf("no vanilla disc: set %s to the ISO or CISO", vanillatest.EnvVar)
	}
	img, err := inject.LoadImage(p)
	if err != nil {
		t.Skipf("vanilla disc %s: %v", p, err)
	}
	vanillaArc, err := inject.RELSArcFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	out, res, err := m.PatchISOInMemory(img)
	if err != nil {
		t.Fatal(err)
	}
	img = nil // let the GC have the input
	if !res.HasREL || !res.RELSArcReplaced() || res.ModuleID != puppetModule || res.File != "mmem/d_a_rectangle.rel" || res.BuildID != inject.RELBuildID {
		t.Errorf("result %+v", res)
	}
	t.Logf("FST 0x%X -> 0x%X, RELS.arc at 0x%X (%d bytes, %+d), REL %d bytes, build 0x%08X",
		res.OldFSTOffset, res.NewFSTOffset, res.RELSArcOffset, res.RELSArcSize, res.Growth, res.RELSize, res.BuildID)
	if res.Growth <= 0 || res.Growth > rels.RELSArcMaxGrowth {
		t.Errorf("growth %d, want between 1 and the guard %d", res.Growth, rels.RELSArcMaxGrowth)
	}

	d, err := inject.NewDisc(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("the output doesn't parse: %v", err)
	}
	f, err := d.Stat(vanillatest.RELSArc)
	if err != nil || f.Offset != res.RELSArcOffset || f.Size != res.RELSArcSize {
		t.Fatalf("RELS.arc entry %+v, %v; the result says 0x%X (%d bytes)", f, err, res.RELSArcOffset, res.RELSArcSize)
	}
	arc, err := d.ReadFile(vanillatest.RELSArc)
	if err != nil {
		t.Fatal(err)
	}
	after, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatalf("the patched RELS.arc doesn't parse: %v", err)
	}
	before, err := stage.ParseArchive(vanillaArc)
	if err != nil {
		t.Fatal(err)
	}
	want, lay, err := rel.FromELF(m.ELF, m.ID, rel.ELFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	section, offset, ok := lay.Symbol(m.Profile)
	if !ok {
		t.Fatalf("no %s in the ELF", m.Profile)
	}
	bf, af := before.Files(), after.Files()
	if len(bf) != len(af) {
		t.Fatalf("%d files, the disc has %d", len(af), len(bf))
	}
	replaced, lists := "", ""
	for i := range bf {
		b, a := bf[i], af[i]
		if a.Path != b.Path || a.Index != b.Index || a.ID != b.ID || a.Attr != b.Attr {
			t.Errorf("entry %d: %+v, the disc has %+v", i, a, b)
		}
		was, err := before.Read(b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := after.Read(a)
		if err != nil {
			t.Fatalf("%s: %v", a.Path, err)
		}
		switch binary.BigEndian.Uint32(was) {
		case puppetModule:
			replaced = a.Path
			r, err := rel.Parse(got)
			if err != nil {
				t.Fatalf("%s: %v", a.Path, err)
			}
			if r.ID != m.ID || r.Prolog != want.Prolog || r.Epilog != want.Epilog || r.Unresolved != want.Unresolved {
				t.Errorf("%s: module 0x%x prolog %+v epilog %+v unresolved %+v; want 0x%x %+v %+v %+v",
					a.Path, r.ID, r.Prolog, r.Epilog, r.Unresolved, m.ID, want.Prolog, want.Epilog, want.Unresolved)
			}
			if !bytes.Equal(got, wantBytes) {
				t.Errorf("%s: %d bytes differ from the REL FromELF makes (%d)", a.Path, len(got), len(wantBytes))
			}
			if res.RELSize != len(got) {
				t.Errorf("result says the REL is %d bytes, it is %d", res.RELSize, len(got))
			}
		case rels.ProfileListModule:
			lists = a.Path
			lf, err := rel.Parse(got)
			if err != nil {
				t.Fatalf("%s: %v", a.Path, err)
			}
			r, err := lf.SoleReloc(puppetModule)
			if err != nil || r.Type != rel.RelocAddr32 || r.TargetSection != section || r.Addend != offset {
				t.Errorf("%s: relocation against 0x%x = %+v, %v; want ADDR32 to section %d offset 0x%x", a.Path, puppetModule, r, err, section, offset)
			}
			if len(got) != len(was) {
				t.Errorf("%s: %d bytes, the disc's is %d", a.Path, len(got), len(was))
			}
		default:
			if !bytes.Equal(got, was) || a.Size != b.Size {
				t.Errorf("%s changed (%d bytes stored, was %d)", a.Path, a.Size, b.Size)
			}
		}
	}
	if replaced != "mmem/d_a_rectangle.rel" || lists != "mmem/f_pc_profile_lst.rel" {
		t.Errorf("replaced %q, profile list %q", replaced, lists)
	}
	if _, _, err := m.PatchISOInMemory(out); !errors.Is(err, inject.ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
}
