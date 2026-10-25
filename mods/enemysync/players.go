package enemysync

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The remote-Link Go side (ww-multiplayer-handoff/design/
// remote-link-design.md §7.2, §8; the block is wwapi/mailbox/remote.go,
// the C side inject/include/wwremote.h). The host's enemies target and hit
// every player, not only the host's Link:
//
//   - RemoteHost (the enemy host's Go) writes the WWRP block every tick:
//     one slot per pose-feed slot (mini-Link) with that remote player's
//     smoothed position (the session's feed.Roster, what PuppetSync draws)
//     and its PSTATE (stage, room, guard, i-frames, cut), plus the flags,
//     tunables, PROC_POLICY and FF denylist from the WW_ENEMY_REMOTE*
//     knobs. It reads the REL's hit ring (enemy At on a body) and sends
//     each hit to that player's enemy client as a PHIT, and logs the PACK
//     that comes back. HitApplier asks it for ATTRIB's hit_apply flags.
//   - RemoteClient (each joiner's mirror) sends PSTATE from its own Link,
//     gates each PHIT (epoch, stage, room, in play, the APPLY flag),
//     writes it into its WWRP block's apply record for the REL to replay
//     as an At sphere on its own Link ("v2 in reverse", §8.5), and answers
//     with the REL's result as a PACK.
//
// Nothing is written into the WWRP block when WW_ENEMY_REMOTE turns it
// off (off/none/false/0; RemoteConfigFromEnv): magic 0 / never written is
// today's game, and so is every flag clear. Unset is step6 with
// DefaultPolicy. The PSTATE stream runs whenever the enemy client
// runs (it only informs the host).

// Remote-Link timing.
const (
	// PStateEvery is the PSTATE cadence (and any flag change sends one at
	// once): about two host frames.
	PStateEvery = 50 * time.Millisecond
	// PStateStale is how long a host keeps using a remote's last PSTATE:
	// past it the slot carries no stage (the REL parks the body on a stage
	// mismatch) and NO_HIT. 3 s is WWREMOTE_STALE_FRAMES' 90 frames at
	// 30 Hz.
	PStateStale = 3 * time.Second
	// RosterStale is how old the roster may be before the host treats
	// every slot as empty (PuppetSync publishes every 16 ms).
	RosterStale = time.Second
	// SettleDist is how far a slot's smoothed position may trail the
	// remote's last position before the slot counts as still crossing a
	// jump (heapgate.go holds its FULL create: a body placed mid-slide can
	// land over the void and never find ground). Running trails by about
	// 35 units and the ship at full sail by about 110; a stage change,
	// warp or void-out moves thousands.
	SettleDist = 300
	// ArriveWait is how long after a load a slot waits for a new
	// position before it stops waiting (the REL gives up on its FULL
	// bit after RL_FULL_WAIT, 120 frames = 4 s): a remote that respawned
	// exactly where it was would otherwise never get a FULL body.
	ArriveWait = 2500 * time.Millisecond
	// HitCutHold is how long a HIT's cut type and count override the
	// PSTATE's in the sender's slot (ATTRIB stages them into the body).
	HitCutHold = 500 * time.Millisecond
	// PHitRateFrames is the PHIT rate limit: one per (target client,
	// attacker setID) per this many host REL frames (§7.2).
	PHitRateFrames = 15
	// applyQueueDepth is how many PHITs a client holds for its REL; the
	// next is dropped (PACK dropped/aborted) at once.
	applyQueueDepth = 4
	// applySettleFrames is how many REL frames a client waits after the
	// first result for the apply to settle (the REL decides EARLY_OUT and
	// records life_after two executes after the cross, §8.5 step 7).
	applySettleFrames = 2
	// RemoteLogEvery is the host's status line cadence.
	RemoteLogEvery = 5 * time.Second
)

// The PHIT sanity gates (phitSane, RemoteHost.phit, RemoteClient.Tick):
// a hit that can no longer be right is dropped once, never retried. The
// shape is datstarkey/ww-online's PvpHitGate (WWOnline.Server/Hubs/
// PvpHitGate.cs, MIT: a field check, a stage check, a distance check and
// a burst bucket), rewritten for our PHIT, which the host makes from its
// own REL's hit ring rather than trusting an attacker's game.
const (
	// PHitStaleFrames: a ring entry this many host REL frames older than
	// the REL's frame counter when Tick reads it is stale (the host's Go
	// stalled: the attacker and the body have moved on). 30 frames is one
	// second at 30 Hz.
	PHitStaleFrames = 30
	// PHitMaxRel bounds |RelHit| (the Tg hit position minus the body
	// position). A judgment, not a game number: the body's Tg cylinder is
	// 30 x 120 by default (WW_ENEMY_REMOTE_CYL_R/_H) and a full body's is
	// Link's own, so a hit point further than this from the body's origin
	// is garbage, not a hit.
	PHitMaxRel = 500
	// PHitMaxSpl is the highest At SPL: dCcG_At_Spl ends at UNKB = 0xB
	// (tww include/d/d_cc_d.h:106-120; 8 and up cannot be guarded).
	PHitMaxSpl = 0xB
	// PHitBurst and PHitPerSec are the per-target-client bucket, on top
	// of the per-(client, attacker) PHitRateFrames: room for a crowd's
	// hits at once (twice the client's applyQueueDepth) and a few a
	// second after that. The client's Link has i-frames after a hit
	// (vanilla 30 frames), so more than that cannot land anyway.
	PHitBurst  = 8
	PHitPerSec = 4
	// PHitClientStale is how long a client holds a PHIT between the read
	// loop and its Tick before it drops it (the client's Go stalled).
	PHitClientStale = 500 * time.Millisecond
)

// phitSane reports whether a PHIT's (or ring entry's) fields can be a
// hit: an SPL the game has, a finite and near hit point and vector.
func phitSane(spl uint8, rel, rvec [3]float32) bool {
	if spl > PHitMaxSpl || finite(rel[0], rel[1], rel[2], rvec[0], rvec[1], rvec[2]) != nil {
		return false
	}
	return rel[0]*rel[0]+rel[1]*rel[1]+rel[2]*rel[2] <= PHitMaxRel*PHitMaxRel
}

// phitBucket is a token bucket over wall time; not safe for concurrent
// use (RemoteHost.Tick's goroutine only).
type phitBucket struct {
	tokens float64
	last   time.Time
}

func (b *phitBucket) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens, b.last = PHitBurst, now
	}
	if d := now.Sub(b.last); d > 0 {
		b.tokens = min(PHitBurst, b.tokens+d.Seconds()*PHitPerSec)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// --- config -------------------------------------------------------------------

// RemoteConfig is the remote-Link block's Go-written settings.
type RemoteConfig struct {
	// On: WW_ENEMY_REMOTE is set. Off, the block is never written.
	On             bool
	Flags          uint16 // mailbox.RemoteFlag*
	MarginQ8       uint8
	MinHold        uint8
	BodyIFrames    uint8
	DYMax          float32
	EyeDY          float32
	CylR           float32
	CylH           float32
	CreateCooldown uint16
	Policy         []mailbox.RemotePolicy
	FFDeny         []string

	// The full bodies (remote-link-stage4-plan.md D4-2, D4-3), host only.
	// HeapGate runs the Go heap gate that sets FULL_ALLOW; it is on
	// whenever Flags has RemoteFlagFullBodies (RemoteConfigFromEnv). It is
	// a Go setting, never a flags bit: flags reach the REL as given. With
	// it off FULL_ALLOW stays 0 and the REL falls back to lite bodies.
	HeapGate bool
	// HeapSizes are the gate's heap-size rules (WW_ENEMY_REMOTE_HEAP;
	// zero fields skip their rule). The counter, in-flight and FullMax
	// rules always apply.
	HeapSizes heap.Thresholds
	// FullMax is how many FULL bodies may be LIVE at once (default 1).
	FullMax uint8
	// Flags2 is the block's FLAGS2 (mailbox.RemoteFlag2*), host only.
	Flags2 uint16
}

// remoteFlagNames are WW_ENEMY_REMOTE's flag names.
var remoteFlagNames = map[string]uint16{
	"bodies":         mailbox.RemoteFlagBodies,
	"swap":           mailbox.RemoteFlagSwap,
	"hittable":       mailbox.RemoteFlagHittable,
	"detect":         mailbox.RemoteFlagDetect,
	"apply":          mailbox.RemoteFlagApply,
	"attrib":         mailbox.RemoteFlagAttrib,
	"name_a9":        mailbox.RemoteFlagNameA9,
	"guard":          mailbox.RemoteFlagGuard,
	"ff_gate_off":    mailbox.RemoteFlagFFGateOff,
	"rouya_gate_off": mailbox.RemoteFlagRouyaGateOff,
	"attacker":       mailbox.RemoteFlagAttacker,
	"group_wide":     mailbox.RemoteFlagGroupWide,
	"both_words":     mailbox.RemoteFlagBothWordsEnable,
	"iframe_stamp":   mailbox.RemoteFlagIFrameStamp,
	"full_bodies":    mailbox.RemoteFlagFullBodies,
}

// remoteSteps are the design's step presets (§10): each adds its flags to
// the one before. step0 is the block with every flag clear.
var remoteSteps = [...]uint16{
	0,
	mailbox.RemoteFlagBodies,
	mailbox.RemoteFlagSwap,
	mailbox.RemoteFlagHittable | mailbox.RemoteFlagDetect | mailbox.RemoteFlagIFrameStamp,
	mailbox.RemoteFlagApply,
	mailbox.RemoteFlagAttrib | mailbox.RemoteFlagAttacker,
	mailbox.RemoteFlagNameA9 | mailbox.RemoteFlagGuard,
	mailbox.RemoteFlagFullBodies, // step7: the real daPy_lk_c bodies (Stage 4), behind the heap gate
}

// ParseRemoteFlags parses WW_ENEMY_REMOTE: a number (0x2F, 47), or names
// and step presets joined by ',' or '+' (bodies, swap, hittable, detect,
// apply, attrib, name_a9, guard, ff_gate_off, rouya_gate_off, attacker,
// group_wide, both_words, iframe_stamp, full_bodies; step0..step7, each
// with the steps before it).
func ParseRemoteFlags(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseUint(s, 0, 16); err == nil {
		return uint16(v), nil
	}
	var flags uint16
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '+' }) {
		name := strings.ToLower(strings.TrimSpace(part))
		if f, ok := remoteFlagNames[name]; ok {
			flags |= f
			continue
		}
		if n, ok := strings.CutPrefix(name, "step"); ok {
			if k, err := strconv.Atoi(n); err == nil && k >= 0 && k < len(remoteSteps) {
				for i := 0; i <= k; i++ {
					flags |= remoteSteps[i]
				}
				continue
			}
		}
		return 0, fmt.Errorf("WW_ENEMY_REMOTE: unknown flag %q", part)
	}
	return flags, nil
}

