package main

// The research probes and developer tools that were in package main's
// main.go: Dolphin memory pokes and peeks, PPC disassembly, the eye/face
// decal investigations, input injection, screenshots and the pose-feed
// experiments. Moved verbatim with Phase D6; none of them ship in
// ww-multiplayer.

import (
	"encoding/binary"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/tunicsync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// classifyPPC returns a short label + operand hint for a PPC instruction.
// Used by `disasm` to help pick safe hook sites — we care most about:
//   - `bl` instructions (opcode 18 with LK=1): safest to replace, caller already
//     assumes volatile reg clobber.
//   - `stwu r1, -N(r1)` and `stw rN, M(r1)` in a function's prologue: UNSAFE to
//     replace, losing them corrupts the stack frame / non-volatile reg saves.
//   - branches that would clobber control flow if replaced.
func classifyPPC(inst uint32) string {
	primary := inst >> 26
	switch primary {
	case 0:
		if inst == 0 {
			return "(zero)"
		}
		return "(illegal)"
	case 14:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		si := int16(inst & 0xFFFF)
		if ra == 0 {
			return fmt.Sprintf("li    r%d, %d", rt, si)
		}
		return fmt.Sprintf("addi  r%d, r%d, %d", rt, ra, si)
	case 15:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		si := inst & 0xFFFF
		if ra == 0 {
			return fmt.Sprintf("lis   r%d, 0x%04X", rt, si)
		}
		return fmt.Sprintf("addis r%d, r%d, 0x%04X", rt, ra, si)
	case 16:
		if inst&1 != 0 {
			return "bcl"
		}
		return "bc"
	case 18:
		disp := inst & 0x03FFFFFC
		if disp&0x02000000 != 0 {
			disp |= 0xFC000000
		}
		if inst&1 != 0 {
			return fmt.Sprintf("bl    +0x%X", int32(disp))
		}
		return fmt.Sprintf("b     +0x%X", int32(disp))
	case 19:
		switch inst & 0x07FF {
		case 0x020:
			return "blr"
		case 0x021:
			return "blrl"
		case 0x420:
			return "bctr"
		case 0x421:
			return "bctrl"
		}
		return "branch-reg"
	case 31:
		xo := (inst >> 1) & 0x3FF
		switch xo {
		case 339:
			return "mfspr"
		case 467:
			return "mtspr"
		case 444:
			rs := (inst >> 21) & 0x1F
			ra := (inst >> 16) & 0x1F
			rb := (inst >> 11) & 0x1F
			if rs == rb {
				return fmt.Sprintf("mr    r%d, r%d", ra, rs)
			}
			return "or"
		}
		return fmt.Sprintf("X-form xo=%d", xo)
	case 32:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		return fmt.Sprintf("lwz   r%d, %d(r%d)", rt, d, ra)
	case 36:
		rs := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		return fmt.Sprintf("stw   r%d, %d(r%d)", rs, d, ra)
	case 37:
		rs := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		if rs == 1 && ra == 1 {
			return fmt.Sprintf("stwu  r1, %d(r1)  <PROLOGUE>", d)
		}
		return fmt.Sprintf("stwu  r%d, %d(r%d)", rs, d, ra)
	case 40:
		return "lhz"
	case 44:
		return "sth"
	case 48:
		return "lfs"
	case 52:
		return "stfs"
	}
	return fmt.Sprintf("op=%d", primary)
}

func runDisasm(addr uint32, count int) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	data, err := d.Read(addr, count*4)
	if err != nil {
		fmt.Printf("read failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Disasm %d instructions from 0x%08X:\n", count, addr)
	for i := 0; i < count; i++ {
		inst := binary.BigEndian.Uint32(data[i*4:])
		fmt.Printf("  0x%08X: %08X  %s\n", addr+uint32(i*4), inst, classifyPPC(inst))
	}
}

// Write a target position into slot `slot` of the mailbox (default 0).
// The frame hook reads the slot each frame and writes into the
// corresponding puppet actor's pos. One-shot write.
func runMovePuppet(xs, ys, zs string, slot int) {
	if slot < 0 || slot >= feed.MaxPuppets {
		fmt.Printf("slot %d out of range [0, %d)\n", slot, feed.MaxPuppets)
		os.Exit(1)
	}
	parseF32 := func(s string) float32 {
		v, err := strconv.ParseFloat(s, 32)
		if err != nil {
			fmt.Printf("bad float %q: %v\n", s, err)
			os.Exit(1)
		}
		return float32(v)
	}
	x, y, z := parseF32(xs), parseF32(ys), parseF32(zs)

	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], math.Float32bits(x))
	binary.BigEndian.PutUint32(buf[4:8], math.Float32bits(y))
	binary.BigEndian.PutUint32(buf[8:12], math.Float32bits(z))
	if err := d.Write(feed.SlotAddr(slot, mailbox.PuppetOffPosX), buf); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	// Ensure the slot is marked active so the C hook syncs it.
	one := make([]byte, 4)
	binary.BigEndian.PutUint32(one, 1)
	d.Write(feed.SlotAddr(slot, mailbox.PuppetOffActive), one)
	fmt.Printf("slot %d target: X=%.1f Y=%.1f Z=%.1f\n", slot, x, y, z)
}

// Reads local Link's position from Dolphin every 50ms and forwards it to the
// server. Pair with puppet-sync on a second host (or same host) to see Link's
// movements mirrored to a puppet actor, proving the full network round-trip.
func runBroadcastLink(name, addr string) {
	fmt.Printf("=== Broadcast Link: %s -> %s ===\n\n", name, addr)

	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	client := engine.NewPlayerClient(name)
	client.OnLog = func(msg string) { fmt.Printf("[net] %s\n", msg) }
	if err := client.Connect(addr); err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	for client.IsConnected() {
		pos, err := player.NewLink(d).Position()
		if err == nil && pos != nil {
			netPos := &network.PlayerPosition{
				PosX: pos.PosX,
				PosY: pos.PosY,
				PosZ: pos.PosZ,
				RotY: pos.RotY,
			}
			if err := client.SendPosition(netPos); err != nil {
				fmt.Printf("send: %v\n", err)
				break
			}
			fmt.Printf("  link -> X:%10.1f Y:%8.1f Z:%10.1f\r", pos.PosX, pos.PosY, pos.PosZ)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("\nDisconnected.")
}

// Iterates every active puppet slot and applies the proc-specific unhide
// poke. Idempotent and safe to call whenever a puppet appears to be hidden.
func runUnhidePuppet() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	any := false
	for i := 0; i < feed.MaxPuppets; i++ {
		active, _ := d.ReadU32(feed.SlotAddr(i, mailbox.PuppetOffActive))
		if active != 1 {
			continue
		}
		ptr, err := d.ReadU32(feed.SlotAddr(i, mailbox.PuppetOffActorPtr))
		if err != nil || ptr < 0x80000000 || ptr >= 0x81800000 {
			continue // actor not yet resolved by C; skip this tick
		}
		procWord, err := d.ReadU32(ptr + 0x08)
		if err != nil {
			continue
		}
		proc := procWord >> 16
		buf := make([]byte, 4)
		switch proc {
		case 0x01CB: // TSUBO: m678 = 2
			binary.BigEndian.PutUint32(buf, 2)
			d.Write(ptr+0x678, buf)
			fmt.Printf("slot %d: unhid TSUBO (actor 0x%08X) m678=2\n", i, ptr)
			any = true
		case 0x00C3: // KAMOME: clear mSwitchNo at +0x2AA (u32 write at +0x2A8 zeroes 4 safe bytes)
			binary.BigEndian.PutUint32(buf, 0)
			d.Write(ptr+0x2A8, buf)
			fmt.Printf("slot %d: unhid KAMOME (actor 0x%08X) mSwitchNo=0\n", i, ptr)
			any = true
		default:
			fmt.Printf("slot %d: unknown proc 0x%04X at 0x%08X\n", i, proc, ptr)
		}
	}
	if !any {
		fmt.Println("No active puppet slots resolved yet — is the game past the 10s spawn gate and has puppet-sync populated a slot?")
		os.Exit(1)
	}
}

func runPokeU32(addrHex, valHex string) {
	parseHex := func(s string) uint32 {
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
		if err != nil {
			fmt.Printf("bad hex %q: %v\n", s, err)
			os.Exit(1)
		}
		return uint32(v)
	}
	addr := parseHex(addrHex)
	val := parseHex(valHex)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, val)
	if err := d.Write(addr, buf); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote 0x%08X to 0x%08X\n", val, addr)
}

// Mailbox field offsets (mailbox.Off*) are generated from
// inject/include/mailbox_core.h into wwapi/mailbox/layout_gen.go. shadow_mode
// is 0 (off) or 5 (pose feed); the dev modes 1-4 were deleted in Phase E0.
//
// The pose-feed slot fields (pose_buf_ptrs u32, pose_joint_counts u16,
// pose_buf_states u8, pose_seqs u8, face_state, face_seqs) are arrays of
// MAILBOX_POSE_SLOT_CAP, of which the runtime uses the first avatar.Cap
// (MAX_REMOTE_LINKS); their per-slot offsets are avatar.Off*.

// v0.1.3: sender-side pose publish buffer (pose_publish_*). The C-side
// draw hook copies Link #1's mpNodeMtx into this GameHeap-resident
// buffer ONCE per frame after daPy_lk_c_draw returns, giving
// broadcast-pose a stable read target that doesn't race the game's calc
// pass. pose_publish_ptr is the GameHeap address of the 2016 B buffer;
// the joint count is 42 for Link; state is 0 unalloc, 1 ready, 0xFD
// alloc failed; seq is bumped every frame after the copy.

// v0.1.8: warp request — Go bumps warp_seq with a target position; the
// C handler in multiplayer_update writes it into Link's home/old/current
// pos (so velocity-from-(current-old) reads zero) and clears mOldSpeed
// (so cached momentum doesn't snap Link back), then bumps warp_ack so
// Go can poll for completion. See inject/include/mailbox_core.h.
//
// Session 9 face-state network sync (see inject/include/mailbox_core.h
// "FACE-STATE NETWORK SYNC"). Per-slot struct of u16 BE values
// mirroring J3DTevBlock+0x08..+0x0B for mat[1] (left pupil) and mat[4]
// (right pupil). Receiver writes these and bumps face_seqs[slot]; the
// C-side J3DMatPacket vtable hook reads them when mini-link's eye
// matpackets draw. Session 15: FaceState grows from 8 B to 16 B (adds
// mouth fields for mat[14] and mat[15]). face_hook_state is
// 0=disarmed 1=installed 0xFC=stride mismatch 0xFE=vtable mismatch
// 0xFD=null array; face_hook_enable is the arm/disarm flag (Go writes;
// C side acts next frame); face_hook_swaps is a u32 saturating count
// of shim swaps; face_hook_mp_size is the observed matpacket stride (0
// until install runs; expect 0x3C); face_hook_patched_count is the
// matpacket slots written by the most recent install pass.
//
// Session 14: face_state_local is LOCAL Link's natural btp face state,
// published by the C side at the top of daPy_draw_hook BEFORE any swap.
// broadcast-pose reads from here instead of resolving the live
// tev_block (which races with the bracket).
//
// Input injection (synthetic PADStatus override): the input_* fields
// mirror PADStatus from libogc; see mailbox_core.h. C-side pad_read_shim
// (hooked over bl PADRead in JUTGamePad::read at 0x802C39A0) overrides
// mPadStatus[0] when input_enable is non-zero (0 = passthrough);
// input_overrides is a u32 saturating override counter.

// Size of the per-slot wire payload appended to a pose message.
// Session 15: bumped 8 → 16 to add mouth state (mat[14] + mat[15]).
const faceStateWireBytes = player.FaceBytes

func runPokeVec3(addrHex, xs, ys, zs string) {
	a, _ := strconv.ParseUint(strings.TrimPrefix(addrHex, "0x"), 16, 32)
	x, _ := strconv.ParseFloat(xs, 32)
	y, _ := strconv.ParseFloat(ys, 32)
	z, _ := strconv.ParseFloat(zs, 32)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], math.Float32bits(float32(x)))
	binary.BigEndian.PutUint32(buf[4:8], math.Float32bits(float32(y)))
	binary.BigEndian.PutUint32(buf[8:12], math.Float32bits(float32(z)))
	if err := d.Write(uint32(a), buf); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote (%.2f, %.2f, %.2f) to 0x%08X\n", x, y, z, a)
}

// runScanPos scans a wide RAM range for cXyz triplets matching Link's
// current.pos — used to find globals like l_debug_keep_pos that live
// outside Link's actor block. Defaults to 0x80300000..0x80800000
// (the .data/.bss region above DOL code, below mailbox).
func runScanPos() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	pos, err := player.NewLink(d).Position()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Current pos: (%.2f, %.2f, %.2f) — scanning 0x80300000..0x80800000 for triplets:\n",
		pos.PosX, pos.PosY, pos.PosZ)
	const chunkSize = 0x10000
	for base := uint32(0x80300000); base < 0x80800000; base += chunkSize {
		buf, err := d.Read(base, chunkSize+8)
		if err != nil {
			continue
		}
		for off := 0; off+12 <= len(buf); off += 4 {
			x := math.Float32frombits(binary.BigEndian.Uint32(buf[off : off+4]))
			if abs32(x-pos.PosX) > 1.0 {
				continue
			}
			y := math.Float32frombits(binary.BigEndian.Uint32(buf[off+4 : off+8]))
			z := math.Float32frombits(binary.BigEndian.Uint32(buf[off+8 : off+12]))
			if abs32(y-pos.PosY) < 1.0 && abs32(z-pos.PosZ) < 1.0 {
				fmt.Printf("  0x%08X = (%.2f, %.2f, %.2f)\n", base+uint32(off), x, y, z)
			}
		}
	}
}

// runPeek reads and prints n bytes at an absolute PPC RAM address —
// in u32 BE chunks plus per-word float interpretation.
func runPeek(addrHex string, n uint32) {
	addr64, err := strconv.ParseUint(strings.TrimPrefix(addrHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad addr: %v\n", err)
		os.Exit(1)
	}
	addr := uint32(addr64)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf, err := d.Read(addr, int(n))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	for i := uint32(0); i+4 <= n; i += 4 {
		u := binary.BigEndian.Uint32(buf[i : i+4])
		f := math.Float32frombits(u)
		fmt.Printf("  0x%08X: 0x%08X  (f32: %.4f)\n", addr+i, u, f)
	}
}

// runWarpForce toggles the C-side force-warp mode. When on, the warp
// handler re-applies the target position every frame regardless of
// seq/ack. Used to test whether continuous writes can outpace whatever
// is reverting our one-shot warp.
func runWarpForce(xs, ys, zs string, on bool) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if on {
		x, _ := strconv.ParseFloat(xs, 32)
		y, _ := strconv.ParseFloat(ys, 32)
		z, _ := strconv.ParseFloat(zs, 32)
		player.WarpForce(d, [3]float32{float32(x), float32(y), float32(z)})
		fmt.Printf("Force-warp ON: target=(%.1f, %.1f, %.1f). Use `warp-force-off` to release.\n", x, y, z)
	} else {
		player.WarpForceOff(d)
		fmt.Println("Force-warp OFF.")
	}
}

// linkModelData returns Link #1's J3DModelData (daPy_lk_c+0x328,
// mpCLModelData). The mini-Links are built from this same model data
// (mini_link_data in inject/src/mp/minilink.c); until Phase E0 the C side also
// published it as mailbox.dbg_model_data.
func linkModelData(d mem.Reader) (uint32, error) {
	linkPtr, err := player.NewLink(d).Ptr()
	if err != nil {
		return 0, err
	}
	if linkPtr == 0 {
		return 0, fmt.Errorf("no Link actor")
	}
	return mem.ReadU32(d, linkPtr+0x328)
}

// linkModel returns Link #1's own J3DModel (daPy_lk_c+0x32C, mpCLModel):
// the one J3DModel on mpCLModelData that is not a mini-Link.
func linkModel(d mem.Reader) uint32 {
	linkPtr, err := player.NewLink(d).Ptr()
	if err != nil || linkPtr == 0 {
		return 0
	}
	v, _ := mem.ReadU32(d, linkPtr+0x32C)
	return v
}

