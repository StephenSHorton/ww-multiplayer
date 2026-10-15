package hub

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// worldClearCount is the mClearCount of every file a world holds. Second
// quest is a non-goal (§2): bind.PlanBind refuses those files and tag
// rule V3 catches a tag ExDataSave carried into one, so a world's files
// are all first-quest, and a hello with another clear count is refused
// (§5.3 "mClearCount differs").
const worldClearCount = 0

// resumeMilestones are the event bits dComIfGs_setGameStartStage checks,
// in order, to pick where a loaded file resumes (l_checkData,
// d_com_inf_game.cpp:1304-1310; the loop, :1313-1320): RODE_KORL,
// MET_KORL, 0x0801, 0x0808 and 0x2401 (d_save_event_flag.inc:322, 111,
// 66, 63, 279). A file lacking one the world has is far behind it (§5.3):
// copying the host's file serves it better than a union, which gives a
// file still on Outset the story of a later one (its holds keep only
// where it resumes, class.Holds), so it binds only under --coop-merge.
// The check is the replica's preflight's (replica.Milestones), so the two
// agree on which bind needs --coop-merge.
//
// The world holds story bits from M4 (DefaultPolicy). A file is far
// behind whatever its player is granted this session: the bind outlasts
// the session, and a later one with story sync gives the file the world's
// milestones (judge). RODE_KORL (set as Link first boards the
// boat, d_a_ship.cpp:1570) is LocalOnly (class.LocalOnly), so no world
// ever holds it: the check rests on the other four, MET_KORL first, which
// the MEETSHISHIOH event sets before Link can board (d_event_manager.cpp:
// 599-600; the boat's own check, d_a_ship.cpp:4548; inferred for every
// path).
var resumeMilestones = []uint16{0x2A08, 0x0F80, 0x0801, 0x0808, 0x2401}

// peer is one co-op connection's session. Only the hub goroutine touches
// it.
type peer struct {
	link *link
	id   world.Player
	name string
	host bool

	state    PeerState
	hello    *wire.CoHello   // the last CO_HELLO
	reason   wire.NoticeCode // why refused or unbound
	want     Feature         // the session features it wants
	features Feature         // what it was granted

	// batch is the last GAINS batch merged; batches on one connection
	// count up, so one at or before it is a repeat.
	batch     uint32
	haveBatch bool

	presence *wire.Presence // its last PRESENCE, relayed to newcomers
	place    world.Place

	// candidate is an unbound peer's last WORLD, checked against the
	// world when it asks to bind (§5.3); nil until it sends one.
	candidate *candidate

	complained    map[wire.NoticeCode]time.Time
	tablesNote    string // the last NOTICE saying why it has no TableFeatures
	rejected      int
	dups          int
	scopedRefused int  // SCOPED bits the M1 verdict refused
	dead          bool // its outbox overflowed or a send failed: it is closing
}

func (p *peer) String() string { return fmt.Sprintf("%s (co-op %d)", p.name, p.id) }

// welcomed reports whether p has a session the others see: unbound or
// bound.
func (p *peer) welcomed() bool { return p.state == StateUnbound || p.state == StateBound }

func (p *peer) bound() bool { return p.state == StateBound }

// candidate is an unbound file's last WORLD, which the bind check (judge)
// compares with the world.
type candidate struct {
	file  fileKey
	image world.Image
}

// verdict is how a candidate stands against the world (§5.3).
type verdict struct {
	gains  []world.Gain // what a merge would add to the world
	behind []uint16     // the resume milestones the world has and the file lacks
	// held are the world's story bits the receiver's holds would keep
	// out of the file once bound (class.Holds), for the preflight.
	held []story.Held
}

