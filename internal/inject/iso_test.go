package inject

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The owner's known-good case (a scrubbed CISO, zeros at 0x500000): the
// FST must still land at 0x500000 and the output must be byte-identical to
// what the patcher has always produced.
func TestPatchISOZeroPaddingUses0x500000(t *testing.T) {
	b := vanillaLike().build(t)
	out, res, err := patchImage(b.img, Patch{})
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, out, wwSafeFSTOff)
	if want := (Result{OldFSTOffset: b.fstOff, NewFSTOffset: wwSafeFSTOff, FSTSize: uint32(len(b.fst))}); res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if !res.FSTMoved() {
		t.Error("FSTMoved() = false for a relocated FST")
	}

	if _, err := PatchISOInMemory(out); !errors.Is(err, ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
}

// Raw 1:1 dumps (CleanRip, Dolphin RVZ->ISO) keep junk padding. No file
// lives at 0x500000, so it's still the right spot.
func TestPatchISOJunkPaddingUses0x500000(t *testing.T) {
	d := vanillaLike()
	d.junk = true
	b := d.build(t)
	out, err := PatchISOInMemory(b.img)
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, out, wwSafeFSTOff)
}

// A file covering 0x500000 whose bytes there are all zero: the old
// zero-scan would have put the FST on top of it.
func TestPatchISOAvoidsFileAt0x500000(t *testing.T) {
	for _, junk := range []bool{false, true} {
		d := vanillaLike()
		d.junk = junk
		d.files = append(d.files, testFile{name: "zeros.bin", off: 0x4F8000, size: 0x10000, zero: true})
		b := d.build(t)
		out, err := PatchISOInMemory(b.img)
		if err != nil {
			t.Fatalf("junk=%v: %v", junk, err)
		}
		// Lowest 64 KiB slot past the patched DOL: the gap after it is free.
		dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
		want := uint32(alignUp64(dolEnd, isoFSTAlignment))
		b.checkPatched(t, out, want)
	}
}

// When files fill everything past the patched DOL in an image smaller than a
// full disc (a trimmed dump), the FST is appended at EOF, the way the
// patcher always handled these: it still ends under the disc-size cap.
func TestPatchISONoFreeSpaceAppendsAtEOF(t *testing.T) {
	d := vanillaLike()
	d.junk = true
	fstEnd := uint32(alignUp64(uint64(d.dolOff)+uint64(testDOLSize), 0x100)) + 0x5000 // past the padded FST
	d.files = []testFile{
		{name: "a.arc", off: fstEnd, size: 0x400000 - fstEnd},
		{name: "b.arc", off: 0x400000, size: 0x123456},
		{name: "c.arc", off: 0x523456, size: testImageSize - 0x523456},
	}
	b := d.build(t)
	if int(b.fstOff)+len(b.fst) > int(fstEnd) {
		t.Fatalf("test layout: FST [0x%x..0x%x] runs into the first file at 0x%x", b.fstOff, int(b.fstOff)+len(b.fst), fstEnd)
	}
	out, err := PatchISOInMemory(b.img)
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, out, uint32(alignUp64(uint64(len(b.img)), isoFSTAlignment)))
}

// An FST smaller than the DOL growth with a file right after it: the old
// patcher silently overwrote the start of that file with T2.
func TestPatchISOGrowthWouldOverwriteFile(t *testing.T) {
	d := vanillaLike()
	d.fstPad = 0
	fstOff := uint32(alignUp64(uint64(d.dolOff)+uint64(testDOLSize), 0x100))
	d.files = append(d.files, testFile{name: "next.arc", off: fstOff + 0x100, size: 0x8000})
	b := d.build(t)
	if len(b.fst) > 0x100 {
		t.Fatalf("test layout: FST is 0x%x bytes, overlaps next.arc", len(b.fst))
	}
	_, err := PatchISOInMemory(b.img)
	if err == nil || !strings.Contains(err.Error(), `would overwrite file "next.arc"`) {
		t.Fatalf("err = %v, want a would-overwrite error naming next.arc", err)
	}
}

