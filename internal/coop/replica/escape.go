package replica

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
)

// The escape hatches (§6.7). Until M9 a create-only actor in the current
// room (a chest, a freestanding item, a bombable wall) shows what it read
// when it was created, so a partner's progress can leave a room stale, or
// a player stuck; and two players far apart want to meet. M3 ships two
// escapes, each one write of a stage-change or warp request the game
// already understands, made at the user's word (Replica.Escape):
//
//   - refresh area reloads the current stage by the entrance it was
//     entered by: mNextStage still holds that request after the load (the
//     new play scene copies it to mCurStage and only clears mEnable,
//     d_s_play.cpp:1277-1278), so writing mEnable back to 1, behind a
//     fresh copy of its fields and mWipe 0, asks for the same stage,
//     point, room and layer again. dScnPly_Draw then changes the scene
//     behind a wipe as for a loading zone (d_s_play.cpp:285-314);
//   - warp to partner, on the same stage, moves Link to the partner's
//     position (pose sync's) through the mod's warp request: warp_x/y/z,
//     then warp_seq + 1 (mailbox.h:211-225), which multiplayer_update
//     carries out on the next frame Link exists and acknowledges in
//     warp_ack (multiplayer.c:365-421). On another stage it asks for the
//     partner's stage by the point the partner's PRESENCE names, then
//     warps to the partner once there.
//
// Both are best effort and inferred (§6.7). dComIfGp_setNextStage also
// records the last scene's speed and mode, with the shield and soup bits,
// and, when asked, the start point (d_com_inf_game.cpp:666-690), which a
// raw mNextStage write skips. So an escape's arrival replays the mode the
// last stage change the game asked for recorded: Link's create picks its
// entry from it, crawling in (modes 2 and 3), getting up from a fall (4
// and 5) or a demo (6), and takes the shields' state and the soup from it
// (d_a_player_main.cpp:12280-12297, 12444-12550, 12601-12616). The start
// point matters only to the volcano and ice island actors
// (d_a_obj_volcano.cpp:174, d_a_tag_volcano.cpp:37, 46,
// d_a_obj_iceisland.cpp:63). M7's SET_NEXT_STAGE op replaces both
// writes. Every byte goes through the WriteGuard (TargetEscape), only
// while ARMED, STABLE, IDLE and HEARTBEAT, and never in a dry run.

// EscapeKind is one of the escape hatches.
type EscapeKind uint8

const (
	// EscapeRefresh reloads the current stage by the entrance Link came
	// in by ("refresh area").
	EscapeRefresh EscapeKind = iota + 1
	// EscapeWarp moves Link to the partner ("warp to partner").
	EscapeWarp
)

func (k EscapeKind) String() string {
	switch k {
	case EscapeRefresh:
		return "refresh"
	case EscapeWarp:
		return "warp"
	}
	return fmt.Sprintf("EscapeKind(%d)", uint8(k))
}

// EscapeResult is what an escape did.
type EscapeResult struct {
	Kind EscapeKind
	// Did says what the escape wrote, or, with Err, what it was about
	// to.
	Did string
	// Err is why nothing was written. ErrNotNow: the game or the partner
	// is busy, and the same escape may work in a moment.
	Err error
}

// ErrNotNow is matched by an escape refused for now: a gate is closed, a
// warp is still under way, the partner is changing stage.
var ErrNotNow = errors.New("not now")

// ErrStopped is an escape refused because the replica no longer ticks
// (Run returned): unlike ErrNotNow, asking again does not help.
var ErrStopped = errors.New("co-op has stopped for this session: no escape is made")

// errRoomNotLoaded is a same-stage warp refused because the partner's room
// has no collision in this game: Link would fall through it.
var errRoomNotLoaded = errors.New("not loaded here")

// FollowTimeout is how long a warp to a partner on another stage waits,
// after the stage change it asked for, to warp onto the partner.
const FollowTimeout = time.Minute

// PartnerSettle is how long the partner must have been in its stage, with
// no stage change pending, before a warp to its side on Link's stage, by
// the partner's own PRESENCE. The position comes over pose sync, another
// connection than PRESENCE's, so a position heard just as the partner
// arrives may still be the one it left on its last stage. Pose sync sends
// many times a second once the partner's Link exists, so a second is
// ample. Inferred.
const PartnerSettle = time.Second