// judge compares candidate c with the world as it is now:
//
//   - gains: the file's shared progress the world lacks, within the policy
//     (so a LocalOnly, game-cleared or refight story bit never counts, nor
//     a memory switch the host's M1 verdict keeps out). A file with any is
//     divergent, or ahead;
//   - behind: the resume milestones the world has and the file lacks
//     (resumeMilestones). A file with any is far behind;
//   - held: the world's story bits the file's holds would keep back once
//     bound.
//
// Divergent, ahead and far behind each need --coop-merge on the host.
//
// The judgement is the bind's, not the session's: it counts every byte the
// world takes, the memory switches and story bits included, whatever
// features the player is granted now. A bound file is never judged again
// (onBoundHello), and any later session that grants it more merges its
// WORLD whole (onWorld) and writes the world's story into it: a file bound
// while story sync was off for it (a --coop-dry-run join, or stage tables
// not the host's) must not bring in, or be given, what a bind with it
// would have needed --coop-merge for (Appendix C item 111).
func (h *Hub) judge(c *candidate) verdict {
	w := h.store.World()
	cur := w.Image()
	v := verdict{gains: w.Merge(&c.image).Delta}
	for _, id := range resumeMilestones {
		b := addr.EventByte(id)
		off := addr.OffEvent + b.Off
		if cur[off]&b.Mask == b.Mask && c.image[off]&b.Mask != b.Mask {
			v.behind = append(v.behind, id)
		}
	}
	v.held = story.Pending(&cur, &c.image, nil, nil).Held
	return v
}

// fileKey names the loaded file a hello describes, so a WORLD sent for one
// file is never taken for another's.
type fileKey struct {
	slot, clearCount uint8
	nameHash         uint32
}

func keyOf(m *wire.CoHello) fileKey {
	return fileKey{slot: m.Slot, clearCount: m.ClearCount, nameHash: m.NameHash}
}

func (h *Hub) onFrame(p *peer, payload []byte) {
	m, err := wire.Decode(payload, wire.Up)
	if errors.Is(err, wire.ErrSchema) {
		h.refuse(p, wire.NoticeSchema, "your ww-multiplayer speaks another co-op schema (%v); co-op is off for this session, pose sync still works", err)
		return
	}
	if err != nil {
		h.complain(p, wire.NoticeRejected, "refused a co-op message: %v", err)
		return
	}
	if hello, ok := m.(*wire.CoHello); ok {
		h.onHello(p, hello)
		return
	}
	switch p.state {
	case StateHello:
		h.complain(p, wire.NoticeRejected, "refused %s: send CO_HELLO first", m.Sub())
		return
	case StateRefused:
		return // its welcome said why
	}
	switch m := m.(type) {
	case *wire.World:
		h.onWorld(p, m)
	case *wire.GainsUp:
		h.onGains(p, m)
	case *wire.Presence:
		h.onPresence(p, m)
	case *wire.Scoped:
		h.onScoped(p, m)
	case *wire.Ctrl:
		h.onCtrl(p, m)
	}
}

// --- CO_HELLO -----------------------------------------------------------------

// onHello classifies p from its hello (§4.4, §5.2-5.3). A peer may say
// hello again on the same connection whenever its file changes (another
// file loaded, a bind, a scrub): it is classified afresh.
func (h *Hub) onHello(p *peer, m *wire.CoHello) {
	p.hello = m
	p.want = Feature(m.Want) & SessionFeatures
	wid := world.ID(m.WorldID)
	switch {
	case m.ClassHash != wire.ClassHash():
		h.refuse(p, wire.NoticeSchema, "your co-op class table (%016x) differs from the host's (%016x): a modded build? Co-op is off for this session, pose sync still works", m.ClassHash, wire.ClassHash())
	case m.ClearCount != worldClearCount:
		h.refuse(p, wire.NoticeClearCount, "your file has clear count %d: second-quest files can't join co-op", m.ClearCount)
	case m.FileState == wire.FileForeign:
		h.refuse(p, wire.NoticeWorldMismatch, "your file is bound to another co-op world (%s); the host runs %s. Pose sync still works", m.WorldID, h.id)
	case m.FileState == wire.FileBound && wid != h.id:
		h.refuse(p, wire.NoticeWorldMismatch, "your file is bound to co-op world %s, but the host runs %s. Pose sync still works", m.WorldID, h.id)
	case m.FileState == wire.FileBound:
		h.noteTables(p)
		h.onBoundHello(p, m)
	case Feature(m.Want)&AskBind != 0:
		h.noteTables(p)
		h.onBindHello(p, m)
	default:
		h.noteTables(p)
		h.onUnboundHello(p, m)
	}
}

// tablesMatch reports whether p's M1 stage tables are the host's: the
// same identity, and not 0, which means none (Config.StageTblHash).
func (h *Hub) tablesMatch(p *peer) bool {
	return h.cfg.StageTblHash != 0 && p.hello != nil && p.hello.StageTblHash == h.cfg.StageTblHash
}

