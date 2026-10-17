package write

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// FuzzAllowlist adds spans built from the input and checks the list
// against a brute-force map: spans stay sorted and disjoint, an
// overlapping span is refused, and At, Mask and Allows agree with what
// was added.
func FuzzAllowlist(f *testing.F) {
	f.Add([]byte{0, 0, 3, 0xFF, 1, 2, 0, 8, 2, 1, 1})
	f.Add([]byte{0, 0, 4, 0, 0, 0, 0, 0, 2, 2, 7, 7})
	f.Fuzz(func(t *testing.T, in []byte) {
		const base = uint32(addr.MEM1Base)
		var l Allowlist[uint16]
		type cell struct {
			mask byte
			rule uint16
			name string
		}
		want := map[uint32]cell{}
		owner := map[uint32]string{} // every byte of every span added, mask 0 or not
		for k := 0; len(in) >= 3; k++ {
			start := base + uint32(binary.BigEndian.Uint16(in[:2]))%64
			n := int(in[2]%8) + 1
			in = in[3:]
			mask := make([]byte, n)
			copy(mask, in)
			in = in[min(n, len(in)):]
			rules := make([]uint16, n)
			for i := range rules {
				rules[i] = uint16(k<<8 | i)
			}
			name := string(rune('a' + k%26))
			overlaps, some := false, false
			for i := range n {
				_, taken := owner[start+uint32(i)]
				overlaps = overlaps || taken
				some = some || mask[i] != 0
			}
			err := l.Add(Span[uint16]{Start: start, Mask: mask, Rules: rules, Name: name})
			switch {
			case !some:
				if err != nil {
					t.Fatalf("an all-zero span: %v", err)
				}
				continue
			case overlaps:
				if err == nil {
					t.Fatalf("span %s at 0x%08X +%d overlaps and was added", name, start, n)
				}
				continue
			case err != nil:
				t.Fatalf("span %s: %v", name, err)
			}
			for i := range n {
				a := start + uint32(i)
				owner[a] = name
				want[a] = cell{mask: mask[i], rule: rules[i], name: name}
			}
		}
		spans := l.Spans()
		for i := 1; i < len(spans); i++ {
			if spans[i-1].End() > spans[i].Start {
				t.Fatalf("spans %d and %d overlap", i-1, i)
			}
		}
		for a := base; a < base+80; a++ {
			name, r, ok := l.At(a)
			w, in := want[a]
			if name != w.name || ok != (in && w.mask != 0) || in && r != w.rule || l.Mask(a) != w.mask {
				t.Fatalf("0x%08X: At = %q, %d, %v, Mask 0x%02X; want %+v (held %v)", a, name, r, ok, l.Mask(a), w, in)
			}
			if l.Allows(a, 1) != (w.mask != 0) {
				t.Fatalf("0x%08X: Allows = %v", a, l.Allows(a, 1))
			}
		}
		if err := Disjoint(l.Ranges(), l.Ranges()); len(spans) > 0 && err == nil {
			t.Fatal("a set is disjoint from itself")
		}
	})
}

