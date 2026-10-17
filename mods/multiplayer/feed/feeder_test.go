package feed

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar/avatartest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// recorder is a log.Reporter that keeps every line.
type recorder struct{ lines []string }

func (r *recorder) Log(_ log.Level, msg string) { r.lines = append(r.lines, msg) }

func (r *recorder) has(sub string) bool {
	for _, l := range r.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// poseBufBase is where the fake C side allocates pose_bufs (MEM1).
const poseBufBase = 0x80C00000

// cside stands in for the C side of the pose feed: while shadow_mode is
// 5, every slot without a pose_buf (state != 1) gets a fresh one, as the
// next mode-5 draw lazy-allocates it.
type cside struct {
	ram    *avatartest.RAM
	allocs int
}

func (c *cside) frame() {
	if c.ram.U8(mailbox.Mailbox+mailbox.OffShadowMode) != avatar.ModePoseFeed {
		return
	}
	for s := 0; s < avatar.Cap; s++ {
		if c.ram.U8(mailbox.Mailbox+avatar.OffPoseBufState(s)) == 1 {
			continue
		}
		c.allocs++
		c.ram.Put32(mailbox.Mailbox+avatar.OffPoseBufPtr(s), poseBufBase+uint32(c.allocs)*0x1000)
		c.ram.Write(mailbox.Mailbox+avatar.OffPoseBufState(s), []byte{1})
	}
}

// saveStateLoad is what loading a save state does to the mailbox: the
// snapshot has shadow_mode = 0 and no pose_bufs.
func (c *cside) saveStateLoad() {
	c.ram.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{avatar.ModeOff})
	for s := 0; s < avatar.Cap; s++ {
		c.ram.Put32(mailbox.Mailbox+avatar.OffPoseBufPtr(s), 0)
		c.ram.Write(mailbox.Mailbox+avatar.OffPoseBufState(s), []byte{0})
	}
}

