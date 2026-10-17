package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// Tunic colors, session side (docs/09-player-colors.md §5.4): which color
// each tunic slot shows, from the session's player list, plus the
// appearance byte of the pose trailer and the tunic dev commands. The
// slots themselves (the hero texture, the recolor, the mailbox writes)
// are wwapi/avatar's Tunics.

// remoteCasual reports whether a remote's pose trailer says it wears the
// casual clothes: byte faceStateWireBytes (the 17th) is the appearance
// byte, bit 0 = casual (docs/09 §5.4). Senders before it send 16 bytes.
func remoteCasual(face []byte) bool {
	return len(face) > faceStateWireBytes && face[faceStateWireBytes]&appearanceCasual != 0
}

const appearanceCasual = 0x01

// localAppearance is the appearance byte broadcast-pose appends to the
// face trailer: bit 0 = Link #1 wears the casual clothes. ok is false
// while Link isn't loaded.
func localAppearance(m avatar.TunicMem) (b byte, ok bool) {
	link, err := m.ReadU32(addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return 0, false
	}
	flg, err := m.ReadU32(link + addr.PlayerOffNoResetFlg1)
	if err != nil {
		return 0, false
	}
	if flg&addr.PlayerFlg1CasualClothes != 0 {
		b |= appearanceCasual
	}
	return b, true
}

// printTunicMailbox is the dump command's tunic block.
func printTunicMailbox(m avatar.TunicMem) {
	blk, err := m.Read(mailbox.Mailbox+mailbox.OffTunicBufs, mailbox.MailboxSize-mailbox.OffTunicBufs)
	if err != nil || len(blk) != mailbox.MailboxSize-mailbox.OffTunicBufs {
		return
	}
	at := func(off int) []byte { return blk[off-mailbox.OffTunicBufs:] }
	fmt.Printf("tunic: state=0x%02X  buf_size=%d  swaps=%d\n",
		at(mailbox.OffTunicState)[0], binary.BigEndian.Uint32(at(mailbox.OffTunicBufSize)), binary.BigEndian.Uint32(at(mailbox.OffTunicSwaps)))
	for s := 0; s < avatar.TunicSlots; s++ {
		name := fmt.Sprintf("slot %d", s)
		if s == avatar.TunicSlotLocal {
			name = "local "
		}
		fmt.Printf("tunic[%s]: buf=0x%08X  enable=%d\n", name, binary.BigEndian.Uint32(at(mailbox.OffTunicBufs+4*s)), at(mailbox.OffTunicEnable)[s])
	}
}

// syncTunics sets every slot's wanted color from the session and steps
// RAM toward it. Link slots follow remoteToLinkSlot; a remote in the
// casual clothes gets the casual shirt (avatar.TunicEnableCasual, whatever its
// color: only the hero's clothes are recolored).
// Only server-assigned colors are drawn: until this client's own color
// arrives (a server from before colors, or the moment before the first
// player list) every color stays off, so everyone keeps the vanilla look
// instead of a by-ID guess that other clients would not agree on (each
// human has two connections, so two IDs). The casual shirt is not a
// color: a casual remote gets it with or without haveMine.
//
// mine/haveMine are network.Client.MyColor().
func syncTunics(t *avatar.Tunics, mine playercolor.Color, haveMine bool, remotes []network.RemotePlayer, linkSlot map[byte]int, now time.Time) {
	if haveMine {
		t.SetLocal(mine)
	} else {
		t.Set(avatar.TunicSlotLocal, false, playercolor.Color{})
	}
	var set [avatar.TunicSlotLocal]bool
	for i := range remotes {
		rp := &remotes[i]
		s, ok := linkSlot[rp.ID]
		if !ok || s < 0 || s >= avatar.TunicSlotLocal {
			continue
		}
		switch {
		case remoteCasual(rp.FaceState):
			t.SetCasual(s)
		case haveMine:
			t.Set(s, rp.HasColor, rp.Color)
		default:
			t.Set(s, false, playercolor.Color{})
		}
		set[s] = true
	}
	for s, ok := range set {
		if !ok {
			t.Set(s, false, playercolor.Color{})
		}
	}
	t.Tick(now)
}

// runTunicSlot is the tunic-slot dev command (docs/09 §7.2): put one
// slot's tunic in a color, or turn it off, on the selected Dolphin. Run
// puppet-sync / mp-local with WW_TUNIC=off alongside it, or puppet-sync
// puts the session's colors back.
func runTunicSlot(slotArg, colorArg string) {
	slot := -1
	switch slotArg {
	case "0", "1":
		slot = int(slotArg[0] - '0')
	case "local", "2":
		slot = avatar.TunicSlotLocal
	}
	if slot < 0 {
		fmt.Println("slot must be 0, 1 or local")
		os.Exit(1)
	}
	on, c := false, playercolor.Color{}
	casual := colorArg == "casual"
	if casual && slot == avatar.TunicSlotLocal {
		fmt.Println("casual is for slots 0 and 1 only")
		os.Exit(1)
	}
	if colorArg != "off" && !casual {
		var err error
		if c, err = playercolor.Parse(colorArg); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		on = true
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	t := avatar.NewTunics(d, func(format string, args ...any) { fmt.Printf(format+"\n", args...) })
	if casual {
		t.SetCasual(slot)
	} else {
		t.Set(slot, on, c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		t.Tick(time.Now())
		if t.Settled(slot) {
			if casual {
				fmt.Printf("tunic slot %s is casual\n", slotArg)
			} else if on {
				fmt.Printf("tunic slot %s is %s (buf 0x%08X)\n", slotArg, c, t.Buf(slot))
			} else {
				fmt.Printf("tunic slot %s is off\n", slotArg)
			}
			return
		}
		time.Sleep(16 * time.Millisecond)
	}
	fmt.Println("timed out; run dump: tunic state must be 1 (the C side allocates after ~5 s in game)")
	os.Exit(1)
}
