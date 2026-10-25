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

// maxNested is the largest zip entry Scan opens as a zip of its own (it is
// read into memory); a bigger one is checked by name and header only.
const maxNested = 256 << 20

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

func (r *Result) add(display string, h []byte) {
	r.Checked++
	for _, w := range Check(display, h) {
		r.Findings = append(r.Findings, Finding{display, w})
	}
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
	r.add(display, h)
	if !isZip(display, h) {
		return nil
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

// zip checks every entry of zr, a zip shown as display.
func (r *Result) zip(zr *zip.Reader, display string, depth int) error {
	for _, e := range zr.File {
		if strings.HasSuffix(e.Name, "/") {
			continue
		}
		name := display + "!" + strings.ReplaceAll(e.Name, `\`, "/")
		rc, err := e.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		h := make([]byte, HeaderLen)
		n, err := io.ReadFull(rc, h)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			rc.Close()
			return fmt.Errorf("%s: %w", name, err)
		}
		h = h[:n]
		r.add(name, h)
		if depth < maxDepth && isZip(e.Name, h) && e.UncompressedSize64 <= maxNested {
			rest, err := io.ReadAll(io.LimitReader(rc, maxNested))
			if err == nil {
				b := append(h, rest...)
				if inner, err := zip.NewReader(bytes.NewReader(b), int64(len(b))); err == nil {
					if err := r.zip(inner, name, depth+1); err != nil {
						rc.Close()
						return err
					}
				}
			}
		}
		rc.Close()
	}
	return nil
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