// SeedPolicy is the PROC_POLICY seed (§6.4, widened by the enemy audit of
// 2026-10-20, ww-multiplayer-handoff/archive/enemy-audit): ALLOW the
// enemies whose execute only reads the player (the Moblin, §3, and the
// audited ones; the Bokoblin, Darknut, ChuChu and warship rely on the
// REL's per-proc m714 rewrite and demo gates, remote.c rl_m714_off and
// rl_proc_gated), Stalfos without the latch; DENY the enemies that grab,
// possess or move the player or order events with it, and the bosses.
// The DENY entries in the ENEMY group matter under GROUP_WIDE, which
// swaps every unlisted enemy.
func SeedPolicy() []mailbox.RemotePolicy {
	var p []mailbox.RemotePolicy
	for _, proc := range []int16{addr.ProcMo2, addr.ProcBK, addr.ProcTN, addr.ProcCC, addr.ProcOship, addr.ProcBB,
		addr.ProcAM, addr.ProcAM2, addr.ProcKI, addr.ProcBWDS, addr.ProcBMDFoot, addr.ProcBGN2, addr.ProcBGN3} {
		p = append(p, mailbox.RemotePolicy{Proc: proc, Flags: mailbox.RemotePFAllow})
	}
	p = append(p, mailbox.RemotePolicy{Proc: addr.ProcST, Flags: mailbox.RemotePFAllow | mailbox.RemotePFNoLatch})
	return append(p, seedDeny()...)
}

// seedDeny is the seed's DENY entries, SeedPolicy's and DefaultPolicy's
// alike.
func seedDeny() []mailbox.RemotePolicy {
	var p []mailbox.RemotePolicy
	for _, proc := range []int16{addr.ProcFM, addr.ProcRD, addr.ProcPW, addr.ProcSSS, addr.ProcNpcJi1, addr.ProcFGMahou,
		addr.ProcBPW, addr.ProcBWD, addr.ProcDaiocta, addr.ProcBTD, addr.ProcBMD, addr.ProcBDK, addr.ProcBST, addr.ProcFganon,
		addr.ProcBGN} {
		p = append(p, mailbox.RemotePolicy{Proc: proc, Flags: mailbox.RemotePFDeny})
	}
	return p
}

// DefaultPolicy is the PROC_POLICY of the default bodies (WW_ENEMY_REMOTE
// unset, release-readiness A4): ALLOW only the Proc of each kind kinds
// enables (the mirrored kinds: mo2, bk, cc by default; WW_ENEMY_MIRROR_KINDS
// naming ki ALLOWs the Keese), and the seed's DENY list. Every other enemy
// is no ALLOW, so without group_wide the REL never swaps its target to a
// body (remote.c) and the host sends no PHIT for it (RemoteHost.phit):
// an unmirrored enemy runs apart on each game and ignores the remote
// bodies, as before there were bodies, on the host and the joiner alike.
func DefaultPolicy(kinds KindSet) []mailbox.RemotePolicy {
	var p []mailbox.RemotePolicy
	for i := range Kinds {
		if kinds.On[i] {
			p = append(p, mailbox.RemotePolicy{Proc: int16(Kinds[i].Proc), Flags: mailbox.RemotePFAllow})
		}
	}
	return append(p, seedDeny()...)
}

// defaultRemoteSteps is the default bodies' flags: step6 (bodies, swap,
// hittable, detect, iframe_stamp, apply, attrib, attacker, name_a9,
// guard), without step7's full_bodies.
const defaultRemoteSteps = 6

// remoteOff reports a WW_ENEMY_REMOTE value that turns the block off
// (off, none, false, 0; any case).
func remoteOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "none", "false", "0":
		return true
	}
	return false
}

// SeedFFDeny is the FF stage denylist seed (addr.StageFF*).
func SeedFFDeny() []string {
	return []string{addr.StageFFOutside, addr.StageFFRooms, addr.StageFFRooms2, addr.StageFFTower}
}

// remotePolicyFlagNames are WW_ENEMY_REMOTE_POLICY's flag names.
var remotePolicyFlagNames = map[string]uint8{
	"deny":       mailbox.RemotePFDeny,
	"allow":      mailbox.RemotePFAllow,
	"both_words": mailbox.RemotePFBothWords,
	"no_latch":   mailbox.RemotePFNoLatch,
}

// ParseRemotePolicy applies WW_ENEMY_REMOTE_POLICY to base: entries
// "proc=flag+flag" (proc a number, e.g. 0xBD; flags allow, deny,
// both_words, no_latch, or "off" to remove the proc) joined by ','. An
// entry replaces base's entry for its proc, or is appended. "none" alone
// starts from an empty table.
func ParseRemotePolicy(s string, base []mailbox.RemotePolicy) ([]mailbox.RemotePolicy, error) {
	out := append([]mailbox.RemotePolicy(nil), base...)
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	if strings.EqualFold(s, "none") {
		return nil, nil
	}
	for _, ent := range strings.Split(s, ",") {
		procS, flagS, ok := strings.Cut(strings.TrimSpace(ent), "=")
		if !ok {
			return nil, fmt.Errorf("WW_ENEMY_REMOTE_POLICY: %q is not proc=flags", ent)
		}
		pv, err := strconv.ParseUint(strings.TrimSpace(procS), 0, 15)
		if err != nil {
			return nil, fmt.Errorf("WW_ENEMY_REMOTE_POLICY: proc %q: %v", procS, err)
		}
		proc := int16(pv)
		var flags uint8
		remove := false
		for _, f := range strings.Split(flagS, "+") {
			f = strings.ToLower(strings.TrimSpace(f))
			if f == "off" {
				remove = true
				continue
			}
			v, ok := remotePolicyFlagNames[f]
			if !ok {
				return nil, fmt.Errorf("WW_ENEMY_REMOTE_POLICY: unknown flag %q", f)
			}
			flags |= v
		}
		i := 0
		for i < len(out) && out[i].Proc != proc {
			i++
		}
		switch {
		case remove && i < len(out):
			out = append(out[:i], out[i+1:]...)
		case remove:
		case i < len(out):
			out[i].Flags = flags
		default:
			out = append(out, mailbox.RemotePolicy{Proc: proc, Flags: flags})
		}
	}
	if len(out) > mailbox.RemoteProcPolicyMax {
		return nil, fmt.Errorf("WW_ENEMY_REMOTE_POLICY: %d entries (max %d)", len(out), mailbox.RemoteProcPolicyMax)
	}
	return out, nil
}