// matchMiniLinkSlot returns the pose slot whose published pose buffer
// (pose_buf_ptrs[i], driven: pose_seqs[i] != 0) has the same first 48
// bytes (joint 0's 3x4 matrix) as the mpNodeMtx at nodeMtx, or -1. C
// copies pose_bufs[i] over mini_link_models[i]'s mpNodeMtx before the
// no-op-walker calc each frame, so the two agree except while Go is mid-write;
// a few retries ride over that.
func matchMiniLinkSlot(d mem.Reader, nodeMtx uint32) int {
	const n = 48
	for try := 0; try < 5; try++ {
		got, err := d.Read(nodeMtx, n)
		if err != nil || len(got) != n {
			return -1
		}
		for i := 0; i < mailbox.MailboxPoseSlotCap; i++ {
			seq, err := d.Read(mailbox.Mailbox+avatar.OffPoseSeq(i), 1)
			if err != nil || len(seq) != 1 || seq[0] == 0 {
				continue
			}
			ptr, err := mem.ReadU32(d, mailbox.Mailbox+avatar.OffPoseBufPtr(i))
			if err != nil || ptr < 0x80000000 || ptr >= 0x81800000 {
				continue
			}
			buf, err := d.Read(ptr, n)
			if err == nil && len(buf) == n && string(buf) == string(got) {
				return i
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return -1
}

// runEyeFixGates reads Link #1's three gating-state fields that
// daPy_lk_c::draw consults to decide whether to run the eye-decal
// four-pass recipe (matched to the disasm at 0x80107308 against the
// decomp d_a_player_main.cpp:1775-1891):
//
//   - field_0x2b0 (this+0x2B0, float): r24 = (field_0x2b0 <= -85.0f).
//     The fcmpo+cror+rlwinm.+beq at 0x801076A0..0x801076B8 routes
//     r24=TRUE to block A (if-r24 body, legs-only). Subtle: the
//     `rlwinm.` (Rc=1) RESETS CR0[eq] = (r24 == 0), so the following
//     `beq` is taken when r24==0 (i.e. when "<=" is FALSE), routing
//     to 0x80107790 (the elseif-attention check).
//   - this+0x2A0 (u32): bit 0x800 = checkFreezeState(). The four-pass
//     body at 0x80107860 starts with `lwz r0, 0x2A0(this); rlwinm. r0,
//     r0, 0, 20, 20; bne 0x80107AC4` — if freeze set, skips four-pass
//     and runs hideHatAndBackle path.
//   - mCameraInfoIdx (this+0x356C, u32) → cameraInfo[idx] at
//     0x803CA720 + idx*0x34. Bit 0x20 = the elseif-attention gate.
//     0x80107790..0x801077A0 reads the entry, masks 0x20, and
//     `beq cr0, 0x80107860` jumps to four-pass if the bit is CLEAR.
//
// Four-pass recipe RUNS when ALL hold:
//
//	(field_0x2b0 > -85.0f) AND (cameraInfo & 0x20 == 0) AND (NOT freeze)
//
// Compare a run with no mini-Link drawn (shadow-mode 0) against one with
// the pose feed drawing (shadow-mode 5) to see which gate (if any)
// run_eye_fix flips between frames.
func runEyeFixGates() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	linkPtr, err := player.NewLink(d).Ptr()
	if err != nil || linkPtr == 0 {
		fmt.Printf("Link actor not loaded: %v\n", err)
		os.Exit(1)
	}
	field2b0, err := d.ReadF32(linkPtr + 0x2B0)
	if err != nil {
		fmt.Printf("read field_0x2b0: %v\n", err)
		os.Exit(1)
	}
	flags2a0, err := d.ReadU32(linkPtr + 0x2A0)
	if err != nil {
		fmt.Printf("read this+0x2a0: %v\n", err)
		os.Exit(1)
	}
	camIdx, err := d.ReadU32(linkPtr + 0x356C)
	if err != nil {
		fmt.Printf("read mCameraInfoIdx: %v\n", err)
		os.Exit(1)
	}
	camEntryAddr := uint32(0x803CA720) + camIdx*0x34
	camEntry, err := d.ReadU32(camEntryAddr)
	if err != nil {
		fmt.Printf("read cameraInfo[%d]: %v\n", camIdx, err)
		os.Exit(1)
	}
	// mNoResetFlg0 at this+0x29C — checkPlayerNoDraw() at line 1696
	// returns TRUE if (cameraInfo & 0x02) || (mNoResetFlg0 & daPyFlg0_NO_DRAW
	// = 0x08000000). If TRUE, daPy_lk_c::draw early-returns BEFORE the four-pass.
	noResetFlg0, err := d.ReadU32(linkPtr + 0x29C)
	if err != nil {
		fmt.Printf("read mNoResetFlg0: %v\n", err)
		os.Exit(1)
	}
	r24 := field2b0 <= -85.0
	freezeBit := (flags2a0 & 0x800) != 0
	attentionBit := (camEntry & 0x20) != 0
	camAttn02 := (camEntry & 0x02) != 0
	noDrawBit := (noResetFlg0 & 0x08000000) != 0
	playerNoDraw := camAttn02 || noDrawBit

	fmt.Printf("Link actor:           0x%08X\n", linkPtr)
	fmt.Println()
	fmt.Printf("field_0x2b0:          %f  (r24 = (<= -85.0) = %v)\n", field2b0, r24)
	fmt.Printf("this+0x2A0:           0x%08X  (& 0x800 freeze → %v)\n", flags2a0, freezeBit)
	fmt.Printf("mCameraInfoIdx:       %d  → cameraInfo @ 0x%08X = 0x%08X\n",
		camIdx, camEntryAddr, camEntry)
	fmt.Printf("                                         (& 0x20 attention → %v)\n", attentionBit)
	fmt.Printf("                                         (& 0x02 noDraw    → %v)\n", camAttn02)
	fmt.Printf("mNoResetFlg0 (+0x29C): 0x%08X  (& 0x08000000 NO_DRAW → %v)\n", noResetFlg0, noDrawBit)
	fmt.Printf("checkPlayerNoDraw() = %v  (early-returns at draw line 1696 if true)\n", playerNoDraw)
	fmt.Println()
	if !playerNoDraw && !r24 && !attentionBit && !freezeBit {
		fmt.Println("Four-pass: RUNS  (no early-return, r24=FALSE, attention=clear, !freeze)")
	} else {
		fmt.Println("Four-pass: SKIPPED — gate(s) routing elsewhere:")
		if playerNoDraw {
			fmt.Println("  - checkPlayerNoDraw() TRUE → EARLY RETURN at line 1696 (no draw at all)")
		}
		if r24 {
			fmt.Println("  - r24=TRUE (field_0x2b0 <= -85.0) → block A (legs-only body)")
		}
		if attentionBit && !r24 {
			fmt.Println("  - attention bit 0x20 set (and r24=FALSE) → block B (elseif body)")
		}
		if freezeBit && !r24 && !attentionBit {
			fmt.Println("  - freeze bit 0x800 set → hideHatAndBackle (no eye-decal recipe)")
		}
	}
}

// runEyeAnimFind scans MEM1 for J3DModel candidates whose +0x04 (mModelData)
// equals Link's mpCLModelData (= mini_link_data). Filters by sanity checks on
// other header fields and marks Link #1's own model (daPy_lk_c+0x32C); the
// other candidates are the mini-Links. Prints all candidates with their
// headers.
func runEyeAnimFind() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	modelData, err := linkModelData(d)
	if err != nil || modelData == 0 {
		fmt.Printf("Link's mpCLModelData not available (got 0x%08X, err=%v)\n", modelData, err)
		os.Exit(1)
	}
	ownModel := linkModel(d)
	fmt.Printf("mini_link_data       = 0x%08X (Link+0x328)\n", modelData)
	fmt.Printf("Link #1's J3DModel   = 0x%08X (Link+0x32C)\n\n", ownModel)

	const memLo, memHi = uint32(0x80000000), uint32(0x81800000)
	const chunk = uint32(2 * 1024 * 1024)
	// J3DModel layout — mModelData is at +0x04 (not +0x10 as initially assumed).
	const modelDataOff = uint32(0x04)
	hits := []uint32{}
	for base := memLo; base < memHi; base += chunk {
		size := chunk
		if base+size > memHi {
			size = memHi - base
		}
		buf, err := d.Read(base, int(size))
		if err != nil {
			continue
		}
		for o := 0; o+4 <= len(buf); o += 4 {
			if binary.BigEndian.Uint32(buf[o:o+4]) != modelData {
				continue
			}
			candAt := base + uint32(o)
			if candAt < modelDataOff {
				continue
			}
			cand := candAt - modelDataOff
			hits = append(hits, cand)
		}
	}
	fmt.Printf("MEM1 scan: %d u32-aligned addresses had +0x04 == mini_link_data\n\n", len(hits))

	isMEM1 := func(v uint32) bool { return v >= 0x80000000 && v < 0x81800000 }
	for _, h := range hits {
		hdr, err := d.Read(h, 0xC0)
		if err != nil {
			continue
		}
		vtable := binary.BigEndian.Uint32(hdr[0x00:0x04])
		flag := binary.BigEndian.Uint32(hdr[0x0C:0x10])
		userArea := binary.BigEndian.Uint32(hdr[0x14:0x18])
		nodeMtx := binary.BigEndian.Uint32(hdr[0x8C:0x90])
		matField := binary.BigEndian.Uint32(hdr[0xB4:0xB8])
		// Skip obvious non-J3DModels: vtable must be a code-region pointer,
		// userArea + matField sane.
		if !isMEM1(vtable) || vtable >= 0x80800000 {
			continue
		}
		mark := "  (mini-Link)"
		if h == ownModel {
			mark = "  (Link #1's own model)"
		}
		fmt.Printf("J3DModel @ 0x%08X%s\n", h, mark)
		fmt.Printf("  +0x00 vtable=0x%08X  +0x04 modelData=0x%08X  +0x0C flag=0x%08X\n", vtable, modelData, flag)
		fmt.Printf("  +0x14 userArea=0x%08X  +0x8C mpNodeMtx=0x%08X  +0xB4 matpktField=0x%08X\n",
			userArea, nodeMtx, matField)
	}
}

// dumpHexLine prints one 16-byte line of hex starting at baseAddr+off.
// Bytes whose value matches `highlight` are prefixed with `*`.
func dumpHexLine(buf []byte, baseAddr uint32, indent string, highlight byte, hasHL bool) {
	for off := 0; off < len(buf); off += 16 {
		end := off + 16
		if end > len(buf) {
			end = len(buf)
		}
		line := fmt.Sprintf("%s+0x%03X:", indent, off)
		for k := off; k < end; k++ {
			tag := " "
			if hasHL && buf[k] == highlight {
				tag = "*"
			}
			line += fmt.Sprintf("%s%02X", tag, buf[k])
		}
		fmt.Println(line)
	}
}

// runEyeAnimDLDump dumps mini-Link's matpacket entries for mat[0..5], traces
// every MEM1-pointer-shaped field in matpacket[i] (looking for a J3DDisplayListObj
// candidate), and for each candidate dumps the first n bytes of memory it points
// to with byte 0x27 (the eye-open texno baked into mini-link's private DL)
// highlighted. Use the model address from `eye-anim-find`.
func runEyeAnimDLDump(modelAddrHex string, n uint32) {
	addr64, err := strconv.ParseUint(strings.TrimPrefix(modelAddrHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad model addr %q: %v\n", modelAddrHex, err)
		os.Exit(1)
	}
	modelAddr := uint32(addr64)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	hdr, err := d.Read(modelAddr, 0xC0)
	if err != nil {
		fmt.Printf("read model header: %v\n", err)
		os.Exit(1)
	}
	modelData := binary.BigEndian.Uint32(hdr[0x10:0x14])
	matPktField := binary.BigEndian.Uint32(hdr[0xB4:0xB8])
	fmt.Printf("J3DModel @ 0x%08X:  +0x10=0x%08X (modelData)  +0xB4=0x%08X (matpacket field)\n\n",
		modelAddr, modelData, matPktField)

	const stride = uint32(0x3C)
	type baseCand struct {
		name string
		addr uint32
	}
	bases := []baseCand{
		{"matpkt-field-as-pointer", matPktField},
		{"in-place-at-model+0xB4", modelAddr + 0xB4},
	}
	isMEM1 := func(v uint32) bool { return v >= 0x80000000 && v < 0x81800000 }
	matIdx := []int{0, 1, 2, 3, 4, 5}

	for _, b := range bases {
		fmt.Printf("=== matpacket base candidate: %s @ 0x%08X ===\n", b.name, b.addr)
		if !isMEM1(b.addr) {
			fmt.Println("  (out of MEM1)")
			fmt.Println()
			continue
		}
		for _, i := range matIdx {
			pktAddr := b.addr + uint32(i)*stride
			pkt, err := d.Read(pktAddr, int(stride))
			if err != nil {
				fmt.Printf("  matpacket[%d] @ 0x%08X — read failed: %v\n", i, pktAddr, err)
				break
			}
			role := ""
			switch i {
			case 1:
				role = "  // left pupil eye material"
			case 4:
				role = "  // right pupil eye material"
			}
			fmt.Printf("  matpacket[%d] @ 0x%08X%s\n", i, pktAddr, role)
			for off := uint32(0); off < stride; off += 16 {
				line := fmt.Sprintf("    +0x%02X:", off)
				for k := uint32(0); k < 16 && off+k < stride; k += 4 {
					w := binary.BigEndian.Uint32(pkt[off+k : off+k+4])
					line += fmt.Sprintf(" %08X", w)
				}
				fmt.Println(line)
			}
			if i == 1 || i == 4 {
				tryOffs := []uint32{0x08, 0x0C, 0x10, 0x14, 0x18, 0x1C, 0x20, 0x24, 0x28, 0x2C, 0x30, 0x34, 0x38}
				for _, o := range tryOffs {
					v := binary.BigEndian.Uint32(pkt[o : o+4])
					if !isMEM1(v) {
						continue
					}
					objHdr, err := d.Read(v, 0x10)
					if err != nil {
						continue
					}
					p0 := binary.BigEndian.Uint32(objHdr[0:4])
					p1 := binary.BigEndian.Uint32(objHdr[4:8])
					p2 := binary.BigEndian.Uint32(objHdr[8:12])
					fmt.Printf("    [+0x%02X→0x%08X] obj{+0x00:%08X +0x04:%08X +0x08:%08X}\n",
						o, v, p0, p1, p2)
					if isMEM1(p0) && p1 > 0 && p1 < 0x100000 {
						raw, err := d.Read(p0, int(min32(n, p1)))
						if err == nil {
							fmt.Printf("      DL bytes @ 0x%08X (size 0x%X), highlighting 0x27:\n", p0, p1)
							dumpHexLine(raw, p0, "        ", 0x27, true)
						}
					} else if v >= 0x80000000 && v < 0x81800000 {
						raw, err := d.Read(v, int(n))
						if err == nil {
							hits := 0
							for _, bb := range raw {
								if bb == 0x27 {
									hits++
								}
							}
							if hits > 0 {
								fmt.Printf("      raw bytes @ 0x%08X, %d occurrences of 0x27, dumping with highlight:\n", v, hits)
								dumpHexLine(raw, v, "        ", 0x27, true)
							}
						}
					}
				}
			}
			fmt.Println()
		}
	}
}

func min32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// runEyeAnimWatch reads `size` bytes at `addr` repeatedly for `secs` seconds,
// comparing against the initial snapshot. Reports every byte offset that ever
// differs and the set of distinct values observed at that offset. Used to find
// where btp is patching DL bytes per frame (if anywhere).
func runEyeAnimWatch(addrHex, sizeHex string, secs int) {
	addr64, err := strconv.ParseUint(strings.TrimPrefix(addrHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad addr: %v\n", err)
		os.Exit(1)
	}
	addr := uint32(addr64)
	size64, err := strconv.ParseUint(strings.TrimPrefix(sizeHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad size: %v\n", err)
		os.Exit(1)
	}
	size := uint32(size64)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	initial, err := d.Read(addr, int(size))
	if err != nil {
		fmt.Printf("initial read: %v\n", err)
		os.Exit(1)
	}
	seenValues := make(map[uint32]map[byte]bool)
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	samples := 0
	for time.Now().Before(deadline) {
		buf, err := d.Read(addr, int(size))
		if err != nil {
			continue
		}
		for i := uint32(0); i < size; i++ {
			if buf[i] != initial[i] {
				if seenValues[i] == nil {
					seenValues[i] = map[byte]bool{initial[i]: true}
				}
				seenValues[i][buf[i]] = true
			}
		}
		samples++
		time.Sleep(8 * time.Millisecond)
	}

	fmt.Printf("Watched 0x%X bytes at 0x%08X for %ds (%d samples)\n", size, addr, secs, samples)
	if len(seenValues) == 0 {
		fmt.Println("No bytes changed during the watch window.")
		return
	}
	fmt.Printf("%d byte offsets changed during watch:\n", len(seenValues))
	keys := make([]uint32, 0, len(seenValues))
	for k := range seenValues {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, off := range keys {
		vals := seenValues[off]
		valList := make([]byte, 0, len(vals))
		for v := range vals {
			valList = append(valList, v)
		}
		sort.Slice(valList, func(i, j int) bool { return valList[i] < valList[j] })
		fmt.Printf("  +0x%04X (abs 0x%08X): values seen = ", off, addr+off)
		for i, v := range valList {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("0x%02X", v)
		}
		fmt.Println()
	}
}

// runEyeAnimPoke is the per-frame DL-byte-poke probe (Option C from session 7
// roadmap). Given a J3DModel address, identifies mat[1] (left pupil) and
// mat[4] (right pupil) private-DL byte regions and patches every occurrence
// of the bytes 0x27 / 0x06 (the texno bytes baked at create time) with
// whatever value btp is currently writing to the SHARED TevBlock at
// J3DMaterial+0x2C+0x08 each tick. Runs for `secs` seconds at 60 Hz.
//
// Goal: prove that overwriting these DL bytes makes mini-Link's eyes blink
// in sync with Link #1's eyes. Once visually confirmed, port to C-side.
func runEyeAnimPoke(modelAddrHex string, secs int) {
	addr64, err := strconv.ParseUint(strings.TrimPrefix(modelAddrHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad model addr %q: %v\n", modelAddrHex, err)
		os.Exit(1)
	}
	modelAddr := uint32(addr64)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	hdr, err := d.Read(modelAddr, 0xC0)
	if err != nil {
		fmt.Printf("read model: %v\n", err)
		os.Exit(1)
	}
	modelData := binary.BigEndian.Uint32(hdr[0x10:0x14])
	matPktField := binary.BigEndian.Uint32(hdr[0xB4:0xB8])
	matArrPtr, err := d.ReadU32(modelData + 0x58 + 0x08)
	if err != nil || matArrPtr == 0 {
		fmt.Printf("can't resolve material array via modelData=0x%08X\n", modelData)
		os.Exit(1)
	}

	isMEM1 := func(v uint32) bool { return v >= 0x80000000 && v < 0x81800000 }
	resolveMatpacketBase := func() uint32 {
		if isMEM1(matPktField) {
			return matPktField
		}
		return modelAddr + 0xB4
	}
	matpktBase := resolveMatpacketBase()
	const stride = uint32(0x3C)

	type matInfo struct {
		idx        int
		pktAddr    uint32
		sharedTev  uint32
		dlBytesPtr uint32
		dlBytesLen uint32
		texHits    []uint32
	}
	mats := []matInfo{}
	for _, idx := range []int{1, 4} {
		matPtr, _ := d.ReadU32(matArrPtr + uint32(idx)*4)
		if matPtr == 0 {
			fmt.Printf("mat[%d]: NULL ptr — skip\n", idx)
			continue
		}
		tevBlock, _ := d.ReadU32(matPtr + 0x2C)
		if tevBlock == 0 {
			fmt.Printf("mat[%d]: NULL tevBlock — skip\n", idx)
			continue
		}

		pktAddr := matpktBase + uint32(idx)*stride
		pkt, err := d.Read(pktAddr, int(stride))
		if err != nil {
			fmt.Printf("mat[%d]: matpacket read fail: %v\n", idx, err)
			continue
		}
		// Try each plausible offset for the J3DDisplayListObj* and pick the
		// one whose DL bytes contain 0x27 occurrences (= baked texno).
		var bestPtr, bestLen, bestObj uint32
		var bestHits []uint32
		for _, o := range []uint32{0x18, 0x1C, 0x20, 0x24, 0x28, 0x2C, 0x30, 0x34, 0x38} {
			v := binary.BigEndian.Uint32(pkt[o : o+4])
			if !isMEM1(v) {
				continue
			}
			oh, err := d.Read(v, 8)
			if err != nil {
				continue
			}
			p0 := binary.BigEndian.Uint32(oh[0:4])
			p1 := binary.BigEndian.Uint32(oh[4:8])
			if !isMEM1(p0) || p1 == 0 || p1 > 0x10000 {
				continue
			}
			raw, err := d.Read(p0, int(p1))
			if err != nil {
				continue
			}
			var hits []uint32
			for k := 0; k < len(raw); k++ {
				if raw[k] == 0x27 {
					hits = append(hits, uint32(k))
				}
			}
			if len(hits) > len(bestHits) {
				bestHits = hits
				bestPtr = p0
				bestLen = p1
				bestObj = v
			}
		}
		if bestPtr == 0 {
			fmt.Printf("mat[%d]: no DL pointer with 0x27 occurrences found in matpacket bytes\n", idx)
			continue
		}
		fmt.Printf("mat[%d]: DLObj=0x%08X dlBytes=0x%08X size=0x%X tevBlock=0x%08X — %d 0x27 hits\n",
			idx, bestObj, bestPtr, bestLen, tevBlock, len(bestHits))
		mats = append(mats, matInfo{
			idx: idx, pktAddr: pktAddr,
			sharedTev: tevBlock, dlBytesPtr: bestPtr, dlBytesLen: bestLen,
			texHits: bestHits,
		})
	}
	if len(mats) == 0 {
		fmt.Println("No mat DLs identified; cannot poke. Run eye-anim-dl-dump first.")
		os.Exit(1)
	}

	fmt.Printf("\nPoking each frame for %d seconds at 60 Hz...\n", secs)
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	frames := 0
	for time.Now().Before(deadline) {
		for _, m := range mats {
			tx, err := d.Read(m.sharedTev+0x08, 4)
			if err != nil {
				continue
			}
			// tx[0..1] = texNo[0] (BE u16), tx[2..3] = texNo[1] (BE u16).
			// Write low byte of each occurrence based on heuristic: the baked
			// value 0x27 = low byte of texNo[1] (eye-open). All occurrences
			// of 0x27 in the DL are presumed to be that low byte.
			lowByte := tx[3]
			payload := []byte{lowByte}
			for _, off := range m.texHits {
				_ = d.Write(m.dlBytesPtr+off, payload)
			}
		}
		frames++
		time.Sleep(16 * time.Millisecond)
	}
	fmt.Printf("Done. Wrote %d frame batches.\n", frames)
}

// runFaceDlLoop is the Go-side stand-in for the C-side shim's intended
// behavior. The session-10 C-shim has a bug: it reads dl_len from
// DLObj+0x04 but the actual layout puts it at +0x08 (DLObj+0x04 holds a
// duplicate of the start ptr, not a length). install bails the bounds
// check, face_hook_dl_count stays 0, shim no-ops.
//
// Rather than recompile + recapture save state to test the fix, this
// loop replicates the intended write path entirely from Go: each tick,
// read face_state[slot] from mailbox, walk to mini-link's mat[1] and
// mat[4] matpacket DL bytes, and overwrite the texno-LSB byte at each
// 0x06/0x27 offset. If this makes mini-link's pupil reflect the remote
// face_state (rather than local btp), the mechanism is proven and we
// just need to fix the C-side install offset.
//
// secs: 0 = run until Ctrl+C, else run for that many seconds then exit.
// forceLsb: if >= 0, write this byte value to ALL cached DL offsets each
// tick (skipping the mailbox face_state read). Useful for asymmetric
// visual tests — set forceLsb to a wildly wrong value (e.g. 0x99) and
// observe whether mini-link's pupil glitches in only this Dolphin.
func runFaceDlLoop(secs int, forceLsb int) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Locate the mini-links' J3DModels. Same logic as runEyeAnimFind
	// condensed: scan MEM1 for J3DModel candidates whose +0x04 ==
	// mini_link_data (Link's mpCLModelData), minus Link's own model.
	miniData, err := linkModelData(d)
	if err != nil || miniData < 0x80000000 || miniData >= 0x81800000 {
		fmt.Printf("Link's mpCLModelData not available (got 0x%08X)\n", miniData)
		os.Exit(1)
	}

	// Find ALL J3DModels with the same modelData. With MAX_REMOTE_LINKS=2,
	// expect 2-3 candidates: mini_link_models[0], mini_link_models[1],
	// and Link's own model. The two whose +0x8C is in heap (not in
	// the local-Link's range) are the mini-links.
	const memLo, memHi = uint32(0x80000000), uint32(0x81800000)
	const chunk = uint32(2 * 1024 * 1024)
	type candidate struct {
		addr    uint32
		matpkts uint32
		nodeMtx uint32
	}
	var cands []candidate
	for base := memLo; base < memHi; base += chunk {
		size := chunk
		if base+size > memHi {
			size = memHi - base
		}
		buf, err := d.Read(base, int(size))
		if err != nil {
			continue
		}
		for o := 0; o+4 <= len(buf); o += 4 {
			if binary.BigEndian.Uint32(buf[o:o+4]) != miniData {
				continue
			}
			candAt := base + uint32(o)
			if candAt < 4 {
				continue
			}
			cand := candAt - 4
			hdr, err := d.Read(cand, 0xC0)
			if err != nil {
				continue
			}
			vt := binary.BigEndian.Uint32(hdr[0:4])
			if vt < 0x80000000 || vt >= 0x80800000 {
				continue
			}
			nm := binary.BigEndian.Uint32(hdr[0x8C:0x90])
			mp := binary.BigEndian.Uint32(hdr[0xB4:0xB8])
			if nm == 0 || mp < 0x80000000 || mp >= 0x81800000 {
				continue
			}
			cands = append(cands, candidate{addr: cand, matpkts: mp, nodeMtx: nm})
		}
	}
	if len(cands) == 0 {
		fmt.Println("no J3DModel candidates found")
		os.Exit(1)
	}

	// Identify slot for each candidate. The mini-links are the
	// candidates that aren't Link's own model. The slot number decides
	// which face_state the loop writes onto that model, so it must be
	// the real one: C copies pose_bufs[slot] (published as
	// pose_buf_ptrs[slot]) over the model's mpNodeMtx before its second
	// calc every frame, so the candidate whose mpNodeMtx head matches a
	// slot's pose buffer head is that slot. A candidate that matches no
	// driven slot (pose_seqs == 0) is skipped.
	const matpacketSize = uint32(0x3C)
	const matLeft = 1
	const matRight = 4
	type slotInfo struct {
		slot    int
		modelAt uint32
		dlPtr   [2]uint32 // [0]=mat[1], [1]=mat[4]
		dlLen   [2]uint32
		dlOffs  [2][]uint32 // 0x06/0x27 byte offsets per mat
	}
	var slots []slotInfo
	ownModel := linkModel(d)
	for _, c := range cands {
		// Skip Link's own model. Mini-link models have mpNodeMtx in
		// the allocated mod/heap range (~0x80EF..0x810F); Link's own
		// is up in 0x815F**. Filter on nodeMtx range to keep only
		// mini-links.
		if c.nodeMtx < 0x80000000 || c.nodeMtx >= 0x81100000 || c.addr == ownModel {
			continue
		}
		slot := matchMiniLinkSlot(d, c.nodeMtx)
		if slot < 0 {
			fmt.Printf("model@0x%08X: mpNodeMtx matches no driven slot's pose buffer, skipped\n", c.addr)
			continue
		}
		s := slotInfo{slot: slot, modelAt: c.addr}
		// Walk to mat[1] and mat[4] matpackets via matpkts + idx*0x3C.
		mats := [2]int{matLeft, matRight}
		for mi, idx := range mats {
			mpAddr := c.matpkts + uint32(idx)*matpacketSize
			dlObj, err := d.ReadU32(mpAddr + 0x20)
			if err != nil || dlObj < 0x80000000 || dlObj >= 0x81800000 {
				continue
			}
			dlPtr, _ := d.ReadU32(dlObj + 0x00)
			dlLen, _ := d.ReadU32(dlObj + 0x08) // CORRECTED: length at +0x08 (not +0x04)
			if dlPtr < 0x80000000 || dlPtr >= 0x81800000 || dlLen == 0 || dlLen > 0x10000 {
				continue
			}
			raw, err := d.Read(dlPtr, int(dlLen))
			if err != nil {
				continue
			}
			var hits []uint32
			for k := 0; k < len(raw); k++ {
				if raw[k] == 0x06 || raw[k] == 0x27 {
					hits = append(hits, uint32(k))
				}
			}
			s.dlPtr[mi] = dlPtr
			s.dlLen[mi] = dlLen
			s.dlOffs[mi] = hits
		}
		slots = append(slots, s)
	}
	if len(slots) == 0 {
		fmt.Println("no mini-link slots resolved")
		os.Exit(1)
	}
	for _, s := range slots {
		fmt.Printf("slot %d: model@0x%08X  mat[1] dl=0x%08X len=0x%X hits=%d  mat[4] dl=0x%08X len=0x%X hits=%d\n",
			s.slot, s.modelAt, s.dlPtr[0], s.dlLen[0], len(s.dlOffs[0]),
			s.dlPtr[1], s.dlLen[1], len(s.dlOffs[1]))
	}

	fmt.Println("\nLooping: face_state -> mini-link DL bytes at ~60 Hz. Ctrl+C to stop.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	var deadline time.Time
	if secs > 0 {
		deadline = time.Now().Add(time.Duration(secs) * time.Second)
	}
	ticks := 0
	for {
		select {
		case <-stop:
			fmt.Printf("\nstopped after %d ticks\n", ticks)
			return
		default:
		}
		if secs > 0 && time.Now().After(deadline) {
			fmt.Printf("\ntime's up after %d ticks\n", ticks)
			return
		}

		// Read face_state for each slot.
		for _, s := range slots {
			var mat1Lsb, mat4Lsb byte
			if forceLsb >= 0 {
				mat1Lsb = byte(forceLsb)
				mat4Lsb = byte(forceLsb)
			} else {
				seq, _ := d.Read(mailbox.Mailbox+avatar.OffFaceSeq(s.slot), 1)
				if len(seq) != 1 || seq[0] == 0 {
					continue // gate: skip until first network sample
				}
				fs, _ := d.Read(mailbox.Mailbox+avatar.OffFaceState(s.slot), 8)
				if len(fs) != 8 {
					continue
				}
				// mat1_tex1 at bytes [2..4], LSB at [3]. mat4_tex1 at [6..8], LSB at [7].
				mat1Lsb = fs[3]
				mat4Lsb = fs[7]
			}
			if s.dlPtr[0] != 0 && len(s.dlOffs[0]) > 0 {
				payload := []byte{mat1Lsb}
				for _, off := range s.dlOffs[0] {
					_ = d.Write(s.dlPtr[0]+off, payload)
				}
			}
			if s.dlPtr[1] != 0 && len(s.dlOffs[1]) > 0 {
				payload := []byte{mat4Lsb}
				for _, off := range s.dlOffs[1] {
					_ = d.Write(s.dlPtr[1]+off, payload)
				}
			}
		}
		ticks++
		// Every ~1 sec, sample mini-link's DL bytes at the cached
		// offsets to see what's actually in memory. If our writes
		// dominate (most reads = our LSB value), the writes commit
		// and persist long enough that whatever consumes them sees
		// our value.
		if ticks%60 == 0 {
			for _, s := range slots {
				if s.dlPtr[0] != 0 && len(s.dlOffs[0]) > 0 {
					counts := map[byte]int{}
					for _, off := range s.dlOffs[0] {
						b, _ := d.Read(s.dlPtr[0]+off, 1)
						if len(b) == 1 {
							counts[b[0]]++
						}
					}
					var keys []byte
					for k := range counts {
						keys = append(keys, k)
					}
					sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
					summary := ""
					for _, k := range keys {
						summary += fmt.Sprintf(" 0x%02X×%d", k, counts[k])
					}
					fmt.Printf("[t+%4ds] slot %d mat[1] dl readback:%s\n", ticks/60, s.slot, summary)
				}
			}
		}
		time.Sleep(16 * time.Millisecond)
	}
}

// runFaceSyncTevPoll forces face_state[0] to a clearly-non-btp value at
// high rate while polling the shared mat[1]/mat[4] tev_block contents
// from outside Dolphin, also at high rate. Goal: detect whether the
// session-9 shim's transient swap (between tex_ptr[0]=new_t0 and the
// post-orig_draw restore) ever lands in RAM observably.
//
// Hook must be armed for this to be meaningful. mp-local must be running
// (otherwise mini-link doesn't render and the matpacket draws don't fire).
//
// Interpretation:
//   - Swap value appears in poll histogram -> shim writes commit; issue
//     is timing/GPU-read (GPU consumed tev_block earlier in the frame
//     than our shim runs).
//   - Swap value never appears, only btp values seen -> shim's writes
//     either aren't being emitted as PPC stores or the tev_block we
//     hooked isn't the same address PPC writes hit.
func runFaceSyncTevPoll(secs int, faceHex string) {
	if len(faceHex) != 16 {
		fmt.Printf("face_hex must be 16 hex chars (8 bytes), got %d\n", len(faceHex))
		os.Exit(1)
	}
	face := make([]byte, 8)
	for i := 0; i < 8; i++ {
		v, err := strconv.ParseUint(faceHex[i*2:i*2+2], 16, 8)
		if err != nil {
			fmt.Printf("bad hex at byte %d: %v\n", i, err)
			os.Exit(1)
		}
		face[i] = byte(v)
	}
	swapMat1 := binary.BigEndian.Uint32(face[0:4]) // expected mat[1] swap value
	swapMat4 := binary.BigEndian.Uint32(face[4:8]) // expected mat[4] swap value

	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Printf("link actor not resolved (linkPtr=0x%08X)\n", linkPtr)
		os.Exit(1)
	}
	dataPtr, err := d.ReadU32(linkPtr + 0x0328)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		fmt.Printf("mpCLModel not resolved (dataPtr=0x%08X)\n", dataPtr)
		os.Exit(1)
	}
	matArr, err := d.ReadU32(dataPtr + 0x58 + 0x08)
	if err != nil || matArr < 0x80000000 || matArr >= 0x81800000 {
		fmt.Printf("matArr not resolved (0x%08X)\n", matArr)
		os.Exit(1)
	}
	resolveTev := func(idx int) uint32 {
		matPtr, err := d.ReadU32(matArr + uint32(idx)*4)
		if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
			return 0
		}
		tev, err := d.ReadU32(matPtr + 0x2C)
		if err != nil || tev < 0x80000000 || tev >= 0x81800000 {
			return 0
		}
		return tev
	}
	mat1Tev := resolveTev(1)
	mat4Tev := resolveTev(4)
	if mat1Tev == 0 || mat4Tev == 0 {
		fmt.Printf("tev resolution failed (mat1=0x%08X mat4=0x%08X)\n", mat1Tev, mat4Tev)
		os.Exit(1)
	}
	fmt.Printf("mat[1] tev=0x%08X  mat[4] tev=0x%08X\n", mat1Tev, mat4Tev)
	fmt.Printf("expected swap_mat1=0x%08X  swap_mat4=0x%08X\n", swapMat1, swapMat4)

	// Sanity: snapshot face_hook_state and face_hook_swaps before/after
	// so we can verify the shim was firing during the poll window.
	state0, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookState, 1)
	swaps0, _ := d.ReadU32(mailbox.Mailbox + mailbox.OffFaceHookSwaps)
	fmt.Printf("face_hook_state=0x%02X  swaps=%d (pre)\n", state0[0], swaps0)
	if state0[0] != 1 {
		fmt.Println("WARNING: face_hook_state != 1 (hook not installed). Run face-hook-arm first; results below are meaningless.")
	}

	type sample struct{ mat1, mat4 uint32 }
	counts := make(map[sample]int)
	hitsMat1 := 0
	hitsMat4 := 0
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	polls := 0
	for time.Now().Before(deadline) {
		// Re-assert face_state and seq each iteration so puppet-sync's
		// 60 Hz writes don't overwrite our value.
		_ = d.Write(mailbox.Mailbox+avatar.OffFaceState(0), face)
		_ = d.Write(mailbox.Mailbox+avatar.OffFaceSeq(0), []byte{1})

		b1, _ := d.Read(mat1Tev+0x08, 4)
		b4, _ := d.Read(mat4Tev+0x08, 4)
		if len(b1) != 4 || len(b4) != 4 {
			continue
		}
		s := sample{
			mat1: binary.BigEndian.Uint32(b1),
			mat4: binary.BigEndian.Uint32(b4),
		}
		counts[s]++
		if s.mat1 == swapMat1 {
			hitsMat1++
		}
		if s.mat4 == swapMat4 {
			hitsMat4++
		}
		polls++
	}

	state1, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookState, 1)
	swaps1, _ := d.ReadU32(mailbox.Mailbox + mailbox.OffFaceHookSwaps)

	type entry struct {
		s sample
		n int
	}
	sorted := make([]entry, 0, len(counts))
	for s, n := range counts {
		sorted = append(sorted, entry{s, n})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].n > sorted[j].n })

	fmt.Printf("\nPolled %d samples over %ds\n", polls, secs)
	fmt.Printf("face_hook_state=0x%02X  swaps=%d (post; Δ=%d)\n", state1[0], swaps1, swaps1-swaps0)
	fmt.Printf("Top tev (mat1, mat4) tuples seen:\n")
	for i, e := range sorted {
		if i >= 25 {
			fmt.Printf("  ...%d more rare tuples\n", len(sorted)-25)
			break
		}
		marker := ""
		if e.s.mat1 == swapMat1 || e.s.mat4 == swapMat4 {
			marker = "  <-- swap-value"
		}
		fmt.Printf("  mat1=0x%08X  mat4=0x%08X  ×%d%s\n", e.s.mat1, e.s.mat4, e.n, marker)
	}
	fmt.Printf("\nmat[1] swap-value hits: %d / %d  (%.2f%%)\n",
		hitsMat1, polls, 100*float64(hitsMat1)/float64(polls))
	fmt.Printf("mat[4] swap-value hits: %d / %d  (%.2f%%)\n",
		hitsMat4, polls, 100*float64(hitsMat4)/float64(polls))
	if hitsMat1 == 0 && hitsMat4 == 0 && swaps1 > swaps0 {
		fmt.Println("\nDIAGNOSIS: hook fired during the window (swaps incremented), but our")
		fmt.Println("swap value was NEVER observed in tev_block. Shim's writes are not")
		fmt.Println("reaching this address — wrong tev_block, or stores compiled away,")
		fmt.Println("or restore happens too quickly to be observable from outside.")
	} else if hitsMat1 > 0 || hitsMat4 > 0 {
		fmt.Println("\nDIAGNOSIS: shim writes ARE committing to RAM. The visual-no-op")
		fmt.Println("must be on the GPU side — likely tev_block is consumed earlier")
		fmt.Println("in the frame than our shim runs (per-frame load cache), or the")
		fmt.Println("matpacket we hooked draws to a hidden buffer / null shape.")
	} else if swaps1 == swaps0 {
		fmt.Println("\nDIAGNOSIS: hook didn't fire during poll. Hook may not be armed,")
		fmt.Println("or mini-link wasn't being rendered. Check mp-local + face-hook-arm.")
	}

	// Don't reset face_state — leave it at the test value so subsequent
	// face-sync-watch / status calls show the post-test state. Caller
	// can run face-hook-disarm or write 0 to face_seqs[0] to clear.
}