// Vanilla WW's shape: the FST is bigger than the growth and a file starts
// right where it ends. The growth stays inside the vacated FST; a naive
// "bytes between FST end and DOL end" guard would slice backwards here.
func TestPatchISOGrowthInsideOldFST(t *testing.T) {
	d := vanillaLike()
	d.junk = true
	fstOff := uint32(alignUp64(uint64(d.dolOff)+uint64(testDOLSize), 0x100))
	fstSize := uint32(len(buildFST(append(d.files, testFile{name: "next.arc"}), d.fstPad)))
	if fstSize <= uint32(len(T2Bytes)) {
		t.Fatalf("test layout: FST (0x%x) must be bigger than T2 (0x%x)", fstSize, len(T2Bytes))
	}
	d.files = append(d.files, testFile{name: "next.arc", off: fstOff + fstSize, size: 0x8000})
	b := d.build(t)
	out, err := PatchISOInMemory(b.img)
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, out, wwSafeFSTOff)
}

// A `wit copy` + FST-shift image (the owner's dev ISO) has room for the
// bigger DOL: the FST stays put.
func TestPatchISOFastPathKeepsFST(t *testing.T) {
	d := vanillaLike()
	d.fstOff = wwSafeFSTOff
	b := d.build(t)
	out, res, err := patchImage(b.img, Patch{})
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, out, wwSafeFSTOff)
	if res.FSTMoved() {
		t.Errorf("FSTMoved() = true, result %+v", res)
	}
}

func TestPatchISOFastPathFileInReservedSpace(t *testing.T) {
	d := vanillaLike()
	d.fstOff = wwSafeFSTOff
	d.files = append(d.files, testFile{name: "squatter.bin", off: testDOLOff + testDOLSize + 0x1000, size: 0x100})
	b := d.build(t)
	_, err := PatchISOInMemory(b.img)
	if err == nil || !strings.Contains(err.Error(), `would overwrite file "squatter.bin"`) {
		t.Fatalf("err = %v, want a would-overwrite error naming squatter.bin", err)
	}
}

func TestPatchISODetectsOtherVersion(t *testing.T) {
	b := vanillaLike().build(t)
	out, err := PatchISOInMemory(b.img)
	if err != nil {
		t.Fatal(err)
	}
	// Change one byte of T2, as a different blob would.
	out[int(b.dolOff)+len(b.dol)+0x40] ^= 0xFF
	if _, err := PatchISOInMemory(out); !errors.Is(err, ErrPatchedByOtherVersion) {
		t.Fatalf("err = %v, want ErrPatchedByOtherVersion", err)
	}
}

func TestPatchISOWrongGameID(t *testing.T) {
	for _, id := range []string{"GZLP01", "GZLJ01", "GALE01"} {
		b := vanillaLike().build(t)
		copy(b.img, id)
		_, err := PatchISOInMemory(b.img)
		if err == nil || !strings.Contains(err.Error(), "GZLE01-only") {
			t.Errorf("%s: err = %v, want a GZLE01-only error", id, err)
		}
	}
}

func TestPatchISORejectsCraftedDOLHeader(t *testing.T) {
	b := vanillaLike().build(t)
	// T6 off = size = 0x7FFFF800: the end wrapped in 32-bit math, which
	// used to pass the bounds checks and then allocate 4 GB and panic.
	put32(b.img, int(b.dolOff)+0x18, 0x7FFFF800)
	put32(b.img, int(b.dolOff)+0xA8, 0x7FFFF800)
	_, err := PatchISOInMemory(b.img)
	if err == nil || !strings.Contains(err.Error(), "more than fits in GameCube RAM") {
		t.Fatalf("err = %v, want an oversize-DOL error", err)
	}
}

// The disc header and apploader are reserved like files: an apploader that
// claims to run past the DOL gets refused, not overwritten.
func TestPatchISOReservesApploader(t *testing.T) {
	b := vanillaLike().build(t)
	put32(b.img, isoApploaderOff+0x14, 0x400000)
	_, err := PatchISOInMemory(b.img)
	if err == nil || !strings.Contains(err.Error(), "would overwrite the disc header/apploader") {
		t.Fatalf("err = %v, want a would-overwrite-apploader error", err)
	}
}

func TestPatchISORejectsMalformedFST(t *testing.T) {
	b := vanillaLike().build(t)
	put32(b.img, int(b.fstOff)+8, 0x100000) // root claims far more entries than fit
	if _, err := PatchISOInMemory(b.img); err == nil || !strings.Contains(err.Error(), "fst:") {
		t.Fatalf("err = %v, want an FST error", err)
	}
}

