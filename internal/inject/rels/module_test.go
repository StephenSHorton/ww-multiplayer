package rels_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// blobModule is this build's module, or skips t when the blob was
// generated without one (RELELF empty: no devkitPPC). A blob from before
// the body profile (no Body: its DOL does not alias proc 0x27) gets a
// stand-in: the body's entry pointed at the keeper's profile, so the
// two-profile patch runs on this ELF either way.
func blobModule(t *testing.T) rels.Module {
	t.Helper()
	m := rels.Blob()
	if m.Empty() {
		t.Skip("this blob has no REL module (RELELF is empty)")
	}
	if m.Body == nil {
		t.Logf("this blob predates the body profile (no %s): the body entry points at %s", inject.RELBodyProfileSymbol, m.Profile)
		m.Body = &rels.BodyProfile{Proc: inject.RELBodyProcID, Symbol: m.Profile}
	}
	return m
}

// TestBlobBodyPairing: the blob has a Body exactly when its DOL patches
// alias proc 0x27 to the module, and then the ELF defines the body
// profile; a blob without the alias patches the list as before (one
// entry).
func TestBlobBodyPairing(t *testing.T) {
	if want := inject.RELBodyNameEntryAddr + 6 - 0x80003000; want != 0x33693E {
		t.Fatalf("the name pointer low half is at file 0x%X", want)
	}
	m := rels.Blob()
	if (m.Body != nil) != rels.BlobAliasesBody() {
		t.Fatalf("Body %+v, DOL alias %v", m.Body, rels.BlobAliasesBody())
	}
	if m.Body == nil {
		t.Logf("this blob predates the body profile: no Body (the DOL does not alias proc 0x%X)", inject.RELBodyProcID)
		if !m.Empty() && m.HasBody() {
			t.Errorf("the ELF defines %s but the DOL does not alias proc 0x%X: rebuild with build.py's name patch", inject.RELBodyProfileSymbol, inject.RELBodyProcID)
		}
		return
	}
	if *m.Body != (rels.BodyProfile{Proc: inject.RELBodyProcID, Symbol: inject.RELBodyProfileSymbol}) || !m.HasBody() {
		t.Errorf("Body %+v, ELF has it %v", m.Body, m.HasBody())
	}
}

// expectREL is the REL rel.FromELF makes of m, as Bytes writes it, and
// where its profile is.
func expectREL(t *testing.T, m rels.Module) (*rel.File, []byte, rel.Symbol) {
	f, b, p, _ := expectRELBody(t, m)
	return f, b, p
}