// runFaceHookArm flips mailbox.face_hook_enable. C-side processes the
// flag once per frame in multiplayer_update — install_for_model probes
// the matpacket layout and refuses to write if the stride doesn't
// match J3DMATPACKET_SIZE (0x3C). Idempotent across calls.
func runFaceHookArm(arm bool) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	v := byte(0)
	if arm {
		v = 1
	}
	if err := d.Write(mailbox.Mailbox+mailbox.OffFaceHookEnable, []byte{v}); err != nil {
		fmt.Printf("write face_hook_enable failed: %v\n", err)
		os.Exit(1)
	}
	// Wait one frame so the C side has a chance to process the change.
	time.Sleep(50 * time.Millisecond)
	state, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookState, 1)
	mpsize, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookMpSize, 1)
	patched, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookPatchedCount, 1)
	fmt.Printf("face_hook_enable := %d\n", v)
	fmt.Printf("face_hook_state  : 0x%02X (1=installed, 0=disarmed, 0xFC=stride mismatch, 0xFE=vtable mismatch, 0xFD=null array)\n", state[0])
	fmt.Printf("face_hook_mp_size: 0x%02X (expect 0x3C)\n", mpsize[0])
	fmt.Printf("face_hook_patched: %d (this-frame writes; 0 in steady state once installed)\n", patched[0])
}

