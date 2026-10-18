package update

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MaxUnzipSize bounds what Unzip writes in all (a zip bomb stops there).
const MaxUnzipSize = 1 << 30

// MaxUnzipFiles bounds the entries Unzip extracts.
const MaxUnzipFiles = 20000

// Unzip extracts the zip at src into the directory dest (made if needed).
// It refuses an entry that would land outside dest (a "../" path, an
// absolute one, or one under a symlink it extracted) and a symlink whose
// target is absolute or has a ".." element (so no chain of links can
// point out either), skips macOS's __MACOSX resource entries, keeps the
// files' modes (the +x bits) and the symlinks inside dest (build-mac.sh's
// ww-multiplayer link, to WW Multiplayer.app/Contents/MacOS/...), and
// stops at MaxUnzipSize bytes or MaxUnzipFiles entries.
func Unzip(src, dest string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("update: the download isn't a zip: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > MaxUnzipFiles {
		return fmt.Errorf("%w: the zip has %d entries", ErrTooLarge, len(zr.File))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	var written int64
	links := map[string]bool{} // the symlinks extracted so far (rel paths)
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(path.Base(name), "._") {
			continue
		}
		rel, err := safeRel(name)
		if err != nil {
			return err
		}
		if rel == "." {
			continue
		}
		// Nothing is written through a link (nor over one).
		for p := rel; p != "."; p = path.Dir(p) {
			if links[p] {
				return fmt.Errorf("update: the zip's %q is under its link %q", name, p)
			}
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		mode := f.Mode()
		switch {
		case mode.IsDir() || strings.HasSuffix(name, "/"):
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("update: %w", err)
			}
		case mode&fs.ModeSymlink != 0:
			link, err := readSmall(f, 4096)
			if err != nil {
				return err
			}
			if !safeLink(link) {
				return fmt.Errorf("update: the zip's link %q points outside its folder", name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("update: %w", err)
			}
			if err := os.Symlink(link, target); err != nil {
				return fmt.Errorf("update: %w", err)
			}
			links[rel] = true
		case mode.IsRegular():
			n, err := extractFile(f, target, MaxUnzipSize-written)
			written += n
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("update: the zip's %q is not a file, folder or link", name)
		}
	}
	return nil
}

// safeRel cleans a zip entry name into a relative slash path inside the
// destination, or refuses it.
func safeRel(name string) (string, error) {
	if strings.Contains(name, "\\") || strings.Contains(name, "\x00") {
		return "", fmt.Errorf("update: the zip has a bad entry name %q", name)
	}
	if path.IsAbs(name) || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("update: the zip's %q is an absolute path", name)
	}
	rel := path.Clean(name)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("update: the zip's %q points outside it", name)
	}
	return rel, nil
}

// safeLink says a symlink's target stays below the link's own folder: a
// relative slash path with no ".." element (nor a backslash, a NUL or a
// volume). A target that can't climb can't escape, however links chain.
func safeLink(link string) bool {
	if link == "" || strings.ContainsAny(link, "\\\x00") || path.IsAbs(link) || filepath.IsAbs(link) || filepath.VolumeName(link) != "" {
		return false
	}
	for _, el := range strings.Split(link, "/") {
		if el == ".." {
			return false
		}
	}
	return true
}

func readSmall(f *zip.File, limit int64) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	if int64(len(b)) > limit {
		return "", fmt.Errorf("update: the zip's link %q is too long", f.Name)
	}
	return string(b), nil
}

func extractFile(f *zip.File, target string, room int64) (int64, error) {
	if room <= 0 {
		return 0, fmt.Errorf("%w: the zip unpacks to over %d bytes", ErrTooLarge, int64(MaxUnzipSize))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}
	defer rc.Close()
	perm := f.Mode().Perm() | 0o600
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(rc, room+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("update: unpacking %s: %w", f.Name, err)
	}
	if n > room {
		return n, fmt.Errorf("%w: the zip unpacks to over %d bytes", ErrTooLarge, int64(MaxUnzipSize))
	}
	return n, nil
}

// FindApp returns the .app bundle under dir whose Contents/MacOS holds
// exeName (the zip's "WW Multiplayer/WW Multiplayer.app"), at most two
// levels down.
func FindApp(dir, exeName string) (string, error) {
	for _, pat := range []string{"*.app", filepath.Join("*", "*.app")} {
		matches, _ := filepath.Glob(filepath.Join(globEscape(dir), pat))
		for _, m := range matches {
			fi, err := os.Lstat(filepath.Join(m, "Contents", "MacOS", exeName))
			if err == nil && fi.Mode().IsRegular() {
				return m, nil
			}
		}
	}
	return "", errors.New("update: the download has no " + exeName + " app in it")
}