// maxEntries bounds each map of known entrances: what a hostile partner's
// PRESENCE can grow it to.
const maxEntries = 512

// roomStatusOffBgW is dStage_roomStatus_c::mpBgW (d_stage.h:865), the
// room's collision: the BG actor sets it as it creates the room's dBgW and
// clears it on delete (d_a_bg.cpp:213, 241).
const roomStatusOffBgW = 0x110

// roomBgW is the address of mStatus[room].mpBgW.
func roomBgW(room int8) uint32 {
	return addr.RoomStatus + uint32(room)*addr.RoomStatusStride + roomStatusOffBgW
}

// Pos is a position in a stage's world: Link's current.pos.
type Pos struct{ X, Y, Z float32 }

// valid reports whether p is a position the warp may take: finite, and
// within a million units (a stage is tens of thousands across).
func (p Pos) valid() bool {
	for _, v := range []float32{p.X, p.Y, p.Z} {
		if f := float64(v); math.IsNaN(f) || math.Abs(f) > 1e6 {
			return false
		}
	}
	return true
}

// StageRequest is a stage change as mNextStage holds it
// (dStage_startStage_c, d_stage.h:949-952).
type StageRequest struct {
	Name  string
	Point int16
	Room  int8
	Layer int8
}

func (h StageRequest) String() string {
	return fmt.Sprintf("%s by point %d into room %d, layer %d", h.Name, h.Point, h.Room, h.Layer)
}

// Check reports whether the game takes h, as the WriteGuard's mNextStage
// rules have it (escapeRules): a name of 1-7 letters, digits or '_', a
// point 0..255, a room 0..63 and a layer -1..MaxStageLayer.
func (h StageRequest) Check() error {
	switch {
	case h.Name == "" || len(h.Name) >= addr.StageNameLen:
		return fmt.Errorf("stage name %q: want 1-%d characters", h.Name, addr.StageNameLen-1)
	case h.Point < 0 || h.Point > 0xFF:
		return fmt.Errorf("point %d: an entrance is 0..255 (a PLYR entry's angle.z, d_stage.cpp:1455-1463)", h.Point)
	case h.Room < 0 || int(h.Room) >= addr.RoomCount:
		return fmt.Errorf("room %d: want 0..%d", h.Room, addr.RoomCount-1)
	case h.Layer < -1 || h.Layer > MaxStageLayer:
		return fmt.Errorf("layer %d: want -1..%d", h.Layer, MaxStageLayer)
	}
	for i := range len(h.Name) {
		if b := h.Name[i]; b == 0 || !stageNameByte(b) {
			return fmt.Errorf("stage name %q: only letters, digits and '_'", h.Name)
		}
	}
	return nil
}

// Bytes is h's dStage_startStage_c: name[8] (NUL-padded, as
// dStage_startStage_c::set strcpys it, d_stage.cpp:112-117), point s16,
// room s8, layer s8.
func (h StageRequest) Bytes() [addr.StageSize]byte {
	var b [addr.StageSize]byte
	copy(b[addr.StageOffName:addr.StageOffName+addr.StageNameLen-1], h.Name)
	binary.BigEndian.PutUint16(b[addr.StageOffPoint:], uint16(h.Point))
	b[addr.StageOffRoomNo] = byte(h.Room)
	b[addr.StageOffLayer] = byte(h.Layer)
	return b
}

// Fields are the writes that ask the game for h, as ApplyFields makes
// them: mNextStage's twelve bytes, mWipe 0 (l_wipeType[0], the plain fade,
// d_s_play.cpp:291-303), then mEnable 1 last, the byte dScnPly_Draw acts
// on (d_s_play.cpp:291-314). M0's stage-hop writes the same bytes in the
// same order (coop_diag.go).
func (h StageRequest) Fields() []Field {
	b := h.Bytes()
	return []Field{
		{Addr: addr.NextStage, Data: b[:]},
		{Addr: addr.NextStage + addr.NextOffWipe, Data: []byte{0}},
		{Addr: addr.NextStage + addr.NextOffEnable, Data: []byte{1}},
	}
}