// noteTables tells p, once per reason, why it gets none of the
// TableFeatures it wants and the host allows: the host or p has no stage
// tables, or they differ. Its session goes on without them.
func (h *Hub) noteTables(p *peer) {
	if h.features&p.want&TableFeatures == 0 || h.tablesMatch(p) {
		return
	}
	// Each text fits a NOTICE (wire.MaxNoticeLen) unclipped.
	const off = "so memory switches, small keys, heart counts, puzzles and kills don't sync this session; chests, items, story and the rest do"
	var text string
	switch {
	case h.cfg.StageTblHash == 0:
		text = "the host has no co-op stage tables for its disc (the host's log says why), " + off
	case p.hello.StageTblHash == 0:
		text = "you have no co-op stage tables for your disc (the co-op warning in your log says why and what to do), " + off
	default:
		text = fmt.Sprintf("your co-op stage tables (%016x) aren't the host's (%016x): another disc or ww-multiplayer version? %s", p.hello.StageTblHash, h.cfg.StageTblHash, off)
	}
	if text == p.tablesNote {
		return
	}
	p.tablesNote = text
	h.notify(p, wire.NoticeSchema, text)
	h.logf("co-op: %s: %s", p, text)
}

// refuse ends p's co-op for this session: a NOTICE saying why, and a
// CO_WELCOME with features 0.
func (h *Hub) refuse(p *peer, code wire.NoticeCode, format string, args ...any) {
	h.leaveBound(p)
	h.leaveView(p)
	text := fmt.Sprintf(format, args...)
	p.state, p.reason, p.features, p.candidate = StateRefused, code, 0, nil
	h.notify(p, code, text)
	h.welcome(p, wire.StatusRefused, code, 0, false)
	h.logf("co-op: %s refused: %s", p, text)
}

func (h *Hub) welcome(p *peer, st wire.Status, reason wire.NoticeCode, f Feature, fresh bool) {
	h.send(p, &wire.CoWelcome{
		Status: st, Reason: reason, Features: uint32(f),
		WorldID: wire.ID(h.id), Seq: h.store.Seq(), FreshBind: fresh,
	})
}

// onUnboundHello: an unbound file (no tag, or a scrubbed one) or a leaked
// tag (§5.2). The session runs, but nothing the file proposes counts until
// it is bound; it gets the world for the join preflight.
func (h *Hub) onUnboundHello(p *peer, m *wire.CoHello) {
	h.leaveBound(p)
	first := !p.welcomed()
	p.state, p.reason = StateUnbound, wire.NoticeNotBound
	p.features = h.grant(p)
	// A tag of this world that fails only V5 may be a fresh bind whose
	// nonce the replica has not learned yet: FreshBind tells it.
	tagWorld := world.ID(m.WorldID)
	fresh := tagWorld == h.id && !m.BindNonce.IsZero() && h.store.IsFreshBind(world.ID(m.BindNonce))
	leakedFresh := m.FileState == wire.FileLeaked && fresh
	switch {
	case leakedFresh:
		// No NOTICE: the replica starts each process knowing no pending
		// fresh binds, so a fresh-bound file not yet loaded from the card
		// says hello as leaked, and FreshBind is the answer. If V5 was all
		// that failed, it says hello again, bound; if V2-V4 failed too, it
		// offers the scrub itself.
	case m.FileState == wire.FileLeaked && !tagWorld.IsZero() && tagWorld != h.id:
		// Only the tag's own world's host knows that world's pending
		// fresh binds, so a V5-only failure may be one of them. The
		// replica offers no scrub for that (Appendix C item 49), and
		// the NOTICE does not say the file should be scrubbed; if V2-V4
		// failed too, the replica offers the scrub itself.
		h.notify(p, wire.NoticeNotBound, fmt.Sprintf("your file carries a co-op tag of world %s: a tag leaked from another file, or a fresh bind to that world not yet loaded from the card. Only that world's host can tell which. Nothing is synced until you bind the file", tagWorld))
	case m.FileState == wire.FileLeaked:
		h.notify(p, wire.NoticeNotBound, "your file carries a co-op tag leaked from another file; it binds nothing and should be scrubbed. Nothing is synced until you bind the file")
	default:
		h.notify(p, wire.NoticeNotBound, fmt.Sprintf("your file is not bound to co-op world %s; nothing is synced until you bind it", h.id))
	}
	h.welcome(p, wire.StatusUnbound, wire.NoticeNotBound, p.features, fresh)
	h.sendWorld(p)
	if first {
		h.sendPresences(p)
	}
	if leakedFresh {
		h.logf("co-op: %s joined with a tag naming pending fresh bind %s, as a file bound fresh does until it is loaded from the card; told its replica the bind is pending", p, m.BindNonce)
	} else {
		h.logf("co-op: %s joined with an unbound file (%s)", p, m.FileState)
	}
}

