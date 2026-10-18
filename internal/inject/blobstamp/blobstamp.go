// Package blobstamp binds internal/inject/blob.go to the C inputs it was
// built from, so CI can tell a stale blob without devkitPPC.
//
// CI cannot rebuild the blob: that needs devkitPPC, Freighter and the
// owner's vanilla DOL. What it can check is that nothing changed on either
// side since the last regen. internal/inject/blob.stamp records a SHA-256
// of every C input (inject/src, inject/include, inject/rel, inject/build.py),
// of the blob payload in blob.go (T2Address, T2Bytes, DOLPatches) and of
// the REL module it embeds (RELELF, the `rel` line) as they stood when
// scripts/extract_blob.py last ran; it runs `go run ./tools/blobstamp`
// after writing blob.go. TestBlobFresh fails when an input changed
// without a regen, or blob.go changed without one.
//
// What it does not prove: that the recorded bytes are what devkitPPC
// would produce from the recorded inputs (only a rebuild proves that), or
// that the toolchain (devkitPPC, Freighter) and the vanilla DOL are the
// same. A comment-only C change still trips it: rebuild, confirm blob.go's
// payload is unchanged, and restamp.
package blobstamp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// StampPath is the stamp file, relative to the repo root.
const StampPath = "internal/inject/blob.stamp"

// inputDirs are walked for C sources (and the REL module's linker
// script); inputFiles are taken as is. Only these extensions count, so
// build products, docs and editor files in a local checkout do not.
var (
	inputDirs  = []string{"inject/src", "inject/include", "inject/rel"}
	inputFiles = []string{"inject/build.py"}
	sourceExt  = map[string]bool{".c": true, ".h": true, ".s": true, ".S": true, ".inc": true, ".ld": true}
)

// Stamp is the parsed content of blob.stamp.
type Stamp struct {
	Blob   string            // hex SHA-256 of the blob payload
	Rel    string            // hex SHA-256 of RELELF (the REL module); "" in a stamp from before it
	Inputs map[string]string // slash path -> hex SHA-256 of its LF-normalised bytes
}

// Root finds the repo root (the directory holding go.mod) at or above dir.
func Root(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		p := filepath.Dir(d)
		if p == d {
			return "", errors.New("blobstamp: no go.mod above " + dir)
		}
		d = p
	}
}

// Current computes the stamp for the tree at root and the blob compiled
// into this binary.
func Current(root string) (Stamp, error) {
	s := Stamp{Blob: BlobHash(), Rel: RelHash(), Inputs: map[string]string{}}
	var files []string
	for _, d := range inputDirs {
		dir := filepath.Join(root, filepath.FromSlash(d))
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue // a tree without this unit (Diff reports its inputs as removed)
		}
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.Type().IsRegular() && sourceExt[filepath.Ext(p)] {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return Stamp{}, err
		}
	}
	files = append(files, inputFiles...)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return Stamp{}, err
		}
		// The repo is LF (.gitattributes), but a CRLF checkout must not
		// look like a source change.
		sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
		s.Inputs[f] = hex.EncodeToString(sum[:])
	}
	return s, nil
}

// BlobHash hashes the payload blob.go carries: what the patcher writes.
// It ignores blob.go's formatting and its Built timestamp.
func BlobHash() string {
	h := sha256.New()
	var w [4]byte
	u32 := func(v uint32) { binary.BigEndian.PutUint32(w[:], v); h.Write(w[:]) }
	u32(inject.T2Address)
	u32(uint32(len(inject.T2Bytes)))
	h.Write(inject.T2Bytes)
	u32(uint32(len(inject.DOLPatches)))
	for _, p := range inject.DOLPatches {
		u32(p.FileOff)
		u32(uint32(len(p.Bytes)))
		h.Write(p.Bytes)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RelHash hashes the REL module blob.go embeds (RELELF, the relocatable
// ELF the patcher converts), apart from BlobHash so the T2 payload's
// line stays comparable across REL-only rebuilds. An empty RELELF (a
// blob generated without devkitPPC) hashes to the SHA-256 of nothing.
func RelHash() string {
	sum := sha256.Sum256(inject.RELELF)
	return hex.EncodeToString(sum[:])
}

const header = "# Binds internal/inject/blob.go to the C inputs it was built from.\n" +
	"# Written by `go run ./tools/blobstamp`, which scripts/extract_blob.py runs\n" +
	"# after writing blob.go. Checked on every PR by TestBlobFresh\n" +
	"# (internal/inject/blobstamp). Do not edit by hand.\n" +
	"# Any PR that edits inject/src, inject/include or inject/build.py,\n" +
	"# comment-only edits included, must carry its own restamp.\n"

// Format renders s as blob.stamp.
func (s Stamp) Format() []byte {
	var b bytes.Buffer
	b.WriteString(header)
	fmt.Fprintf(&b, "blob %s\n", s.Blob)
	if s.Rel != "" {
		fmt.Fprintf(&b, "rel %s\n", s.Rel)
	}
	names := make([]string, 0, len(s.Inputs))
	for n := range s.Inputs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "input %s %s\n", s.Inputs[n], n)
	}
	return b.Bytes()
}

// Parse reads blob.stamp.
func Parse(b []byte) (Stamp, error) {
	s := Stamp{Inputs: map[string]string{}}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "blob" && s.Blob == "":
			s.Blob = f[1]
		case len(f) == 2 && f[0] == "rel" && s.Rel == "":
			s.Rel = f[1]
		case len(f) == 3 && f[0] == "input" && path.Clean(f[2]) == f[2]:
			if _, dup := s.Inputs[f[2]]; dup {
				return Stamp{}, fmt.Errorf("blob.stamp:%d: %s listed twice", n, f[2])
			}
			s.Inputs[f[2]] = f[1]
		default:
			return Stamp{}, fmt.Errorf("blob.stamp:%d: cannot parse %q", n, line)
		}
	}
	if err := sc.Err(); err != nil {
		return Stamp{}, err
	}
	if s.Blob == "" || len(s.Inputs) == 0 {
		return Stamp{}, errors.New("blob.stamp: no blob line or no input lines")
	}
	return s, nil
}

// Diff lists how have differs from want (the recorded stamp), one line
// per problem; empty means fresh.
func Diff(want, have Stamp) []string {
	names := map[string]bool{}
	for n := range want.Inputs {
		names[n] = true
	}
	for n := range have.Inputs {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []string
	for _, n := range sorted {
		w, inW := want.Inputs[n]
		h, inH := have.Inputs[n]
		switch {
		case !inW:
			out = append(out, "new C input since the last regen: "+n)
		case !inH:
			out = append(out, "C input removed since the last regen: "+n)
		case w != h:
			out = append(out, "C input changed since the last regen: "+n)
		}
	}
	if want.Blob != have.Blob {
		out = append(out, "blob.go's payload (T2Address, T2Bytes, DOLPatches) differs from the one stamped at the last regen")
	}
	if want.Rel != have.Rel {
		out = append(out, "blob.go's REL module (RELELF) differs from the one stamped at the last regen")
	}
	return out
}
