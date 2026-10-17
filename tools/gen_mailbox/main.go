// Command gen_mailbox writes wwapi/mailbox/layout_gen.go from
// inject/include/mailbox.h (package internal/mailboxgen), so the Go side
// never hand-mirrors a mailbox offset:
//
//	go generate ./wwapi/mailbox              # same as the next line
//	go run ./tools/gen_mailbox               # write layout_gen.go
//	go run ./tools/gen_mailbox -check        # exit 1 if it is stale
//
// wwapi/mailbox's TestMailboxLayout runs the same generator in memory,
// so a mailbox.h edit without a regen fails CI. Regenerating changes no
// C and no blob; a layout change in mailbox.h still needs the blob
// rebuild and restamp CLAUDE.md describes.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/internal/mailboxgen"
)

func main() {
	check := flag.Bool("check", false, "compare instead of writing; exit 1 if stale")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "gen_mailbox:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(mailboxgen.HeaderPath)))
	if err != nil {
		return err
	}
	out, err := mailboxgen.Generate(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(mailboxgen.OutputPath))
	if check {
		have, err := os.ReadFile(dst)
		if err != nil {
			return err
		}
		if !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), out) {
			return fmt.Errorf("%s is stale: run go run ./tools/gen_mailbox", mailboxgen.OutputPath)
		}
		return nil
	}
	return os.WriteFile(dst, out, 0o644)
}

// moduleRoot is the nearest directory at or above the working directory
// that holds go.mod (go generate runs in wwapi/mailbox).
func moduleRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		p := filepath.Dir(d)
		if p == d {
			return "", errors.New("no go.mod above the working directory")
		}
		d = p
	}
}