// runFaceTevHammer is the session-10 first diagnostic: prove (or rule out)
// that mini-link's pupil is rendered through Link's shared J3DTevBlock.
// Resolves Link's mat[1] and mat[4] tevBlocks the same way broadcast-pose
// does (PLAYER[0] -> +0x0328 -> +0x58+0x08 -> mat[i] -> +0x2C), snapshots
// the 4 texno bytes at +0x08, then writes a clearly-different texno value
// (default 0x0099 for both texNo[0] and texNo[1]) at 60 Hz for `secs`
// seconds. Restores originals on exit. While running, the user looks at
// THIS Dolphin's window:
//   - Both Link's eye AND mini-link's eye change -> shared tevBlock IS
//     the source for both renders; shim/face-state can drive mini-link
//     with the right bracketing.
//   - Only Link's eye changes  -> mini-link's pupil is rendered from a
//     recipe-packet-baked DL with the texture address pre-resolved.
//     Need to replace DL bytes per-slot or hook recipe draw.
//   - Neither changes  -> btp's writes are winning the race; freeze btp
//     first or pick another test.
//
// Use WW_DOLPHIN_INDEX=0 / =1 to pick which Dolphin to hammer.
func runFaceTevHammer(secs int, texHex string) {
	tex, err := strconv.ParseUint(strings.TrimPrefix(texHex, "0x"), 16, 16)
	if err != nil {
		fmt.Printf("bad texno %q: %v\n", texHex, err)
		os.Exit(1)
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Printf("link actor not resolved (linkPtr=0x%08X)\n", linkPtr)
		os.Exit(1)
	}
	dataPtr, err := d.ReadU32(linkPtr + 0x0328)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		fmt.Printf("mpCLModel not resolved (dataPtr=0x%08X)\n", dataPtr)
		os.Exit(1)
	}
	matArr, err := d.ReadU32(dataPtr + 0x58 + 0x08)
	if err != nil || matArr < 0x80000000 || matArr >= 0x81800000 {
		fmt.Printf("matArr not resolved (0x%08X)\n", matArr)
		os.Exit(1)
	}
	type matSlot struct {
		idx  int
		tev  uint32
		orig []byte
	}
	slots := []matSlot{}
	for _, idx := range []int{1, 4} {
		matPtr, err := d.ReadU32(matArr + uint32(idx)*4)
		if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
			fmt.Printf("mat[%d] ptr bad (0x%08X) — skip\n", idx, matPtr)
			continue
		}
		tev, err := d.ReadU32(matPtr + 0x2C)
		if err != nil || tev < 0x80000000 || tev >= 0x81800000 {
			fmt.Printf("mat[%d] tevBlock bad (0x%08X) — skip\n", idx, tev)
			continue
		}
		orig, err := d.Read(tev+0x08, 4)
		if err != nil || len(orig) != 4 {
			fmt.Printf("mat[%d] tev+0x08 read failed: %v\n", idx, err)
			continue
		}
		slots = append(slots, matSlot{idx: idx, tev: tev, orig: orig})
		fmt.Printf("mat[%d] tev=0x%08X  orig texno bytes = %02X %02X %02X %02X (texNo[0]=0x%02X%02X texNo[1]=0x%02X%02X)\n",
			idx, tev, orig[0], orig[1], orig[2], orig[3], orig[0], orig[1], orig[2], orig[3])
	}
	if len(slots) == 0 {
		fmt.Println("no tevBlocks resolved; aborting")
		os.Exit(1)
	}

	// Build the 4-byte payload: same u16 in texNo[0] and texNo[1].
	hi := byte((tex >> 8) & 0xFF)
	lo := byte(tex & 0xFF)
	payload := []byte{hi, lo, hi, lo}
	fmt.Printf("\nHammering tev+0x08..+0x0B = %02X %02X %02X %02X for %d s at 60 Hz on %d slot(s).\n",
		payload[0], payload[1], payload[2], payload[3], secs, len(slots))
	fmt.Println("Look at the Dolphin window: did Link's eye change? Did mini-link's eye change?")

	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	frames := 0
	for time.Now().Before(deadline) {
		for _, s := range slots {
			_ = d.Write(s.tev+0x08, payload)
		}
		frames++
		time.Sleep(16 * time.Millisecond)
	}
	// Restore.
	for _, s := range slots {
		_ = d.Write(s.tev+0x08, s.orig)
	}
	fmt.Printf("Done. Wrote %d frames; restored original texno bytes.\n", frames)
}

// runFaceSyncStatus prints the C-side face-state hook diagnostic fields.
// Quick health check after launching mp-local: face_hook_state should be
// 1 (vtable patched + matpackets repointed) and face_hook_swaps should
// be increasing if at least one slot has fresh face_seqs.
func runFaceSyncStatus() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	state, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookState, 1)
	enable, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookEnable, 1)
	mpsize, _ := d.Read(mailbox.Mailbox+mailbox.OffFaceHookMpSize, 1)
	swaps, _ := d.ReadU32(mailbox.Mailbox + mailbox.OffFaceHookSwaps)
	fmt.Printf("face_hook_enable: %d (Go arm/disarm flag)\n", enable[0])
	fmt.Printf("face_hook_state : 0x%02X (1=installed, 0=disarmed, 0xFC=stride mismatch, 0xFE=vtable mismatch, 0xFD=null array)\n", state[0])
	fmt.Printf("face_hook_mp_size: 0x%02X (expect 0x3C)\n", mpsize[0])
	fmt.Printf("face_hook_swaps : %d (saturating)\n", swaps)
	for slot := 0; slot < avatar.Cap; slot++ {
		seq, _ := d.Read(mailbox.Mailbox+avatar.OffFaceSeq(slot), 1)
		fs, _ := d.Read(mailbox.Mailbox+avatar.OffFaceState(slot), faceStateWireBytes)
		var sb byte
		if len(seq) == 1 {
			sb = seq[0]
		}
		var t1_0, t1_1, t4_0, t4_1, t14_0, t14_1, t15_0, t15_1 uint16
		if len(fs) == faceStateWireBytes {
			t1_0 = binary.BigEndian.Uint16(fs[0:2])
			t1_1 = binary.BigEndian.Uint16(fs[2:4])
			t4_0 = binary.BigEndian.Uint16(fs[4:6])
			t4_1 = binary.BigEndian.Uint16(fs[6:8])
			t14_0 = binary.BigEndian.Uint16(fs[8:10])
			t14_1 = binary.BigEndian.Uint16(fs[10:12])
			t15_0 = binary.BigEndian.Uint16(fs[12:14])
			t15_1 = binary.BigEndian.Uint16(fs[14:16])
		}
		fmt.Printf("slot %d: seq=%3d  eyes mat1=(0x%04X,0x%04X) mat4=(0x%04X,0x%04X)  mouth mat14=(0x%04X,0x%04X) mat15=(0x%04X,0x%04X)\n",
			slot, sb, t1_0, t1_1, t4_0, t4_1, t14_0, t14_1, t15_0, t15_1)
	}
}