// onBindHello: an unbound file asks to bind to this world (AskBind, §5.3).
// It is allowed when the file's last WORLD adds nothing the world lacks
// and the world is not far ahead of it, or the host merges. On a fresh
// file (AskUnsaved) the nonce is recorded as a pending fresh bind, and
// written to disk, before the replica is told to write the tag, so V5
// holds for the file from its first tick bound.
func (h *Hub) onBindHello(p *peer, m *wire.CoHello) {
	h.leaveBound(p)
	first := !p.welcomed()
	p.state, p.reason = StateUnbound, wire.NoticeNotBound
	p.features = h.grant(p)
	deny := func(code wire.NoticeCode, format string, args ...any) {
		text := fmt.Sprintf(format, args...)
		h.notify(p, code, text)
		h.welcome(p, wire.StatusUnbound, code, p.features, false)
		h.logf("co-op: %s may not bind: %s", p, text)
	}
	nonce := world.ID(m.BindNonce)
	c := p.candidate
	// The world may have grown since the file's WORLD: the check is made
	// again now.
	var v verdict
	if c != nil && c.file == keyOf(m) {
		v = h.judge(c)
	}
	switch {
	case world.ID(m.WorldID) != h.id:
		deny(wire.NoticeRejected, "a bind must name the host's world %s, not %s", h.id, m.WorldID)
	case nonce.IsZero():
		deny(wire.NoticeRejected, "a bind needs a nonce")
	case c == nil || c.file != keyOf(m):
		deny(wire.NoticeRejected, "send this file's WORLD before asking to bind it")
	case len(v.gains) > 0 && !h.cfg.Merge:
		deny(wire.NoticeDivergent, "%s", withHeld(fmt.Sprintf("your file has progress the world lacks (%s); binding it needs --coop-merge on the host", summarize(v.gains)), v.held))
	case len(v.behind) > 0 && !h.cfg.Merge:
		deny(wire.NoticeDivergent, "%s", withHeld(fmt.Sprintf("the world is far ahead of your file (story milestones %s); copy the host's save in (it replaces all three of your files), or bind with --coop-merge on the host", events(v.behind)), v.held))
	default:
		fresh := Feature(m.Want)&AskUnsaved != 0
		if fresh {
			_, err := h.store.AddFreshBind(nonce)
			if err == nil {
				err = h.store.Flush()
			}
			if err != nil {
				deny(wire.NoticeRejected, "the host could not record the bind: %v", err)
				break
			}
		}
		h.welcome(p, wire.StatusUnbound, 0, p.features|FeatureBind, fresh)
		how := "a subset of the world"
		switch {
		case len(v.gains) > 0:
			how = "merged under --coop-merge"
		case len(v.behind) > 0:
			how = "far behind, merged under --coop-merge"
		}
		if len(v.held) > 0 {
			how += fmt.Sprintf(", %d holds", len(v.held))
		}
		if fresh {
			how += ", a fresh bind"
		}
		h.logf("co-op: %s may bind its file to world %s (nonce %s; %s)", p, h.id, nonce, how)
	}
	if first {
		h.sendPresences(p)
	}
}

// onBoundHello: the file's tag passes V1-V6 for this world. The session
// runs with what the host allows; the replica sends its WORLD next.
func (h *Hub) onBoundHello(p *peer, m *wire.CoHello) {
	first := !p.welcomed()
	nonce := world.ID(m.BindNonce)
	fresh := h.store.IsFreshBind(nonce)
	if fresh && Feature(m.Want)&AskUnsaved == 0 {
		// The file has been loaded from the card since its bind
		// (mDateIPL != 0), so V5 holds without the record (§5.2).
		if _, err := h.store.ClearFreshBind(nonce); err != nil {
			h.logf("co-op: clearing fresh bind %s: %v", nonce, err)
		}
		fresh = false
	}
	p.reason, p.candidate = 0, nil
	p.features = h.grant(p)
	h.welcome(p, wire.StatusOK, 0, p.features, fresh)
	if !p.bound() {
		p.state = StateBound
		h.enterScopes(p)
		h.logf("co-op: %s joined world %s (seq %d; %s)", p, h.id, h.store.Seq(), p.features)
	}
	if p.features&FeatureImage != 0 {
		h.sendTombstones(p)
	}
	if first {
		h.sendPresences(p)
	}
}