// FuzzWriteFields makes a batch of fields over an allowlist with random
// gates, refusals and contents, and checks that it is all or nothing
// before the beat: nothing is written when a byte is closed or refused,
// and when it writes, memory holds every field and Wrote names exactly
// the bytes that changed.
func FuzzWriteFields(f *testing.F) {
	f.Add([]byte{0, 4, 1, 2, 3, 4, 8, 2, 9, 9}, uint64(0), uint64(0), []byte{0, 0, 0, 0})
	f.Add([]byte{0, 1, 7, 0, 1, 0}, uint64(1), uint64(2), []byte{7, 7})
	f.Fuzz(func(t *testing.T, spec []byte, closed, refused uint64, initial []byte) {
		const base = uint32(addr.MEM1Base) + 0x100
		g := wholeGuard(t, Range{Addr: base, Len: 48})
		for i := range 48 {
			if closed>>i&1 != 0 && i%5 == 0 {
				g.closed[base+uint32(i)] = true
			}
		}
		g.refuse = func(a uint32, old, val byte) bool {
			i := a - base
			return i < 64 && refused>>i&1 != 0 && i%7 == 0
		}
		var fs []Field
		for len(spec) >= 2 && len(fs) < 6 {
			off, n := uint32(spec[0]%64), int(spec[1]%8)+1
			spec = spec[2:]
			data := make([]byte, n)
			copy(data, spec)
			spec = spec[min(n, len(spec)):]
			fs = append(fs, Field{Addr: base + off, Data: data})
		}
		m := newBeatMem(10, 1, 1)
		if err := m.Store(base, initial[:min(len(initial), 64)]); err != nil && len(initial) > 0 {
			t.Fatal(err)
		}
		before, _ := m.FakeMem.Read(base, 80)
		m.Record(true)
		w := &FrameWriter{Mem: m, Breaker: NewBreaker(BreakerConfig{}), Timeout: 50 * time.Millisecond}
		res, err := w.WriteFields(context.Background(), Batch{Guard: g, From: &Beat{Heartbeat: 10, At: time.Now()}}, fs)

		// What the guard says of each byte of the batch, against memory
		// before it: the batch must go through only if every byte would.
		ok := true
		for _, fl := range fs {
			for i, v := range fl.Data {
				a := fl.Addr + uint32(i)
				switch {
				case g.l.Mask(a) != 0 && g.closed[a]:
					ok = false
				case g.Check(a, before[a-base], v) != nil:
					ok = false
				}
			}
		}
		if !ok {
			if err == nil || len(m.Writes()) != 0 {
				t.Fatalf("a closed or refused byte: err %v, %d writes", err, len(m.Writes()))
			}
			return
		}
		if err != nil {
			// After the beat, a later field may be refused against what an
			// earlier field of the batch wrote; nothing else may fail.
			if res.Refused != 1 {
				t.Fatalf("WriteFields = %+v, %v", res, err)
			}
			return
		}
		after, _ := m.FakeMem.Read(base, 80)
		sim := bytes.Clone(before)
		var wrote []ByteWrite
		for _, fl := range fs {
			changed := false
			for i, v := range fl.Data {
				changed = changed || sim[fl.Addr-base+uint32(i)] != v
			}
			if !changed {
				continue
			}
			for i, v := range fl.Data {
				o := fl.Addr - base + uint32(i)
				if sim[o] != v {
					wrote = append(wrote, ByteWrite{Addr: fl.Addr + uint32(i), Old: sim[o], New: v})
				}
				sim[o] = v
			}
		}
		if !bytes.Equal(after, sim) {
			t.Fatalf("memory % X, want % X", after, sim)
		}
		if len(wrote) != len(res.Wrote) {
			t.Fatalf("Wrote %+v, want %+v", res.Wrote, wrote)
		}
		for i := range wrote {
			if wrote[i] != res.Wrote[i] {
				t.Fatalf("Wrote[%d] = %+v, want %+v", i, res.Wrote[i], wrote[i])
			}
		}
	})
}

// FuzzStageRequest checks that a request Check takes encodes to bytes
// that decode back to it, with a NUL-terminated name of stage-name
// characters.
func FuzzStageRequest(f *testing.F) {
	f.Add("sea", int16(1), int8(44), int8(-1))
	f.Add("M_NewD2", int16(255), int8(0), int8(11))
	f.Fuzz(func(t *testing.T, name string, point int16, room, layer int8) {
		h := StageRequest{Name: name, Point: point, Room: room, Layer: layer}
		if h.Check() != nil {
			return
		}
		b := h.Bytes()
		n := bytes.IndexByte(b[:addr.StageNameLen], 0)
		if n < 1 || string(b[:n]) != name {
			t.Fatalf("name % X, want %q", b[:addr.StageNameLen], name)
		}
		for _, c := range b[:addr.StageNameLen] {
			if !StageNameByte(c) {
				t.Fatalf("0x%02X in the name", c)
			}
		}
		if int16(binary.BigEndian.Uint16(b[addr.StageOffPoint:])) != point || int8(b[addr.StageOffRoomNo]) != room || int8(b[addr.StageOffLayer]) != layer {
			t.Fatalf("% X does not decode to %+v", b, h)
		}
		fs := h.Fields()
		if fs[len(fs)-1].Addr != addr.NextStageEnable {
			t.Fatal("mEnable is not the last field")
		}
	})
}
