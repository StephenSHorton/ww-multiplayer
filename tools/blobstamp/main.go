// Command blobstamp writes internal/inject/blob.stamp, which binds
// internal/inject/blob.go to the C inputs it was built from (see package
// internal/inject/blobstamp). scripts/extract_blob.py runs it after
// writing blob.go. Run it by hand only after a real C rebuild:
//
//	go run ./tools/blobstamp          # write the stamp
//	go run ./tools/blobstamp -check   # exit 1 if blob.go is stale
//
// It reads blob.go's payload from the compiled package, so `go run` must
// see the blob.go on disk: run it after extract_blob.py has written it.
// There is deliberately no go:generate line for it: a casual
// `go generate ./...` would restamp a stale blob and silence the check.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/blobstamp"
)

func main() {
	check := flag.Bool("check", false, "compare instead of writing; exit 1 if stale")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "blobstamp:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := blobstamp.Root(".")
	if err != nil {
		return err
	}
	have, err := blobstamp.Current(root)
	if err != nil {
		return err
	}
	file := filepath.Join(root, filepath.FromSlash(blobstamp.StampPath))
	if check {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		want, err := blobstamp.Parse(b)
		if err != nil {
			return err
		}
		if d := blobstamp.Diff(want, have); len(d) > 0 {
			for _, l := range d {
				fmt.Fprintln(os.Stderr, "blobstamp:", l)
			}
			return fmt.Errorf("%s is stale", blobstamp.StampPath)
		}
		fmt.Printf("%s matches blob.go and %d C inputs\n", blobstamp.StampPath, len(have.Inputs))
		return nil
	}
	out := have.Format()
	if old, err := os.ReadFile(file); err == nil && bytes.Equal(old, out) {
		fmt.Printf("%s unchanged\n", blobstamp.StampPath)
		return nil
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d C inputs)\n", blobstamp.StampPath, len(have.Inputs))
	return nil
}