func TestChooseFSTRelocationStaysUnderDiscCap(t *testing.T) {
	used := []extent{{start: 0, end: gcDiscSize - 0x8000}}
	if off, err := chooseFSTRelocation(used, 0x8000, 0, gcDiscSize+0x10000000); err != nil || off != gcDiscSize-0x8000 {
		t.Errorf("0x8000-byte FST: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8000)
	}
	// A bigger FST only fits past the cap, even though the image is larger.
	if off, err := chooseFSTRelocation(used, 0x8001, 0, gcDiscSize+0x10000000); err == nil {
		t.Errorf("0x8001-byte FST placed at 0x%x, past the disc-size cap", off)
	}
	// A trimmed image appends at EOF, but never past the cap either.
	if off, err := chooseFSTRelocation(used, 0x8000, 0, gcDiscSize-0x8000); err != nil || off != gcDiscSize-0x8000 {
		t.Errorf("EOF append of 0x8000 bytes: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8000)
	}
	if off, err := chooseFSTRelocation(used, 0x8001, 0, gcDiscSize-0x8000); err == nil ||
		!strings.Contains(err.Error(), "no free space found to relocate the FST") {
		t.Errorf("EOF append of 0x8001 bytes: off 0x%x err %v, want a no-free-space error", off, err)
	}
}

func TestPatchISOFile(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	out := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, b.img)
	writeFile(t, out, []byte("stale output from an earlier run"))
	if err := os.Chmod(out, 0o640); err != nil {
		t.Fatal(err)
	}

	res, err := PatchISO(in, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewFSTOffset != wwSafeFSTOff {
		t.Errorf("result %+v, want FST at 0x%x", res, wwSafeFSTOff)
	}
	b.checkPatched(t, readFile(t, out), wwSafeFSTOff)
	if !bytes.Equal(readFile(t, in), b.img) {
		t.Error("input file changed")
	}
	if runtime.GOOS != "windows" { // the replaced output keeps its mode, as with os.WriteFile
		if st, err := os.Stat(out); err != nil || st.Mode().Perm() != 0o640 {
			t.Errorf("output mode = %v (%v), want the old output's 0640", st.Mode().Perm(), err)
		}
	}
	checkNoTempFiles(t, dir, "ww.iso", "ww-multiplayer.iso")
}

func TestPatchISOFileCISO(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.ciso")
	out := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, makeCISO(b.img, 0x100000))

	if _, err := PatchISO(in, out); err != nil {
		t.Fatal(err)
	}
	b.checkPatched(t, readFile(t, out), wwSafeFSTOff)
}

// A failed patch leaves an existing output alone and no temp file behind.
func TestPatchISOFileFailureWritesNothing(t *testing.T) {
	b := vanillaLike().build(t)
	copy(b.img, "GZLP01")
	dir := t.TempDir()
	in := filepath.Join(dir, "pal.iso")
	out := filepath.Join(dir, "pal-multiplayer.iso")
	writeFile(t, in, b.img)
	writeFile(t, out, []byte("keep me"))

	if _, err := PatchISO(in, out); err == nil {
		t.Fatal("patched a GZLP01 image")
	}
	if got := readFile(t, out); string(got) != "keep me" {
		t.Errorf("existing output overwritten with %d bytes", len(got))
	}
	checkNoTempFiles(t, dir, "pal.iso", "pal-multiplayer.iso")

	if _, err := PatchISO(in, filepath.Join(dir, "fresh.iso")); err == nil {
		t.Fatal("patched a GZLP01 image")
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.iso")); !os.IsNotExist(err) {
		t.Errorf("failed patch created its output (stat err %v)", err)
	}
}

// PatchISO itself writes atomically: when the final rename fails after a
// successful patch, the old output survives and the temp file is removed.
func TestPatchISOFileRenameFailureKeepsOldOutput(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	out := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, b.img)
	writeFile(t, out, []byte("keep me"))

	injected := errors.New("injected rename failure")
	calls := 0
	defer func(orig func(string, string) error) { replaceFile = orig }(replaceFile)
	replaceFile = func(oldpath, newpath string) error {
		calls++
		if newpath != out || filepath.Dir(oldpath) != dir {
			t.Errorf("rename %s -> %s, want a temp file in %s renamed to %s", oldpath, newpath, dir, out)
		}
		b.checkPatched(t, readFile(t, oldpath), wwSafeFSTOff) // the temp holds the whole image
		return injected
	}

	if _, err := PatchISO(in, out); !errors.Is(err, injected) {
		t.Fatalf("err = %v, want the rename failure", err)
	}
	if calls != 1 {
		t.Errorf("rename called %d times, want 1", calls)
	}
	if got := readFile(t, out); string(got) != "keep me" {
		t.Errorf("existing output overwritten with %d bytes", len(got))
	}
	checkNoTempFiles(t, dir, "ww.iso", "ww-multiplayer.iso")
}