// expectRELBody is expectREL with where the body profile is (zero
// without a Body).
func expectRELBody(t *testing.T, m rels.Module) (*rel.File, []byte, rel.Symbol, rel.Symbol) {
	t.Helper()
	f, lay, err := rel.FromELF(m.ELF, m.ID, rel.ELFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	section, offset, ok := lay.Symbol(m.Profile)
	if !ok {
		t.Fatalf("no %s in the ELF", m.Profile)
	}
	var body rel.Symbol
	if m.Body != nil {
		bs, bo, ok := lay.Symbol(m.Body.Symbol)
		if !ok {
			t.Fatalf("no %s in the ELF", m.Body.Symbol)
		}
		body = rel.Symbol{Section: bs, Offset: bo}
	}
	return f, b, rel.Symbol{Section: section, Offset: offset}, body
}

// relocAgainst is the relocation against module at offset in the list
// lf, and how many there are.
func relocAgainst(lf *rel.File, module, offset uint32) (*rel.Reloc, int) {
	var found *rel.Reloc
	n := 0
	for i := range lf.Imports {
		if lf.Imports[i].Module != module {
			continue
		}
		for j := range lf.Imports[i].Relocs {
			if r := &lf.Imports[i].Relocs[j]; r.Offset == offset && r.Type != rel.RelocNop {
				found, n = r, n+1
			}
		}
	}
	return found, n
}

// checkModuleInArc asserts arc holds m's REL as module m.ID (reading
// back as the REL FromELF makes, with its ID and prolog) and the profile
// list's relocation against it pointing at the profile. It returns the
// module's archive path.
func checkModuleInArc(t *testing.T, arc []byte, m rels.Module) string {
	t.Helper()
	want, wantBytes, profile, body := expectRELBody(t, m)
	a, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatalf("the archive doesn't parse: %v", err)
	}
	path := ""
	for _, f := range a.Files() {
		b, err := a.Read(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) < 4 {
			continue
		}
		switch binary.BigEndian.Uint32(b) {
		case m.ID:
			path = f.Path
			got, err := rel.Parse(b)
			if err != nil {
				t.Fatalf("%s: %v", f.Path, err)
			}
			if got.ID != m.ID || got.Prolog != want.Prolog || got.Epilog != want.Epilog || len(got.Sections) != len(want.Sections) {
				t.Errorf("%s: module 0x%x prolog %+v epilog %+v %d sections; want 0x%x %+v %+v %d",
					f.Path, got.ID, got.Prolog, got.Epilog, len(got.Sections), m.ID, want.Prolog, want.Epilog, len(want.Sections))
			}
			if !bytes.Equal(b, wantBytes) {
				t.Errorf("%s: %d bytes differ from the REL FromELF makes (%d)", f.Path, len(b), len(wantBytes))
			}
		case rels.ProfileListModule:
			lf, err := rel.Parse(b)
			if err != nil {
				t.Fatalf("%s: %v", f.Path, err)
			}
			if m.Body == nil {
				r, err := lf.SoleReloc(m.ID)
				if err != nil || r.Type != rel.RelocAddr32 || r.TargetSection != profile.Section || r.Addend != profile.Offset {
					t.Errorf("%s: relocation against 0x%x = %+v, %v; want ADDR32 to %+v", f.Path, m.ID, r, err, profile)
				}
				break
			}
			// Both entries against the module: its own and the body's, the
			// body's old module's import gone, the other module's entry
			// as it was.
			for _, c := range []struct {
				proc uint16
				want rel.Symbol
			}{{m.Proc, profile}, {m.Body.Proc, body}} {
				r, n := relocAgainst(lf, m.ID, 4*uint32(c.proc))
				if n != 1 || r.Type != rel.RelocAddr32 || r.TargetSection != c.want.Section || r.Addend != c.want.Offset {
					t.Errorf("%s: %d relocations against 0x%x at proc 0x%x's entry (%+v); want one ADDR32 to %+v", f.Path, n, m.ID, c.proc, r, c.want)
				}
			}
			if imp := lf.Import(relstest.SeatagModule); imp != nil {
				t.Errorf("%s: the import of module 0x%x is still there: %+v", f.Path, relstest.SeatagModule, imp)
			}
			if r, n := relocAgainst(lf, relstest.OtherModule, 4*relstest.OtherProc); n != 1 || r.Addend != 0x20 {
				t.Errorf("%s: module 0x%x's entry %+v (%d)", f.Path, relstest.OtherModule, r, n)
			}
		}
	}
	if path == "" {
		t.Errorf("no module 0x%x in the archive", m.ID)
	}
	return path
}