// RemoteConfigFromEnv reads the WW_ENEMY_REMOTE* knobs (CLAUDE.md).
// WW_ENEMY_REMOTE unset or blank is the default bodies: the block on at
// step6 with DefaultPolicy of the mirrored kinds (MirrorKinds, the set
// the Host, the HitApplier and the Mirror read from the same env, so the
// policy and the PHIT filter agree); off, none, false or 0 is the block
// off, quietly; any other value is parsed as flags with SeedPolicy (the
// dev rig's table). WW_ENEMY_REMOTE_POLICY edits whichever table that
// is. A bad value is reported through rep and its default kept; a bad
// WW_ENEMY_REMOTE leaves the block off.
func RemoteConfigFromEnv(rep log.Reporter) RemoteConfig {
	c := DefaultRemoteConfig()
	warn := func(err error) { log.Logf(rep, log.Warn, "enemy remote: %v", err) }
	switch v := os.Getenv("WW_ENEMY_REMOTE"); {
	case strings.TrimSpace(v) == "":
		kinds, _ := MirrorKinds() // the host and the client log a bad value
		c.On, c.Policy = true, DefaultPolicy(kinds)
		for i := 0; i <= defaultRemoteSteps; i++ {
			c.Flags |= remoteSteps[i]
		}
	case remoteOff(v):
	default:
		f, err := ParseRemoteFlags(v)
		if err != nil {
			warn(fmt.Errorf("%v; the remote-Link block stays off", err))
		} else {
			c.On, c.Flags = true, f
		}
	}
	u8 := func(key string, dst *uint8) {
		if v := os.Getenv(key); v != "" {
			n, err := strconv.ParseUint(v, 0, 8)
			if err != nil {
				warn(fmt.Errorf("%s=%q: %v", key, v, err))
				return
			}
			*dst = uint8(n)
		}
	}
	f32 := func(key string, dst *float32) {
		if v := os.Getenv(key); v != "" {
			n, err := strconv.ParseFloat(v, 32)
			if err != nil || n != n || n > 1e9 || n < -1e9 {
				warn(fmt.Errorf("%s=%q: not a finite number", key, v))
				return
			}
			*dst = float32(n)
		}
	}
	u8("WW_ENEMY_REMOTE_MARGIN", &c.MarginQ8)
	u8("WW_ENEMY_REMOTE_MIN_HOLD", &c.MinHold)
	u8("WW_ENEMY_REMOTE_IFRAMES", &c.BodyIFrames)
	f32("WW_ENEMY_REMOTE_DY_MAX", &c.DYMax)
	f32("WW_ENEMY_REMOTE_EYE_DY", &c.EyeDY)
	f32("WW_ENEMY_REMOTE_CYL_R", &c.CylR)
	f32("WW_ENEMY_REMOTE_CYL_H", &c.CylH)
	if v := os.Getenv("WW_ENEMY_REMOTE_COOLDOWN"); v != "" {
		if n, err := strconv.ParseUint(v, 0, 16); err != nil {
			warn(fmt.Errorf("WW_ENEMY_REMOTE_COOLDOWN=%q: %v", v, err))
		} else {
			c.CreateCooldown = uint16(n)
		}
	}
	if p, err := ParseRemotePolicy(os.Getenv("WW_ENEMY_REMOTE_POLICY"), c.Policy); err != nil {
		warn(err)
	} else {
		c.Policy = p
	}
	if v := os.Getenv("WW_ENEMY_REMOTE_FF"); strings.TrimSpace(v) != "" {
		ff, err := parseFFDeny(v)
		if err != nil {
			warn(err)
		} else {
			c.FFDeny = ff
		}
	}
	c.HeapGate = c.Flags&mailbox.RemoteFlagFullBodies != 0
	if v := os.Getenv("WW_ENEMY_REMOTE_HEAP"); strings.TrimSpace(v) != "" {
		if th, err := parseHeapSizes(v); err != nil {
			warn(err)
		} else {
			c.HeapSizes = th
		}
	}
	u8("WW_ENEMY_REMOTE_FULL_MAX", &c.FullMax)
	if v := os.Getenv("WW_ENEMY_REMOTE_FLAGS2"); strings.TrimSpace(v) != "" {
		if f, err := parseFlags2(v); err != nil {
			warn(err)
		} else {
			c.Flags2 = f
		}
	}
	return c
}

// parseHeapSizes parses WW_ENEMY_REMOTE_HEAP: "off" (no heap-size rule;
// the counter, in-flight and FullMax rules still hold) or
// <game_largest>,<game_free>,<zelda_largest> (numbers, 0x allowed; the
// archive rule keeps its default).
func parseHeapSizes(s string) (heap.Thresholds, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "off") {
		return heap.Thresholds{}, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return heap.Thresholds{}, fmt.Errorf("WW_ENEMY_REMOTE_HEAP=%q: want off or <game_largest>,<game_free>,<zelda_largest>", s)
	}
	var v [3]uint32
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 0, 32)
		if err != nil {
			return heap.Thresholds{}, fmt.Errorf("WW_ENEMY_REMOTE_HEAP=%q: %v", s, err)
		}
		v[i] = uint32(n)
	}
	th := heap.DefaultThresholds()
	th.GameLargest, th.GameFree, th.ZeldaLargest = v[0], v[1], v[2]
	return th, nil
}

// remoteFlag2Names are WW_ENEMY_REMOTE_FLAGS2's names.
var remoteFlag2Names = map[string]uint16{
	"full_co": mailbox.RemoteFlag2FullCo,
}

// parseFlags2 parses WW_ENEMY_REMOTE_FLAGS2: a number or names joined by
// ',' or '+' (full_co).
func parseFlags2(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseUint(s, 0, 16); err == nil {
		return uint16(v), nil
	}
	var f uint16
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '+' }) {
		v, ok := remoteFlag2Names[strings.ToLower(strings.TrimSpace(part))]
		if !ok {
			return 0, fmt.Errorf("WW_ENEMY_REMOTE_FLAGS2: unknown flag %q", part)
		}
		f |= v
	}
	return f, nil
}

// parseFFDeny parses WW_ENEMY_REMOTE_FF: stage names joined by ',', or
// "none" (an empty denylist).
func parseFFDeny(s string) ([]string, error) {
	if strings.EqualFold(strings.TrimSpace(s), "none") {
		return nil, nil
	}
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if len(n) > mailbox.RemoteStageLen {
			return nil, fmt.Errorf("WW_ENEMY_REMOTE_FF: stage %q is longer than %d bytes", n, mailbox.RemoteStageLen)
		}
		out = append(out, n)
	}
	if len(out) > mailbox.RemoteFFDenyMax {
		return nil, fmt.Errorf("WW_ENEMY_REMOTE_FF: %d stages (max %d)", len(out), mailbox.RemoteFFDenyMax)
	}
	return out, nil
}

// DefaultRemoteConfig is the block off, with the contract's default
// tunables (mailbox.RemoteDef*) and the seed policy and FF denylist: the
// base RemoteConfigFromEnv starts from (its default bodies turn the block
// on with DefaultPolicy instead).
func DefaultRemoteConfig() RemoteConfig {
	return RemoteConfig{
		MarginQ8: mailbox.RemoteDefMarginQ8, MinHold: mailbox.RemoteDefMinHold, BodyIFrames: mailbox.RemoteDefBodyIFrames,
		DYMax: mailbox.RemoteDefDYMax, EyeDY: mailbox.RemoteDefEyeDY, CylR: mailbox.RemoteDefCylR, CylH: mailbox.RemoteDefCylH,
		CreateCooldown: mailbox.RemoteDefCreateCooldown, Policy: SeedPolicy(), FFDeny: SeedFFDeny(),
		HeapSizes: heap.DefaultThresholds(), FullMax: 1,
	}
}

