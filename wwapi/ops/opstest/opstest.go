// Package opstest is a fake of the C runtime's rings (inject/src/core/ops.c)
// over a mem.FakeMem: the same boot, drain and push steps, written in Go,
// for tests of wwapi/ops and of the commands built on it.
package opstest

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Op is a fake op: a, b, c in, a result out (0 reads as failed, as in C).
type Op func(a, b, c uint32) uint8

// C is the fake runtime. Its writes are the game's own (FakeMem.Store).
type C struct {
	M   *mem.FakeMem
	ops [mailbox.OpsMaxOps]Op
}

// New returns a fake over m, not booted yet.
func New(m *mem.FakeMem) *C { return &C{M: m} }

func (c *C) u8(o uint32) uint8 { b, _ := c.M.Read(mailbox.OpsAddr+o, 1); return b[0] }
func (c *C) u32(o uint32) uint32 {
	b, _ := c.M.Read(mailbox.OpsAddr+o, 4)
	return mem.U32BE(b)
}
func (c *C) put8(o uint32, v uint8) { _ = c.M.Store(mailbox.OpsAddr+o, []byte{v}) }
func (c *C) put16(o uint32, v uint16) {
	var b [2]byte
	mem.PutU16BE(b[:], v)
	_ = c.M.Store(mailbox.OpsAddr+o, b[:])
}
func (c *C) put32(o uint32, v uint32) {
	var b [4]byte
	mem.PutU32BE(b[:], v)
	_ = c.M.Store(mailbox.OpsAddr+o, b[:])
}

// Boot is ops_init: clear the header and rings, the directory, the core
// ops (ECHO, and KILL refusing with NOTFOUND unless kill is set), then the
// magic and the boot event. A second Boot is a reboot.
func (c *C) Boot(kill Op) {
	_ = c.M.Store(mailbox.OpsAddr, make([]byte, mailbox.OpsRingEnd))
	c.ops = [mailbox.OpsMaxOps]Op{}
	c.put16(mailbox.OpsLayoutOff, mailbox.OpsLayout)
	for i, d := range []struct {
		tag       uint32
		off, size uint16
	}{
		{mailbox.OpsTagEvents, mailbox.OpsEvWriteOff, mailbox.OpsCmdHeadOff - mailbox.OpsEvWriteOff},
		{mailbox.OpsTagCommands, mailbox.OpsCmdHeadOff, mailbox.OpsRingEnd - mailbox.OpsCmdHeadOff},
		{mailbox.OpsTagCoop, mailbox.OpsCoopOff, mailbox.OpsCoopSize},
	} {
		e := uint32(mailbox.OpsDirOff + i*mailbox.OpsDirEntrySize)
		c.put32(e, d.tag)
		c.put16(e+4, d.off)
		c.put16(e+6, d.size)
	}
	c.put16(mailbox.OpsDirCountOff, 3)
	c.Register(mailbox.OpsOpEcho, func(a, b, cc uint32) uint8 {
		c.Push(mailbox.OpsEvEcho, a, b, cc)
		return mailbox.OpsROK
	})
	if kill == nil {
		kill = func(uint32, uint32, uint32) uint8 { return mailbox.OpsRNotFound }
	}
	c.Register(mailbox.OpsOpKill, kill)
	c.put32(mailbox.OpsMagicOff, mailbox.OpsMagic)
	c.Push(mailbox.OpsEvBoot, mailbox.OpsLayout, 0, 0)
}

// Register is ww_cmd_register.
func (c *C) Register(op uint8, fn Op) bool {
	if op == 0 || int(op) >= mailbox.OpsMaxOps || c.ops[op] != nil {
		return false
	}
	c.ops[op] = fn
	return true
}

// Push is ww_event_push.
func (c *C) Push(kind uint16, a, b, cc uint32) bool {
	if c.u32(mailbox.OpsMagicOff) != mailbox.OpsMagic {
		return false
	}
	w := c.u32(mailbox.OpsEvWriteOff)
	if w-c.u32(mailbox.OpsEvReadOff) >= mailbox.OpsRing {
		c.put32(mailbox.OpsStatEvDropOff, c.u32(mailbox.OpsStatEvDropOff)+1)
		return false
	}
	e := uint32(mailbox.OpsEvRingOff) + (w%mailbox.OpsRing)*mailbox.OpsEntrySize
	seq := uint8(w)
	c.put16(e+mailbox.OpsEKind, kind)
	c.put8(e+mailbox.OpsESeq, seq)
	c.put32(e+mailbox.OpsEA, a)
	c.put32(e+mailbox.OpsEB, b)
	c.put32(e+mailbox.OpsEC, cc)
	c.put8(e+mailbox.OpsECheck, mailbox.OpsEventCheck(kind, seq, a, b, cc))
	c.put32(mailbox.OpsEvWriteOff, w+1)
	return true
}

// Frame is ops_frame: run at most OpsCmdPerFrame queued commands.
func (c *C) Frame() {
	for n := 0; n < mailbox.OpsCmdPerFrame; n++ {
		t := c.u32(mailbox.OpsCmdTailOff)
		if c.u32(mailbox.OpsCmdHeadOff) == t {
			return
		}
		e := uint32(mailbox.OpsCmdRingOff) + (t%mailbox.OpsRing)*mailbox.OpsEntrySize
		op, seq := c.u8(e+mailbox.OpsCOp), c.u8(e+mailbox.OpsCSeq)
		a, b, cc := c.u32(e+mailbox.OpsCA), c.u32(e+mailbox.OpsCB), c.u32(e+mailbox.OpsCC)
		var r uint8
		switch {
		case seq != uint8(t) || c.u8(e+mailbox.OpsCCheck) != mailbox.OpsCmdCheck(op, seq, a, b, cc):
			r = mailbox.OpsRBadCheck
			c.put32(mailbox.OpsStatBadOff, c.u32(mailbox.OpsStatBadOff)+1)
		case int(op) >= mailbox.OpsMaxOps || c.ops[op] == nil:
			r = mailbox.OpsRUnknown
			c.put32(mailbox.OpsStatUnknownOff, c.u32(mailbox.OpsStatUnknownOff)+1)
		default:
			if r = c.ops[op](a, b, cc); r == 0 {
				r = mailbox.OpsRFailed
			}
		}
		c.put8(e+mailbox.OpsCRes, r)
		c.put32(mailbox.OpsStatDoneOff, c.u32(mailbox.OpsStatDoneOff)+1)
		if r != mailbox.OpsROK {
			c.put32(mailbox.OpsStatRefusedOff, c.u32(mailbox.OpsStatRefusedOff)+1)
		}
		c.Push(mailbox.OpsEvCmd, uint32(op)|uint32(seq)<<8|uint32(r)<<16, a, t)
		c.put32(mailbox.OpsCmdTailOff, t+1)
	}
}