// runFaceSyncWatch polls face_state[0..N] + face_hook_swaps every 100 ms
// for `secs` seconds, printing transitions. Confirms the receiver is
// writing fresh values and the C hook is firing on those values.
func runFaceSyncWatch(secs int) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("Watching face_state for %d seconds...\n", secs)
	type slotState struct {
		seq                        byte
		t1_0, t1_1, t4_0, t4_1     uint16
		t14_0, t14_1, t15_0, t15_1 uint16
	}
	prev := make([]slotState, avatar.Cap)
	var lastSwaps uint32
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	tick := 0
	for time.Now().Before(deadline) {
		tick++
		swaps, _ := d.ReadU32(mailbox.Mailbox + mailbox.OffFaceHookSwaps)
		if tick%10 == 0 || swaps != lastSwaps {
			delta := swaps - lastSwaps
			fmt.Printf("[t+%5dms] hook_swaps=%d (Δ=%d)\n",
				tick*100, swaps, delta)
			lastSwaps = swaps
		}
		for slot := 0; slot < avatar.Cap; slot++ {
			seq, _ := d.Read(mailbox.Mailbox+avatar.OffFaceSeq(slot), 1)
			fs, _ := d.Read(mailbox.Mailbox+avatar.OffFaceState(slot), faceStateWireBytes)
			if len(seq) != 1 || len(fs) != faceStateWireBytes {
				continue
			}
			cur := slotState{
				seq:   seq[0],
				t1_0:  binary.BigEndian.Uint16(fs[0:2]),
				t1_1:  binary.BigEndian.Uint16(fs[2:4]),
				t4_0:  binary.BigEndian.Uint16(fs[4:6]),
				t4_1:  binary.BigEndian.Uint16(fs[6:8]),
				t14_0: binary.BigEndian.Uint16(fs[8:10]),
				t14_1: binary.BigEndian.Uint16(fs[10:12]),
				t15_0: binary.BigEndian.Uint16(fs[12:14]),
				t15_1: binary.BigEndian.Uint16(fs[14:16]),
			}
			if cur != prev[slot] {
				fmt.Printf("[t+%5dms] slot %d: seq=%d eyes mat1=(%04X,%04X) mat4=(%04X,%04X) mouth mat14=(%04X,%04X) mat15=(%04X,%04X)\n",
					tick*100, slot, cur.seq, cur.t1_0, cur.t1_1, cur.t4_0, cur.t4_1, cur.t14_0, cur.t14_1, cur.t15_0, cur.t15_1)
				prev[slot] = cur
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// runFaceSyncFake writes a hardcoded 8-byte face state into mailbox.face_state[slot]
// and bumps face_seqs[slot]. Lets you test the C hook in isolation without
// needing a remote sender — useful for "did mini-link just visibly blink?".
// hex example: "0006002700060027" forces both eyes to texno=(0x0006, 0x0027)
// and leaves mouth zero-padded. Pass 32 hex chars (16 B) to also drive
// mouth state: "<8B eyes><8B mouth>" where mouth = mat14_t0 mat14_t1
// mat15_t0 mat15_t1 (each u16 BE).
func runFaceSyncFake(slot int, hexStr string) {
	if slot < 0 || slot >= avatar.Cap {
		fmt.Printf("slot must be 0..%d\n", avatar.Cap-1)
		os.Exit(1)
	}
	buf := parseFaceStateHex(hexStr)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if err := d.Write(mailbox.Mailbox+avatar.OffFaceState(slot), buf); err != nil {
		fmt.Printf("write face_state failed: %v\n", err)
		os.Exit(1)
	}
	next, err := avatar.BumpSeq(d, mailbox.Mailbox+avatar.OffFaceSeq(slot))
	if err != nil {
		fmt.Printf("write face_seq failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("slot %d face_state := eyes %02X%02X%02X%02X %02X%02X%02X%02X  mouth %02X%02X%02X%02X %02X%02X%02X%02X  seq -> %d\n",
		slot, buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7],
		buf[8], buf[9], buf[10], buf[11], buf[12], buf[13], buf[14], buf[15], next)
}

// parseFaceStateHex parses 16 or 32 hex chars (8 or 16 B) into a 16-byte
// face_state buffer. 8-B input = eyes-only, mouth zero-padded. Exits on
// parse error.
func parseFaceStateHex(hexStr string) []byte {
	hexStr = strings.TrimPrefix(hexStr, "0x")
	if len(hexStr) != 16 && len(hexStr) != 32 {
		fmt.Printf("hex must be 16 chars (8 B eyes-only) or 32 chars (16 B eyes+mouth), got %d\n", len(hexStr))
		os.Exit(1)
	}
	buf := make([]byte, 16)
	n := len(hexStr) / 2
	for i := 0; i < n; i++ {
		v, err := strconv.ParseUint(hexStr[i*2:i*2+2], 16, 8)
		if err != nil {
			fmt.Printf("bad hex at byte %d: %v\n", i, err)
			os.Exit(1)
		}
		buf[i] = byte(v)
	}
	return buf
}

// runFaceMatList enumerates Link's J3DModelData material table and prints
// each material's index, address, tev_block address, and current texno
// bytes. Used to find candidate material indices before probing with
// face-mat-hammer (e.g. mouth, eye-white, etc.).
func runFaceMatList() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Printf("link actor not resolved (linkPtr=0x%08X)\n", linkPtr)
		os.Exit(1)
	}
	dataPtr, err := d.ReadU32(linkPtr + 0x0328)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		fmt.Printf("mpCLModelData not resolved (dataPtr=0x%08X)\n", dataPtr)
		os.Exit(1)
	}
	// J3DMaterialTable layout: +0x04 is u16 mMaterialNum,
	// +0x08 is J3DMaterial** mpMaterials.
	matCountBytes, err := d.Read(dataPtr+0x58+0x04, 2)
	if err != nil || len(matCountBytes) != 2 {
		fmt.Printf("read matCount: %v\n", err)
		os.Exit(1)
	}
	matCount := int(matCountBytes[0])<<8 | int(matCountBytes[1])
	matArr, err := d.ReadU32(dataPtr + 0x58 + 0x08)
	if err != nil || matArr < 0x80000000 || matArr >= 0x81800000 {
		fmt.Printf("matArr not resolved (0x%08X)\n", matArr)
		os.Exit(1)
	}
	fmt.Printf("Link J3DModelData = 0x%08X  matCount=%d  matArr=0x%08X\n", dataPtr, matCount, matArr)
	for i := 0; i < matCount; i++ {
		matPtr, err := d.ReadU32(matArr + uint32(i)*4)
		if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
			fmt.Printf("  mat[%2d] = 0x%08X (bad)\n", i, matPtr)
			continue
		}
		tev, err := d.ReadU32(matPtr + 0x2C)
		if err != nil || tev < 0x80000000 || tev >= 0x81800000 {
			fmt.Printf("  mat[%2d] = 0x%08X  tev=0x%08X (bad)\n", i, matPtr, tev)
			continue
		}
		texno, err := d.Read(tev+0x08, 4)
		if err != nil || len(texno) != 4 {
			fmt.Printf("  mat[%2d] = 0x%08X  tev=0x%08X  texno=read-fail\n", i, matPtr, tev)
			continue
		}
		fmt.Printf("  mat[%2d] = 0x%08X  tev=0x%08X  texno[0..1]=%02X%02X %02X%02X\n",
			i, matPtr, tev, texno[0], texno[1], texno[2], texno[3])
	}
}

// runFaceMatHammer tight-loops writes a fake u16 (repeated as both texno[0]
// and texno[1]) to mat[idx]'s J3DTevBlock+0x08. Used to probe which
// material is what (e.g. find the mouth by hammering each candidate
// index until Link's mouth visibly changes). On stop (Ctrl+C or secs
// elapsed), restores the original 4 bytes.
func runFaceMatHammer(idx int, texHex string, secs int) {
	tex, err := strconv.ParseUint(texHex, 16, 16)
	if err != nil {
		fmt.Printf("bad texno %q: %v\n", texHex, err)
		os.Exit(1)
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Printf("link actor not resolved (linkPtr=0x%08X)\n", linkPtr)
		os.Exit(1)
	}
	dataPtr, err := d.ReadU32(linkPtr + 0x0328)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		fmt.Printf("mpCLModelData not resolved (dataPtr=0x%08X)\n", dataPtr)
		os.Exit(1)
	}
	matArr, err := d.ReadU32(dataPtr + 0x58 + 0x08)
	if err != nil || matArr < 0x80000000 || matArr >= 0x81800000 {
		fmt.Printf("matArr not resolved (0x%08X)\n", matArr)
		os.Exit(1)
	}
	matPtr, err := d.ReadU32(matArr + uint32(idx)*4)
	if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
		fmt.Printf("mat[%d] ptr bad (0x%08X)\n", idx, matPtr)
		os.Exit(1)
	}
	tev, err := d.ReadU32(matPtr + 0x2C)
	if err != nil || tev < 0x80000000 || tev >= 0x81800000 {
		fmt.Printf("mat[%d] tevBlock bad (0x%08X)\n", idx, tev)
		os.Exit(1)
	}
	orig, err := d.Read(tev+0x08, 4)
	if err != nil || len(orig) != 4 {
		fmt.Printf("mat[%d] tev+0x08 read failed: %v\n", idx, err)
		os.Exit(1)
	}
	hi := byte((tex >> 8) & 0xFF)
	lo := byte(tex & 0xFF)
	payload := []byte{hi, lo, hi, lo}
	fmt.Printf("mat[%d] tev=0x%08X  orig=%02X%02X %02X%02X  hammering -> %02X%02X %02X%02X (Ctrl+C to stop)\n",
		idx, tev, orig[0], orig[1], orig[2], orig[3], payload[0], payload[1], payload[2], payload[3])

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt)
	defer signal.Stop(stopCh)
	var deadline time.Time
	if secs > 0 {
		deadline = time.Now().Add(time.Duration(secs) * time.Second)
	}
	tick := time.NewTicker(4 * time.Millisecond)
	defer tick.Stop()
	writes := 0
	addr := tev + 0x08
	defer func() {
		d.Write(addr, orig)
		fmt.Printf("\nRestored mat[%d] tev_block.texno to %02X%02X %02X%02X (%d writes).\n",
			idx, orig[0], orig[1], orig[2], orig[3], writes)
	}()
	for {
		select {
		case <-stopCh:
			return
		case <-tick.C:
			d.Write(addr, payload)
			writes++
			if !deadline.IsZero() && time.Now().After(deadline) {
				return
			}
		}
	}
}

// runFaceSyncFakeLoop hammers face_state[slot] at hzTarget Hz while
// mp-local runs in parallel. puppet-sync is also writing face_state[slot]
// at game-tick rate (~60 Hz); to dominate that race we need >120 Hz so
// our value is what the bracket reads at most mini-link bakes. Open the
// Dolphin handle once and write in a tight loop — vs the 49 ms-per-call
// overhead of repeated face-sync-fake invocations.
func runFaceSyncFakeLoop(slot int, hexStr string, secs int) {
	if slot < 0 || slot >= avatar.Cap {
		fmt.Printf("slot must be 0..%d\n", avatar.Cap-1)
		os.Exit(1)
	}
	buf := parseFaceStateHex(hexStr)
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt)
	defer signal.Stop(stopCh)

	var deadline time.Time
	if secs > 0 {
		deadline = time.Now().Add(time.Duration(secs) * time.Second)
	}
	fmt.Printf("Hammering slot %d face_state := %s at ~250Hz (Ctrl+C to stop)...\n", slot, hexStr)

	stateAddr := mailbox.Mailbox + avatar.OffFaceState(slot)
	seqAddr := mailbox.Mailbox + avatar.OffFaceSeq(slot)
	tick := time.NewTicker(4 * time.Millisecond)
	defer tick.Stop()
	writes := 0
	var seq byte = 1
	for {
		select {
		case <-stopCh:
			fmt.Printf("\nStopped after %d writes.\n", writes)
			return
		case <-tick.C:
			d.Write(stateAddr, buf)
			seq = avatar.NextSeq(seq)
			d.Write(seqAddr, []byte{seq})
			writes++
			if !deadline.IsZero() && time.Now().After(deadline) {
				fmt.Printf("\nDone (%d writes).\n", writes)
				return
			}
		}
	}
}

// runEyeFixChain dumps the OPA P0 draw buffer's bucket-0 chain so we can
// see in what order packets render, whether the chain has cycles, and
// where Link #1's face/hair matpackets sit. Reads mpOpaListP0 from
// 0x803CA92C, then J3DDrawBuffer.mpBuf[0] (offset 0x00 → first u32),
// then walks via mpNextPacket at packet offset 0x04. Stops at NULL or
// after 200 entries.
func runEyeFixChain() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	listAddrEnv := os.Getenv("WW_DRAWLIST_PTR")
	listAddr := uint32(0x803CA92C)
	if listAddrEnv != "" {
		v, _ := strconv.ParseUint(strings.TrimPrefix(listAddrEnv, "0x"), 16, 32)
		listAddr = uint32(v)
	}
	bufPtr, err := d.ReadU32(listAddr)
	if err != nil || bufPtr == 0 {
		fmt.Printf("buf ptr NULL @ 0x%08X (got 0x%08X, err=%v)\n", listAddr, bufPtr, err)
		os.Exit(1)
	}
	fmt.Printf("J3DDrawBuffer @ 0x%08X (from drawlist 0x%08X)\n", bufPtr, listAddr)

	mpBuf, err := d.ReadU32(bufPtr) // mpBuf field at offset 0
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("mpBuf array @ 0x%08X\n", mpBuf)

	bucket0, err := d.ReadU32(mpBuf) // mpBuf[0]
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("bucket[0] head = 0x%08X\n", bucket0)

	// Read face + hair shape addresses via Link's model data → joint chain.
	var faceShape, hairShape uint32
	if md, err := linkModelData(d); err == nil && md != 0 {
		if jointArr, err := d.ReadU32(md + 0x2C); err == nil && jointArr != 0 {
			if linkRoot, err := d.ReadU32(jointArr); err == nil && linkRoot != 0 {
				if mtl, err := d.ReadU32(linkRoot + 0x60); err == nil && mtl != 0 {
					for i := 0; i < 16 && mtl != 0; i++ {
						s, _ := d.ReadU32(mtl + 0x08)
						if i == 2 {
							faceShape = s
						} else if i == 5 {
							hairShape = s
						}
						mtl, _ = d.ReadU32(mtl + 0x04)
					}
				}
			}
		}
	}

	seen := map[uint32]int{}
	cur := bucket0
	for i := 0; cur != 0 && i < 200; i++ {
		if prev, ok := seen[cur]; ok {
			fmt.Printf("[%3d] 0x%08X  *** CYCLE: same as [%d] ***\n", i, cur, prev)
			break
		}
		seen[cur] = i
		// Read mpShapePacket (matpacket+0x2C) → mpShape (shapepacket+0x24)
		shapePkt, _ := d.ReadU32(cur + 0x2C)
		var shape uint32
		if shapePkt != 0 {
			shape, _ = d.ReadU32(shapePkt + 0x24)
		}
		marker := ""
		if shape != 0 && shape == faceShape {
			marker = "  (face)"
		} else if shape != 0 && shape == hairShape {
			marker = "  (hair)"
		}
		fmt.Printf("[%3d] 0x%08X  shape=0x%08X%s\n", i, cur, shape, marker)
		next, err := d.ReadU32(cur + 0x04) // mpNextPacket
		if err != nil {
			fmt.Printf("        read next failed: %v\n", err)
			break
		}
		cur = next
	}
	if cur == 0 {
		fmt.Println("--- chain terminates at NULL ---")
	}
}