// --- WORLD and GAINS ------------------------------------------------------------

func (h *Hub) onWorld(p *peer, m *wire.World) {
	img := world.Image(m.Image)
	id := world.ID(m.WorldID)
	if !p.bound() {
		// An unbound file's image is never merged: it is what the bind
		// check compares with the world.
		if !id.IsZero() && id != h.id {
			h.complain(p, wire.NoticeWorldMismatch, "your WORLD names world %s; the host runs %s", m.WorldID, h.id)
			return
		}
		h.preflight(p, &img)
		h.sendWorld(p)
		return
	}
	if p.features&FeatureImage == 0 {
		h.complain(p, wire.NoticeRejected, "refused WORLD: image sync is off for this session")
		return
	}
	if id != h.id {
		h.complain(p, wire.NoticeWorldMismatch, "your WORLD names world %s; the host runs %s", m.WorldID, h.id)
		return
	}
	// The bytes of the features p lacks are neither merged nor counted
	// against it as gains: its replica proposes them all the same.
	img, n := strip(&img, p.features)
	p.rejected += n
	r, err := h.store.Merge(p.id, &img)
	if err != nil {
		h.logf("co-op: merging %s's WORLD: %v", p, err)
		return
	}
	h.sendWorld(p)
	h.relay(p, r, false)
	h.rejects(p, r.Rejected)
	if len(r.Delta) > 0 {
		h.logf("co-op: %s's WORLD added %d bytes to the world (seq %d)", p, len(r.Delta), r.Seq)
	}
	if m.Seq > r.Seq {
		// The replica saw a later world than the file holds: the host
		// lost commits (a crash before a flush). Its image brought back
		// what it still has (§4.4 Self-healing).
		h.logf("co-op: %s had seen seq %d, the world file is at %d: commits were lost, and its WORLD restored what it holds", p, m.Seq, r.Seq)
	}
}

// preflight records an unbound file's image and tells the replica when it
// can't bind without --coop-merge (judge). The NOTICE also names the
// world's story bits the file's holds would keep back once it is bound.
func (h *Hub) preflight(p *peer, img *world.Image) {
	c := &candidate{file: keyOf(p.hello), image: *img}
	p.candidate = c
	v := h.judge(c)
	switch {
	case len(v.gains) > 0:
		how := "binding it needs --coop-merge on the host"
		if h.cfg.Merge {
			how = "binding it merges that into the world (--coop-merge)"
		}
		h.notify(p, wire.NoticeDivergent, withHeld(fmt.Sprintf("your file has progress the world lacks (%s); %s", summarize(v.gains), how), v.held))
	case len(v.behind) > 0:
		how := "a union needs --coop-merge on the host"
		if h.cfg.Merge {
			how = "the host's --coop-merge allows a union"
		}
		h.notify(p, wire.NoticeDivergent, withHeld(fmt.Sprintf("the world is far ahead of your file (story milestones %s); copying the host's save in (all three files) is better than a union, and %s", events(v.behind), how), v.held))
	}
}

// withHeld is text, a NOTICE's, followed by "; held until your file
// catches up: " and the names of the holds held lists, as many as fit in
// a NOTICE with the rest counted ("and 2 more"); text alone for none.
func withHeld(text string, held []story.Held) string {
	if len(held) == 0 {
		return text
	}
	for n := len(held); ; n-- {
		names := make([]string, n)
		for i, x := range held[:n] {
			names[i] = x.Hold.Name
		}
		list := strings.Join(names, ", ")
		switch rest := len(held) - n; {
		case n == 0:
			list = fmt.Sprintf("%d story bits", rest)
		case rest > 0:
			list += fmt.Sprintf(" and %d more", rest)
		}
		s := text + "; held until your file catches up: " + list
		if len(s) <= wire.MaxNoticeLen || n == 0 {
			return s
		}
	}
}

