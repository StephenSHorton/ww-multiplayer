package gamefiles

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Finding is one game file: where it is ("dist/x.zip!a/b.rel" for a zip
// entry) and why.
type Finding struct {
	Path   string
	Reason string
}

func (f Finding) String() string { return f.Path + ": " + f.Reason }

// memNested is the largest nested zip read into memory; a bigger one is
// copied to a temporary file first (up to MaxEmbeddedScan, past which the
// scan fails), so every nested zip is opened whatever its size.
var memNested uint64 = 64 << 20 // a var: a test makes it small

// maxDepth is how many zips deep Scan looks.
const maxDepth = 4

// isZip says whether a file named name with first bytes h is a zip (by
// extension or by its local header): our macOS release zip, a .nupkg, a
// .jar.
func isZip(name string, h []byte) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".zip", ".nupkg", ".jar", ".ipa":
		return true
	}
	return bytes.HasPrefix(h, []byte("PK\x03\x04"))
}

// Result is what a Scan found and how many files (zip entries included)
// it looked at.
type Result struct {
	Findings []Finding
	Checked  int
}

// add checks one file by name and header; it says whether it was flagged.
func (r *Result) add(display string, h []byte) bool {
	r.Checked++
	why := Check(display, h)
	for _, w := range why {
		r.Findings = append(r.Findings, Finding{display, w})
	}
	return len(why) > 0
}

// embedded checks the whole of a file that passed by name and header
// (Embedded): game data inside it, such as an archive in our exe's data.
func (r *Result) embedded(display string, rd io.Reader) error {
	w, err := Embedded(rd)
	if err != nil {
		return fmt.Errorf("%s: %w", display, err)
	}
	if w != "" {
		r.Findings = append(r.Findings, Finding{display, w})
	}
	return nil
}

// File checks the file at fsPath, shown as display, and every entry of it
// when it is a zip. A file that is gone (a tracked file deleted in the
// working tree) is checked by name.
func (r *Result) File(fsPath, display string) error {
	f, err := os.Open(fsPath)
	if errors.Is(err, fs.ErrNotExist) {
		r.add(display, nil)
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return nil // a submodule's directory: nothing of ours to check
	}
	h := make([]byte, HeaderLen)
	n, err := io.ReadFull(f, h)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", display, err)
	}
	h = h[:n]
	flagged := r.add(display, h)
	if !isZip(display, h) {
		if flagged {
			return nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("%s: %w", display, err)
		}
		return r.embedded(display, f)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		if bytes.HasPrefix(h, []byte("PK")) {
			return fmt.Errorf("%s: a zip that can't be read: %w", display, err)
		}
		return nil // named .zip but not one: its name and header were checked
	}
	return r.zip(zr, display, 1)
}

// zip checks every entry of zr, a zip shown as display: by name and
// header, then a nested zip's entries, or else the whole entry
// (Embedded).
func (r *Result) zip(zr *zip.Reader, display string, depth int) error {
	for _, e := range zr.File {
		if strings.HasSuffix(e.Name, "/") {
			continue
		}
		name := display + "!" + strings.ReplaceAll(e.Name, `\`, "/")
		if err := r.entry(e, name, depth); err != nil {
			return err
		}
	}
	return nil
}

func (r *Result) entry(e *zip.File, name string, depth int) error {
	rc, err := e.Open()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer rc.Close()
	h := make([]byte, HeaderLen)
	n, err := io.ReadFull(rc, h)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", name, err)
	}
	h = h[:n]
	if r.add(name, h) {
		return nil
	}
	rest := io.MultiReader(bytes.NewReader(h), rc)
	if !isZip(e.Name, h) || depth >= maxDepth {
		return r.embedded(name, rest)
	}
	inner, done, err := openNested(rest, e.UncompressedSize64)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer done()
	if inner == nil {
		return nil // named .zip but not one: its name and header were checked
	}
	return r.zip(inner, name, depth+1)
}

// openNested opens a nested zip read from rd: in memory up to memNested,
// else from a temporary file that done removes. A zip that can't be read
// is nil.
func openNested(rd io.Reader, size uint64) (_ *zip.Reader, done func(), err error) {
	done = func() {}
	if size <= memNested {
		b, err := io.ReadAll(io.LimitReader(rd, int64(memNested)+1))
		if err != nil {
			return nil, done, err
		}
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return nil, done, nil
		}
		return zr, done, nil
	}
	tmp, err := os.CreateTemp("", "check_game_files-*.zip")
	if err != nil {
		return nil, done, err
	}
	done = func() { tmp.Close(); os.Remove(tmp.Name()) }
	n, err := io.Copy(tmp, io.LimitReader(rd, MaxEmbeddedScan+1))
	if err != nil {
		return nil, done, err
	}
	if n > MaxEmbeddedScan {
		return nil, done, fmt.Errorf("a nested zip %w (more than %d MiB)", ErrTooBig, MaxEmbeddedScan>>20)
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return nil, done, nil
	}
	return zr, done, nil
}

// Tree checks every file under root (a file or a directory), each shown as
// its slash path below root's parent ("dist/WW Multiplayer/...").
func (r *Result) Tree(root string) error {
	st, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return r.File(root, filepath.ToSlash(root))
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			r.add(filepath.ToSlash(p), nil) // the macOS zip's CLI symlink: by name only
			return nil
		}
		return r.File(p, filepath.ToSlash(p))
	})
}