// newTestFeeder is a Feeder over fresh fake RAM whose arming poll runs a
// C-side frame instead of sleeping, already connected (session 1).
func newTestFeeder(t *testing.T) (*Feeder, *cside, *recorder) {
	t.Helper()
	t.Setenv("WW_POSE_RAW", "")
	for _, k := range []string{"WW_LINK2_OFFSET_X", "WW_LINK2_OFFSET_Y", "WW_LINK2_OFFSET_Z"} {
		t.Setenv(k, "")
	}
	c := &cside{ram: avatartest.NewRAM()}
	c.stage("sea", avatartest.Link, false) // in play, as most tests assume
	rep := &recorder{}
	f := NewFeeder(c.ram, "Self", rep)
	f.after = func(time.Duration) <-chan time.Time {
		c.frame()
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	f.Connected(7)
	return f, c, rep
}

// stage puts this game in stage name with Link's actor at link (0: none),
// a stage change pending when changing.
func (c *cside) stage(name string, link uint32, changing bool) {
	var b [addr.StageNameLen]byte
	copy(b[:], name)
	c.ram.Write(addr.CurStageName, b[:])
	c.ram.Put32(addr.PlayerPtr, link)
	en := byte(0)
	if changing {
		en = 1
	}
	c.ram.Write(addr.NextStageEnable, []byte{en})
}

// remote is player id at (x, 0, 0); withPose adds a whole Link pose (every
// byte id) and a face.
func remote(id byte, x float32, withPose bool, now time.Time) network.RemotePlayer {
	rp := network.RemotePlayer{
		ID:       id,
		Name:     fmt.Sprintf("P%d", id),
		Position: &network.PlayerPosition{PosX: x, RotY: 0x4000},
	}
	if withPose {
		rp.PoseJoints = avatar.JointCount
		rp.PoseMatrices = bytes.Repeat([]byte{id}, avatar.JointCount*avatar.MtxBytes)
		rp.FaceState = bytes.Repeat([]byte{0xF0 | id}, player.FaceBytes)
		rp.LastPose = now
	}
	return rp
}

func (c *cside) u32(a uint32) uint32 {
	b, _ := c.ram.Read(a, 4)
	return binary.BigEndian.Uint32(b)
}

// actorActive reports actor-puppet slot idx's active flag.
func (c *cside) actorActive(idx int) bool { return c.u32(SlotAddr(idx, mailbox.PuppetOffActive)) == 1 }

// actorX is actor-puppet slot idx's position X.
func (c *cside) actorX(idx int) float32 {
	return math.Float32frombits(c.u32(SlotAddr(idx, mailbox.PuppetOffPosX)))
}

// poseSeq is link slot s's pose_seq.
func (c *cside) poseSeq(s int) byte { return c.ram.U8(mailbox.Mailbox + avatar.OffPoseSeq(s)) }

// posePtr is link slot s's pose_buf pointer.
func (c *cside) posePtr(s int) uint32 { return c.u32(mailbox.Mailbox + avatar.OffPoseBufPtr(s)) }

// wrotePoseOf reports whether link slot s's pose_buf holds player id's
// pose (the rotation bytes of joint 0 are id; ApplyAt moves only the
// translation column).
func (c *cside) wrotePoseOf(s int, id byte) bool {
	b, _ := c.ram.Read(c.posePtr(s), 4)
	return c.poseSeq(s) != 0 && bytes.Equal(b, []byte{id, id, id, id})
}

func TestFeederSlotAssignment(t *testing.T) {
	for n := 1; n <= 4; n++ {
		t.Run(fmt.Sprintf("%d remotes", n), func(t *testing.T) {
			f, c, _ := newTestFeeder(t)
			now := time.Unix(1000, 0)
			var rs []network.RemotePlayer
			for i := 1; i <= n; i++ {
				rs = append(rs, remote(byte(i), float32(100*i), true, now))
			}
			// The co-located broadcast twin is filtered out.
			self := remote(9, 5, true, now)
			self.Name = "Self"
			rs = append(rs, self)
			f.Feed(context.Background(), rs, now)

			wantLink := map[byte]int{}
			for i := 1; i <= n && i <= avatar.Cap; i++ {
				wantLink[byte(i)] = i - 1
			}
			if got := f.LinkSlots(); fmt.Sprint(got) != fmt.Sprint(wantLink) {
				t.Errorf("link slots = %v, want %v", got, wantLink)
			}
			for id, s := range wantLink {
				if !c.wrotePoseOf(s, id) {
					t.Errorf("link slot %d: no pose of player %d (seq %d)", s, id, c.poseSeq(s))
				}
				if fs := c.ram.U8(mailbox.Mailbox + avatar.OffFaceState(s)); fs != 0xF0|id {
					t.Errorf("link slot %d face = %#x, want %#x", s, fs, 0xF0|id)
				}
			}
			for s := n; s < avatar.Cap; s++ {
				if c.poseSeq(s) != 0 {
					t.Errorf("unused link slot %d has pose_seq %d", s, c.poseSeq(s))
				}
			}
			// Every remote holds an actor-puppet slot carrying its
			// position, in arrival order; the twin holds none.
			if len(f.remoteToSlot) != n {
				t.Errorf("actor slots = %v, want %d", f.remoteToSlot, n)
			}
			for i := 1; i <= n; i++ {
				idx, ok := f.remoteToSlot[byte(i)]
				if !ok || idx != i-1 {
					t.Errorf("player %d actor slot = %d %v, want %d", i, idx, ok, i-1)
					continue
				}
				if x := c.actorX(idx); x != float32(100*i) {
					t.Errorf("actor slot %d X = %v, want %v", idx, x, 100*i)
				}
			}
			for i := 0; i < avatar.Cap && i < n; i++ {
				if c.actorActive(i) {
					t.Errorf("actor slot %d active under a Link-driven remote", i)
				}
			}
		})
	}
}

// TestFeederActorPuppetFallback: with both link slots taken, a third
// player stays on its actor puppet (KAMOME / NPC_OB1), and moves to a
// link slot when one frees up.
func TestFeederActorPuppetFallback(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)

	// Player 3 is first seen before its first pose (position only), and
	// stays poseless: its actor puppet comes up after actorGrace, not before.
	f.Feed(ctx, []network.RemotePlayer{
		remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 300, false, now),
	}, now)
	if c.actorActive(2) {
		t.Fatal("player 3's actor puppet came up before actorGrace")
	}
	now = now.Add(actorGrace)
	f.Feed(ctx, []network.RemotePlayer{
		remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 300, false, now),
	}, now)
	if !c.actorActive(2) || !rep.has("slot 2 := player 3 (P3)") {
		t.Fatalf("player 3 has no active actor puppet (active=%v)", c.actorActive(2))
	}

	// Its poses arrive: no link slot is free, so the actor puppet stays
	// up and keeps following it (lerped).
	for i := 0; i < 3; i++ {
		now = now.Add(16 * time.Millisecond)
		f.Feed(ctx, []network.RemotePlayer{
			remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 400, true, now),
		}, now)
	}
	if _, ok := f.LinkSlots()[3]; ok {
		t.Fatalf("player 3 got a link slot beyond avatar.Cap: %v", f.LinkSlots())
	}
	if !c.actorActive(2) {
		t.Error("player 3's actor puppet went down while it has no link slot")
	}
	if x := c.actorX(2); x <= 300 || x >= 400 {
		t.Errorf("player 3's actor X = %v, want lerped between 300 and 400", x)
	}
	if c.actorActive(0) || c.actorActive(1) {
		t.Error("an actor puppet is up under a Link-driven remote")
	}

	// Player 1 leaves: its link slot frees at the end of that tick, and
	// player 3 takes it over from its actor puppet on the next.
	for i := 0; i < 2; i++ {
		now = now.Add(16 * time.Millisecond)
		f.Feed(ctx, []network.RemotePlayer{remote(2, 200, true, now), remote(3, 400, true, now)}, now)
	}
	if got := f.LinkSlots()[3]; got != 0 {
		t.Fatalf("player 3 link slot = %v, want 0 (%v)", got, f.LinkSlots())
	}
	if !c.wrotePoseOf(0, 3) {
		t.Error("link slot 0 does not hold player 3's pose")
	}
	if c.actorActive(2) {
		t.Error("player 3's actor puppet is still up beside its Link")
	}
	if _, ok := f.remoteToSlot[1]; ok {
		t.Error("player 1 kept its actor slot after leaving")
	}
}