// block is the Go-written span for role with c's settings (no slots, no
// apply record, and FULL_ALLOW, HEAP_REASON and FLAGS2 0: the host's Tick
// sets them, the client never does).
func (c *RemoteConfig) block(role uint8, seq, epoch uint32) *mailbox.RemoteBlock {
	return &mailbox.RemoteBlock{Magic: mailbox.RemoteMagic, Seq: seq, Layout: mailbox.RemoteLayout, Flags: c.Flags, Epoch: epoch,
		MarginQ8: c.MarginQ8, MinHold: c.MinHold, BodyIFrames: c.BodyIFrames, Role: role, DYMax: c.DYMax,
		CreateCooldown: c.CreateCooldown, FFDeny: c.FFDeny, Policy: c.Policy}
}

// nextSeq bumps a block seq, never to 0 (the REL reads seq 0 as "never
// written", like the enemy block's).
func nextSeq(s *uint32) uint32 {
	*s++
	if *s == 0 {
		*s = 1
	}
	return *s
}

// --- PSTATE: the client Link -----------------------------------------------------

// ReadPState reads this game's Link as a PSTATE (§8.1) in scope: cut type
// and count, i-frames, the guard (checkPlayerGuard's rule,
// d_a_player_main.cpp:1038-1040: mCurProc CROUCH_DEFENSE or a guard slip,
// or the upper ATNG/ATNGHAM anime), mModeFlg's DAMAGE, SWIM and IN_SHIP,
// dead (life 0 or the death demo), in an event (mEvtCtrl.mMode or a player
// demo) and loading (out of play). Without a Link in play it is a
// LOADING-only PSTATE with the scope's stage and room.
func ReadPState(m mem.Reader, scope Scope, epoch uint32) (*PState, error) {
	p := &PState{Epoch: epoch, Stage: scope.Stage, Room: scope.Room}
	if len(p.Stage) > StageLen {
		p.Stage = p.Stage[:StageLen]
	}
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return nil, err
	}
	if !scope.InPlay || !addr.InMEM1(link) {
		p.Flags = PSLoading
		return p, nil
	}
	cut, err := m.Read(link+addr.PlayerOffCutType, addr.PlayerOffDamageWait+2-addr.PlayerOffCutType)
	if err != nil {
		return nil, err
	}
	if len(cut) != addr.PlayerOffDamageWait+2-addr.PlayerOffCutType {
		return nil, fmt.Errorf("enemysync: Link cut read %d bytes", len(cut))
	}
	p.CutType, p.CutCount = cut[0], cut[addr.PlayerOffCutCount-addr.PlayerOffCutType]
	p.IFrames = int16(mem.U16BE(cut[addr.PlayerOffDamageWait-addr.PlayerOffCutType:]))
	proc, err := mem.ReadU32(m, link+addr.PlayerOffCurProc)
	if err != nil {
		return nil, err
	}
	upper, err := mem.ReadU16(m, link+addr.PlayerOffUpperAnm)
	if err != nil {
		return nil, err
	}
	shield, err := mem.ReadU16(m, link+addr.PlayerOffShieldAngleY)
	if err != nil {
		return nil, err
	}
	p.ShieldAngleY = int16(shield)
	mode, err := mem.ReadU32(m, link+addr.PlayerOffModeFlg)
	if err != nil {
		return nil, err
	}
	demo, err := mem.ReadU16(m, link+addr.PlayerOffDemoType)
	if err != nil {
		return nil, err
	}
	life, err := mem.ReadU16(m, addr.Life)
	if err != nil {
		return nil, err
	}
	evt, err := mem.ReadU8(m, addr.EvtMode)
	if err != nil {
		return nil, err
	}
	crouchDef := proc == addr.PlayerProcCrouchDefense || proc == addr.PlayerProcCrouchDefenseSlip
	if crouchDef {
		p.Flags |= PSCrouchDef
	}
	if crouchDef || proc == addr.PlayerProcGuardSlip || upper == addr.PlayerAnmAtng || upper == addr.PlayerAnmAtngHam {
		p.Flags |= PSGuard
	}
	if mode&addr.PlayerModeDamage != 0 {
		p.Flags |= PSDamageMode
	}
	if mode&addr.PlayerModeSwim != 0 {
		p.Flags |= PSSwim
	}
	if mode&addr.PlayerModeInShip != 0 {
		p.Flags |= PSOnShip
	}
	if life == 0 || proc == addr.PlayerProcDemoDead {
		p.Flags |= PSDead
	}
	if evt != 0 || demo != 0 {
		p.Flags |= PSInEvent
	}
	return p, nil
}

// --- host ---------------------------------------------------------------------------

// RemoteHost is the enemy host's remote-Link side. OnPState, OnPAck and
// NoteHit take the read loop's messages (safe for concurrent use with
// Tick); Tick runs on the pipeline's tick.
type RemoteHost struct {
	m     mem.Mem
	rep   log.Reporter
	cfg   RemoteConfig
	epoch uint32 // the host's STATE epoch, which PHIT carries

	mu      sync.Mutex
	pstates map[uint8]pstateEntry // by player id
	packs   []*PAck
	hitCuts map[uint8]hitCut  // by player id: the last HIT's cut type and count
	arrive  map[uint8]arrival // by player id: has a position come in since its stage changed

	seq           uint32
	blockEpoch    uint32 // the WWRP epoch: bumped on a stage change, a reload and a remote's arrival (Tick)
	stage         string
	inPlay        bool // the host's scope was in play at the last Tick
	wrote         bool
	owners        [mailbox.RemoteMaxSlots]slotOwner
	haveHead      bool
	lastHead      uint32
	lastPHit      map[phitKey]uint32    // (client, attacker pid) -> host REL frame of the last PHIT
	phitSeq       map[uint8]uint16      // per target client
	phitBurst     map[uint8]*phitBucket // per target client (PHitBurst)
	logNext       time.Time
	dropWarned    map[string]bool
	gate          *heapGate // the full-body heap gate, under FULL_BODIES with HeapGate
	statusAt      time.Time // when Stats.LastStatus was read
	gateErrLogged bool

	// Kinds is the host's enabled set (the Host's): an attacker of an
	// enabled kind is sent like an ALLOW proc, as the mirror's own.
	Kinds KindSet

	Stats RemoteHostStats
}

// phitKey is a PHIT rate key: one per (target client, attacker).
type phitKey struct {
	client uint8
	pid    uint32
}

// NoMirrorSetID is the apply record's owner id for an attacker the client
// has no mirror of: above every keyed setID and outside OwnerSlotBase's
// range, so the client REL takes its stand-in owner. A PHIT's SetID field
// carries the attacker's handle (wire version 4), NoMirrorHandle for one
// the host does not list, and the client translates it (RemoteClient.Owner).
const NoMirrorSetID = 0xFFFF

// arrival is what slots keeps per remote to tell when its position
// belongs to the stage its PSTATE names: during and after a load the
// position stream still carries the stage before's coordinates until the
// new Link sends one.
type arrival struct {
	stage   string
	target  [3]float32 // the position when the load began
	loading bool
	moved   bool      // a new position has come in since
	loaded  time.Time // when the load ended
}

type pstateEntry struct {
	p  PState
	at time.Time
}

type hitCut struct {
	cutType, cutCount uint8
	at                time.Time
}

type slotOwner struct {
	player uint8
	gen    uint8
}

// RemoteHostStats are counters a probe or test reads.
type RemoteHostStats struct {
	Writes, PStates, PStatesUnmapped, PHits, PAcks uint64
	// Ring hits dropped by why: not a Moblin, not keyed, no owner, no
	// client id for the owner, rate limited.
	DropNotAllowed, DropNoOwner, DropNoClient, DropRate uint64
	// The sanity gates': fields no hit has (SPL, hit point), older than
	// PHitStaleFrames, the owner not in the host's stage and room (or
	// loading, or its PSTATE stale), the body trailing the player past
	// SettleDist, over the per-client burst.
	DropBad, DropStale, DropStage, DropRange, DropBurst uint64
	// StandIn counts the sent PHITs from an attacker the host does not
	// list (with NoMirrorHandle).
	StandIn    uint64
	LastStatus mailbox.RemoteStatus
}