// runPPCDisasm reads num*4 bytes from the given absolute address and prints
// each word as a partially-decoded PPC instruction. Decodes the most common
// memory-access opcodes (lwz/stw/stwu/lhz/lbz/stb), float loads (lfs/lfd),
// integer/float compares (cmpwi/cmplwi/cmpw/cmplw/fcmpu), branches
// (b/bl/bla and conditional bc with beq/bne/blt/bge/bgt/ble mnemonics).
// That's enough to trace the gating conditional structure inside daPy_lk_c::draw
// (which has FP comparisons against -85.0f and integer-flag tests).
//
// Source: WW_DOL_PATH env var picks an offline DOL file (no Dolphin needed —
// preferred for static-code disassembly like daPy_lk_c::draw at 0x80107308).
// If unset, reads from the live Dolphin process.
func runPPCDisasm(addr uint32, num int) {
	var buf []byte
	if dolPath := os.Getenv("WW_DOL_PATH"); dolPath != "" {
		var err error
		buf, err = readDOLBytes(dolPath, addr, num*4)
		if err != nil {
			fmt.Printf("DOL read failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		d, err := findDolphin("GZLE01")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer d.Close()
		buf, err = d.Read(addr, num*4)
		if err != nil {
			fmt.Printf("read failed: %v\n", err)
			os.Exit(1)
		}
	}
	for i := 0; i+4 <= len(buf); i += 4 {
		insn := binary.BigEndian.Uint32(buf[i : i+4])
		pc := addr + uint32(i)
		fmt.Printf("  0x%08X: %08X    %s\n", pc, insn, decodePPC(insn, pc))
	}
}

// decodePPC returns a partial mnemonic for a PPC instruction at the given PC.
// Covers what we need to read daPy_lk_c::draw and similar J3D internals;
// unknown ops fall back to "op=NN".
func decodePPC(insn, pc uint32) string {
	op := (insn >> 26) & 0x3F
	rD := (insn >> 21) & 0x1F
	rA := (insn >> 16) & 0x1F
	d16 := int32(int16(insn & 0xFFFF))
	switch op {
	case 32:
		return fmt.Sprintf("lwz   r%d, %d(r%d)", rD, d16, rA)
	case 33:
		return fmt.Sprintf("lwzu  r%d, %d(r%d)", rD, d16, rA)
	case 34:
		return fmt.Sprintf("lbz   r%d, %d(r%d)", rD, d16, rA)
	case 36:
		return fmt.Sprintf("stw   r%d, %d(r%d)", rD, d16, rA)
	case 37:
		return fmt.Sprintf("stwu  r%d, %d(r%d)", rD, d16, rA)
	case 38:
		return fmt.Sprintf("stb   r%d, %d(r%d)", rD, d16, rA)
	case 40:
		return fmt.Sprintf("lhz   r%d, %d(r%d)", rD, d16, rA)
	case 44:
		return fmt.Sprintf("sth   r%d, %d(r%d)", rD, d16, rA)
	case 48:
		return fmt.Sprintf("lfs   f%d, %d(r%d)", rD, d16, rA)
	case 50:
		return fmt.Sprintf("lfd   f%d, %d(r%d)", rD, d16, rA)
	case 52:
		return fmt.Sprintf("stfs  f%d, %d(r%d)", rD, d16, rA)
	case 54:
		return fmt.Sprintf("stfd  f%d, %d(r%d)", rD, d16, rA)
	case 14:
		imm := int32(int16(insn & 0xFFFF))
		if rA == 0 {
			return fmt.Sprintf("li    r%d, %d", rD, imm)
		}
		return fmt.Sprintf("addi  r%d, r%d, %d", rD, rA, imm)
	case 15:
		imm := int32(int16(insn & 0xFFFF))
		return fmt.Sprintf("addis r%d, r%d, %d   (=0x%04X)", rD, rA, imm, uint16(imm))
	case 10:
		// cmpli (cmplwi when L=0): unsigned compare with imm.
		crfD := (insn >> 23) & 0x7
		uimm := uint32(insn & 0xFFFF)
		return fmt.Sprintf("cmplwi cr%d, r%d, 0x%X", crfD, rA, uimm)
	case 11:
		// cmpi (cmpwi when L=0): signed compare with imm.
		crfD := (insn >> 23) & 0x7
		return fmt.Sprintf("cmpwi cr%d, r%d, %d", crfD, rA, d16)
	case 16:
		// bc — conditional branch. BO field = rD, BI field = rA, displacement
		// in bottom 16 bits (sign-extended, low 2 bits 0).
		bo := rD
		bi := rA
		bd := int32(int16(insn & 0xFFFC)) // already low-2-zero by encoding
		absBit := insn & 0x2
		lkBit := insn & 0x1
		target := uint32(int32(pc) + bd)
		if absBit != 0 {
			target = uint32(bd)
		}
		// Branch-if-condition: BO bit 0x10 means "decrement CTR not used"
		// In typical compiler output: BO=12 → branch if CR[BI]==1; BO=4 → branch if CR[BI]==0.
		crf := bi >> 2
		bit := bi & 0x3 // 0=lt, 1=gt, 2=eq, 3=so
		bitName := []string{"lt", "gt", "eq", "so"}[bit]
		var mnem string
		switch bo {
		case 12: // branch-if-true
			switch bit {
			case 0:
				mnem = "blt"
			case 1:
				mnem = "bgt"
			case 2:
				mnem = "beq"
			case 3:
				mnem = "bso"
			}
		case 4: // branch-if-false
			switch bit {
			case 0:
				mnem = "bge"
			case 1:
				mnem = "ble"
			case 2:
				mnem = "bne"
			case 3:
				mnem = "bns"
			}
		default:
			mnem = fmt.Sprintf("bc(BO=%d,BI=%d:%s)", bo, bi, bitName)
		}
		if lkBit != 0 {
			mnem += "l"
		}
		if absBit != 0 {
			mnem += "a"
		}
		return fmt.Sprintf("%s   cr%d, 0x%08X", mnem, crf, target)
	case 18:
		// b/bl/bla
		li := int32(insn&0x03FFFFFC) << 6 >> 6
		absBit := insn & 0x2
		lkBit := insn & 0x1
		target := uint32(int32(pc) + li)
		if absBit != 0 {
			target = uint32(li)
		}
		s := "b"
		if lkBit != 0 {
			s = "bl"
		}
		if absBit != 0 {
			s += "a"
		}
		return fmt.Sprintf("%s    0x%08X", s, target)
	case 19:
		sub := (insn >> 1) & 0x3FF
		switch sub {
		case 16:
			return "blr   "
		case 528:
			return "bctr  "
		case 257:
			return "crand "
		case 449:
			return "cror  "
		default:
			return fmt.Sprintf("ctrl-19 sub=%d", sub)
		}
	case 21:
		// rlwinm — common bitfield extract; show MB/ME for clarity.
		sh := (insn >> 11) & 0x1F
		mb := (insn >> 6) & 0x1F
		me := (insn >> 1) & 0x1F
		return fmt.Sprintf("rlwinm r%d, r%d, %d, %d, %d", rA, rD, sh, mb, me)
	case 24:
		return fmt.Sprintf("ori   r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 25:
		return fmt.Sprintf("oris  r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 28:
		return fmt.Sprintf("andi. r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 29:
		return fmt.Sprintf("andis. r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 31:
		sub := (insn >> 1) & 0x3FF
		rB := (insn >> 11) & 0x1F
		switch sub {
		case 0:
			crfD := (insn >> 23) & 0x7
			return fmt.Sprintf("cmpw  cr%d, r%d, r%d", crfD, rA, rB)
		case 32:
			crfD := (insn >> 23) & 0x7
			return fmt.Sprintf("cmplw cr%d, r%d, r%d", crfD, rA, rB)
		case 23:
			return fmt.Sprintf("lwzx  r%d, r%d, r%d", rD, rA, rB)
		case 151:
			return fmt.Sprintf("stwx  r%d, r%d, r%d", rD, rA, rB)
		case 467:
			return "mtspr "
		case 339:
			return "mfspr "
		case 444:
			return fmt.Sprintf("or    r%d, r%d, r%d", rA, rD, rB)
		case 28:
			return fmt.Sprintf("and   r%d, r%d, r%d", rA, rD, rB)
		case 24:
			return fmt.Sprintf("slw   r%d, r%d, r%d", rA, rD, rB)
		case 536:
			return fmt.Sprintf("srw   r%d, r%d, r%d", rA, rD, rB)
		case 266:
			return fmt.Sprintf("add   r%d, r%d, r%d", rD, rA, rB)
		case 40:
			return fmt.Sprintf("subf  r%d, r%d, r%d", rD, rA, rB)
		default:
			return fmt.Sprintf("ext31 sub=%d  rD=%d rA=%d rB=%d", sub, rD, rA, rB)
		}
	case 63:
		// FP extended ops. fcmpu = sub 0, fcmpo = sub 32, fmr = sub 72, fneg = sub 40.
		sub := (insn >> 1) & 0x3FF
		fA := rA
		fB := (insn >> 11) & 0x1F
		crfD := (insn >> 23) & 0x7
		switch sub {
		case 0:
			return fmt.Sprintf("fcmpu cr%d, f%d, f%d", crfD, fA, fB)
		case 32:
			return fmt.Sprintf("fcmpo cr%d, f%d, f%d", crfD, fA, fB)
		case 72:
			return fmt.Sprintf("fmr   f%d, f%d", rD, fB)
		case 40:
			return fmt.Sprintf("fneg  f%d, f%d", rD, fB)
		default:
			return fmt.Sprintf("fp63  sub=%d f%d,f%d", sub, fA, fB)
		}
	default:
		return fmt.Sprintf("op=%d (0x%02X)", op, op)
	}
}

// readDOLBytes opens a Wii/GC DOL file and returns `n` bytes starting at the
// given virtual load address (e.g. 0x80107308 for daPy_lk_c::draw). The DOL
// file format places its 0x100-byte header at file offset 0:
//
//	+0x00..+0x47 — file offsets[0..17]   (7 text + 11 data)
//	+0x48..+0x8F — load addrs[0..17]
//	+0x90..+0xD7 — section sizes[0..17]
//
// We linearly find the section containing addr and read from
// fileOffset[i] + (addr - loadAddr[i]).
func readDOLBytes(path string, addr uint32, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hdr [0x100]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return nil, err
	}
	for i := 0; i < 18; i++ {
		off := binary.BigEndian.Uint32(hdr[i*4:])
		la := binary.BigEndian.Uint32(hdr[0x48+i*4:])
		sz := binary.BigEndian.Uint32(hdr[0x90+i*4:])
		if sz == 0 || la == 0 {
			continue
		}
		if addr >= la && addr+uint32(n) <= la+sz {
			out := make([]byte, n)
			if _, err := f.ReadAt(out, int64(off+(addr-la))); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("addr 0x%08X (n=%d) not contained in any DOL section of %s", addr, n, path)
}

// runJ3DSysProbe dumps the first 0x128 bytes of the j3dSys global at
// 0x803EDA58 and, for every u32 in there, reports whether it matches one
// of the three known drawbuffer pointers (opa_p0/opa_p1/xlu_p1, looked up
// via the dDlst_list_c fields at 0x803CA92C/0x930/0x934). The matching
// offset is the real `mDrawBuffer` slot. wwapi.h currently assumes
// J3D_SYS_DRAWBUFFER_OPA_OFFSET=0x48 / XLU_OFFSET=0x4C, but evidence from
// eye-fix-find-shape (mini-link body landing in opa_p0 instead of opa_p1
// after a "swap to P1" write) suggests those offsets are wrong.
func runJ3DSysProbe() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	const j3dSysAddr = uint32(0x803EDA58)
	const j3dSysSize = 0x128

	// Resolve the three known drawbuffer pointers.
	type dl struct {
		name   string
		bufPtr uint32
	}
	var dls []dl
	for _, x := range []struct {
		name string
		addr uint32
	}{
		{"opa_p0", 0x803CA92C},
		{"opa_p1", 0x803CA930},
		{"xlu_p1", 0x803CA934},
	} {
		v, err := d.ReadU32(x.addr)
		if err == nil && v != 0 {
			dls = append(dls, dl{x.name, v})
		}
	}
	fmt.Println("Known drawbuffers:")
	for _, x := range dls {
		fmt.Printf("  %s = 0x%08X\n", x.name, x.bufPtr)
	}
	fmt.Println()

	buf, err := d.Read(j3dSysAddr, j3dSysSize)
	if err != nil {
		fmt.Printf("read failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("j3dSys @ 0x%08X (0x%X bytes):\n", j3dSysAddr, j3dSysSize)

	for off := 0; off < len(buf); off += 16 {
		line := fmt.Sprintf("  +0x%03X:", off)
		for k := 0; k < 16 && off+k < len(buf); k += 4 {
			w := binary.BigEndian.Uint32(buf[off+k : off+k+4])
			line += fmt.Sprintf(" %08X", w)
		}
		fmt.Println(line)
	}

	fmt.Println()
	fmt.Println("Drawbuffer-pointer matches:")
	hits := 0
	for off := 0; off+4 <= len(buf); off += 4 {
		v := binary.BigEndian.Uint32(buf[off : off+4])
		for _, x := range dls {
			if v == x.bufPtr {
				fmt.Printf("  +0x%03X = 0x%08X (%s)\n", off, v, x.name)
				hits++
			}
		}
	}
	if hits == 0 {
		fmt.Println("  (none — drawbuffer ptrs not currently held in j3dSys, sample a busier frame)")
	}
}

// runEyeFixFindShape exhaustively searches every bucket of opa_p0, opa_p1,
// xlu_p1 (the three known dDlst_list_c drawlists) for the face (mtl 2) and
// hair (mtl 5) shape pointer values. For each non-empty bucket it walks the
// chain via mpNextPacket@+0x04, and for each packet scans the first 0x40
// bytes for the target shape addresses. Prints which drawlist + bucket index
// + packet address + byte-offset-within-packet contains face/hair, plus a
// hex dump of every matching packet so we can confirm matpacket layout.
//
// Also dumps each J3DDrawBuffer's first 0x40 bytes so the layout (mpBuf
// offset, NumBuckets) can be cross-checked. Honors WW_NUM_BUCKETS to
// override the bucket count to walk (default 256).
//
// Background: eye-fix-chain only walks bucket[0] of opa_p0 and assumes
// matpacket+0x2C → shapepacket+0x24 → shape. That dump didn't show face
// or hair even at baseline (where they visibly render), so either the
// offsets are wrong or face/hair are in different buckets/lists. This
// command bypasses both assumptions by scanning packet bytes directly.
func runEyeFixFindShape() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Resolve face + hair shape addrs via Link's model data.
	modelData, err := linkModelData(d)
	if err != nil || modelData == 0 {
		fmt.Printf("Link's mpCLModelData not available (got 0x%08X, err=%v)\n", modelData, err)
		os.Exit(1)
	}
	jointArr, err := d.ReadU32(modelData + 0x2C)
	if err != nil || jointArr == 0 {
		fmt.Printf("joint array NULL: %v\n", err)
		os.Exit(1)
	}
	linkRoot, err := d.ReadU32(jointArr)
	if err != nil || linkRoot == 0 {
		fmt.Printf("link_root NULL: %v\n", err)
		os.Exit(1)
	}
	mtl, err := d.ReadU32(linkRoot + 0x60)
	if err != nil || mtl == 0 {
		fmt.Printf("link_root mMesh NULL: %v\n", err)
		os.Exit(1)
	}
	var faceShape, hairShape uint32
	for i := 0; i < 16 && mtl != 0; i++ {
		s, _ := d.ReadU32(mtl + 0x08)
		if i == 2 {
			faceShape = s
		} else if i == 5 {
			hairShape = s
		}
		mtl, _ = d.ReadU32(mtl + 0x04)
	}
	fmt.Printf("Targets: faceShape=0x%08X  hairShape=0x%08X\n\n", faceShape, hairShape)

	numBuckets := 256
	if env := os.Getenv("WW_NUM_BUCKETS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			numBuckets = v
		}
	}

	// Sweep the dComIfGd_drawlist region for every u32 that looks like a
	// J3DDrawBuffer pointer. We know opa_p0 / opa_p1 / xlu_p1 live at
	// 0x803CA92C / 0x930 / 0x934, and the buffers themselves sit in a heap
	// allocation around 0x8068B8E0 (the live snapshot from this build), so
	// scan ±0x100 around the 3 known drawlist fields and accept any u32
	// pointer that falls inside MEM1 (0x80000000..0x817FFFFF).
	type drawlist struct {
		name    string
		fieldAt uint32
		bufPtr  uint32
	}
	knownNames := map[uint32]string{
		0x803CA92C: "opa_p0",
		0x803CA930: "opa_p1",
		0x803CA934: "xlu_p1",
	}
	scanLo, scanHi := uint32(0x803CA820), uint32(0x803CAA20)
	scanBuf, err := d.Read(scanLo, int(scanHi-scanLo))
	if err != nil {
		fmt.Printf("drawlist scan failed: %v\n", err)
		os.Exit(1)
	}
	var lists []drawlist
	for o := 0; o+4 <= len(scanBuf); o += 4 {
		v := binary.BigEndian.Uint32(scanBuf[o : o+4])
		if v < 0x80000000 || v >= 0x81800000 {
			continue
		}
		fieldAt := scanLo + uint32(o)
		name, ok := knownNames[fieldAt]
		if !ok {
			name = fmt.Sprintf("dl@0x%08X", fieldAt)
		}
		lists = append(lists, drawlist{name, fieldAt, v})
	}
	fmt.Printf("Scanned 0x%08X..0x%08X for drawlist fields: found %d MEM1-pointer candidates\n",
		scanLo, scanHi, len(lists))
	fmt.Println()

	// Dedupe: multiple drawlist fields can point to the same J3DDrawBuffer
	// (or the same memory region). Walk each unique bufPtr exactly once.
	seenBuf := map[uint32]bool{}
	for _, dl := range lists {
		if seenBuf[dl.bufPtr] {
			continue
		}
		seenBuf[dl.bufPtr] = true

		fmt.Printf("=== %s (field @ 0x%08X → bufPtr 0x%08X) ===\n", dl.name, dl.fieldAt, dl.bufPtr)

		hdr, err := d.Read(dl.bufPtr, 0x40)
		if err != nil {
			fmt.Printf("  header read failed: %v\n\n", err)
			continue
		}
		fmt.Printf("  header[0x00..0x40]:\n")
		for off := 0; off < len(hdr); off += 16 {
			line := fmt.Sprintf("    +0x%02X: ", off)
			for k := 0; k < 16 && off+k < len(hdr); k += 4 {
				w := binary.BigEndian.Uint32(hdr[off+k : off+k+4])
				line += fmt.Sprintf(" %08X", w)
			}
			fmt.Println(line)
		}

		mpBuf := binary.BigEndian.Uint32(hdr[0:4])
		mNumBuckets := binary.BigEndian.Uint32(hdr[4:8])
		// Reject anything that doesn't look like a real J3DDrawBuffer.
		if mpBuf < 0x80000000 || mpBuf >= 0x81800000 || mNumBuckets == 0 || mNumBuckets > 4096 {
			fmt.Printf("  not a J3DDrawBuffer (mpBuf=0x%08X, mNumBuckets=%d) — skip\n\n",
				mpBuf, mNumBuckets)
			continue
		}
		// Allow override but cap to mNumBuckets.
		walkN := int(mNumBuckets)
		if numBuckets > 0 && numBuckets < walkN {
			walkN = numBuckets
		}
		fmt.Printf("  mpBuf=0x%08X mNumBuckets=%d — walking %d buckets\n", mpBuf, mNumBuckets, walkN)

		bucketBytes, err := d.Read(mpBuf, walkN*4)
		if err != nil {
			fmt.Printf("  bucket array read failed: %v\n\n", err)
			continue
		}

		nonEmpty, totalPkts, hits := 0, 0, 0
		// Wider scan window: J3DShapePacket constructor is 0x60 bytes per
		// the symbol map, but the field layout is unknown — read 0x100 to
		// cover any plausible mpShape offset.
		const pktBytes = 0x100
		dumpAll := os.Getenv("WW_DUMP_ALL") != ""
		for b := 0; b < walkN; b++ {
			head := binary.BigEndian.Uint32(bucketBytes[b*4 : b*4+4])
			if head == 0 {
				continue
			}
			nonEmpty++
			seen := map[uint32]bool{}
			cur := head
			depth := 0
			for cur != 0 && depth < 1000 {
				if seen[cur] {
					fmt.Printf("  bucket[%d] CYCLE detected at 0x%08X (depth %d)\n", b, cur, depth)
					break
				}
				seen[cur] = true

				pkt, err := d.Read(cur, pktBytes)
				if err != nil {
					fmt.Printf("  bucket[%d] read failed @ 0x%08X: %v\n", b, cur, err)
					break
				}
				totalPkts++

				if dumpAll {
					vtable := binary.BigEndian.Uint32(pkt[0:4])
					next := binary.BigEndian.Uint32(pkt[4:8])
					f24 := binary.BigEndian.Uint32(pkt[0x24:0x28])
					f28 := binary.BigEndian.Uint32(pkt[0x28:0x2C])
					f2C := binary.BigEndian.Uint32(pkt[0x2C:0x30])
					fmt.Printf("    [%2d] pkt=0x%08X  vtbl=0x%08X next=0x%08X  +24=0x%08X +28=0x%08X +2C=0x%08X\n",
						depth, cur, vtable, next, f24, f28, f2C)
				}

				match := func(v uint32) (string, bool) {
					if faceShape != 0 && v == faceShape {
						return "FACE", true
					}
					if hairShape != 0 && v == hairShape {
						return "HAIR", true
					}
					return "", false
				}

				dumpPkt := func(addr uint32, raw []byte) {
					for off := 0; off < len(raw); off += 16 {
						line := fmt.Sprintf("        +0x%02X: ", off)
						for k := 0; k < 16 && off+k < len(raw); k += 4 {
							w := binary.BigEndian.Uint32(raw[off+k : off+k+4])
							line += fmt.Sprintf(" %08X", w)
						}
						fmt.Println(line)
					}
				}

				directHit := false
				for o := 0; o+4 <= len(pkt); o += 4 {
					v := binary.BigEndian.Uint32(pkt[o : o+4])
					if label, ok := match(v); ok {
						fmt.Printf("  *** %s direct in %s bucket[%d] packet 0x%08X at +0x%02X ***\n",
							label, dl.name, b, cur, o)
						dumpPkt(cur, pkt)
						hits++
						directHit = true
						break
					}
				}
				if directHit {
					cur = binary.BigEndian.Uint32(pkt[4:8])
					depth++
					continue
				}

				// One-level dereference: if any heap pointer in the packet's
				// first 0x40 bytes (where mpShapePacket-style fields live)
				// points at an object whose first 0x40 bytes contain face or
				// hair, report it. That catches the matpacket → shapepacket
				// → mpShape indirection where the matpacket itself doesn't
				// store the shape pointer.
				for o := 0; o+4 <= 0x40; o += 4 {
					v := binary.BigEndian.Uint32(pkt[o : o+4])
					if v < 0x80000000 || v >= 0x81800000 {
						continue
					}
					if v == cur || (cur != 0 && v == binary.BigEndian.Uint32(pkt[4:8])) {
						continue
					}
					sub, err := d.Read(v, 0x40)
					if err != nil {
						continue
					}
					for so := 0; so+4 <= len(sub); so += 4 {
						sv := binary.BigEndian.Uint32(sub[so : so+4])
						if label, ok := match(sv); ok {
							fmt.Printf("  *** %s indirect in %s bucket[%d]: packet 0x%08X +0x%02X → 0x%08X +0x%02X ***\n",
								label, dl.name, b, cur, o, v, so)
							fmt.Printf("      packet @ 0x%08X:\n", cur)
							dumpPkt(cur, pkt[:0x80])
							fmt.Printf("      derefd  @ 0x%08X:\n", v)
							dumpPkt(v, sub)
							hits++
							break
						}
					}
				}

				cur = binary.BigEndian.Uint32(pkt[4:8])
				depth++
			}
		}
		fmt.Printf("  summary: %d non-empty buckets, %d packets walked, %d face/hair hits\n\n",
			nonEmpty, totalPkts, hits)
	}
}

// runEyeFixProbe walks the live J3D pointer chain from Link's mpCLModelData
// to read link_root.mMesh[i].mShape.mFlags for every material on link_root.
// Prints whether the HIDE bit is set per material. Used to figure out
// whether our run_eye_fix is leaving face/hair (mtl indices 2 and 5) with
// the J3DShpFlag_Hide bit set, vs the visibility loss being caused by GX
// state pollution that doesn't touch mFlags.
func runEyeFixProbe() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	modelData, err := linkModelData(d)
	if err != nil || modelData == 0 {
		fmt.Printf("Link's mpCLModelData not available (got 0x%08X, err=%v)\n", modelData, err)
		os.Exit(1)
	}
	fmt.Printf("link J3DModelData @ 0x%08X\n", modelData)

	jointArr, err := d.ReadU32(modelData + 0x2C) // mJointNodePointer
	if err != nil || jointArr == 0 {
		fmt.Printf("joint array NULL (got 0x%08X, err=%v)\n", jointArr, err)
		os.Exit(1)
	}
	linkRoot, err := d.ReadU32(jointArr) // joint_arr[0]
	if err != nil || linkRoot == 0 {
		fmt.Printf("link_root NULL (got 0x%08X, err=%v)\n", linkRoot, err)
		os.Exit(1)
	}
	fmt.Printf("link_root joint @ 0x%08X\n", linkRoot)

	mtl, err := d.ReadU32(linkRoot + 0x60) // mMesh
	if err != nil || mtl == 0 {
		fmt.Printf("link_root mMesh NULL (got 0x%08X, err=%v)\n", mtl, err)
		os.Exit(1)
	}
	for i := 0; i < 16 && mtl != 0; i++ {
		shape, _ := d.ReadU32(mtl + 0x08) // mShape
		flags := uint32(0)
		if shape != 0 {
			flags, _ = d.ReadU32(shape + 0x0C) // mFlags
		}
		hidden := flags & 0x0001
		marker := "       "
		if i == 2 {
			marker = "(face) "
		} else if i == 5 {
			marker = "(hair) "
		}
		fmt.Printf("  mtl[%2d]%s shape=0x%08X mFlags=0x%08X HIDE=%d\n", i, marker, shape, flags, hidden)
		mtl, _ = d.ReadU32(mtl + 0x04) // mNext
	}
}

// runTrackPos polls current.pos + speed at 100 ms intervals for the
// given duration and prints the temporal evolution. Used to diagnose
// whether a warp reverts as a slow drift (something setting velocity
// each frame) or a clean snap (something hard-resetting current.pos).
func runTrackPos(secs int) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := player.NewLink(d).Ptr()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Tracking Link @ 0x%08X for %ds (50 ms tick)...\n", linkPtr, secs)
	fmt.Println("  t(ms)        X        Y        Z   speed.x   speed.y   speed.z   speedF")
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	start := time.Now()
	for time.Now().Before(deadline) {
		posBuf, err := d.Read(linkPtr+0x1F8, 12)
		if err != nil {
			break
		}
		spdBuf, err := d.Read(linkPtr+0x220, 12)
		if err != nil {
			break
		}
		spdFBuf, err := d.Read(linkPtr+0x254, 4)
		if err != nil {
			break
		}
		x := math.Float32frombits(binary.BigEndian.Uint32(posBuf[0:4]))
		y := math.Float32frombits(binary.BigEndian.Uint32(posBuf[4:8]))
		z := math.Float32frombits(binary.BigEndian.Uint32(posBuf[8:12]))
		sx := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[0:4]))
		sy := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[4:8]))
		sz := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[8:12]))
		sf := math.Float32frombits(binary.BigEndian.Uint32(spdFBuf))
		fmt.Printf("  %4d  %9.1f  %7.1f  %9.1f  %8.2f  %8.2f  %8.2f  %7.2f\n",
			time.Since(start).Milliseconds(), x, y, z, sx, sy, sz, sf)
		time.Sleep(100 * time.Millisecond)
	}
}

