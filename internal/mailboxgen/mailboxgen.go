// Package mailboxgen turns the mailbox headers into Go: the mailbox's
// #define values and every field offset of its structs. The headers are
// the source of truth: inject/include/mailbox_core.h and the
// mailbox_*.h headers it includes (Phase E3 split mailbox.h into
// mailbox_core.h and mailbox_mp.h), read in include order as one
// header. tools/gen_mailbox writes the result to
// wwapi/mailbox/layout_gen.go, and wwapi/mailbox's TestMailboxLayout
// regenerates it in memory and fails when the committed file is stale.
//
// The layout follows the PowerPC EABI the blob is built with: natural
// alignment for scalars, a struct aligned to its widest member, and
// its size rounded up to that alignment.
package mailboxgen

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The generator's input and output, relative to the module root.
// HeaderPath is the root header; the headers it includes sit next to it.
const (
	HeaderPath = "inject/include/mailbox_core.h"
	OutputPath = "wwapi/mailbox/layout_gen.go"
)

// Reader returns an included header's bytes by the name its #include
// gives ("mailbox_mp.h").
type Reader func(name string) ([]byte, error)

// DirReader reads included headers from dir.
func DirReader(dir string) Reader {
	return func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, name))
	}
}

// Source is one header the layout was parsed from.
type Source struct {
	Name string // "mailbox_core.h"
	Src  []byte
}

// Field is one member of a C struct in the header.
type Field struct {
	CName  string // as written in the header, e.g. "spawn_trigger"
	CType  string // "u32", "Puppet", ...
	Dim    string // array length as written ("" for a scalar)
	Len    int    // array length (1 for a scalar)
	Offset int    // byte offset inside its struct
	Size   int    // element size
}

// Struct is one typedef'd struct of the header, in declaration order.
type Struct struct {
	Name   string
	Size   int
	Align  int
	Fields []Field
}

// Define is one numeric #define.
type Define struct {
	Name  string
	Value int64
	Text  string // the value as written, e.g. "0x80412F00"
}

// Layout is everything the headers declare that Go mirrors.
type Layout struct {
	Defines []Define
	Structs []Struct
	Sources []Source // the root header first, then each include as it was reached
}

// Mailbox returns the Mailbox struct, or nil.
func (l *Layout) Mailbox() *Struct {
	for i := range l.Structs {
		if l.Structs[i].Name == "Mailbox" {
			return &l.Structs[i]
		}
	}
	return nil
}

// Define returns a #define's value.
func (l *Layout) Define(name string) (int64, bool) {
	for _, d := range l.Defines {
		if d.Name == name {
			return d.Value, true
		}
	}
	return 0, false
}

var (
	defRE    = regexp.MustCompile(`^#define\s+(\w+)\s+(0x[0-9A-Fa-f]+|\d+)\s*(?://.*)?$`)
	fieldRE  = regexp.MustCompile(`^\s*(\w+)\s+(\w+)(?:\[(\w+)\])?\s*;\s*(?://\s*\+(0x[0-9A-Fa-f]+))?`)
	endRE    = regexp.MustCompile(`^}\s*(\w+)\s*;`)
	scalars  = map[string]int{"u8": 1, "s8": 1, "u16": 2, "s16": 2, "u32": 4, "s32": 4, "f32": 4}
	cComment = regexp.MustCompile(`^\s*(//.*)?$`)
	// Only the mailbox's own headers are followed; any other include
	// (wwapi.h, for the C types) is skipped.
	incRE = regexp.MustCompile(`^#include\s+"(mailbox_\w+\.h)"\s*(?://.*)?$`)
)

// Parse reads the root header name (src) and, through read, every
// "mailbox_*.h" it includes, in place and once each, as if they were one
// header. It refuses anything it does not understand inside a struct
// (an unknown type, an unknown array size, a block comment, an include)
// rather than guess a layout, and it checks the headers' own
// "// +0xNN" comments against the offsets it computes. read may be nil
// for a header that includes none.
func Parse(name string, src []byte, read Reader) (*Layout, error) {
	p := &parser{
		l:     &Layout{},
		defs:  map[string]int64{},
		types: map[string]struct{ size, align int }{},
		read:  read,
		seen:  map[string]bool{name: true},
	}
	for k, v := range scalars {
		p.types[k] = struct{ size, align int }{v, v}
	}
	if err := p.file(name, src); err != nil {
		return nil, err
	}
	if p.cur != nil {
		return nil, fmt.Errorf("%s: unterminated struct", name)
	}
	if p.l.Mailbox() == nil {
		return nil, fmt.Errorf("%s: no Mailbox struct", name)
	}
	if _, ok := p.defs["MAILBOX_ADDR"]; !ok {
		return nil, fmt.Errorf("%s: no MAILBOX_ADDR", name)
	}
	return p.l, nil
}