func (h *Hub) onGains(p *peer, m *wire.GainsUp) {
	if !p.bound() {
		h.complain(p, wire.NoticeNotBound, "refused GAINS: your file is not bound to world %s", h.id)
		return
	}
	if p.features&FeatureImage == 0 {
		h.complain(p, wire.NoticeRejected, "refused GAINS: image sync is off for this session")
		return
	}
	if p.haveBatch && int32(m.Batch-p.batch) <= 0 {
		p.dups++
		return
	}
	p.batch, p.haveBatch = m.Batch, true
	in, n := dropGated(m.Gains, p.features)
	p.rejected += n
	gs := make([]world.Gain, len(in))
	for i, g := range in {
		gs[i] = world.Gain(g)
	}
	r, err := h.store.ApplyGains(p.id, gs)
	if err != nil {
		h.logf("co-op: merging %s's GAINS: %v", p, err)
		return
	}
	h.relay(p, r, true)
	h.rejects(p, r.Rejected)
}

// relay sends r's effective delta as GAINS to the bound peers with the
// image: the others, and with toOrigin the origin as well (a GAINS answer
// keeps its view the world's; a WORLD answer has done that already).
func (h *Hub) relay(origin *peer, r world.Result, toOrigin bool) {
	if len(r.Delta) == 0 {
		return
	}
	gs := make([]wire.Gain, len(r.Delta))
	for i, g := range r.Delta {
		gs[i] = wire.Gain(g)
	}
	to := h.sorted(func(q *peer) bool {
		return q.bound() && q.features&FeatureImage != 0 && (toOrigin || q != origin)
	})
	// A peer without switch or story sync gets the delta without those
	// bytes, or nothing when that is all it was. Peers granted the same
	// share one encoding.
	var groups []Feature
	byGroup := map[Feature][]*peer{}
	for _, q := range to {
		g := q.features & gated
		if byGroup[g] == nil {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], q)
	}
	for _, g := range groups {
		if rest, _ := dropGated(gs, g); len(rest) > 0 {
			h.sendAll(byGroup[g], &wire.GainsDown{Seq: r.Seq, Origin: byte(origin.id), Gains: rest})
		}
	}
}

// rejects counts what the world refused of p's proposal and tells p about
// tombstoned bits. Policy refusals are only counted: the replica can't
// know the host's policy, and in M2 every story bit is one.
func (h *Hub) rejects(p *peer, rej []world.Reject) {
	if len(rej) == 0 {
		return
	}
	p.rejected += len(rej)
	var tomb []world.Gain
	for _, r := range rej {
		if r.Why == world.ReasonTombstone {
			tomb = append(tomb, world.Gain{Off: r.Off, Val: r.Val})
		}
	}
	if len(tomb) > 0 {
		h.complain(p, wire.NoticeTombstone, "the host blocked %s; those bits are not shared", summarize(tomb))
	}
}

// sendWorld sends p the world image, without the memory switches or story
// bits unless p syncs them.
func (h *Hub) sendWorld(p *peer) {
	cur := h.store.Image()
	img, _ := strip(&cur, p.features)
	h.send(p, &wire.World{WorldID: wire.ID(h.id), Seq: h.store.Seq(), Image: wire.Image(img)})
}

// sendTombstones tells p every bit the host blocked, so its replica never
// proposes them.
func (h *Hub) sendTombstones(p *peer) {
	tomb := h.store.World().Tombstones()
	for _, e := range tomb.Entries() {
		h.send(p, &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(e.Off, e.Val)})
	}
}

// --- PRESENCE and SCOPED ------------------------------------------------------

// placeOf is where a PRESENCE says the player is.
func placeOf(m *wire.Presence) world.Place {
	if m.Flags&wire.PresenceInGame == 0 {
		return world.Nowhere
	}
	return world.Place{Stage: m.Stage.String(), Table: int(m.Table), Room: int(m.Room)}
}

func (h *Hub) onPresence(p *peer, m *wire.Presence) {
	p.presence = m
	p.place = placeOf(m)
	if p.bound() {
		mv := h.scopes.SetPresence(p.id, p.place)
		h.sendEntries(p, mv.Enter, p.features)
		h.dropped(mv.Dropped)
	}
	h.sendAll(h.sorted(func(q *peer) bool { return q != p && q.welcomed() }), m)
}

// sendPresences sends a newcomer where every other peer is.
func (h *Hub) sendPresences(p *peer) {
	for _, q := range h.sorted(func(q *peer) bool { return q != p && q.welcomed() && q.presence != nil }) {
		h.send(p, q.presence)
	}
}

