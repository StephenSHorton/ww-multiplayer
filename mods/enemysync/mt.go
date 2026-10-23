package enemysync

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The Magtail's row helpers (ww-multiplayer-handoff/design/
// enemy-mirror-batch-d.json "magtail", G-D6; batch C's cut row, restored
// from archive/enemy-batch-c/mt-row-restore.patch; the row Kinds[KindMT],
// off by default: mt+u, every DRC Magtail being unkeyed). MT is batch D's
// first cut (cut_order 1): everything MT-only on the Go side is in this
// file, plus one arm per dispatch switch, so a cut deletes the file and
// those arms. P0-MT2 verified the retail body model live (archive/
// enemy-batch-d/P0.md). This contract file holds the live gate only; the
// Go implementer restores ReadSampleMT (with G-D6's slot +0x18 and aux
// changes), mtKillHit, mtHitCollider and mtWas from the patch
// (CONTRACT.md "Go").

// MtLiveRefusal is the Magtail's LiveOK (the REL's em_mt_gate at
// listing): "" when the live bytes pass, else why not: a mode other than
// MOVE, FIGHT or the pinned mode (BALL is local custody, DEAD dying), the
// appear switch wait (+0x2BB), the death flag (+0x18F8), the killed flag
// (+0x1CBC), the light-arrow shrink (+0x1906) or a carry. An adopt gate
// (liveSticky): a listed Magtail stays listed when it curls (mode 2 is the
// custody signal the client's REL hands back on) and while it dies.
func MtLiveRefusal(g LiveGate) string {
	switch {
	case g.Mode != MtModeMove && g.Mode != MtModeFight && g.Mode != MtModePinned:
		return fmt.Sprintf("mode%d", g.Mode)
	case g.MtAppear != 0:
		return "appear"
	case g.MtDeath != 0:
		return "death"
	case g.MtKilled != 0:
		return "killed"
	case g.MtIce != 0:
		return "ice"
	case g.Carried:
		return "carried"
	}
	return ""
}

// readMtGate reads a Magtail's LiveGate bytes beyond Mode and Carried:
// u8 +0x2BB, s8 +0x18F8, s8 +0x1906 and u8 +0x1CBC.
func readMtGate(m mem.Reader, a *actors.Actor, g *LiveGate) error {
	var err error
	u8 := func(off uint32) uint8 {
		if err != nil {
			return 0
		}
		var v uint8
		v, err = mem.ReadU8(m, a.Addr+off)
		return v
	}
	g.MtAppear = u8(addr.MtOffAppear)
	g.MtDeath = u8(addr.MtOffDeath)
	g.MtIce = u8(addr.MtOffIceShrink)
	g.MtKilled = u8(addr.MtOffKilled)
	return err
}