// TestFeederNoActorWhenPoseFollows: a remote first seen with its position
// alone (the pose is a separate message, so this is the usual first tick
// of a Link that just appeared) whose pose follows within actorGrace
// never gets its actor puppet: no KAMOME spawned only to be deleted.
func TestFeederNoActorWhenPoseFollows(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	everActive := false
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
	everActive = everActive || c.actorActive(0)
	for i := 0; i < 3; i++ {
		now = now.Add(16 * time.Millisecond)
		f.Feed(ctx, []network.RemotePlayer{remote(1, 100, i > 0, now)}, now)
		everActive = everActive || c.actorActive(0)
	}
	now = now.Add(2 * actorGrace)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now)}, now)
	everActive = everActive || c.actorActive(0)
	for _, l := range rep.lines {
		if strings.HasPrefix(l, "slot 0 := player 1") {
			everActive = true
		}
	}
	if everActive {
		t.Error("player 1's actor puppet came up though its pose followed its position")
	}
	if !c.wrotePoseOf(0, 1) {
		t.Error("player 1's pose is not in link slot 0")
	}
}

// TestFeederActorForThirdPoseRemote: a third remote that sends its pose
// from the start has no link slot to get, so its actorGrace wait runs out
// and it gets its actor puppet; until then it has none.
func TestFeederActorForThirdPoseRemote(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	tick := func() {
		f.Feed(ctx, []network.RemotePlayer{
			remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 300, true, now),
		}, now)
	}
	tick()
	now = now.Add(actorGrace - time.Millisecond)
	tick()
	if c.actorActive(2) {
		t.Fatal("player 3's actor puppet came up before actorGrace")
	}
	now = now.Add(time.Millisecond)
	tick()
	if !c.actorActive(2) || !rep.has("slot 2 := player 3 (P3)") {
		t.Fatal("player 3 (beyond avatar.Cap, pose from the start) has no actor puppet")
	}
	if c.actorActive(0) || c.actorActive(1) {
		t.Error("an actor puppet is up under a Link-driven remote")
	}
}