// enterScopes puts a newly bound peer where it is.
func (h *Hub) enterScopes(p *peer) {
	mv := h.scopes.SetPresence(p.id, p.place)
	h.sendEntries(p, mv.Enter, p.features)
	h.dropped(mv.Dropped)
}

// leaveBound takes p out of the scoped stores when it stops being bound.
func (h *Hub) leaveBound(p *peer) {
	if p.bound() {
		h.dropped(h.scopes.Leave(p.id))
	}
}

// leaveView tells the other peers that p is gone (a PRESENCE of nowhere),
// if they ever saw it.
func (h *Hub) leaveView(p *peer) {
	if !p.welcomed() || p.presence == nil {
		return
	}
	p.presence = nil
	gone := &wire.Presence{Table: -1, Room: -1, Layer: -1}
	h.sendAll(h.sorted(func(q *peer) bool { return q != p && q.welcomed() }), gone)
}

func (h *Hub) dropped(ks []world.Key) {
	for _, k := range ks {
		h.logf("co-op: dropped the %s store: nobody is there", k)
	}
}

// scopeFeature is the feature a kind of scoped store needs.
func scopeFeature(k wire.ScopeKind) Feature {
	if k == wire.ScopeKill {
		return FeatureCombat
	}
	return FeaturePuzzle
}

func (h *Hub) onScoped(p *peer, m *wire.Scoped) {
	if !p.bound() {
		h.complain(p, wire.NoticeNotBound, "refused SCOPED: your file is not bound to world %s", h.id)
		return
	}
	need := scopeFeature(m.Kind)
	if p.features&need == 0 {
		h.complain(p, wire.NoticeRejected, "refused SCOPED %s: %s sync is off for this session", m.Kind, need)
		return
	}
	k := world.Key{Kind: world.ScopeKind(m.Kind), Stage: m.Stage.String(), Table: int(m.Table), Room: int(m.Room)}
	if k.Kind == world.ScopeDan {
		k.Room = -1 // a dan store is per stage and table (world.Scopes)
	}
	words := make([]world.Word, len(m.Words))
	for i, w := range m.Words {
		words[i] = world.Word(w)
	}
	r, err := h.scopes.Apply(p.id, k, words)
	if err != nil {
		h.complain(p, wire.NoticeRejected, "refused SCOPED: %v", err)
		return
	}
	// The bits the M1 verdict refuses are only counted, as the memory
	// switch policy's are (rejects): a replica granted FeaturePuzzle has
	// the host's tables (TableFeatures) and its sender proposes with the
	// same verdict (ScopeFilter), so only another build's sender, or a
	// buggy one, is ever refused.
	for _, w := range r.Filtered {
		p.scopedRefused += bits.OnesCount32(w.Bits)
	}
	if len(r.Delta) == 0 {
		return
	}
	var to []*peer
	for _, id := range r.To {
		if q := h.byID[id]; q != nil && q.bound() && q.features&need != 0 {
			to = append(to, q)
		}
	}
	h.sendAll(to, scopedMsg(m.Stage, k, r.Delta))
}

// sendEntries sends p the stored content es of the kinds features f
// cover: what p newly reaches, or newly may take.
func (h *Hub) sendEntries(p *peer, es []world.Entry, f Feature) {
	for _, e := range es {
		if f&scopeFeature(wire.ScopeKind(e.Key.Kind)) == 0 || len(e.Words) == 0 {
			continue
		}
		st, err := wire.MakeStage(e.Key.Stage)
		if err != nil {
			h.logf("co-op: bug: store %s: %v", e.Key, err)
			continue
		}
		h.send(p, scopedMsg(st, e.Key, e.Words))
	}
}

func scopedMsg(st wire.Stage, k world.Key, ws []world.Word) *wire.Scoped {
	out := &wire.Scoped{Kind: wire.ScopeKind(k.Kind), Stage: st, Table: int8(k.Table), Room: int8(k.Room)}
	out.Words = make([]wire.ScopedWord, len(ws))
	for i, w := range ws {
		out.Words[i] = wire.ScopedWord(w)
	}
	return out
}

// --- CTRL ---------------------------------------------------------------------