// Build: the synthetic archive with this build's module in it, the
// facts about what went in, and the errors of each step.
func TestModuleBuild(t *testing.T) {
	m := blobModule(t)
	vanilla := relstest.Arc()
	b, err := m.Build(vanilla)
	if err != nil {
		t.Fatal(err)
	}
	_, wantBytes, profile := expectREL(t, m)
	if b.File != relstest.PuppetFile || !bytes.Equal(b.REL, wantBytes) || b.Profile != profile || b.Growth != len(b.Arc)-len(vanilla) || b.Growth <= 0 {
		t.Errorf("built %q, REL %d bytes, profile %+v, growth %d; want %q, %d, %+v, >0", b.File, len(b.REL), b.Profile, b.Growth, relstest.PuppetFile, len(wantBytes), profile)
	}
	if got := checkModuleInArc(t, b.Arc, m); got != b.File {
		t.Errorf("the module is at %q, Built says %q", got, b.File)
	}
	real := rels.Blob()
	if arc, err := rels.BuildRELSArc(vanilla); err != nil {
		t.Errorf("BuildRELSArc: %v", err)
	} else if real.Body != nil && !bytes.Equal(arc, b.Arc) {
		t.Error("BuildRELSArc differs from Build")
	} else if real.Body == nil {
		// One entry only: the archive Batch A patched.
		checkModuleInArc(t, arc, real)
	}
	if !b.HasBody || (real.Body == nil && b.BodyProfile != b.Profile) {
		t.Errorf("built body %v at %+v (profile %+v)", b.HasBody, b.BodyProfile, b.Profile)
	}
	// Deterministic, and a fixed point.
	if again, err := m.Build(b.Arc); err != nil || !bytes.Equal(again.Arc, b.Arc) || again.Growth != 0 {
		t.Errorf("building from the built archive: %v, differs %v, growth %d", err, !bytes.Equal(again.Arc, b.Arc), again.Growth)
	}

	// Each step's failure names it.
	for name, tc := range map[string]struct {
		m    rels.Module
		arc  []byte
		want string
	}{
		"empty":            {rels.Module{}, vanilla, "no REL module in this build"},
		"not an ELF":       {rels.Module{ELF: []byte("not an ELF"), ID: m.ID, Profile: m.Profile}, vanilla, "rels: ELF to REL: rel:"},
		"no profile":       {rels.Module{ELF: m.ELF, ID: m.ID, Profile: "g_profile_NOPE"}, vanilla, "no g_profile_NOPE symbol in the module"},
		"module 0":         {rels.Module{ELF: m.ELF, ID: 0, Profile: m.Profile}, vanilla, "rels: ELF to REL: rel: module ID 0"},
		"no such module":   {rels.Module{ELF: m.ELF, ID: 0x59, Profile: m.Profile}, vanilla, "patch RELS.arc: rels: module 0x59: rarc: no file starts with the word 0x00000059"},
		"not an archive":   {m, []byte("RARC but no"), "patch RELS.arc: rels: rarc:"},
		"over the growth":  {rels.Module{ELF: m.ELF, ID: m.ID, Profile: m.Profile, MaxGrowth: 1}, vanilla, "RELS.arc would grow past the ARAM guard"},
		"growth with size": {rels.Module{ELF: m.ELF, ID: m.ID, Profile: m.Profile, MaxGrowth: 1}, vanilla, "), limit 1; shrink the REL"},
		"no body symbol": {rels.Module{ELF: m.ELF, ID: m.ID, Proc: m.Proc, Profile: m.Profile, Body: &rels.BodyProfile{Proc: 0x27, Symbol: "g_profile_NOPE"}}, vanilla,
			"no g_profile_NOPE symbol in the module (the body profile of process 0x27)"},
		"no body entry": {rels.Module{ELF: m.ELF, ID: m.ID, Proc: m.Proc, Profile: m.Profile, Body: &rels.BodyProfile{Proc: 0x30, Symbol: m.Profile}}, vanilla,
			"no relocation at process 0x30's entry (section 2 offset 0xc0)"},
		"main not at its entry": {rels.Module{ELF: m.ELF, ID: m.ID, Proc: 0xB6, Profile: m.Profile}, vanilla,
			"0 relocations against module 0x58 at process 0xb6's entry (offset 0x2d8), want one"},
		"body entry is the module's own": {rels.Module{ELF: m.ELF, ID: m.ID, Proc: m.Proc, Profile: m.Profile, Body: &rels.BodyProfile{Proc: m.Proc, Symbol: m.Profile}}, vanilla,
			"process 0xb5 given twice"},
	} {
		_, err := tc.m.Build(tc.arc)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if strings.HasPrefix(name, "growth") || name == "over the growth" {
			if !errors.Is(err, rels.ErrRELSArcGrowth) || !strings.Contains(err.Error(), "bytes ("+strconv.Itoa(len(vanilla))+" -> ") {
				t.Errorf("%s: err = %v, want ErrRELSArcGrowth with the sizes", name, err)
			}
		}
	}
}

