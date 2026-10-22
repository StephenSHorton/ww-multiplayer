package enemysync

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// Layout 4's knob table (ww-multiplayer-handoff/design/
// enemy-mirror-batch-c.json layout4.knob_table, D3; mailbox.EnemyKnobs at
// Mailbox+0x850): live quiet-death frames, collider-2 centre heights and
// invuln waits for kind ids 3..15, which layout 3 compiled into the REL.
// Each side writes its own table before the first block that names its
// gen (EnemyBlock.KnobGen), as its own write: the table is outside the
// block's seq span, so the REL takes it only when both gen words equal
// the block's knob_gen (em_knob_validate) and echoes that gen at block
// +0x194 (EnemyStatus.KnobGenSeen). A torn, clobbered or save-state-stale
// table is the REL's compiled values, never a hook turned on (the hook
// bytes of ids 7..14 are the block's own, EnemyBlock.HkdHi, D2).
//
// knobWriter is one side's table: a new gen (never 0) at the first block,
// after a disarm, on an epoch change and whenever the values change; and
// one rewrite, logged, whenever the REL acked a block naming the gen but
// echoes another (a clobber, a save state: batch C test_plan L4-4).
type knobWriter struct {
	// gen is the gen the table in RAM carries (0: none written since the
	// last reset); epoch the block epoch it was written under; vals what
	// it holds (Gen aside).
	gen   uint16
	epoch uint32
	vals  mailbox.EnemyKnobs
	// next is the last gen handed out (seeded per process, so a restarted
	// Go never reuses the gen a stale table in RAM still carries).
	next   uint16
	seeded bool
	// since is the seq of the first block written naming gen (sinceOK
	// once one was): an ack at or past it is the REL's verdict on gen.
	since   uint32
	sinceOK bool
	// Rewrites counts the echo-triggered rewrites.
	Rewrites uint64
}

// seed sets the first gen's base (once); s is the side's start time or
// epoch.
func (k *knobWriter) seed(s uint64) {
	if !k.seeded {
		k.next, k.seeded = uint16(s^s>>16^s>>32^s>>48), true
	}
}

// newGen is the next gen, never 0 and never the one in RAM.
func (k *knobWriter) newGen() uint16 {
	for {
		k.next++
		if k.next != 0 && k.next != k.gen {
			return k.next
		}
	}
}

// reset forgets the table (a disarm): the next block writes a new one.
func (k *knobWriter) reset() { k.gen, k.sinceOK = 0, false }

// prepare is called before each block write with that block's epoch and
// seq and the values wanted: it writes the table first when there is
// none, the epoch changed or the values did (a new gen), and returns the
// gen the block names.
func (k *knobWriter) prepare(m mailbox.Writer, want *mailbox.EnemyKnobs, epoch, seq uint32) (uint16, error) {
	w := *want
	w.Gen = 0
	if k.gen == 0 || epoch != k.epoch || w != k.vals {
		gen := k.newGen()
		if err := k.write(m, &w, gen); err != nil {
			return 0, err
		}
		k.epoch = epoch
	}
	if !k.sinceOK {
		k.since, k.sinceOK = seq, true
	}
	return k.gen, nil
}

// write writes vals under gen and makes it the table in RAM; the next
// block written becomes since.
func (k *knobWriter) write(m mailbox.Writer, vals *mailbox.EnemyKnobs, gen uint16) error {
	t := *vals
	t.Gen = gen
	if err := mailbox.WriteEnemyKnobs(m, &t); err != nil {
		k.gen = 0
		return err
	}
	t.Gen = 0
	k.gen, k.vals, k.sinceOK = gen, t, false
	return nil
}

// check compares the REL's echo with the gen: once the REL acked a block
// naming it (AckSeq at or past since) and echoes another, the table is
// rewritten under the same gen and one line logged; the next check waits
// for an ack of a block written after the rewrite, so a REL that keeps
// refusing it gets one rewrite per acked block, not one per tick.
func (k *knobWriter) check(m mailbox.Writer, rep log.Reporter, who string, st *mailbox.EnemyStatus) error {
	if k.gen == 0 || !k.sinceOK || st.KnobGenSeen == k.gen || int32(st.AckSeq-k.since) < 0 {
		return nil
	}
	seen := st.KnobGenSeen
	if err := k.write(m, &k.vals, k.gen); err != nil {
		return err
	}
	k.Rewrites++
	log.Logf(rep, log.Info, "%s: knob table rewritten: seen G%d want G%d", who, seen, k.gen)
	return nil
}