func (h *Hub) onCtrl(p *peer, m *wire.Ctrl) {
	switch m.Op {
	case wire.CtrlResync:
		if p.bound() && p.features&FeatureImage == 0 {
			h.complain(p, wire.NoticeRejected, "refused RESYNC: image sync is off for this session")
			return
		}
		h.sendWorld(p)
	case wire.CtrlCoopOff:
		// The replica turned features off (or back on) on its side.
		p.want = Feature(m.Arg) & SessionFeatures
		h.regrant(p)
	case wire.CtrlTombstone:
		if !p.host {
			h.complain(p, wire.NoticeRejected, "refused TOMBSTONE: only the host blocks bits")
			return
		}
		off, mask, _ := m.Tombstone()
		if err := h.tombstone(int(off), mask, p.String()); err != nil {
			h.complain(p, wire.NoticeRejected, "refused TOMBSTONE: %v", err)
		}
	}
}

// regrant recomputes p's features and tells it (CTRL COOP_OFF with the
// features it now has). A bound peer that gets the image, its switches or
// its story bits back is asked for a fresh WORLD, since it missed those
// relays and the hub dropped those bytes of its proposals. One that gets
// puzzle or combat sync back is sent the stores of that kind its place
// reaches, as on entering it: it missed their relays too, and its own
// sender proposes its words again on its next scope entry or refresh
// (replica.Differ.ShareScopes).
func (h *Hub) regrant(p *peer) {
	old := p.features
	p.features = h.grant(p)
	h.send(p, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(p.features)})
	if p.bound() && p.features&FeatureImage != 0 && p.features&^old&(FeatureImage|gated) != 0 {
		h.send(p, &wire.Ctrl{Op: wire.CtrlResync})
	}
	if gained := p.features &^ old & (FeaturePuzzle | FeatureCombat); p.bound() && gained != 0 {
		h.sendEntries(p, h.scopes.Reached(p.id), gained)
	}
}

// tombstone blocks bits mask of image byte off and tells every peer.
func (h *Hub) tombstone(off int, mask byte, by string) error {
	before := h.store.Seq()
	r, err := h.store.Tombstone(off, mask)
	if err != nil {
		return err
	}
	if r.Seq == before {
		return nil // already blocked
	}
	mask &= world.SharedMask(off)
	rule := class.Of(off)
	what := fmt.Sprintf("bits 0x%02X of %s (image byte 0x%03X)", mask, rule.Field, off)
	h.logf("co-op: %s blocked %s for good (seq %d)", by, what, r.Seq)
	to := h.sorted((*peer).welcomed)
	h.sendAll(to, &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(uint16(off), mask)})
	h.sendAll(to, &wire.Notice{Code: wire.NoticeTombstone, Text: clip(fmt.Sprintf("%s blocked %s: no longer shared; files that already have them keep them", by, what))})
	return nil
}

// --- NOTICE -------------------------------------------------------------------

// notify sends p a NOTICE.
func (h *Hub) notify(p *peer, code wire.NoticeCode, text string) {
	h.send(p, &wire.Notice{Code: code, Text: clip(text)})
}

// complain is notify for a refused frame, at most once per complainEvery
// for each code, and logged likewise.
func (h *Hub) complain(p *peer, code wire.NoticeCode, format string, args ...any) {
	now := h.now()
	if t, ok := p.complained[code]; ok && now.Sub(t) < complainEvery && now.Sub(t) >= 0 {
		return
	}
	p.complained[code] = now
	text := fmt.Sprintf(format, args...)
	h.notify(p, code, text)
	h.logf("co-op: %s: %s", p, text)
}

// clip makes s a valid NOTICE text: control characters become spaces,
// invalid UTF-8 is dropped, and it is cut to wire.MaxNoticeLen bytes on a
// rune boundary, with an ellipsis when cut.
func clip(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) <= wire.MaxNoticeLen {
		return s
	}
	const more = "..."
	n := wire.MaxNoticeLen - len(more)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + more
}

// summarize names the fields gains touch, with how many bytes of each
// ("mGetItem (2), memBit[3] mTbox (1)"), at most five fields.
func summarize(gs []world.Gain) string {
	var names []string
	count := map[string]int{}
	for _, g := range gs {
		f := class.Of(int(g.Off)).Field
		if count[f] == 0 {
			names = append(names, f)
		}
		count[f]++
	}
	var parts []string
	for i, f := range names {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more fields", len(names)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", f, count[f]))
	}
	return fmt.Sprintf("%d bytes: %s", len(gs), strings.Join(parts, ", "))
}

// events lists event bit ids as 0xHHLL.
func events(ids []uint16) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("0x%04X", id)
	}
	return strings.Join(parts, ", ")
}