// NewRemoteHost returns the host side over m with cfg; epoch is the host
// STATE epoch every PHIT carries.
func NewRemoteHost(m mem.Mem, rep log.Reporter, cfg RemoteConfig, epoch uint32) *RemoteHost {
	return &RemoteHost{m: m, rep: rep, cfg: cfg, epoch: epoch, blockEpoch: epoch << 8,
		pstates: map[uint8]pstateEntry{}, hitCuts: map[uint8]hitCut{}, arrive: map[uint8]arrival{}, lastPHit: map[phitKey]uint32{},
		phitSeq: map[uint8]uint16{}, phitBurst: map[uint8]*phitBucket{}, dropWarned: map[string]bool{}, gate: newHeapGate(cfg.HeapSizes, int(cfg.FullMax))}
}

// Config is the host's settings.
func (r *RemoteHost) Config() RemoteConfig { return r.cfg }

// OnPState keeps a client's PSTATE by its server-stamped player id; one
// with player id 0 (the server found no player of that name) is counted
// and dropped: its pose slot cannot be known.
func (r *RemoteHost) OnPState(p *PState, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Stats.PStates++
	if p.PlayerID == 0 {
		r.Stats.PStatesUnmapped++
		return
	}
	r.pstates[p.PlayerID] = pstateEntry{p: *p, at: now}
}

// OnPAck queues a PACK for the next Tick's log.
func (r *RemoteHost) OnPAck(a *PAck) {
	r.mu.Lock()
	r.packs = append(r.packs, a)
	r.mu.Unlock()
}

// NoteHit keeps a client HIT's cut type and count for the sender's slot
// (HitCutHold): ATTRIB stages the slot's into the body for at_power_check.
func (r *RemoteHost) NoteHit(h *Hit, now time.Time) {
	if h.PlayerID == 0 {
		return
	}
	r.mu.Lock()
	r.hitCuts[h.PlayerID] = hitCut{cutType: h.CutType, cutCount: h.CutCount, at: now}
	r.mu.Unlock()
}

// slotOf is the WWRP slot player owns, or -1.
func (r *RemoteHost) slotOf(player uint8) int {
	if player == 0 {
		return -1
	}
	for i := range mailbox.MaxRemoteLinks {
		if r.owners[i].player == player {
			return i
		}
	}
	return -1
}

// AttribFlags is HitApplier's ATTRIB hook (§8.6): the hit_apply flags
// that attribute h to its sender's body, RemoteHAFAttribBody plus
// RemoteHAFSlot1 for slot 1, when the block is on with ATTRIB and the
// sender owns a slot; else 0 (the host Link's path). The REL falls back to
// the host Link (HD_NO_BODY) when that body is not LIVE.
func (r *RemoteHost) AttribFlags(h *Hit) uint8 {
	if !r.cfg.On || r.cfg.Flags&mailbox.RemoteFlagAttrib == 0 {
		return 0
	}
	switch r.slotOf(h.PlayerID) {
	case 0:
		return mailbox.RemoteHAFAttribBody
	case 1:
		return mailbox.RemoteHAFAttribBody | mailbox.RemoteHAFSlot1
	}
	return 0
}

// slotFlags translates a PSTATE into the slot's sflags (wwremote.h:
// Go-derived, not a copy): TARGETABLE only for a remote in the host's
// stage and room that is in play, alive, not in an event and drawn live;
// NO_HIT while loading, in an event or with a stale pose (the body would
// stand where the remote no longer is).
func slotFlags(p *PState, scope Scope, live bool) uint8 {
	var sf uint8
	for _, b := range [...][2]uint8{
		{PSGuard, mailbox.RemoteSFGuard}, {PSCrouchDef, mailbox.RemoteSFCrouchDef}, {PSDead, mailbox.RemoteSFDead},
		{PSInEvent, mailbox.RemoteSFInEvent}, {PSSwim, mailbox.RemoteSFSwim}, {PSOnShip, mailbox.RemoteSFOnShip},
	} {
		if p.Flags&b[0] != 0 {
			sf |= b[1]
		}
	}
	away := p.Flags&(PSLoading|PSInEvent) != 0 || !live
	if away {
		sf |= mailbox.RemoteSFNoHit
	}
	if !away && p.Flags&PSDead == 0 && scope.InPlay && p.Stage == scope.Stage && p.Room == scope.Room {
		sf |= mailbox.RemoteSFTargetable
	}
	return sf
}

// slots lays out the block's slots: slot i is pose-feed slot i (the
// roster's LinkSlot), so PoseSlot = i. An owner change bumps the slot's
// gen (the REL recreates the body). settling marks the slots whose
// position is still more than SettleDist from the remote's (the smoother
// crossing a jump). arrived reports a remote whose load ended in the
// host's stage (Tick bumps the epoch for it).
func (r *RemoteHost) slots(now time.Time, scope Scope, views []posefeed.RemoteView) (out [mailbox.RemoteMaxSlots]mailbox.RemoteSlot, settling [mailbox.RemoteMaxSlots]bool, arrived bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range mailbox.MaxRemoteLinks {
		var v *posefeed.RemoteView
		for k := range views {
			if views[k].LinkSlot == i {
				v = &views[k]
			}
		}
		if v == nil {
			r.owners[i].player = 0
			continue
		}
		if r.owners[i].player != v.ID {
			r.owners[i].player = v.ID
			r.owners[i].gen++
		}
		s := mailbox.RemoteSlot{Active: true, PlayerID: v.ID, Pos: v.Pos, AngleY: v.RotY, EyeDY: r.cfg.EyeDY,
			CylR: r.cfg.CylR, CylH: r.cfg.CylH, PoseSlot: uint8(i), Gen: r.owners[i].gen}
		var d2 float32
		for k := range 3 {
			d := v.Target[k] - v.Pos[k]
			d2 += d * d
		}
		settling[i] = d2 > SettleDist*SettleDist
		ps, ok := r.pstates[v.ID]
		if !ok || now.Sub(ps.at) > PStateStale {
			// No stage: the REL parks the body (stage mismatch).
			s.SFlags = mailbox.RemoteSFNoHit
			out[i] = s
			continue
		}
		p := &ps.p
		loading := p.Flags&PSLoading != 0
		a, seen := r.arrive[v.ID]
		switch {
		case loading && !(seen && a.loading):
			// A load begins: the position now is the stage before's (no
			// Link sends one until the new stage spawns it). Wait for a
			// new one; it may come in before PSTATE's loading flag clears.
			a = arrival{stage: p.Stage, target: v.Target, loading: true}
		case loading:
			a.stage = p.Stage
		case a.loading:
			a.loading, a.stage, a.loaded = false, p.Stage, now
			arrived = arrived || p.Stage == scope.Stage
		case !seen || p.Stage != a.stage:
			// First sight, or a change seen in play: whatever position
			// came with it is the new stage's (SettleDist covers a slide).
			a = arrival{stage: p.Stage, target: v.Target, moved: true}
		}
		if v.Target != a.target || (!a.loading && now.Sub(a.loaded) > ArriveWait) {
			a.moved = true
		}
		r.arrive[v.ID] = a
		settling[i] = settling[i] || !a.moved
		if loading {
			// Loading: no stage, as for a stale PSTATE, so the REL neither
			// creates a body at the stage before's position nor runs down
			// its FULL wait; a body it has parks.
			s.SFlags = slotFlags(p, scope, v.Live)
			out[i] = s
			continue
		}
		s.Stage, s.Room, s.IFrames, s.ShieldAngleY = p.Stage, p.Room, p.IFrames, p.ShieldAngleY
		s.CutType, s.CutCount = p.CutType, p.CutCount
		if hc, ok := r.hitCuts[v.ID]; ok && now.Sub(hc.at) <= HitCutHold {
			s.CutType, s.CutCount = hc.cutType, hc.cutCount
		}
		s.SFlags = slotFlags(p, scope, v.Live)
		out[i] = s
	}
	return out, settling, arrived
}