// An output path that is a symlink is written through, as os.WriteFile
// would: the link's target gets the patched image and the link stays.
func TestPatchISOFileFollowsOutputSymlink(t *testing.T) {
	b := vanillaLike().build(t)
	dir, ext := t.TempDir(), t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	target := filepath.Join(ext, "ww-multiplayer.iso")
	link := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, b.img)
	writeFile(t, target, []byte("stale output on another drive"))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("can't create symlinks here: %v", err)
	}

	if _, err := PatchISO(in, link); err != nil {
		t.Fatal(err)
	}
	if !isSymlink(t, link) {
		t.Fatal("output link replaced by a regular file")
	}
	b.checkPatched(t, readFile(t, target), wwSafeFSTOff)
	checkNoTempFiles(t, dir, "ww.iso", "ww-multiplayer.iso")
	checkNoTempFiles(t, ext, "ww-multiplayer.iso")
}

// A dangling output symlink is an error, not a regular file in its place.
func TestPatchISOFileDanglingOutputSymlink(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	link := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, b.img)
	if err := os.Symlink(filepath.Join(dir, "unplugged", "ww-multiplayer.iso"), link); err != nil {
		t.Skipf("can't create symlinks here: %v", err)
	}

	if _, err := PatchISO(in, link); err == nil {
		t.Fatal("patched through a dangling symlink")
	}
	if !isSymlink(t, link) {
		t.Error("dangling link replaced by a regular file")
	}
	checkNoTempFiles(t, dir, "ww.iso", "ww-multiplayer.iso")
}

func TestPatchISORefusesSamePath(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	writeFile(t, in, b.img)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	sep := string(filepath.Separator)
	outs := map[string]string{
		"same string":     in,
		"dot segment":     dir + sep + "." + sep + "ww.iso",
		"dot-dot segment": dir + sep + "sub" + sep + ".." + sep + "ww.iso",
	}
	if link := filepath.Join(dir, "hardlink.iso"); os.Link(in, link) == nil {
		outs["hard link"] = link
	}
	if link := filepath.Join(dir, "symlink.iso"); os.Symlink(in, link) == nil {
		outs["symlink"] = link
	}
	if upper := filepath.Join(dir, "WW.ISO"); fileExists(upper) { // case-insensitive FS (macOS, Windows)
		outs["case-only difference"] = upper
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, in); err == nil {
			outs["relative path"] = rel
		}
	}
	for name, out := range outs {
		_, err := PatchISO(in, out)
		if !errors.Is(err, ErrOutputIsInput) {
			t.Errorf("%s (%s): err = %v, want ErrOutputIsInput", name, out, err)
		}
	}
	if !bytes.Equal(readFile(t, in), b.img) {
		t.Fatal("input file changed")
	}
	checkNoTempFiles(t, dir, "ww.iso", "hardlink.iso", "symlink.iso", "sub")
}

func TestPatchISOFileUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.rvz")
	out := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, append([]byte("RVZ\x01"), make([]byte, 0x1000)...))

	_, err := PatchISO(in, out)
	var ufe *UnsupportedFormatError
	if !errors.As(err, &ufe) || ufe.Format != "RVZ" {
		t.Fatalf("err = %v, want an RVZ UnsupportedFormatError", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output written for an RVZ input (stat err %v)", err)
	}
}

func TestWriteFileAtomicCleansUpOnFailure(t *testing.T) {
	// On Windows this fails with ERROR_ACCESS_DENIED, which is retried; skip the waits.
	defer func(orig func(time.Duration)) { renameSleep = orig }(renameSleep)
	renameSleep = func(time.Duration) {}
	dir := t.TempDir()
	target := filepath.Join(dir, "out.iso")
	if err := os.Mkdir(target, 0o755); err != nil { // renaming onto a directory fails
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "keep"), []byte("x"))
	if err := writeFileAtomic(target, []byte("data"), 0o644); err == nil {
		t.Fatal("renamed a file over a non-empty directory")
	}
	checkNoTempFiles(t, dir, "out.iso")
	if got := readFile(t, filepath.Join(target, "keep")); string(got) != "x" {
		t.Error("target directory contents changed")
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// checkNoTempFiles fails if dir holds anything but the named entries.
func checkNoTempFiles(t *testing.T, dir string, keep ...string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !slices.Contains(keep, e.Name()) {
			t.Errorf("unexpected file left in output dir: %s", e.Name())
		}
	}
}
