// Package mailboxgen turns inject/include/mailbox.h into Go: the
// mailbox's #define values and every field offset of its structs. The
// header is the source of truth; tools/gen_mailbox writes the result to
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
	"regexp"
	"strconv"
	"strings"
)

// The generator's input and output, relative to the module root.
const (
	HeaderPath = "inject/include/mailbox.h"
	OutputPath = "wwapi/mailbox/layout_gen.go"
)

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

// Layout is everything the header declares that Go mirrors.
type Layout struct {
	Defines []Define
	Structs []Struct
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
)

// Parse reads mailbox.h. It refuses anything it does not understand
// inside a struct (an unknown type, an unknown array size, a block
// comment) rather than guess a layout, and it checks the header's own
// "// +0xNN" comments against the offsets it computes.
func Parse(src []byte) (*Layout, error) {
	l := &Layout{}
	defs := map[string]int64{}
	types := map[string]struct{ size, align int }{}
	for k, v := range scalars {
		types[k] = struct{ size, align int }{v, v}
	}
	var cur *Struct
	off := 0
	sc := bufio.NewScanner(bytes.NewReader(src))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.Contains(line, "/*") {
			return nil, fmt.Errorf("mailbox.h:%d: block comments are not supported", n)
		}
		if m := defRE.FindStringSubmatch(line); m != nil {
			if cur != nil {
				return nil, fmt.Errorf("mailbox.h:%d: #define inside a struct", n)
			}
			v, err := strconv.ParseInt(m[2], 0, 64)
			if err != nil {
				return nil, fmt.Errorf("mailbox.h:%d: %v", n, err)
			}
			if _, dup := defs[m[1]]; dup {
				return nil, fmt.Errorf("mailbox.h:%d: %s defined twice", n, m[1])
			}
			defs[m[1]] = v
			l.Defines = append(l.Defines, Define{Name: m[1], Value: v, Text: m[2]})
			continue
		}
		if strings.HasPrefix(line, "typedef struct {") {
			if cur != nil {
				return nil, fmt.Errorf("mailbox.h:%d: nested struct", n)
			}
			cur, off = &Struct{Align: 1}, 0
			continue
		}
		if cur == nil {
			continue
		}
		if m := endRE.FindStringSubmatch(line); m != nil {
			if _, dup := types[m[1]]; dup {
				return nil, fmt.Errorf("mailbox.h:%d: type %s declared twice", n, m[1])
			}
			cur.Name = m[1]
			cur.Size = (off + cur.Align - 1) &^ (cur.Align - 1)
			types[cur.Name] = struct{ size, align int }{cur.Size, cur.Align}
			l.Structs = append(l.Structs, *cur)
			cur = nil
			continue
		}
		m := fieldRE.FindStringSubmatch(line)
		if m == nil {
			if !cComment.MatchString(line) {
				return nil, fmt.Errorf("mailbox.h:%d: cannot parse struct line %q", n, line)
			}
			continue
		}
		ty, ok := types[m[1]]
		if !ok {
			return nil, fmt.Errorf("mailbox.h:%d: unknown type %q", n, m[1])
		}
		count := 1
		if m[3] != "" {
			if v, err := strconv.Atoi(m[3]); err == nil {
				count = v
			} else if v, ok := defs[m[3]]; ok {
				count = int(v)
			} else {
				return nil, fmt.Errorf("mailbox.h:%d: unknown array size %q", n, m[3])
			}
		}
		off = (off + ty.align - 1) &^ (ty.align - 1)
		if m[4] != "" {
			want, _ := strconv.ParseInt(m[4], 0, 64)
			if int(want) != off {
				return nil, fmt.Errorf("mailbox.h:%d: comment says %s is at +0x%X, the struct puts it at +0x%X", n, m[2], want, off)
			}
		}
		for _, f := range cur.Fields {
			if f.CName == m[2] {
				return nil, fmt.Errorf("mailbox.h:%d: field %s declared twice", n, m[2])
			}
		}
		cur.Fields = append(cur.Fields, Field{CName: m[2], CType: m[1], Dim: m[3], Len: count, Offset: off, Size: ty.size})
		if ty.align > cur.Align {
			cur.Align = ty.align
		}
		off += ty.size * count
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		return nil, fmt.Errorf("mailbox.h: unterminated struct")
	}
	if l.Mailbox() == nil {
		return nil, fmt.Errorf("mailbox.h: no Mailbox struct")
	}
	if _, ok := defs["MAILBOX_ADDR"]; !ok {
		return nil, fmt.Errorf("mailbox.h: no MAILBOX_ADDR")
	}
	return l, nil
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

// Generate parses mailbox.h and returns the gofmt'd source of
// wwapi/mailbox/layout_gen.go.
func Generate(src []byte) ([]byte, error) {
	l, err := Parse(src)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	seen := map[string]string{}
	name := func(goName, from string) (string, error) {
		if prev, dup := seen[goName]; dup {
			return "", fmt.Errorf("Go name %s for both %s and %s", goName, prev, from)
		}
		seen[goName] = from
		return goName, nil
	}
	b.WriteString("// Code generated by tools/gen_mailbox from inject/include/mailbox.h. DO NOT EDIT.\n\n")
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