// ParseDir parses the root header at headerPath and the headers it
// includes from the same directory.
func ParseDir(headerPath string) (*Layout, error) {
	src, err := os.ReadFile(headerPath)
	if err != nil {
		return nil, err
	}
	return Parse(filepath.Base(headerPath), src, DirReader(filepath.Dir(headerPath)))
}

type parser struct {
	l     *Layout
	defs  map[string]int64
	types map[string]struct{ size, align int }
	read  Reader
	seen  map[string]bool
	cur   *Struct
	off   int
}

func (p *parser) file(name string, src []byte) error {
	p.l.Sources = append(p.l.Sources, Source{Name: name, Src: src})
	sc := bufio.NewScanner(bytes.NewReader(src))
	n := 0
	for sc.Scan() {
		n++
		if err := p.line(name, n, strings.TrimRight(sc.Text(), "\r")); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (p *parser) line(name string, n int, line string) error {
	if strings.Contains(line, "/*") {
		return fmt.Errorf("%s:%d: block comments are not supported", name, n)
	}
	if m := incRE.FindStringSubmatch(line); m != nil {
		if p.cur != nil {
			return fmt.Errorf("%s:%d: #include inside a struct", name, n)
		}
		inc := m[1]
		if p.seen[inc] {
			return nil // included already: its include guard skips it
		}
		p.seen[inc] = true
		if p.read == nil {
			return fmt.Errorf("%s:%d: cannot read included %s", name, n, inc)
		}
		src, err := p.read(inc)
		if err != nil {
			return fmt.Errorf("%s:%d: %v", name, n, err)
		}
		return p.file(inc, src)
	}
	if m := defRE.FindStringSubmatch(line); m != nil {
		if p.cur != nil {
			return fmt.Errorf("%s:%d: #define inside a struct", name, n)
		}
		v, err := strconv.ParseInt(m[2], 0, 64)
		if err != nil {
			return fmt.Errorf("%s:%d: %v", name, n, err)
		}
		if _, dup := p.defs[m[1]]; dup {
			return fmt.Errorf("%s:%d: %s defined twice", name, n, m[1])
		}
		p.defs[m[1]] = v
		p.l.Defines = append(p.l.Defines, Define{Name: m[1], Value: v, Text: m[2]})
		return nil
	}
	if strings.HasPrefix(line, "typedef struct {") {
		if p.cur != nil {
			return fmt.Errorf("%s:%d: nested struct", name, n)
		}
		p.cur, p.off = &Struct{Align: 1}, 0
		return nil
	}
	cur := p.cur
	if cur == nil {
		return nil
	}
	if m := endRE.FindStringSubmatch(line); m != nil {
		if _, dup := p.types[m[1]]; dup {
			return fmt.Errorf("%s:%d: type %s declared twice", name, n, m[1])
		}
		cur.Name = m[1]
		cur.Size = (p.off + cur.Align - 1) &^ (cur.Align - 1)
		p.types[cur.Name] = struct{ size, align int }{cur.Size, cur.Align}
		p.l.Structs = append(p.l.Structs, *cur)
		p.cur = nil
		return nil
	}
	m := fieldRE.FindStringSubmatch(line)
	if m == nil {
		if !cComment.MatchString(line) {
			return fmt.Errorf("%s:%d: cannot parse struct line %q", name, n, line)
		}
		return nil
	}
	ty, ok := p.types[m[1]]
	if !ok {
		return fmt.Errorf("%s:%d: unknown type %q", name, n, m[1])
	}
	count := 1
	if m[3] != "" {
		if v, err := strconv.Atoi(m[3]); err == nil {
			count = v
		} else if v, ok := p.defs[m[3]]; ok {
			count = int(v)
		} else {
			return fmt.Errorf("%s:%d: unknown array size %q", name, n, m[3])
		}
	}
	p.off = (p.off + ty.align - 1) &^ (ty.align - 1)
	if m[4] != "" {
		want, _ := strconv.ParseInt(m[4], 0, 64)
		if int(want) != p.off {
			return fmt.Errorf("%s:%d: comment says %s is at +0x%X, the struct puts it at +0x%X", name, n, m[2], want, p.off)
		}
	}
	for _, f := range cur.Fields {
		if f.CName == m[2] {
			return fmt.Errorf("%s:%d: field %s declared twice", name, n, m[2])
		}
	}
	cur.Fields = append(cur.Fields, Field{CName: m[2], CType: m[1], Dim: m[3], Len: count, Offset: p.off, Size: ty.size})
	if ty.align > cur.Align {
		cur.Align = ty.align
	}
	p.off += ty.size * count
	return nil
}

// GoName turns a C identifier into an exported Go one: spawn_trigger →
// SpawnTrigger, MAX_PUPPETS → MaxPuppets, _reserved_hdr → ReservedHdr.
func GoName(c string) string {
	var b strings.Builder
	for _, p := range strings.Split(c, "_") {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(strings.ToLower(p[1:]))
	}
	return b.String()
}

// prefix is the Go name prefix of a struct's constants: "" for the
// mailbox itself (Off*, Len*, MailboxSize), the struct name otherwise
// (PuppetOff*, PuppetLen*, PuppetSize).
func prefix(s *Struct) string {
	if s.Name == "Mailbox" {
		return ""
	}
	return s.Name
}

// Generate parses the headers as Parse does and returns the gofmt'd
// source of wwapi/mailbox/layout_gen.go.
func Generate(name string, src []byte, read Reader) ([]byte, error) {
	l, err := Parse(name, src, read)
	if err != nil {
		return nil, err
	}
	return l.Go()
}

// GenerateDir is Generate for the root header at headerPath and the
// headers it includes from the same directory.
func GenerateDir(headerPath string) ([]byte, error) {
	l, err := ParseDir(headerPath)
	if err != nil {
		return nil, err
	}
	return l.Go()
}

// Go returns the gofmt'd source of wwapi/mailbox/layout_gen.go.
func (l *Layout) Go() ([]byte, error) {
	var b bytes.Buffer
	seen := map[string]string{}
	name := func(goName, from string) (string, error) {
		if prev, dup := seen[goName]; dup {
			return "", fmt.Errorf("Go name %s for both %s and %s", goName, prev, from)
		}
		seen[goName] = from
		return goName, nil
	}
	from := path.Join(path.Dir(HeaderPath), l.Sources[0].Name)
	if len(l.Sources) > 1 {
		var inc []string
		for _, f := range l.Sources[1:] {
			inc = append(inc, f.Name)
		}
		from += " (with " + strings.Join(inc, ", ") + ")"
	}
	b.WriteString("// Code generated by tools/gen_mailbox from " + from + ". DO NOT EDIT.\n\n")
	b.WriteString("package mailbox\n\n")

	b.WriteString("// The header's #defines.\nconst (\n")
	for _, d := range l.Defines {
		if d.Name == "MAILBOX_ADDR" {
			if _, err := name("Mailbox", d.Name); err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "\t// Mailbox is the mailbox's address (MAILBOX_ADDR).\n\tMailbox uint32 = %s\n", d.Text)
			continue
		}
		n, err := name(GoName(d.Name), d.Name)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\t%s = %s // %s\n", n, d.Text, d.Name)
	}
	b.WriteString(")\n")

	defGo := map[string]string{}
	for _, d := range l.Defines {
		defGo[d.Name] = GoName(d.Name)
	}
	for i := range l.Structs {
		s := &l.Structs[i]
		p := prefix(s)
		if p == "" {
			fmt.Fprintf(&b, "\n// Mailbox field offsets, from Mailbox. Len* is an array field's length.\nconst (\n")
		} else {
			fmt.Fprintf(&b, "\n// %s field offsets, from the start of a %s. %sLen* is an array field's length.\nconst (\n", s.Name, s.Name, p)
		}
		sz, err := name(s.Name+"Size", s.Name)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\t%s = 0x%X\n", sz, s.Size)
		for _, f := range s.Fields {
			decl := f.CType + " " + f.CName
			if f.Dim != "" {
				decl += "[" + f.Dim + "]"
			}
			on, err := name(p+"Off"+GoName(f.CName), s.Name+"."+f.CName)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "\t%s = 0x%02X // %s\n", on, f.Offset, decl)
			if f.Dim != "" {
				ln, err := name(p+"Len"+GoName(f.CName), s.Name+"."+f.CName+" length")
				if err != nil {
					return nil, err
				}
				if g, ok := defGo[f.Dim]; ok {
					fmt.Fprintf(&b, "\t%s = %s\n", ln, g)
				} else {
					fmt.Fprintf(&b, "\t%s = %d\n", ln, f.Len)
				}
			}
		}
		b.WriteString(")\n")
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %v", err)
	}
	return out, nil
}