// nextStageOf is the request mNextStage holds in s.
func nextStageOf(s *snap.Snapshot) StageRequest {
	b := s.Stage[addr.NextStage-snap.StageRegion.Start:]
	name := b[addr.StageOffName : addr.StageOffName+addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return StageRequest{
		Name:  string(name),
		Point: int16(binary.BigEndian.Uint16(b[addr.StageOffPoint:])),
		Room:  int8(b[addr.StageOffRoomNo]),
		Layer: int8(b[addr.StageOffLayer]),
	}
}

// WarpFields are the writes of a same-stage warp to p: warp_x/y/z, then
// warp_seq = seq + 1 last (warpLink's order in package main), so the C
// side never sees a new seq with old coordinates.
func WarpFields(p Pos, seq uint32) []Field {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b[0:], math.Float32bits(p.X))
	binary.BigEndian.PutUint32(b[4:], math.Float32bits(p.Y))
	binary.BigEndian.PutUint32(b[8:], math.Float32bits(p.Z))
	return []Field{
		{Addr: WarpPos, Data: b},
		{Addr: addr.WarpSeq, Data: binary.BigEndian.AppendUint32(nil, seq+1)},
	}
}

// entryKey is an entrance: a stage and the point it is entered by.
type entryKey struct {
	stage string
	point int16
}

// escapeReq is an escape asked for: waiting for a tick (Replica.esc), or
// planned and made after this tick's writes (Replica.escNow).
type escapeReq struct {
	kind   EscapeKind
	done   chan EscapeResult // nil for a warp's second step
	fields []Field
	did    string
	follow *followWarp // set once the fields are written
}

// followWarp is the second step of a warp that changed stage: once Link
// is in stage, warp onto the partner.
type followWarp struct {
	stage string
	until time.Time
}

// partnerTrack follows the partner's PRESENCE to learn the room each
// entrance it comes in by starts in (learnPartner), and since when it has
// been in its stage (notePartnerRest).
type partnerTrack struct {
	have      bool // last holds the last settled PRESENCE of this connection
	last      presencePlace
	arrival   entryKey // the entrance of the arrival being learned
	learning  bool
	from      presencePlace // where the partner was before the arrival
	sawNoLink bool          // a PRESENCE without Link came since the arrival began

	atRest    bool      // the last PRESENCE had the partner in a stage, no change pending
	restAt    entryKey  // the stage and point it is at rest in
	restSince time.Time // when it came to rest there, by this replica's clock
}

// presencePlace is where a settled PRESENCE put the partner.
type presencePlace struct {
	key   entryKey
	table int8
	room  int8
}

