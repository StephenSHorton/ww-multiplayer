package inject

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// RELSArcFromImage returns the bytes the FST's root "RELS.arc" entry
// points at, on a vanilla image and on a patched one (the archive the
// patch wrote), and refuses what the patcher refuses.
func TestRELSArcFromImage(t *testing.T) {
	b := vanillaLike().build(t)
	got, err := RELSArcFromImage(b.img)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, b.img[0x560000:0x560000+0x31000]) {
		t.Errorf("%d bytes, want the vanilla entry's 0x31000", len(got))
	}
	got[0] ^= 0xFF
	if b.img[0x560000] == got[0] {
		t.Error("the archive returned aliases the image")
	}
	arc := testRELSArc(0x40000)
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := RELSArcFromImage(out); err != nil || !bytes.Equal(got, arc) {
		t.Errorf("on the patched image: %d bytes, %v; want the replacement at 0x%x", len(got), err, res.RELSArcOffset)
	}
	for name, img := range map[string][]byte{
		"garbage":      bytes.Repeat([]byte{0x5A}, 0x1000),
		"too small":    b.img[:0x100],
		"no RELS.arc":  func() []byte { d := vanillaLike(); d.files[2].name = "rels.bin"; return d.build(t).img }(),
		"FST past EOF": func() []byte { img := bytes.Clone(b.img); put32(img, isoHeaderFSTSize, 0x7FFFFFFF); return img }(),
	} {
		if _, err := RELSArcFromImage(img); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// The entry running past the image.
	img := bytes.Clone(b.img)
	idx, _ := relsEntry(t, b.fst)
	put32(img, int(b.fstOff)+int(idx)*fstEntrySize+8, 0x7FFFFFFF)
	if _, err := RELSArcFromImage(img); err == nil || !strings.Contains(err.Error(), "runs past the end of the image") {
		t.Errorf("entry past EOF: %v", err)
	}
}

// Patch.RELSArcFrom: the builder is given the disc's RELS.arc and its
// result is placed as a RELSArc is; on this build's output it is given
// the archive the patch wrote and the disc is ErrAlreadyPatched when it
// returns it unchanged, ErrPatchedByOtherVersion when not; its error is
// returned, wrapped; and it is not called for a DOL another build
// patched.
func TestPatchISORELSArcFrom(t *testing.T) {
	b := vanillaLike().build(t)
	vanillaArc := b.img[0x560000 : 0x560000+0x31000]
	arc := testRELSArc(0x40000)
	calls := 0
	var given []byte
	build := func(disc []byte) ([]byte, error) {
		calls++
		given = disc
		return arc, nil
	}
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArcFrom: build})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !bytes.Equal(given, vanillaArc) {
		t.Fatalf("the builder ran %d times, given %d bytes; want once with the vanilla archive", calls, len(given))
	}
	direct, dres, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil || !bytes.Equal(out, direct) || res != dres {
		t.Fatalf("differs from Patch{RELSArc}: %v, %+v vs %+v", err, res, dres)
	}
	dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
	b.checkPatchedWithRELS(t, out, res, wwSafeFSTOff, arc, uint32(alignUp64(dolEnd, 0x8000)))

	// Idempotency: the output's archive is what the builder is given;
	// returning it is ErrAlreadyPatched, returning another is another
	// version.
	calls, given = 0, nil
	same := func(disc []byte) ([]byte, error) { calls++; given = disc; return disc, nil }
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArcFrom: same}); !errors.Is(err, ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
	if calls != 1 || !bytes.Equal(given, arc) {
		t.Errorf("on the output the builder ran %d times, given %d bytes; want once with the replacement", calls, len(given))
	}
	other := func(disc []byte) ([]byte, error) { o := bytes.Clone(disc); o[0x1000] ^= 0xFF; return o, nil }
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArcFrom: other}); !errors.Is(err, ErrPatchedByOtherVersion) {
		t.Errorf("re-patching with another archive: err = %v, want ErrPatchedByOtherVersion", err)
	}

	// The builder's error, and a non-archive from it.
	boom := errors.New("boom: 3 bytes over")
	if _, _, err := PatchISOInMemoryWith(b.img, Patch{RELSArcFrom: func([]byte) ([]byte, error) { return nil, boom }}); !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "iso: RELS.arc: boom") {
		t.Errorf("builder error: %v", err)
	}
	if _, _, err := PatchISOInMemoryWith(b.img, Patch{RELSArcFrom: func([]byte) ([]byte, error) { return []byte("Yaz0 no"), nil }}); err == nil || !strings.Contains(err.Error(), "isn't a RARC archive") {
		t.Errorf("builder returning a non-archive: %v", err)
	}
	if _, _, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc, RELSArcFrom: build}); err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("both fields: %v", err)
	}

	// Another build's DOL is refused before the builder runs; a disc
	// without a RELS.arc fails naming it.
	calls = 0
	foreign := bytes.Clone(out)
	foreign[bytes.Index(foreign, T2Bytes)+len(T2Bytes)-1] ^= 1
	if _, _, err := PatchISOInMemoryWith(foreign, Patch{RELSArcFrom: build}); !errors.Is(err, ErrPatchedByOtherVersion) || calls != 0 {
		t.Errorf("another build's DOL: err = %v, builder ran %d times", err, calls)
	}
	d := vanillaLike()
	d.files[2].name = "rels.bin"
	if _, _, err := PatchISOInMemoryWith(d.build(t).img, Patch{RELSArcFrom: build}); err == nil || !strings.Contains(err.Error(), `no file "RELS.arc"`) || calls != 0 {
		t.Errorf("no RELS.arc: err = %v, builder ran %d times", err, calls)
	}
	// The input wasn't touched.
	if !bytes.Equal(b.img, vanillaLike().build(t).img) {
		t.Error("the input image changed")
	}
}