// Tick runs the host side once at now: with the block on and relOK (the
// REL linked with this build, hostGate), it writes the block (scope the
// host's, views the session's roster published at viewsAt), reads the
// REL's tail and returns the PHITs to send for the ring entries new since
// the last Tick, each attacker named by its handle in listing (the host's
// last STATE). It also logs the PACKs that arrived.
func (r *RemoteHost) Tick(now time.Time, scope Scope, relOK bool, views []posefeed.RemoteView, viewsAt time.Time, listing *Listing) ([]*PHit, error) {
	r.logPAcks()
	if !r.cfg.On || !relOK {
		return nil, nil
	}
	stageChanged := scope.Stage != r.stage
	// A new WWRP epoch (rl_intake, remote.c: the REL clears its latches,
	// every slot's create tries and full attempt) on a new stage, on the
	// host's return to play (a void-out or Game Over reload of the same
	// stage: the teardown deleted the bodies, and remote.c's own reset
	// keys on the stage name only) and on a remote's arrival in the host's
	// stage (its body was retired while it loaded). Without the last two a
	// remote void-out or a host reload spends one of RL_MAX_TRIES, and the
	// third leaves the slot BERR_TRIES, with no body, for the rest of the
	// stage (coop-slice-drc.md A1; lava void-outs in 9 of DRC's 16 rooms).
	reloaded := scope.InPlay && !r.inPlay
	r.inPlay = scope.InPlay
	if stageChanged || reloaded {
		r.stage = scope.Stage
		r.blockEpoch++
	}
	if viewsAt.IsZero() || now.Sub(viewsAt) > RosterStale {
		views = nil
	}
	b := r.cfg.block(mailbox.RemoteRoleHost, nextSeq(&r.seq), r.blockEpoch)
	var settling [mailbox.RemoteMaxSlots]bool
	var arrived bool
	b.Slots, settling, arrived = r.slots(now, scope, views)
	if arrived && !stageChanged && !reloaded {
		r.blockEpoch++
		b.Epoch = r.blockEpoch
	}
	b.Flags2 = r.cfg.Flags2
	if r.cfg.Flags&mailbox.RemoteFlagFullBodies != 0 && r.cfg.HeapGate {
		// The verdict goes out with this block, from the status the last
		// Tick read; a gate read error shuts it (NO_SAMPLE) and is logged.
		was := r.gate.reason
		var gerr error
		b.FullAllow, b.HeapReason, gerr = r.gate.tick(r.m, now, scope, stageChanged, &r.Stats.LastStatus, r.statusAt, &b.Slots, &settling)
		switch {
		case gerr != nil && !r.gateErrLogged:
			r.gateErrLogged = true
			log.Logf(r.rep, log.Warn, "enemy remote: heap gate (the gate stays shut while reads fail; logged once): %v", gerr)
		case gerr == nil:
			r.gateErrLogged = false
		}
		r.gate.logChange(r.rep, was)
	}
	if err := mailbox.WriteRemoteBlock(r.m, b); err != nil {
		return nil, err
	}
	r.wrote = true
	r.Stats.Writes++

	st, err := mailbox.ReadRemoteStatus(r.m)
	if err != nil {
		return nil, err
	}
	r.Stats.LastStatus, r.statusAt = st, now
	if !r.haveHead || st.HitRingHead < r.lastHead {
		// The first read (hits from before this Go are not ours to send)
		// or the REL relinked (its ring counts from 0 again).
		if r.haveHead {
			r.lastHead = 0
		} else {
			r.haveHead, r.lastHead = true, st.HitRingHead
		}
	}
	var out []*PHit
	if hits := st.HitsSince(r.lastHead); len(hits) > 0 {
		// The REL's frame now, for the stale gate (the ring entries carry
		// the frame they were detected at, the same counter). Not
		// AckFrame: after a Go stall that is the frame of the consume
		// before it, which would make stale hits look fresh.
		rel, err := mailbox.ReadREL(r.m)
		if err != nil {
			return nil, err
		}
		g := phitGate{now: now, relFrame: rel.Frames, settling: settling}
		for _, h := range hits {
			if p := r.phit(&h, b, scope, listing, &g); p != nil {
				out = append(out, p)
			}
		}
	}
	r.lastHead = st.HitRingHead
	if now.After(r.logNext) {
		r.logNext = now.Add(RemoteLogEvery)
		gate := ""
		if r.cfg.Flags&mailbox.RemoteFlagFullBodies != 0 {
			gate = " heap " + r.gate.line()
			if !r.cfg.HeapGate {
				gate = " heap gate off (FULL_ALLOW 0)"
			}
		}
		log.Logf(r.rep, log.Info, "enemy remote: seq %d ack %d bodies %s counters %s phits %d packs %d%s", r.seq, st.AckSeq,
			bodiesLine(&st), countersLine(&st), r.Stats.PHits, r.Stats.PAcks, gate)
	}
	return out, nil
}

// phitGate is what one Tick's ring entries are judged against.
type phitGate struct {
	now      time.Time
	relFrame uint32                       // the REL's frame counter at the read
	settling [mailbox.RemoteMaxSlots]bool // slots() verdict: the body trails its player
}

// phit turns a ring entry into the PHIT for its body's player, or nil
// (dropped and counted): an attacker of no enabled kind and not ALLOW in
// the policy (the host's bombs, world hazards, denied or unswapped
// enemies: the joiner's own copy of a shared hazard hits it already,
// design §11.8), one with no actor (pid 0), a slot nobody owns, an owner
// whose enemy client is unknown (no PSTATE yet), or within PHitRateFrames
// of the last PHIT for that (client, attacker). The PHIT names the
// attacker by its handle, looked up in listing by the ring entry's
// attacker pid (mailbox.RemoteHit.PID, the ring's +0x04): keyed and
// unkeyed alike (design closed_before_build "PHIT owner"). One the host
// does not list has no mirror on the client: it goes with
// NoMirrorHandle, and the client REL replays it with its keeper as the
// stand-in At owner (the REL's HF_NOT_MIRROR/HF_NOT_KEYED bits no longer
// decide it).
//
// The sanity gates (PHitStaleFrames and the rest) drop, too: fields no
// hit has, an entry older than PHitStaleFrames, an owner whose PSTATE is
// stale, loading or names another stage or room than the host's, a body
// that trails its player past SettleDist (the player is not where it was
// hit), and a client over its burst. None is retried.
func (r *RemoteHost) phit(h *mailbox.RemoteHit, b *mailbox.RemoteBlock, scope Scope, listing *Listing, g *phitGate) *PHit {
	if !phitSane(h.Spl, h.RelHit, h.RVec) {
		r.Stats.DropBad++
		r.warnDrop("bad", fmt.Sprintf("a ring hit with SPL %d, hit point %v: not a hit, dropped", h.Spl, h.RelHit))
		return nil
	}
	if age := int32(g.relFrame - h.Frame); age > PHitStaleFrames {
		// A negative age (an entry stamped after the frame read) is a
		// race of one frame, not stale. A relink restarts both counters.
		r.Stats.DropStale++
		return nil
	}
	if h.PID == 0 || (!r.Kinds.Enabled(uint16(h.Proc)) && !r.allowed(h.Proc)) {
		r.Stats.DropNotAllowed++
		r.warnDrop("not_allowed", fmt.Sprintf("a proc 0x%X attacker hit a remote body; only the mirrored kinds and ALLOW procs are sent", uint16(h.Proc)))
		return nil
	}
	setID := listing.HandleOfPID(h.PID)
	standIn := setID == NoMirrorHandle
	if int(h.Slot) >= mailbox.MaxRemoteLinks || r.owners[h.Slot].player == 0 {
		r.Stats.DropNoOwner++
		return nil
	}
	player := r.owners[h.Slot].player
	r.mu.Lock()
	ps, ok := r.pstates[player]
	r.mu.Unlock()
	if !ok || ps.p.Client == 0 {
		r.Stats.DropNoClient++
		return nil
	}
	if g.now.Sub(ps.at) > PStateStale || ps.p.Flags&PSLoading != 0 || ps.p.Stage != scope.Stage || ps.p.Room != scope.Room {
		// The client would drop it at its own stage and room gate; this
		// saves the round trip and keeps it out of the client's queue.
		r.Stats.DropStage++
		return nil
	}
	if g.settling[h.Slot] {
		r.Stats.DropRange++
		return nil
	}
	client := ps.p.Client
	key := phitKey{client, h.PID}
	if last, ok := r.lastPHit[key]; ok && h.Frame-last < PHitRateFrames {
		r.Stats.DropRate++
		return nil
	}
	bk := r.phitBurst[client]
	if bk == nil {
		bk = &phitBucket{}
		r.phitBurst[client] = bk
	}
	if !bk.take(g.now) {
		r.Stats.DropBurst++
		r.warnDrop("burst", fmt.Sprintf("over %d PHITs at once (%d a second) to one client; the excess is dropped", PHitBurst, PHitPerSec))
		return nil
	}
	r.lastPHit[key] = h.Frame
	r.phitSeq[client]++
	p := &PHit{Client: client, Epoch: r.epoch, Stage: scope.Stage, Room: scope.Room, Atp: h.Atp, SetID: setID,
		Seq: r.phitSeq[client], Se: h.Se, HitMark: h.HitMark, AtType: h.AtType, Spl: h.Spl, RelHit: h.RelHit, RVec: h.RVec,
		HostFrame: h.Frame}
	if len(p.Stage) > StageLen {
		p.Stage = p.Stage[:StageLen]
	}
	if h.HFlags&mailbox.RemoteHFShield != 0 {
		p.Flags |= PHitHostShield
	}
	if b.Slots[h.Slot].SFlags&mailbox.RemoteSFGuard != 0 {
		p.Flags |= PHitHostGuard
	}
	r.Stats.PHits++
	if standIn {
		r.Stats.StandIn++
	}
	return p
}