// TestFeederNoActorOutOfPlay: an actor puppet never comes up while this
// game is on the title screen, on file select, without Link or with a
// stage change pending; it comes up actorGrace after the game enters a
// play stage (time out of play does not count toward the grace).
func TestFeederNoActorOutOfPlay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stage    string
		link     uint32
		changing bool
	}{
		{"title", addr.StageTitle, avatartest.Link, false},
		{"file select", addr.StageFileSelect, avatartest.Link, false},
		{"no stage", "", avatartest.Link, false},
		{"no Link", "sea", 0, false},
		{"stage change", "sea", avatartest.Link, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, _ := newTestFeeder(t)
			c.stage(tc.stage, tc.link, tc.changing)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
			now = now.Add(2 * actorGrace)
			f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
			if c.actorActive(0) {
				t.Fatal("actor puppet came up out of play")
			}
			c.stage("M_Dai", avatartest.Link, false)
			now = now.Add(16 * time.Millisecond)
			f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
			if c.actorActive(0) {
				t.Fatal("actor puppet came up on the first in-play tick (out-of-play time counted toward the grace)")
			}
			// The clock restarts on every out-of-play tick, so the grace
			// runs from the last one (16 ms before the first in-play tick).
			now = now.Add(actorGrace - 16*time.Millisecond - time.Millisecond)
			f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
			if c.actorActive(0) {
				t.Fatal("actor puppet came up less than actorGrace after entering play")
			}
			now = now.Add(time.Millisecond)
			f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
			if !c.actorActive(0) {
				t.Error("actor puppet did not come up actorGrace after entering play")
			}
		})
	}
}

// TestFeederActorDownOutOfPlay: an actor puppet that is up goes down when
// this game leaves play (reset or quit to the title, file select, a stage
// change), and comes back actorGrace after the game is in play again.
func TestFeederActorDownOutOfPlay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stage    string
		link     uint32
		changing bool
	}{
		{"title", addr.StageTitle, avatartest.Link, false},
		{"file select", addr.StageFileSelect, avatartest.Link, false},
		{"no Link", "sea", 0, false},
		{"stage change", "sea", avatartest.Link, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, _ := newTestFeeder(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			feed := func() { f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now) }
			feed()
			now = now.Add(actorGrace)
			feed()
			if !c.actorActive(0) {
				t.Fatal("actor puppet did not come up in play")
			}
			c.stage(tc.stage, tc.link, tc.changing)
			now = now.Add(16 * time.Millisecond)
			feed()
			if c.actorActive(0) {
				t.Fatal("actor puppet stayed up out of play")
			}
			now = now.Add(2 * actorGrace)
			feed()
			if c.actorActive(0) {
				t.Fatal("actor puppet came back up out of play")
			}
			c.stage("M_Dai", avatartest.Link, false)
			now = now.Add(16 * time.Millisecond)
			feed()
			if c.actorActive(0) {
				t.Fatal("actor puppet came back up without an in-play grace")
			}
			now = now.Add(actorGrace)
			feed()
			if !c.actorActive(0) {
				t.Error("actor puppet did not come back actorGrace after re-entering play")
			}
		})
	}
}