// runFindPos dumps Link's actor memory and reports every 4-byte-aligned
// f32 within ±1.0 of a target X (and matching Y/Z for "triplet" tagging).
// Defaults to current.pos.X but accepts an explicit target via args —
// useful for finding hidden source-of-truth fields when force-warp is
// active and current.pos oscillates.
func runFindPos() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	pos, err := player.NewLink(d).Position()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	linkPtr, err := player.NewLink(d).Ptr()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	tx, ty, tz := pos.PosX, pos.PosY, pos.PosZ
	if len(os.Args) >= 5 {
		if v, err := strconv.ParseFloat(os.Args[2], 32); err == nil {
			tx = float32(v)
		}
		if v, err := strconv.ParseFloat(os.Args[3], 32); err == nil {
			ty = float32(v)
		}
		if v, err := strconv.ParseFloat(os.Args[4], 32); err == nil {
			tz = float32(v)
		}
	}
	const dumpSize = 0x4000
	mem, err := d.Read(linkPtr, dumpSize)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Link @ 0x%08X — searching for f32 ≈ (%.1f, %.1f, %.1f)\n", linkPtr, tx, ty, tz)
	for off := 0; off+4 <= dumpSize; off += 4 {
		v := math.Float32frombits(binary.BigEndian.Uint32(mem[off : off+4]))
		if abs32(v-tx) < 1.0 {
			next4, next8 := float32(0), float32(0)
			if off+12 <= dumpSize {
				next4 = math.Float32frombits(binary.BigEndian.Uint32(mem[off+4 : off+8]))
				next8 = math.Float32frombits(binary.BigEndian.Uint32(mem[off+8 : off+12]))
			}
			marker := ""
			if abs32(next4-ty) < 1.0 && abs32(next8-tz) < 1.0 {
				marker = "  <-- triplet"
			}
			fmt.Printf("  +0x%04X = %12.2f   (+4: %12.2f, +8: %12.2f)%s\n", off, v, next4, next8, marker)
		}
	}
}

func runShadowMode(s string) {
	v, err := strconv.Atoi(s)
	if err != nil || (v != 0 && v != 5) {
		fmt.Println("mode must be 0 (off) or 5 (pose-feed); the dev modes 1-4 were deleted in Phase E0")
		os.Exit(1)
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if err := d.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{byte(v)}); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	label := "off (no Link #2 rendered — default at boot)"
	if v == 5 {
		label = "pose-feed (mpNodeMtx from mailbox.pose_buf; run pose-test or broadcast-pose)"
	}
	fmt.Printf("shadow_mode = %d  [%s]\n", v, label)
}

// Smoke test for shadow_mode 5 (pose-feed) without any networking.
//
//	mirror — every tick, copy Link #1's live mpNodeMtx into pose_buf.
//	         Link #2 should mirror Link #1 — proves the pose_buf ->
//	         mpNodeMtx overwrite + double-calc plumbing is non-destructive.
//	freeze — capture mpNodeMtx ONCE and stop writing. Link #2 should
//	         freeze in that pose while Link #1 keeps animating —
//	         decisive proof that pose_buf is the actual pose source.
//
// Either way the dolphin-side allocation happens lazily on the first
// frame after we set shadow_mode=5, so we poll pose_buf_state until C
// publishes pose_buf_ptr before sending any pose data.
func runPoseTest(mode string, durSec int) {
	if mode != "mirror" && mode != "freeze" {
		fmt.Println("Usage: ww pose-test [mirror|freeze] [seconds]")
		os.Exit(1)
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Flip to mode 5; C will lazy-alloc pose_buf and publish the pointer.
	if err := d.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{5}); err != nil {
		fmt.Printf("set shadow_mode failed: %v\n", err)
		os.Exit(1)
	}

	var poseBufPtr uint32
	var jointCount uint16
	for i := 0; i < 60; i++ {
		time.Sleep(50 * time.Millisecond)
		state, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(0), 1)
		if len(state) == 0 {
			continue
		}
		switch state[0] {
		case 1:
			poseBufPtr, _ = d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(0))
			jcBytes, _ := d.Read(mailbox.Mailbox+avatar.OffPoseJointCount(0), 2)
			if len(jcBytes) == 2 {
				jointCount = binary.BigEndian.Uint16(jcBytes)
			}
		case 0xFE:
			fmt.Println("pose_buf_state = 0xFE (bad joint count — is mini-Link rendering?)")
			os.Exit(1)
		case 0xFD:
			fmt.Println("pose_buf_state = 0xFD (GameHeap alloc failed)")
			os.Exit(1)
		}
		if poseBufPtr != 0 {
			break
		}
	}
	if poseBufPtr == 0 {
		fmt.Println("timed out waiting for pose_buf_ptr — did mode 5 ever fire? Check ./wwdev.exe dump")
		os.Exit(1)
	}
	if jointCount == 0 || jointCount > 128 {
		fmt.Printf("bad joint count from C: %d\n", jointCount)
		os.Exit(1)
	}
	poseSize := int(jointCount) * 48
	fmt.Printf("pose_buf_ptr = 0x%08X  joint_count = %d  pose_size = %d B\n", poseBufPtr, jointCount, poseSize)

	bumpSeq := func() {
		avatar.BumpSeq(d, mailbox.Mailbox+avatar.OffPoseSeq(0))
	}

	// Read Link #1's mpNodeMtx, NOT mini-Link's. Mini-Link's mpNodeMtx is
	// what mode 5 overwrites every draw frame, so reading it back gives us
	// our own previously-written pose (== freeze). Link #1's mpNodeMtx is
	// the real animation source: daPy_lk_c + 0x032C = J3DModel* mpCLModel
	// (per zeldaret/tww decomp), then J3DModel + 0x8C = Mtx* mpNodeMtx.
	// This is also exactly what broadcast-pose will read on the sender.
	const linkMpCLModelOff = 0x032C
	const j3dModelMpNodeMtxOff = 0x8C
	captureFromLinkOne := func() []byte {
		linkPtr, err := player.NewLink(d).Ptr()
		if err != nil || linkPtr == 0 {
			return nil
		}
		modelPtr, err := d.ReadU32(linkPtr + linkMpCLModelOff)
		if err != nil || modelPtr < 0x80000000 || modelPtr >= 0x81800000 {
			return nil
		}
		nodePtr, err := d.ReadU32(modelPtr + j3dModelMpNodeMtxOff)
		if err != nil || nodePtr < 0x80000000 || nodePtr >= 0x81800000 {
			return nil
		}
		data, err := d.Read(nodePtr, poseSize)
		if err != nil {
			return nil
		}
		return data
	}

	deadline := time.Now().Add(time.Duration(durSec) * time.Second)

	if mode == "freeze" {
		// Wait briefly for at least one calc cycle to populate mpNodeMtx,
		// capture once, write to pose_buf, then sit and watch.
		var captured []byte
		for i := 0; i < 30 && captured == nil; i++ {
			time.Sleep(50 * time.Millisecond)
			captured = captureFromLinkOne()
		}
		if captured == nil {
			fmt.Println("could not capture mpNodeMtx — is mini-Link calc running?")
			os.Exit(1)
		}
		if err := d.Write(poseBufPtr, captured); err != nil {
			fmt.Printf("write pose_buf failed: %v\n", err)
			os.Exit(1)
		}
		bumpSeq()
		fmt.Printf("freeze: captured %d B and wrote to pose_buf. Link #2 should hold this pose for %ds while Link #1 moves.\n", len(captured), durSec)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
		}
		fmt.Println("done.")
		return
	}

	// mirror mode: continuously echo live mpNodeMtx -> pose_buf
	fmt.Printf("mirror: copying live mpNodeMtx -> pose_buf for %ds. Link #2 should mirror Link #1.\n", durSec)
	// One-shot debug: prove the read chain works and the matrices change.
	if linkPtr, _ := player.NewLink(d).Ptr(); linkPtr != 0 {
		modelPtr, _ := d.ReadU32(linkPtr + 0x032C)
		nodePtr, _ := d.ReadU32(modelPtr + 0x8C)
		fmt.Printf("[debug] link=0x%08X  link+0x032C=0x%08X  +0x8C=0x%08X\n", linkPtr, modelPtr, nodePtr)
		if nodePtr != 0 {
			row, _ := d.Read(nodePtr, 16)
			fmt.Printf("[debug] mpNodeMtx[0] row0: % X\n", row)
		}
	}
	ticks := 0
	var lastFirstRow []byte
	changes := 0
	for time.Now().Before(deadline) {
		data := captureFromLinkOne()
		if data != nil {
			if err := d.Write(poseBufPtr, data); err == nil {
				bumpSeq()
				ticks++
				if lastFirstRow != nil {
					for i := 0; i < 16 && i < len(data); i++ {
						if data[i] != lastFirstRow[i] {
							changes++
							break
						}
					}
				}
				lastFirstRow = append(lastFirstRow[:0], data[:16]...)
			}
		}
		time.Sleep(16 * time.Millisecond)
	}
	fmt.Printf("\ndone. %d ticks written, %d distinct first-row values seen.\n", ticks, changes)
}