// allowed reports whether proc has an ALLOW entry (and no DENY) in the
// block's policy.
func (r *RemoteHost) allowed(proc int16) bool {
	for _, e := range r.cfg.Policy {
		if e.Proc == proc {
			return e.Flags&mailbox.RemotePFAllow != 0 && e.Flags&mailbox.RemotePFDeny == 0
		}
	}
	return false
}

func (r *RemoteHost) warnDrop(kind, msg string) {
	if !r.dropWarned[kind] {
		r.dropWarned[kind] = true
		log.Logf(r.rep, log.Info, "enemy remote: %s (logged once)", msg)
	}
}

func (r *RemoteHost) logPAcks() {
	r.mu.Lock()
	packs := r.packs
	r.packs = nil
	r.mu.Unlock()
	for _, a := range packs {
		r.Stats.PAcks++
		log.Logf(r.rep, log.Info, "enemy remote: PACK client %d seq %d %s/%s life %d %+d frame %d", a.Client, a.Seq,
			PAckResultName(a.Result), PAckDetailName(a.Detail), a.LifeBefore, a.LifeDelta, a.ClientFrame)
	}
}

// Shutdown writes magic 0 when the block was ever written.
func (r *RemoteHost) Shutdown() error {
	if !r.wrote {
		return nil
	}
	return mailbox.ClearRemoteBlock(r.m)
}

// remoteBErrNames name the RemoteBErr* codes by value.
var remoteBErrNames = [...]string{"none", "slot", "block", "vtable", "host_link", "cyl", "create", "lost", "tries", "gone",
	"heap_denied", "phase_timeout", "heap_errors", "profile", "shanm_alloc", "full_vtable"}

func bErrName(e uint8) string {
	if int(e) < len(remoteBErrNames) {
		return remoteBErrNames[e]
	}
	return fmt.Sprintf("err%d", e)
}

// bodiesLine is each body's state, err (with its name when set), hits,
// latch entries, tries and kind (k0 lite, k1 full).
func bodiesLine(st *mailbox.RemoteStatus) string {
	s := ""
	for i := range mailbox.MaxRemoteLinks {
		b := &st.Bodies[i]
		e := fmt.Sprint(b.Err)
		if b.Err != mailbox.RemoteBErrNone {
			e += "(" + bErrName(b.Err) + ")"
		}
		s += fmt.Sprintf("[%d:s%d e%s h%d l%d t%d k%d]", i, b.State, e, b.Hits, b.LatchedBy, b.Tries, b.Kind)
	}
	return s
}

func countersLine(st *mailbox.RemoteStatus) string {
	parts := make([]string, 0, mailbox.RemoteCounters)
	for i, n := range st.Counters {
		parts = append(parts, fmt.Sprintf("%s=%d", mailbox.RemoteCounterNames[i], n))
	}
	return strings.Join(parts, " ")
}

// --- client -------------------------------------------------------------------------

// RemoteClient is a joiner's remote-Link side. OnPHit takes the read
// loop's PHITs (safe for concurrent use with Tick).
type RemoteClient struct {
	m   mem.Mem
	rep log.Reporter
	brk *write.Breaker
	cfg RemoteConfig

	mu    sync.Mutex
	inbox []inboxPHit

	pstateAt   time.Time
	pstateLast []byte
	blockSeq   uint32
	applySeq   uint32
	queue      []*pendingApply
	last       mailbox.RemoteApply // the record the block carries with the queue empty
	epoch      uint32              // the block's epoch: the mirror's (ClientGate.Epoch)
	wrote      bool

	// Owner translates a PHIT's attacker handle into the apply record's
	// owner id when the request is written (Mirror.OwnerOf: a keyed local
	// setID, OwnerSlotBase|j for an unkeyed copy in block slot j, else
	// NoMirrorSetID); never the raw handle, which starts at 1 and would
	// alias keyed setIDs. Nil: every attacker is a stand-in.
	Owner func(handle uint16) uint16

	Stats RemoteClientStats
}

// inboxPHit is a PHIT from the read loop and when it arrived.
type inboxPHit struct {
	p  *PHit
	at time.Time
}

type pendingApply struct {
	p      PHit
	owner  uint16 // the translated attacker (RemoteClient.Owner), set at the write
	seq    uint32
	wrote  bool
	frame  uint32    // the REL frame at the write
	at     time.Time // the wall clock at the write
	result bool      // a result was seen
	settle uint32    // the REL frame the result was first seen at
}

// RemoteClientStats are counters a probe or test reads.
type RemoteClientStats struct {
	PStates, PHits, Applied, Dropped uint64
	// DroppedStale and DroppedBad are the sanity gates' share of Dropped:
	// held past PHitClientStale before a Tick took them, and fields no hit
	// has (phitSane). Both answer PADGoGate: wwremote.h has no detail
	// code of their own yet.
	DroppedStale, DroppedBad uint64
	LastStatus               mailbox.RemoteStatus
}

// NewRemoteClient returns a joiner's side over m with cfg; brk is the
// mirror's breaker (nil: none). The apply seq starts at start's low 16
// bits << 16, so a client Go restarted against a still-linked REL never
// reuses a seq the REL already acked (HitApplier's rule).
func NewRemoteClient(m mem.Mem, rep log.Reporter, brk *write.Breaker, cfg RemoteConfig, start time.Time) *RemoteClient {
	return &RemoteClient{m: m, rep: rep, brk: brk, cfg: cfg, applySeq: uint32(start.Unix()&0xFFFF) << 16}
}

// OnPHit queues a PHIT for the next Tick, arrived now.
func (r *RemoteClient) OnPHit(p *PHit) { r.OnPHitAt(p, time.Now()) }

// OnPHitAt queues a PHIT that arrived at at.
func (r *RemoteClient) OnPHitAt(p *PHit, at time.Time) {
	r.mu.Lock()
	r.inbox = append(r.inbox, inboxPHit{p: p, at: at})
	r.mu.Unlock()
}

// ClientGate is what the mirror knows that RemoteClient.Tick gates on.
type ClientGate struct {
	RELOK     bool   // the contract holds and the REL is linked with this build (Mirror.RemoteGate)
	HostEpoch uint32 // the epoch of the latest host STATE
	HaveHost  bool   // a STATE has arrived
	Frame     uint32 // spawn_trigger, the PACK's client frame
	Epoch     uint32 // the mirror's epoch (a save-state load or relink bumps it), the client block's
}