// PatchISOInMemory: a synthetic disc with a RELS.arc comes out with
// this build's DOL and the archive replaced, the FST's entry pointing at
// it, the Result saying what went in, and patching the output again is
// ErrAlreadyPatched; an Empty module patches the DOL alone; the growth
// guard's failure surfaces with its numbers.
func TestModulePatchISOInMemory(t *testing.T) {
	m := blobModule(t)
	img := tablestest.PatchableISO(nil)
	out, res, err := m.PatchISOInMemory(img)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasREL || !res.RELSArcReplaced() || res.ModuleID != m.ID || res.File != relstest.PuppetFile || res.BuildID != m.BuildID || res.RELSize == 0 || res.Growth <= 0 ||
		res.BodyProc != relstest.SeatagProc {
		t.Errorf("result %+v", res)
	}
	d, err := inject.NewDisc(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("the output doesn't parse: %v", err)
	}
	f, err := d.Stat("RELS.arc")
	if err != nil || f.Offset != res.RELSArcOffset || f.Size != res.RELSArcSize {
		t.Errorf("RELS.arc entry %+v, %v; result says 0x%x (%d bytes)", f, err, res.RELSArcOffset, res.RELSArcSize)
	}
	arc, err := d.ReadFile("RELS.arc")
	if err != nil {
		t.Fatal(err)
	}
	if path := checkModuleInArc(t, arc, m); path != res.File {
		t.Errorf("module at %q, result says %q", path, res.File)
	}
	want, err := m.Build(relstest.Arc())
	if err != nil || !bytes.Equal(arc, want.Arc) || res.Growth != want.Growth || res.RELSize != len(want.REL) {
		t.Errorf("the archive on the disc isn't Build's: %v, growth %d vs %d, REL %d vs %d", err, res.Growth, want.Growth, res.RELSize, len(want.REL))
	}
	if dol, err := d.ReadDOL(); err != nil || !bytes.Contains(dol, inject.T2Bytes) {
		t.Errorf("no T2 in the output's DOL: %v", err)
	}
	if direct, dres, err := inject.PatchISOInMemoryWith(img, inject.Patch{RELSArc: want.Arc}); err != nil || !bytes.Equal(direct, out) || dres != res.Result {
		t.Errorf("differs from inject's patch with the built archive: %v", err)
	}

	// Idempotency: the output holds this build's DOL and archive.
	if _, _, err := m.PatchISOInMemory(out); !errors.Is(err, inject.ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
	// Another build's DOL is refused as such.
	foreign := bytes.Clone(out)
	foreign[bytes.Index(foreign, inject.T2Bytes)+len(inject.T2Bytes)-1] ^= 1
	if _, _, err := m.PatchISOInMemory(foreign); !errors.Is(err, inject.ErrPatchedByOtherVersion) {
		t.Errorf("another build's DOL: err = %v, want ErrPatchedByOtherVersion", err)
	}

	// No module: the DOL alone, the disc's RELS.arc left as it is.
	none := rels.Module{}
	out2, res2, err := none.PatchISOInMemory(img)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := inject.PatchISOInMemory(img)
	if err != nil || !bytes.Equal(out2, plain) {
		t.Errorf("an empty module's patch differs from the DOL-only one: %v", err)
	}
	if res2.HasREL || res2.RELSArcReplaced() || res2.File != "" || res2.BuildID != 0 {
		t.Errorf("an empty module's result %+v", res2)
	}
	if got, err := inject.RELSArcFromImage(out2); err != nil || !bytes.Equal(got, relstest.Arc()) {
		t.Errorf("an empty module changed RELS.arc: %v", err)
	}
	if _, _, err := none.PatchISOInMemory(out2); !errors.Is(err, inject.ErrAlreadyPatched) {
		t.Errorf("re-patching the DOL-only output: err = %v", err)
	}

	// The growth guard, with the numbers.
	tight := m
	tight.MaxGrowth = 1
	_, _, err = tight.PatchISOInMemory(img)
	if !errors.Is(err, rels.ErrRELSArcGrowth) || !strings.Contains(err.Error(), "iso: RELS.arc: patch RELS.arc: rels: RELS.arc would grow past the ARAM guard: ") || !strings.Contains(err.Error(), "limit 1; shrink the REL") {
		t.Errorf("over the guard: %v", err)
	}
	// The input wasn't touched.
	if !bytes.Equal(img, tablestest.PatchableISO(nil)) {
		t.Error("the input image changed")
	}
}

// PatchISO and PatchISOWithREL on files: the output reads back as the
// in-memory patch, the input is untouched, and the output patched
// again is ErrAlreadyPatched.
func TestModulePatchISOFile(t *testing.T) {
	m := rels.Blob() // what PatchISOWithREL patches with
	if m.Empty() {
		t.Skip("this blob has no REL module (RELELF is empty)")
	}
	dir := t.TempDir()
	img := tablestest.PatchableISO(nil)
	in := filepath.Join(dir, "ww.iso")
	if err := os.WriteFile(in, img, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "ww-multiplayer.iso")
	res, err := rels.PatchISOWithREL(in, out)
	if err != nil {
		t.Fatal(err)
	}
	want, wres, err := m.PatchISOInMemory(img)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, want) || res != wres {
		t.Errorf("the file differs from the in-memory patch: %v, %+v vs %+v", err, res, wres)
	}
	if b, err := os.ReadFile(in); err != nil || !bytes.Equal(b, img) {
		t.Error("the input file changed")
	}
	if _, err := rels.PatchISOWithREL(out, filepath.Join(dir, "again.iso")); !errors.Is(err, inject.ErrAlreadyPatched) {
		t.Errorf("re-patching the output: %v", err)
	}
	if _, err := rels.PatchISOWithREL(in, in); !errors.Is(err, inject.ErrOutputIsInput) {
		t.Errorf("out == in: %v", err)
	}
}