func runDump() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Mailbox header (spawn_trigger, progress, pads)
	hdr, _ := d.Read(mailbox.Mailbox, 16)
	fmt.Printf("mailbox hdr  @ 0x%08X: ", mailbox.Mailbox)
	for j, b := range hdr {
		fmt.Printf("%02X", b)
		if j%4 == 3 {
			fmt.Print(" ")
		}
	}
	fmt.Println()

	// The WWMB header and the strict contract check (Phase E1): whether
	// this build may write into the game, and why not.
	if c, err := wwapi.CheckContract(d, wwapi.ContractOptions{}); err != nil {
		fmt.Printf("contract: %v\n", err)
	} else {
		fmt.Printf("WWMB header @ 0x%08X: magic 0x%08X ABI %d layout %d features 0x%02X\n",
			mailbox.Mailbox+mailbox.OffHdrMagic, c.Header.Magic, c.Header.ABI, c.Header.Layout, c.Header.Features)
		fmt.Println("contract: " + wwapi.ContractLine(c))
	}

	if progress, err := d.ReadU32(mailbox.Mailbox + 0x04); err == nil {
		fmt.Printf("progress: %d (1=gate 3=link-ready 5=spawn-queued 8=spawned 9=syncing 10=actor-lost)\n", progress)
	}

	if sh, err := d.Read(mailbox.Mailbox+mailbox.OffShadowMode, 1); err == nil {
		fmt.Printf("shadow_mode: %d (0=off 5=pose-feed)\n", sh[0])
	}
	// Pose-feed (mode 5) diagnostics. pose_buf_state stays 0 until the
	// first mode-5 frame fires the lazy alloc.
	for slot := 0; slot < avatar.Cap; slot++ {
		pb, err := d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(slot))
		if err != nil {
			continue
		}
		jc, _ := d.Read(mailbox.Mailbox+avatar.OffPoseJointCount(slot), 2)
		ps, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(slot), 1)
		sq, _ := d.Read(mailbox.Mailbox+avatar.OffPoseSeq(slot), 1)
		jcVal := uint16(0)
		if len(jc) == 2 {
			jcVal = binary.BigEndian.Uint16(jc)
		}
		fmt.Printf("pose[%d]: buf=0x%08X  joint_count=%d  state=0x%02X  seq=%d\n",
			slot, pb, jcVal, ps[0], sq[0])
	}

	tunicsync.PrintTunicMailbox(d)

	// Per-slot state
	for i := 0; i < feed.MaxPuppets; i++ {
		data, _ := d.Read(feed.SlotAddr(i, 0), mailbox.PuppetSize)
		fmt.Printf("slot %d       @ 0x%08X: ", i, feed.SlotAddr(i, 0))
		for j, b := range data {
			fmt.Printf("%02X", b)
			if j%4 == 3 {
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}

	// Other useful addrs
	ptrs := []uint32{0x80006338, 0x803C4C0C, 0x803F66C0, 0x80410000}
	sizes := []int{8, 4, 4, 16}
	for i, addr := range ptrs {
		data, _ := d.Read(addr, sizes[i])
		fmt.Printf("             @ 0x%08X: ", addr)
		for j, b := range data {
			fmt.Printf("%02X", b)
			if j%4 == 3 {
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
}

func truncateName(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// runSendShiftF1 is a diagnostic that probes whether the user's Dolphin
// build accepts synthetic Shift+F1 (some forks do; mainline 2603a
// filters LLKHF_INJECTED events for hotkeys). Auto-recapture calls the
// same two Win32 paths internally — this command lets you confirm
// which (if any) of them works on your setup before relying on it.
//
// To verify: snapshot <USER_DIR>/StateSaves/GZLE01.s01 mtime, run this,
// snapshot again 3 s later. mtime advanced → your Dolphin accepts
// synthetic Shift+F1; auto-recapture will be fully hands-off.
func runSendShiftF1() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	pid := d.PID()
	d.Close()

	hwnd, err := host.FindWindowByPID(pid)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	host.ForegroundWindow(hwnd)
	time.Sleep(200 * time.Millisecond)

	// Try both paths. SendInput → system input queue (may be filtered).
	// PostMessage → window message queue (may not reach Qt's hotkey handler).
	_ = host.SendChordToFocusedWindow(host.VKShift, host.VKF1)
	time.Sleep(500 * time.Millisecond)
	_ = host.SendKeyChord(hwnd, host.VKShift, host.VKF1)

	fmt.Printf("Sent Shift+F1 to Dolphin pid %d via SendInput + PostMessage.\n", pid)
	fmt.Println("Watch <USER_DIR>/StateSaves/GZLE01.s01 — if mtime advances, your Dolphin")
	fmt.Println("build accepts synthetic hotkeys and auto-recapture will run fully unattended.")
}

// runScreenshot captures Dolphin's window via Win32 PrintWindow and
// writes a PNG to the given path (or a sensible default if empty). Picks
// the Dolphin instance using the standard env knobs — WW_DOLPHIN_PID
// for an exact PID, WW_DOLPHIN_INDEX for the Nth GZLE01 process, or the
// first match if neither is set.
//
// PrintWindow with PW_RENDERFULLCONTENT works for DirectX/Vulkan
// backends on Windows 8.1+. If Dolphin's renderer happens to refuse it,
// the call returns a clear error rather than a silently-black PNG.
func runScreenshot(out string) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	pid := d.PID()
	d.Close()

	hwnd, err := host.FindWindowByPID(pid)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	title := host.WindowTitle(hwnd)

	img, err := host.CaptureWindow(hwnd)
	if err != nil {
		fmt.Printf("ERROR: capture: %v\n", err)
		os.Exit(1)
	}

	if out == "" {
		out = fmt.Sprintf("dolphin-%d-%s.png", pid, time.Now().Format("20060102-150405"))
	}
	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Printf("ERROR: mkdir %s: %v\n", dir, err)
			os.Exit(1)
		}
	}
	f, err := os.Create(out)
	if err != nil {
		fmt.Printf("ERROR: create %s: %v\n", out, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Printf("ERROR: encode: %v\n", err)
		os.Exit(1)
	}
	b := img.Bounds()
	fmt.Printf("Captured pid %d (%q) %dx%d → %s\n", pid, title, b.Dx(), b.Dy(), out)
}

// runInput drives synthetic controller input via the C-side mailbox
// (player.Override, then player.Release after ms; see player.Override
// for how the pad_read_shim takes it). It reports the shim's override
// count and Link's position change.
func runInput(buttonsHex, stickXStr, stickYStr string, ms int) {
	buttons64, err := strconv.ParseUint(strings.TrimPrefix(buttonsHex, "0x"), 16, 16)
	if err != nil {
		fmt.Printf("ERROR: parse buttons %q: %v\n", buttonsHex, err)
		os.Exit(1)
	}
	stickX64, err := strconv.ParseInt(stickXStr, 10, 8)
	if err != nil {
		fmt.Printf("ERROR: parse stickX %q: %v\n", stickXStr, err)
		os.Exit(1)
	}
	stickY64, err := strconv.ParseInt(stickYStr, 10, 8)
	if err != nil {
		fmt.Printf("ERROR: parse stickY %q: %v\n", stickYStr, err)
		os.Exit(1)
	}
	buttons := uint16(buttons64)
	stickX := int8(stickX64)
	stickY := int8(stickY64)

	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	// Snapshot pos for delta reporting.
	var beforeX, beforeZ float32
	if p, err := player.NewLink(d).Position(); err == nil {
		beforeX, beforeZ = p.PosX, p.PosZ
	}
	overridesBefore, _ := player.Overrides(d)

	// Write payload first, then enable (player.Override); substick,
	// triggers and analogs are zeroed so leftover state from a previous
	// call doesn't leak.
	player.Override(d, player.Pad{Buttons: buttons, StickX: stickX, StickY: stickY})

	if ms == 0 {
		fmt.Printf("Input held: buttons=0x%04X stickX=%d stickY=%d (manual release via `input-release`)\n",
			buttons, stickX, stickY)
		return
	}

	fmt.Printf("Input: buttons=0x%04X stickX=%d stickY=%d for %dms\n",
		buttons, stickX, stickY, ms)
	time.Sleep(time.Duration(ms) * time.Millisecond)

	// Release: disable first so the shim becomes a pass-through, then
	// zero the fields.
	player.Release(d)

	overridesAfter, _ := player.Overrides(d)
	frames := overridesAfter - overridesBefore
	var afterX, afterZ float32
	if p, err := player.NewLink(d).Position(); err == nil {
		afterX, afterZ = p.PosX, p.PosZ
	}
	dx := afterX - beforeX
	dz := afterZ - beforeZ
	fmt.Printf("Released. shim_overrides=+%d (target ~%.0f for %dms@60Hz)  pos delta=(%+.1f, %+.1f)\n",
		frames, float64(ms)*0.06, ms, dx, dz)
}

// runInputRelease zeros input_enable so the shim becomes a pass-through.
// Useful for recovering when a long-hold `input ... 0` got abandoned.
func runInputRelease() {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	player.Release(d)
	fmt.Println("Input released (pad_read_shim is now pass-through).")
}

// runInputProbe is an exploratory write loop that drops a synthetic
// PADStatus into 0x803ED84A for the given duration. The address is
// documented as the WW button bitfield in docs/04-ww-addresses.md, but
// whether *writing* there moves Link depends on whether Dolphin's SI
// poll overwrites our value each frame. Hammering >60Hz from Go is the
// cheapest way to find out — if Link drifts, the address is good and
// we can graduate this into a proper `input` subcommand.
//
// Layout assumed (standard libogc PADStatus, big-endian on PPC):
//
//	0x803ED84A: u16 button        (e.g. 0x0008 = DPad Up)
//	0x803ED84C: s8  stickX        (-128..+127, 0 = centred)
//	0x803ED84D: s8  stickY        (-128..+127, 0 = centred)
func runInputProbe(buttonsHex, stickXStr, stickYStr string, ms int) {
	buttons64, err := strconv.ParseUint(strings.TrimPrefix(buttonsHex, "0x"), 16, 16)
	if err != nil {
		fmt.Printf("ERROR: parse buttons %q: %v\n", buttonsHex, err)
		os.Exit(1)
	}
	stickX64, err := strconv.ParseInt(stickXStr, 10, 8)
	if err != nil {
		fmt.Printf("ERROR: parse stickX %q: %v\n", stickXStr, err)
		os.Exit(1)
	}
	stickY64, err := strconv.ParseInt(stickYStr, 10, 8)
	if err != nil {
		fmt.Printf("ERROR: parse stickY %q: %v\n", stickYStr, err)
		os.Exit(1)
	}
	buttons := uint16(buttons64)
	stickX := int8(stickX64)
	stickY := int8(stickY64)

	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	posBefore, _ := player.NewLink(d).Position()
	fmt.Printf("Before: pos=(%.1f, %.1f, %.1f)  buttons=0x%04X  stickX=%d  stickY=%d  duration=%dms\n",
		posBefore.PosX, posBefore.PosY, posBefore.PosZ, buttons, stickX, stickY, ms)

	// Big-endian u16 button + signed-byte sticks, packed into 4 bytes
	// covering 0x803ED84A..0x803ED84D.
	payload := []byte{
		byte(buttons >> 8),
		byte(buttons & 0xFF),
		byte(stickX),
		byte(stickY),
	}

	// 0x803ED818 = JUTGamePad::mPadStatus[0] (PADStatus port 0). This is
	// the SOURCE the game's JUTGamePad::read() consumes each frame —
	// it then derives mPadButton[0] (0x803ED848) and mPadMStick[0]
	// (0x803ED908) from it. Writing to mPadStatus[0] before .update()
	// fires is what propagates input into game logic. 0x803ED84A
	// (which earlier tests targeted) is a downstream cache and writes
	// there get clobbered by the next derivation.
	const padStatusAddr = uint32(0x803ED818)
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	writes := 0
	readbackHits := 0
	readbackSamples := 0
	for time.Now().Before(deadline) {
		if err := d.Write(padStatusAddr, payload); err != nil {
			fmt.Printf("WARN: write failed: %v\n", err)
		}
		writes++
		// Immediately read back: did our write survive the next SI poll?
		if got, err := d.ReadU32(padStatusAddr); err == nil {
			readbackSamples++
			wantHi := uint32(buttons)<<16 | uint32(uint8(stickX))<<8 | uint32(uint8(stickY))
			if got == wantHi {
				readbackHits++
			}
		}
		// ~250Hz — well above the 60Hz emulated SI poll, so even if
		// Dolphin overwrites every frame we still land between polls.
		time.Sleep(4 * time.Millisecond)
	}
	fmt.Printf("Readback: %d/%d samples saw our value (%.1f%%)\n",
		readbackHits, readbackSamples, 100*float64(readbackHits)/float64(readbackSamples))
	// Release: write all zeros so Link doesn't keep moving after the probe.
	d.Write(padStatusAddr, []byte{0, 0, 0, 0})

	posAfter, _ := player.NewLink(d).Position()
	dx := posAfter.PosX - posBefore.PosX
	dy := posAfter.PosY - posBefore.PosY
	dz := posAfter.PosZ - posBefore.PosZ
	moved := dx*dx+dy*dy+dz*dz > 1
	fmt.Printf("After:  pos=(%.1f, %.1f, %.1f)  delta=(%+.1f, %+.1f, %+.1f)  writes=%d  moved=%v\n",
		posAfter.PosX, posAfter.PosY, posAfter.PosZ, dx, dy, dz, writes, moved)
}

// abs32 is |f|; ww-multiplayer's main.go keeps its own copy for warp.
func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