// Tick runs the joiner's side once at now in scope: the PSTATE to send
// (every PStateEvery, or at once on a change), each PHIT gated and queued
// (a Go drop answers a PACK at once), the head written into the block and
// its result polled. It returns the payloads to send.
func (r *RemoteClient) Tick(now time.Time, scope Scope, g ClientGate) ([][]byte, error) {
	var out [][]byte
	ps, err := ReadPState(r.m, scope, g.HostEpoch)
	if err != nil {
		return nil, err
	}
	if b, err := EncodePState(ps); err == nil {
		if !bytesEqual(b, r.pstateLast) || now.Sub(r.pstateAt) >= PStateEvery {
			r.pstateAt, r.pstateLast = now, b
			r.Stats.PStates++
			out = append(out, b)
		}
	}

	r.mu.Lock()
	inbox := r.inbox
	r.inbox = nil
	r.mu.Unlock()
	for _, in := range inbox {
		p := in.p
		r.Stats.PHits++
		// The sanity gates first (a host of an older build has none of its
		// own): held too long, or fields no hit has. Dropped, not retried.
		if now.Sub(in.at) > PHitClientStale {
			r.Stats.DroppedStale++
			out = append(out, r.drop(p, PADGoGate, g.Frame))
			continue
		}
		if !phitSane(p.Spl, p.RelHit, p.RVec) {
			r.Stats.DroppedBad++
			out = append(out, r.drop(p, PADGoGate, g.Frame))
			continue
		}
		if detail, ok := r.gate(p, scope, g); !ok {
			out = append(out, r.drop(p, detail, g.Frame))
			continue
		}
		if len(r.queue) >= applyQueueDepth {
			out = append(out, r.drop(p, PADAborted, g.Frame))
			continue
		}
		r.queue = append(r.queue, &pendingApply{p: *p, seq: nextSeq(&r.applySeq)})
	}

	if !r.cfg.On || !g.RELOK {
		// No block: whatever was queued can no longer be applied.
		for _, q := range r.queue {
			out = append(out, r.drop(&q.p, PADGoGate, g.Frame))
		}
		r.queue = nil
		return out, nil
	}
	if len(r.queue) > 0 && !r.queue[0].wrote {
		rel, err := mailbox.ReadREL(r.m)
		if err != nil {
			return out, err
		}
		q := r.queue[0]
		q.wrote, q.frame, q.at, q.owner = true, rel.Frames, now, r.ownerOf(q.p.SetID)
		r.last = applyRecord(q)
	}
	r.epoch = g.Epoch
	if err := r.writeBlock(); err != nil {
		return out, err
	}
	if len(r.queue) > 0 {
		a, err := r.poll(now, g.Frame)
		if err != nil {
			return out, err
		}
		if a != nil {
			out = append(out, EncodePAck(a))
		}
	}
	return out, nil
}

// gate is a PHIT's Go gate (§8.5): the APPLY flag on, this game in play,
// the PHIT from the host whose STATE this mirror follows, and the host's
// stage and room this game's.
func (r *RemoteClient) gate(p *PHit, scope Scope, g ClientGate) (uint8, bool) {
	switch {
	case !r.cfg.On || r.cfg.Flags&mailbox.RemoteFlagApply == 0 || !g.RELOK:
		return PADGoGate, false
	case !scope.InPlay:
		return PADNotInPlay, false
	case !g.HaveHost || p.Epoch != g.HostEpoch:
		return PADStaleEpoch, false
	case p.Stage != scope.Stage || p.Room != scope.Room:
		return PADGoGate, false
	}
	return PADNone, true
}

func (r *RemoteClient) drop(p *PHit, detail uint8, frame uint32) []byte {
	r.Stats.Dropped++
	return EncodePAck(&PAck{Result: PAckDropped, Seq: p.Seq, Detail: detail, ClientFrame: frame})
}

// ownerOf translates a PHIT's attacker handle (Owner), NoMirrorSetID for
// NoMirrorHandle or without a translator.
func (r *RemoteClient) ownerOf(handle uint16) uint16 {
	if r.Owner == nil || handle == NoMirrorHandle || handle == HandleNone {
		return NoMirrorSetID
	}
	return r.Owner(handle)
}

// LockedSlots reports the block slots an apply the REL has not acked yet
// names (OwnerSlotBase|j): the mirror must not give slot j to another
// enemy before the REL resolved the owner (design closed_before_build
// "PHIT owner").
func (r *RemoteClient) LockedSlots() (out [MaxSlots]bool) {
	if r == nil {
		return out
	}
	for _, q := range r.queue {
		if q.wrote && q.owner >= OwnerSlotBase && q.owner < OwnerSlotBase+MaxSlots {
			out[q.owner-OwnerSlotBase] = true
		}
	}
	return out
}

// applyRecord lays q out as the block's apply record: the translated
// owner id (q.owner, never the PHIT's raw handle), At SPrm Set|VsPlayer
// (mailbox.RemoteApplyAtSPrm: it cannot hit mirrored enemies), the PHIT's
// flags as RemoteAF* (the same bits).
func applyRecord(q *pendingApply) mailbox.RemoteApply {
	p := &q.p
	return mailbox.RemoteApply{Seq: q.seq, SetID: q.owner, Atp: p.Atp, Se: p.Se, HitMark: p.HitMark, Spl: p.Spl,
		AtSPrm: mailbox.RemoteApplyAtSPrm, Flags: p.Flags & (mailbox.RemoteAFHostShield | mailbox.RemoteAFHostGuard),
		AtType: p.AtType, RelHit: p.RelHit, RVec: p.RVec}
}

// writeBlock writes the client's block: role client, no slots, r.last as
// the apply record (the head request, or the one the REL took last, whose
// seq it already acked, so a keepalive never starts a request).
func (r *RemoteClient) writeBlock() error {
	if r.brk != nil && r.brk.Off() {
		return r.brk.Err()
	}
	b := r.cfg.block(mailbox.RemoteRoleClient, nextSeq(&r.blockSeq), r.epoch)
	// The client has no bodies: its policy and FF denylist go unused.
	b.Policy, b.FFDeny = nil, nil
	b.Apply = r.last
	if err := mailbox.WriteRemoteBlock(r.m, b); err != nil {
		if r.brk != nil {
			r.brk.Fault(write.FaultRefusal, "remote-Link block: "+err.Error())
		}
		return err
	}
	r.wrote = true
	return nil
}

// poll reads the REL's apply result for the head request: once apply_ack
// names its seq with a result, it waits applySettleFrames more REL frames
// (the result can still turn EARLY_OUT and life_after lands then), and
// returns the PACK; a request the REL never took in hitAckFrames REL
// frames (or hitAckBackstop) is dropped ABORTED.
func (r *RemoteClient) poll(now time.Time, frame uint32) (*PAck, error) {
	q := r.queue[0]
	st, err := mailbox.ReadRemoteStatus(r.m)
	if err != nil {
		return nil, err
	}
	r.Stats.LastStatus = st
	rel, err := mailbox.ReadREL(r.m)
	if err != nil {
		return nil, err
	}
	a := &PAck{Seq: q.p.Seq, ClientFrame: frame}
	switch {
	case st.ApplyAck == q.seq && st.ApplyResult.Result != PAckNone:
		if !q.result {
			q.result, q.settle = true, rel.Frames
		}
		if rel.Frames-q.settle < applySettleFrames && rel.Frames >= q.settle && now.Sub(q.at) < hitAckBackstop {
			return nil, nil
		}
		res := st.ApplyResult
		a.Result, a.Detail = res.Result, res.Detail
		a.LifeBefore = uint8(min(st.LifeBefore, 255))
		a.LifeDelta = int16(int32(st.LifeAfter) - int32(st.LifeBefore))
		if res.Result == PAckApplied || res.Result == PAckGuarded {
			r.Stats.Applied++
		}
	case rel.Frames < q.frame, rel.Frames-q.frame >= hitAckFrames, now.Sub(q.at) >= hitAckBackstop:
		a.Result, a.Detail = PAckDropped, PADAborted
		r.Stats.Dropped++
		log.Logf(r.rep, log.Warn, "enemy remote: the REL did not take apply %d (PHIT seq %d)", q.seq, q.p.Seq)
	default:
		return nil, nil
	}
	r.queue = r.queue[1:]
	if len(r.queue) > 0 {
		next := r.queue[0]
		next.wrote, next.frame, next.at, next.owner = true, rel.Frames, now, r.ownerOf(next.p.SetID)
		r.last = applyRecord(next)
		if err := r.writeBlock(); err != nil {
			return a, err
		}
	}
	return a, nil
}

// Shutdown writes magic 0 when the block was ever written.
func (r *RemoteClient) Shutdown() error {
	if !r.wrote {
		return nil
	}
	return mailbox.ClearRemoteBlock(r.m)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