// Escape asks for an escape (§6.7), made on a later tick with the gates
// open, and returns a channel that gets its one result. A refusal comes at
// once in a dry run, once Run has returned (ErrStopped), while co-op
// writes are off and while another escape waits; the rest on the next
// tick, or ErrStopped if Run returns first. A new escape drops the second step of
// a warp still waiting for its stage change.
func (r *Replica) Escape(k EscapeKind) <-chan EscapeResult {
	ch := make(chan EscapeResult, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	switch off, why := r.writer.Disabled(); {
	case k != EscapeRefresh && k != EscapeWarp:
		err = fmt.Errorf("replica: no escape %d", uint8(k))
	case r.writer.DryRun():
		err = errors.New("--coop-dry-run writes no game data")
	case r.stopped:
		err = ErrStopped
	case off:
		err = fmt.Errorf("co-op writes are off: %s", why)
	case r.esc != nil:
		err = fmt.Errorf("%w: a %s waits for its tick", ErrNotNow, r.esc.kind)
	}
	if err != nil {
		ch <- EscapeResult{Kind: k, Err: err}
		return ch
	}
	r.follow = nil
	r.esc = &escapeReq{kind: k, done: ch}
	return ch
}

// escapeTick is the escapes' part of a tick (under r.mu): it learns the
// entrance Link came in by, and plans an escape asked for, or a warp's
// second step, into r.escNow for tickContext to make once the
// reconciler's writes are done.
func (r *Replica) escapeTick(o *obs, armed bool) {
	r.learnOwn(o)
	if q := r.esc; q != nil {
		r.esc = nil
		fs, did, follow, err := r.planEscape(o, armed, q.kind)
		if err != nil {
			q.done <- EscapeResult{Kind: q.kind, Did: did, Err: err}
			return
		}
		q.fields, q.did, q.follow = fs, did, follow
		r.escNow = q
		return
	}
	f := r.follow
	if f == nil {
		return
	}
	fs, did, err := r.finishWarp(o, armed, f)
	switch {
	case err == nil:
		r.follow = nil
		r.escNow = &escapeReq{kind: EscapeWarp, fields: fs, did: did}
	case !errors.Is(err, ErrNotNow) && !errors.Is(err, errRoomNotLoaded):
		r.follow = nil
		r.logf("co-op: warp to the partner: %v", err)
	}
}

// failEscape answers a waiting escape with err: the tick could not run.
func (r *Replica) failEscape(err error) {
	if q := r.esc; q != nil {
		r.esc = nil
		q.done <- EscapeResult{Kind: q.kind, Err: fmt.Errorf("%w: %v", ErrNotNow, err)}
	}
}

// escaped records what ApplyFields did with q (under r.mu).
func (r *Replica) escaped(q *escapeReq, res Result, err error, rep *Report) {
	rep.Escape, rep.EscapeErr = q.kind, err
	if err != nil {
		if q.done == nil {
			r.logf("co-op: warp onto the partner failed: %v", err)
		}
		return
	}
	r.logf("co-op: %s: %s (%d bytes after heartbeat %d)", q.kind, q.did, len(res.Wrote), res.Beat)
	if q.follow != nil {
		r.follow = q.follow
	}
}

// planEscape plans escape k on tick o: its fields, what it does, and the
// second step of a warp that changes stage.
func (r *Replica) planEscape(o *obs, armed bool, k EscapeKind) ([]Field, string, *followWarp, error) {
	switch {
	case o.screen != screenGame:
		return nil, "", nil, fmt.Errorf("%w: Link is not in a stage", ErrNotNow)
	case !armed:
		return nil, "", nil, fmt.Errorf("%w: co-op is not running for this file (%s)", ErrNotNow, r.notArmedWhy(o))
	case !o.st.CanWriteIdle():
		return nil, "", nil, fmt.Errorf("%w: %s", ErrNotNow, closedWhy(o.st))
	}
	if k == EscapeRefresh {
		h := nextStageOf(o.s)
		switch {
		case h.Name != o.s.StageName:
			return nil, "", nil, fmt.Errorf("mNextStage names %q, not %q, the stage Link is in: there is no entrance to reload by", h.Name, o.s.StageName)
		case h.Point < 0:
			return nil, "", nil, fmt.Errorf("Link came into %s by a restart (point %d), not an entrance: go through a door or a loading zone first", h.Name, h.Point)
		}
		if err := h.Check(); err != nil {
			return nil, "", nil, fmt.Errorf("mNextStage holds %s: %v", h, err)
		}
		return h.Fields(), "reloading " + h.String(), nil, nil
	}

	p := r.partner
	switch {
	case p == nil:
		return nil, "", nil, errors.New("no word of the partner yet: is its co-op connected?")
	case p.Flags&wire.PresenceInGame == 0:
		return nil, "", nil, fmt.Errorf("%w: the partner is not in a stage", ErrNotNow)
	case p.Flags&wire.PresenceTransitioning != 0:
		return nil, "", nil, fmt.Errorf("%w: the partner is changing stage", ErrNotNow)
	}
	stage := p.Stage.String()
	if stage == o.s.StageName {
		fs, did, err := r.sameStageWarp(p)
		if !errors.Is(err, errRoomNotLoaded) {
			return fs, did, nil, err
		}
		h, how, herr := r.partnerEntrance(p)
		if herr != nil {
			return nil, "", nil, fmt.Errorf("%v, and %v", err, herr)
		}
		return h.Fields(), fmt.Sprintf("%v: reloading %s, the partner's entrance (%s), then warping to the partner", err, h, how), r.newFollow(stage), nil
	}
	h, how, err := r.partnerEntrance(p)
	if err != nil {
		return nil, "", nil, err
	}
	return h.Fields(), fmt.Sprintf("going to %s, the partner's entrance (%s), then onto the partner", h, how), r.newFollow(stage), nil
}

// notArmedWhy is why the session is not armed for o's file.
func (r *Replica) notArmedWhy(o *obs) string {
	if _, why := r.state(o, r.classify(o)); why != "" {
		return why
	}
	return "not armed"
}

func (r *Replica) newFollow(stage string) *followWarp {
	return &followWarp{stage: stage, until: r.now().Add(FollowTimeout)}
}

// finishWarp plans the second step of f once Link and the partner are in
// f's stage: ErrNotNow and errRoomNotLoaded while it should wait, another
// error to give up.
func (r *Replica) finishWarp(o *obs, armed bool, f *followWarp) ([]Field, string, error) {
	p := r.partner
	switch {
	case r.now().After(f.until):
		return nil, "", fmt.Errorf("gave up warping onto the partner: Link was not in %s, settled, within %v", f.stage, FollowTimeout)
	case p != nil && p.Flags&wire.PresenceInGame != 0 && p.Flags&wire.PresenceTransitioning == 0 && p.Stage.String() != f.stage:
		return nil, "", fmt.Errorf("the partner left %s for %s: warp again to follow", f.stage, p.Stage)
	case o.screen != screenGame || !armed || !o.st.CanWriteIdle() || o.s.StageName != f.stage || p == nil:
		return nil, "", ErrNotNow
	}
	return r.sameStageWarp(p)
}

// sameStageWarp plans a warp to partner p on Link's stage: the partner's
// position from pose sync (Config.PartnerPos), once the partner has been
// at rest in its stage for PartnerSettle, into a room whose collision this
// game has loaded (mpBgW), with no warp still under way (warp_seq ==
// warp_ack).
func (r *Replica) sameStageWarp(p *wire.Presence) ([]Field, string, error) {
	if r.cfg.PartnerPos == nil {
		return nil, "", errors.New("this session has no pose sync to take the partner's position from")
	}
	pos, ok := r.cfg.PartnerPos()
	switch {
	case !ok:
		return nil, "", fmt.Errorf("%w: the partner's position has not come in over pose sync", ErrNotNow)
	case !r.track.atRest:
		return nil, "", fmt.Errorf("%w: the partner is between stages", ErrNotNow)
	case r.now().Sub(r.track.restSince) < PartnerSettle:
		return nil, "", fmt.Errorf("%w: the partner has only just come into %s: its position may still be the one it left", ErrNotNow, r.track.restAt.stage)
	case !pos.valid():
		return nil, "", fmt.Errorf("the partner's position (%v, %v, %v) is no place to warp to", pos.X, pos.Y, pos.Z)
	case p.Room < 0:
		return nil, "", fmt.Errorf("%w: the partner's room is not known yet", ErrNotNow)
	}
	b, err := r.m.Read(addr.WarpSeq, 8)
	if err != nil {
		return nil, "", err
	}
	seq, ack := binary.BigEndian.Uint32(b), binary.BigEndian.Uint32(b[4:])
	if seq != ack {
		return nil, "", fmt.Errorf("%w: a warp is still under way (warp_seq %d, warp_ack %d)", ErrNotNow, seq, ack)
	}
	bgw, err := mem.ReadU32(r.m, roomBgW(p.Room))
	if err != nil {
		return nil, "", err
	}
	if bgw == 0 {
		return nil, "", fmt.Errorf("room %d, where the partner is, is %w", p.Room, errRoomNotLoaded)
	}
	return WarpFields(pos, seq), fmt.Sprintf("warping to the partner at (%.0f, %.0f, %.0f) in room %d", pos.X, pos.Y, pos.Z, p.Room), nil
}

// partnerEntrance is the stage request that takes Link to partner p's
// entrance: its stage, by the point it came in by, into the room that
// entrance starts in (entryRoom), layer -1, as the game's own exits ask
// (d_stage.cpp:2362), so the game picks the layer (getLayerNo,
// d_com_inf_game.cpp:185-190).
func (r *Replica) partnerEntrance(p *wire.Presence) (StageRequest, string, error) {
	stage := p.Stage.String()
	if p.Point < 0 {
		return StageRequest{}, "", fmt.Errorf("the partner came into %s by a restart (point %d): there is no entrance to take", stage, p.Point)
	}
	room, how, ok := r.entryRoom(entryKey{stage, p.Point})
	if !ok {
		return StageRequest{}, "", fmt.Errorf("the room point %d of %s starts in is not known yet: it is learned when you, or the partner, come in by it", p.Point, stage)
	}
	h := StageRequest{Name: stage, Point: p.Point, Room: room, Layer: -1}
	return h, how, h.Check()
}

// entryRoom is the room entrance k starts in: from this game's own
// arrivals by it (exact), else from the partner's (inferred).
func (r *Replica) entryRoom(k entryKey) (int8, string, bool) {
	if room, ok := r.entries[k]; ok {
		return room, "as you came in by it", true
	}
	if room, ok := r.heard[k]; ok {
		return room, "as the partner came in by it", true
	}
	return 0, "", false
}

// learnOwn records the entrance Link came into o's stage by: mNextStage
// keeps the request that loaded the stage (d_s_play.cpp:1277-1278), so a
// request naming the current stage and point, with no change pending, is
// one the game took. Its room is the room the request asked for; the PLYR
// entry may then put Link elsewhere (d_stage.cpp:1479), but the request
// is what worked.
func (r *Replica) learnOwn(o *obs) {
	if o.screen != screenGame || o.s.NextEnable != 0 {
		return
	}
	h := nextStageOf(o.s)
	if h.Name != o.s.StageName || h.Point != o.s.StagePoint || h.Check() != nil {
		return
	}
	k := entryKey{h.Name, h.Point}
	if _, ok := r.entries[k]; !ok && len(r.entries) >= maxEntries {
		return
	}
	r.entries[k] = h.Room
}

// notePartnerRest follows a PRESENCE from the partner (under r.mu) to
// know since when it has been in its stage: in game, with no stage change
// pending, at the same stage and point. A room change in the stage keeps
// it at rest (pose sync follows the partner through the stage); a stage
// change, a reload of the same stage (a void-out, a refresh) and a lost
// Link do not.
func (r *Replica) notePartnerRest(p *wire.Presence) {
	t := &r.track
	in := p.Flags&wire.PresenceInGame != 0 && p.Flags&wire.PresenceTransitioning == 0 && !p.Stage.IsEmpty()
	at := entryKey{p.Stage.String(), p.Point}
	switch {
	case !in:
		t.atRest = false
	case !t.atRest || at != t.restAt:
		t.atRest, t.restAt, t.restSince = true, at, r.now()
	}
}

// learnPartner follows a PRESENCE from the partner (under r.mu) to learn
// the room each entrance it comes in by starts in: the first settled
// PRESENCE of an arrival, once the partner's Link is in the new stage.
// The old play scene's delete clears mpPlayerPtr (dScnPly_Delete ends in
// dComIfGp_init, d_s_play.cpp:1136; d_com_inf_game.cpp:60-69) before the
// new one takes the new stage's name from mNextStage (d_s_play.cpp:
// 1277-1278) and builds the stage and Link, so a PRESENCE in game with
// the new name already carries the new table and mStayNo. As a guard
// against a snapshot read across those frames (snap.Read reads the stage
// name, the table and mStayNo apart), an arrival whose table and room are
// the ones the partner left, with no PRESENCE without Link between, is
// not learned from: an entrance whose room has the old one's number in
// the same table is then not known, and the warp says so rather than
// guess. Inferred.
func (r *Replica) learnPartner(p *wire.Presence) {
	t := &r.track
	settled := p.Flags&wire.PresenceInGame != 0 && p.Flags&(wire.PresenceTransitioning|wire.PresencePaused) == 0 &&
		p.Room >= 0 && !p.Stage.IsEmpty() && p.Point >= 0
	if !settled {
		if p.Flags&wire.PresenceInGame == 0 && t.learning {
			t.sawNoLink = true
		}
		return
	}
	here := presencePlace{key: entryKey{p.Stage.String(), p.Point}, table: p.Table, room: p.Room}
	switch {
	case !t.have:
		// The first word of this connection: the partner may be anywhere
		// in its stage, not at the entrance.
	case here.key != t.last.key:
		t.arrival, t.learning, t.from, t.sawNoLink = here.key, true, t.last, false
	}
	if t.learning && here.key == t.arrival {
		if t.sawNoLink || here.table != t.from.table || here.room != t.from.room {
			if _, ok := r.heard[here.key]; ok || len(r.heard) < maxEntries {
				r.heard[here.key] = here.room
			}
			t.learning = false
		}
	}
	t.have, t.last = true, here
}