// TestFeederReconnectRestartsActorGrace: a reconnect restarts a pending
// actorGrace wait, since the new connection delivers each remote's
// position before its pose again.
func TestFeederReconnectRestartsActorGrace(t *testing.T) {
	f, c, _ := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
	now = now.Add(actorGrace / 2)
	f.Connected(7)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
	now = now.Add(actorGrace - time.Millisecond)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
	if c.actorActive(0) {
		t.Fatal("actor puppet came up less than actorGrace after the reconnect")
	}
	now = now.Add(time.Millisecond)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, false, now)}, now)
	if !c.actorActive(0) {
		t.Error("actor puppet did not come up actorGrace after the reconnect")
	}
}

// TestFeederRearmAfterShadowModeCleared: a save-state load mid-session
// puts shadow_mode back to 0 and drops the pose_bufs; the next tick
// re-asserts mode 5 and writes into the newly allocated buffer.
func TestFeederRearmAfterShadowModeCleared(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now)}, now)
	first := c.posePtr(0)
	if !c.wrotePoseOf(0, 1) {
		t.Fatal("slot 0 not armed")
	}

	c.saveStateLoad()
	c.ram.Write(first, make([]byte, 4)) // the old buffer is gone
	now = now.Add(16 * time.Millisecond)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now)}, now)

	if m := c.ram.U8(mailbox.Mailbox + mailbox.OffShadowMode); m != avatar.ModePoseFeed {
		t.Errorf("shadow_mode = %d after the re-arm, want %d", m, avatar.ModePoseFeed)
	}
	if !rep.has("shadow_mode was reset") {
		t.Errorf("no shadow_mode reset warning in %q", rep.lines)
	}
	if second := c.posePtr(0); second == first || second == 0 {
		t.Fatalf("pose_buf = 0x%08X, want a new one (was 0x%08X)", second, first)
	}
	if !c.wrotePoseOf(0, 1) {
		t.Error("the pose did not go to the new pose_buf")
	}
	if b, _ := c.ram.Read(first, 4); !bytes.Equal(b, make([]byte, 4)) {
		t.Error("a pose went to the old, freed pose_buf")
	}
}

// TestFeederReconnectForcesRepoll: after a reconnect the feeder does not
// trust a pose_buf_state that still reads 1 from the prior session; a
// server restart (new epoch) drops every slot mapping.
func TestFeederReconnectForcesRepoll(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, false, now)}, now)
	now = now.Add(actorGrace)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, false, now)}, now)
	if !c.wrotePoseOf(0, 1) || !c.actorActive(1) {
		t.Fatal("setup: player 1 not on link slot 0 or player 2 not on actor slot 1")
	}

	// Same server: the mappings survive, the slot re-arms.
	f.Connected(7)
	if c.poseSeq(0) != 0 {
		t.Error("Connected did not zero pose_seq")
	}
	now = now.Add(16 * time.Millisecond)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, false, now)}, now)
	if f.LinkSlots()[1] != 0 || !c.wrotePoseOf(0, 1) {
		t.Errorf("after a reconnect: link slots %v, pose of 1 in slot 0 = %v", f.LinkSlots(), c.wrotePoseOf(0, 1))
	}
	if f.armedEpoch[0] != f.sessionEpoch {
		t.Errorf("armedEpoch = %d, want session %d", f.armedEpoch[0], f.sessionEpoch)
	}

	// Server restart: every actor puppet goes down and every mapping goes.
	f.Connected(8)
	if len(f.remoteToSlot) != 0 || len(f.LinkSlots()) != 0 || c.actorActive(1) {
		t.Errorf("after a server restart: actors %v links %v active1=%v", f.remoteToSlot, f.LinkSlots(), c.actorActive(1))
	}
	if !rep.has("server restarted") {
		t.Error("no server restart line")
	}
}
