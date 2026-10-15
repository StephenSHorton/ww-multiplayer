package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// The co-op integration tests run the session plumbing of host, join and
// mp-local --coop (coop_session.go) over FakeGames: startCoopHost's
// backup, founding and hub, a real network.Server on loopback, and each
// Dolphin's coopSync with its prompts. Only the Dolphin handle is faked.

// Stages the tests load. The names are only labels; the tables are the
// ones the real stages use.
var (
	coSea     = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	coDRC     = fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
	coDRCBoss = fakegame.Stage{Name: "M_DragB", Table: addr.TableDRC, KeyHUD: true}
	coWoods   = fakegame.Stage{Name: "kindan", Table: addr.TableFW, KeyHUD: true}
)

// coLog is a Reporter for the tests: it keeps every line, from any
// goroutine. on, if set, sees each line once it is kept.
type coLog struct {
	mu    sync.Mutex
	lines []string
	on    func(msg string)
}

func (l *coLog) Log(_ report.Level, msg string) {
	l.mu.Lock()
	l.lines = append(l.lines, msg)
	l.mu.Unlock()
	if l.on != nil {
		l.on(msg)
	}
}

func (l *coLog) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func (l *coLog) has(sub string) bool { return l.count(sub) > 0 }

func (l *coLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// coUserDir is a Dolphin user directory with a memory card in it, for the
// session-start backup.
func coUserDir(t *testing.T) string {
	t.Helper()
	ud := filepath.Join(t.TempDir(), "Dolphin Emulator")
	card := filepath.Join(ud, "GC", "MemoryCardA.USA.raw")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("a memory card"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ud
}

// coGame is one emulated Dolphin: a FakeGame over a FakeMem, playing a
// file that was saved and loaded back from the card (so mDateIPL is set,
// tag rule V5). Its Store writes are the game's; the code under test
// writes with Mem.Write, which the FakeMem records.
type coGame struct {
	t *testing.T
	g *fakegame.FakeGame
	m *mem.FakeMem
}

// PER_PLAYER values each game gets, so a byte of them on the wire is
// spotted: life, rupees and magic.
type coMine struct {
	life, rupee uint16
	magic       byte
}

func newCoGame(t *testing.T, name string, mine coMine) *coGame {
	t.Helper()
	m := mem.NewFakeMem()
	cg := &coGame{t: t, g: fakegame.New(m), m: m}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cg.g.NewGame(0, name))
	must(cg.g.LoadStage(coSea))
	must(m.Store(addr.SaveImage+addr.OffLife, u16b(mine.life)))
	must(m.Store(addr.SaveImage+addr.OffRupee, u16b(mine.rupee)))
	must(m.Store(addr.SaveImage+addr.OffMagic, []byte{mine.magic}))
	must(cg.g.Save())
	cg.g.EnterFileSelect()
	must(cg.g.LoadFile(0))
	must(cg.g.LoadStage(coSea))
	return cg
}

// leakTag puts a tag of another file in the loaded file's dSv_reserve_c,
// as card_to_memory brings one in when a bound file's mReserve survived
// into a new file (§5.2): its name hash is another player's (V4 fails).
func (cg *coGame) leakTag(w bind.WorldID) {
	cg.t.Helper()
	name := make([]byte, bind.PlayerNameSize)
	copy(name, "Aryll")
	tag := bind.Tag{Layout: bind.LayoutV1, World: w, Nonce: bind.Nonce{0x1E, 0xA4}, NameHash: bind.NameHash(name)}
	b := tag.Encode()
	if err := cg.m.Store(addr.Reserve, b[:]); err != nil {
		cg.t.Fatal(err)
	}
}

func (cg *coGame) read(a uint32, n int) []byte {
	cg.t.Helper()
	b, err := cg.m.Read(a, n)
	if err != nil {
		cg.t.Fatal(err)
	}
	return b
}

// byteAddr is where the game keeps image byte off: the live memBit for the
// loaded table's slot (its flag setters write mMemBit, d_save.cpp:
// 1072-1139), the saved image otherwise.
func (cg *coGame) byteAddr(off int) uint32 {
	if off >= addr.OffMemory && off < addr.OffOcean {
		t, rel := (off-addr.OffMemory)/addr.MemBitSize, (off-addr.OffMemory)%addr.MemBitSize
		if t == int(int8(cg.read(addr.DanStageNo, 1)[0])) {
			return addr.LiveMemBit + uint32(rel)
		}
	}
	return addr.SaveImage + uint32(off)
}

// put sets image byte off to v as the game would.
func (cg *coGame) put(off int, v byte) {
	cg.t.Helper()
	if err := cg.m.Store(cg.byteAddr(off), []byte{v}); err != nil {
		cg.t.Fatal(err)
	}
}

func (cg *coGame) get(off int) byte { return cg.read(cg.byteAddr(off), 1)[0] }

// image is the game's canonical image now.
func (cg *coGame) image() world.Image {
	cg.t.Helper()
	s, err := snap.Read(cg.m)
	if err != nil {
		cg.t.Fatal(err)
	}
	return world.Image(s.Image())
}

// play does one random thing in the game: progress (a bit or a raised MAX
// value somewhere the default policy shares) or a transition.
func (cg *coGame) play(rng *rand.Rand, or, mx []int) {
	cg.t.Helper()
	switch k := rng.IntN(20); {
	case k < 12:
		off := or[rng.IntN(len(or))]
		m := world.SharedMask(off)
		for {
			if b := byte(1) << rng.IntN(8); m&b != 0 {
				cg.put(off, cg.get(off)|b)
				return
			}
		}
	case k < 14:
		off := mx[rng.IntN(len(mx))]
		m := world.SharedMask(off)
		cur := cg.get(off)
		if v := byte(rng.IntN(int(m) + 1)); v&^m == 0 && v > cur&m {
			cg.put(off, cur&^m|v)
		}
	case k < 17:
		if err := cg.g.EnterRoom(rng.IntN(addr.RoomCount), rng.IntN(addr.RoomCount)); err != nil {
			cg.t.Fatal(err)
		}
	default:
		stages := []fakegame.Stage{coSea, coDRC, coDRCBoss, coWoods}
		if err := cg.g.LoadStage(stages[rng.IntN(len(stages))]); err != nil {
			cg.t.Fatal(err)
		}
	}
}

// coShareable lists the image bytes the default policy shares, OR ones
// and MAX ones apart.
func coShareable() (or, mx []int) {
	pol := world.DefaultPolicy()
	for off := range addr.SaveImageSize {
		if pol[off] == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			mx = append(mx, off)
		} else {
			or = append(or, off)
		}
	}
	return or, mx
}

// coWithin is img's bits the default policy shares: what a replica
// proposes of it, and what its planner may plan.
func coWithin(img world.Image) world.Image {
	pol := world.DefaultPolicy()
	s := world.Shared(&img)
	for off := range s {
		if class.Of(off).Class == class.Max && s[off]&^pol[off] != 0 {
			s[off] = 0
		} else {
			s[off] &= pol[off]
		}
	}
	return s
}

// coAudit checks every co-op payload that crosses the wire: each must
// decode, and no WORLD or GAINS may carry a bit that never leaves the
// player (a PER_PLAYER, NEVER or TAG byte, or bits outside a merging
// byte's mask). It keeps its findings for the test to report: the read
// loops it runs on may outlive the test.
type coAudit struct {
	mu     sync.Mutex
	bad    []string
	worlds int // WORLD images checked
	gains  int // GAINS entries checked
}

func (a *coAudit) check(dir wire.Dir, p []byte) {
	m, err := wire.Decode(p, dir)
	var bad []string
	worlds, gains := 0, 0
	switch m := m.(type) {
	case nil:
		bad = append(bad, fmt.Sprintf("%s payload does not decode: %v", dir, err))
	case *wire.World:
		worlds++
		for off, b := range m.Image {
			r := class.Of(off)
			switch {
			case r.Class.Merges():
				if b&^r.Mask != 0 {
					bad = append(bad, fmt.Sprintf("%s WORLD byte 0x%03X (%s) = 0x%02X, outside its mask 0x%02X", dir, off, r.Field, b, r.Mask))
				}
			case r.Class == class.Derived:
			case b != 0:
				bad = append(bad, fmt.Sprintf("%s WORLD carries %s byte 0x%03X (%s) = 0x%02X", dir, r.Class, off, r.Field, b))
			}
		}
	case *wire.GainsUp:
		gains += len(m.Gains)
		bad = append(bad, coAuditGains(dir, m.Gains)...)
	case *wire.GainsDown:
		gains += len(m.Gains)
		bad = append(bad, coAuditGains(dir, m.Gains)...)
	}
	a.mu.Lock()
	a.bad = append(a.bad, bad...)
	a.worlds += worlds
	a.gains += gains
	a.mu.Unlock()
}

func coAuditGains(dir wire.Dir, gs []wire.Gain) []string {
	var bad []string
	for _, g := range gs {
		if r := class.Of(int(g.Off)); !r.Class.Merges() || g.Val&^r.Mask != 0 {
			bad = append(bad, fmt.Sprintf("%s GAINS carries %s byte 0x%03X (%s) = 0x%02X", dir, r.Class, g.Off, r.Field, g.Val))
		}
	}
	return bad
}

// coWire sits between a coopSync's replica and its connection and makes
// the connection misbehave the ways the protocol must survive: inbound
// payloads are held and handed over in a random order, some twice, and
// some outbound GAINS, WORLD and PRESENCE are sent twice. Every payload is
// audited both ways.
type coWire struct {
	mu    sync.Mutex
	rng   *rand.Rand
	r     *replica.Replica
	inbox [][]byte
	audit *coAudit
}

// onCoop is the client's OnCoop: it holds the payload.
func (u *coWire) onCoop(p []byte) {
	u.audit.check(wire.Down, p)
	u.mu.Lock()
	u.inbox = append(u.inbox, bytes.Clone(p))
	u.mu.Unlock()
}

// deliver hands the held payloads to the replica, shuffled, some twice.
func (u *coWire) deliver() {
	u.mu.Lock()
	in := u.inbox
	u.inbox = nil
	u.rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
	twice := make([]bool, len(in))
	for i := range twice {
		twice[i] = u.rng.IntN(5) == 0
	}
	u.mu.Unlock()
	for i, p := range in {
		u.r.Handle(p)
		if twice[i] {
			u.r.Handle(p)
		}
	}
}

// session is network.RunCoop's session: the replica's, with what the last
// connection left unread dropped, as a closed socket's would be.
func (u *coWire) session(ctx context.Context, send func([]byte) error) error {
	u.mu.Lock()
	u.inbox = nil
	u.mu.Unlock()
	dup := func(p []byte) error {
		u.audit.check(wire.Up, p)
		if err := send(p); err != nil {
			return err
		}
		u.mu.Lock()
		again := u.rng.IntN(4) == 0
		u.mu.Unlock()
		switch wire.Sub(p[0]) {
		case wire.SubGains, wire.SubWorld, wire.SubPresence:
			if again {
				return send(p)
			}
		}
		return nil
	}
	return u.r.Session(ctx, dup)
}

// coHost is the host side: startCoopHost's session and a network.Server
// with its hub, on a loopback port that survives restarts.
type coHost struct {
	t    *testing.T
	ctx  context.Context
	rep  *coLog
	env  coopEnv
	cs   *coopSession
	srv  *network.Server
	port int
	addr string
}

func (h *coHost) start(f coopFlags) {
	h.t.Helper()
	cs, err := startCoopHost(h.ctx, h.rep, func(s string) { h.rep.Log(report.Net, s) }, f, h.env)
	if err != nil {
		h.t.Fatalf("startCoopHost: %v\n%s", err, h.rep)
	}
	srv := network.NewServer(h.port)
	srv.Coop = cs.hub
	if err := srv.Start(); err != nil {
		cs.stop()
		h.t.Fatal(err)
	}
	h.cs, h.srv = cs, srv
	if h.port == 0 {
		h.port = srv.Addr().(*net.TCPAddr).Port
		h.addr = fmt.Sprintf("127.0.0.1:%d", h.port)
	}
}

// stop ends the session as runHostSession does: the server, then the hub.
func (h *coHost) stop() {
	if h.srv == nil {
		return
	}
	h.srv.Stop()
	h.cs.stop()
	h.srv = nil
}

// restart stops the host and starts it again with plain --coop, which
// serves the only world on disk: the world comes back from its file.
func (h *coHost) restart() {
	h.t.Helper()
	h.stop()
	h.start(coopFlags{on: true, yes: true})
}

// world is the hub's world now.
func (h *coHost) world() world.Image { return h.cs.store.Image() }

// coPlayer is one Dolphin of the session: its game and its coop-sync
// pipeline, connected through a coWire.
type coPlayer struct {
	*coGame
	name string
	s    *coopSync
	u    *coWire
	rep  *coLog
}

// newCoPlayer starts cg's coop-sync pipeline in session cs, logging to
// log, and connects it to h.
func newCoPlayer(t *testing.T, ctx context.Context, h *coHost, cs *coopSession, cg *coGame, name string, log *coLog, audit *coAudit, seed uint64) *coPlayer {
	t.Helper()
	p := &coPlayer{coGame: cg, name: name, rep: log}
	s, err := cs.newSync(ctx, name, cg.m, p.rep)
	if err != nil {
		t.Fatal(err)
	}
	p.s = s
	p.u = &coWire{rng: rand.New(rand.NewPCG(seed, 7)), r: s.r, audit: audit}
	s.client.OnCoop = p.u.onCoop
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, s.client, h.addr, nil, p.u.session)
	}()
	t.Cleanup(func() { <-done })
	return p
}

// step runs a frame of the game, hands the replica what arrived, and runs
// one replica tick, as coopSync.run's ticker would.
func (p *coPlayer) step() {
	p.g.Frame()
	p.u.deliver()
	p.s.r.Tick()
}

// planned is the image the player's game would reach if the replica's
// plan were applied (M3), within the default policy.
func (p *coPlayer) planned() world.Image {
	img := p.image()
	local := world.Shared(&img)
	return coWithin(replica.Planned(&local, p.s.r.Status().Planned))
}

// tagWrites checks the player's recorded memory writes: each one is the
// whole binding tag and nothing else (a bind or a scrub). It returns them.
func (p *coPlayer) tagWrites(t *testing.T) []mem.WriteRecord {
	t.Helper()
	ws := p.m.Writes()
	for _, w := range ws {
		if w.Addr != addr.Reserve || len(w.Data) != bind.TagSize {
			t.Errorf("%s: a write that is not the binding tag: %d bytes at 0x%08X", p.name, len(w.Data), w.Addr)
		}
	}
	return ws
}

// coMonotone reports how cur lowers anything prev held: an OR bit gone or
// a MAX value lowered.
func coMonotone(prev, cur *world.Image) string {
	for off := range prev {
		if class.Of(off).Class == class.Max {
			if cur[off] < prev[off] {
				return fmt.Sprintf("MAX byte 0x%03X fell from 0x%02X to 0x%02X", off, prev[off], cur[off])
			}
		} else if lost := prev[off] &^ cur[off]; lost != 0 {
			return fmt.Sprintf("byte 0x%03X lost bits 0x%02X", off, lost)
		}
	}
	return ""
}

// coDiff names the bytes where a and b differ, for failure messages.
func coDiff(a, b *world.Image) string {
	var parts []string
	for off := range a {
		if a[off] != b[off] {
			parts = append(parts, fmt.Sprintf("0x%03X %s: 0x%02X vs 0x%02X", off, replica.DefaultLabel(off, -1), a[off], b[off]))
		}
	}
	if len(parts) > 8 {
		parts = append(parts[:8], fmt.Sprintf("and %d more", len(parts)-8))
	}
	return strings.Join(parts, "; ")
}

// TestCoopSessionConverges is M2's end-to-end check of the --coop
// plumbing. The host founds a world from game A's file (--coop=new) and
// runs A's coop-sync pipeline in its own session, as host and mp-local do;
// B joins from a session of its own (join --coop). Both bind their files
// through the --yes prompts (B's carries a leaked tag); then both games
// make progress at random and move between stages and rooms while the
// connections deliver out of order, repeat messages and drop, and the
// host restarts from its world file. Afterwards:
//
//   - the world is the merge of both games' shared progress, nothing else,
//     and it never lost a bit on the way;
//   - each replica's planned image (its game plus the writes its dry-run
//     planner would make) is the world: the two converge once M3 applies
//     the plans;
//   - a bit a game clears itself does not leave the world, nor is it
//     planned back into that game: clears never propagate;
//   - the only memory writes are the binding tags;
//   - no PER_PLAYER, NEVER or TAG byte ever crossed the wire.
func TestCoopSessionConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})
	gb.leakTag(bind.WorldID{0xAA, 0xBB, 1, 2, 3, 4, 5, 6})
	for _, off := range []int{addr.OffLife, addr.OffRupee, addr.OffMagic} {
		if c := class.Of(off).Class; c != class.PerPlayer {
			t.Fatalf("image byte 0x%03X is %s, not PER_PLAYER: the audit would not cover it", off, c)
		}
	}
	ga.m.Record(true)
	gb.m.Record(true)

	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true})
	defer h.stop()
	if !h.rep.has("founded world") || !h.rep.has("backed up 1 memory-card file") {
		t.Fatalf("the host did not back up and found:\n%s", h.rep)
	}
	founded := h.world()
	if a := coWithin(ga.image()); founded != a {
		t.Fatalf("the founded world is not A's file: %s", coDiff(&founded, &a))
	}
	cs := h.cs
	jcs := startCoopSession(&coLog{}, coopFlags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}})
	if jcs.noTags != nil {
		t.Fatalf("the joiner's backup: %v", jcs.noTags)
	}
	audit := &coAudit{}
	a := newCoPlayer(t, ctx, h, cs, ga, "Link", &coLog{}, audit, 1)
	b := newCoPlayer(t, ctx, h, jcs, gb, "Tetra", &coLog{}, audit, 2)
	players := []*coPlayer{a, b}

	stepAll := func() {
		for _, p := range players {
			p.step()
		}
	}
	// A dropped connection or a restarted host is over when the player has
	// been welcomed back: until then its status can still say armed, from
	// before the drop.
	welcomes := func(p *coPlayer) int { return p.rep.count("welcome to world") }
	need := map[*coPlayer]int{}
	drop := func(ps ...*coPlayer) {
		for _, p := range ps {
			need[p] = welcomes(p) + 1
		}
	}
	waitArmed := func(why string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			stepAll()
			armed := true
			for _, p := range players {
				armed = armed && p.s.r.Status().State == replica.StateArmed && welcomes(p) >= need[p]
			}
			if armed {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not armed: %+v / %+v\nhost:\n%s\nA:\n%s\nB:\n%s", why, a.s.r.Status(), b.s.r.Status(), h.rep, a.rep, b.rep)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitArmed("binding")

	// The binds: A's file is the world's, B's had a leaked tag (scrubbed
	// first, or written over by the bind). Each write is the whole tag.
	for _, p := range players {
		ws := p.tagWrites(t)
		if len(ws) == 0 {
			t.Fatalf("%s: armed without a tag write", p.name)
		}
		var raw [bind.TagSize]byte
		copy(raw[:], ws[len(ws)-1].Data)
		tag, err := bind.Decode(raw[:])
		if err != nil || tag.World != cs.world {
			t.Errorf("%s: the last tag write is not a tag of world %s: %+v, %v", p.name, cs.world, tag, err)
		}
		for _, w := range ws[:len(ws)-1] {
			if !bytes.Equal(w.Data, make([]byte, bind.TagSize)) {
				t.Errorf("%s: an earlier tag write that is not a scrub: % X", p.name, w.Data)
			}
		}
	}
	if n := len(a.m.Writes()); n != 1 {
		t.Errorf("A made %d tag writes binding a file the world was founded from, want 1", n)
	}
	if n := len(b.m.Writes()); n > 2 {
		t.Errorf("B made %d tag writes, want a bind, perhaps after a scrub", n)
	}
	if !b.rep.has("leaked") {
		t.Errorf("B's leaked tag went unnoticed:\n%s", b.rep)
	}

	// Play, with drops and a host restart.
	rng := rand.New(rand.NewPCG(2026, 1014))
	or, mx := coShareable()
	prev := h.world()
	const steps = 300
	for i := range steps {
		for _, p := range players {
			if rng.IntN(3) == 0 {
				p.play(rng, or, mx)
			}
			p.step()
		}
		switch i {
		case steps / 4:
			drop(a)
			a.s.client.Disconnect()
		case steps / 2:
			drop(a, b)
			h.restart()
		case 3 * steps / 4:
			drop(a, b)
			b.s.client.Disconnect()
			a.s.client.Disconnect()
		}
		cur := h.world()
		if why := coMonotone(&prev, &cur); why != "" {
			t.Fatalf("step %d: the world went backwards: %s", i, why)
		}
		prev = cur
		time.Sleep(time.Millisecond)
	}

	// Quiesce: no more play; wait until the world and both plans agree.
	converge := func(why string, want func() world.Image, plannedFor func(p *coPlayer, w world.Image) world.Image) {
		t.Helper()
		waitArmed(why)
		deadline := time.Now().Add(20 * time.Second)
		for {
			stepAll()
			w := h.world()
			if why := coMonotone(&prev, &w); why != "" {
				t.Fatalf("the world went backwards: %s", why)
			}
			prev = w
			ok := w == want()
			for _, p := range players {
				st := p.s.r.Status()
				ok = ok && st.State == replica.StateArmed && st.Queued == 0 && st.Seq == h.cs.store.Seq() && p.planned() == plannedFor(p, w)
			}
			if ok {
				return
			}
			if time.Now().After(deadline) {
				wt := want()
				t.Errorf("%s: the world is not the merge of the games: %s", why, coDiff(&w, &wt))
				for _, p := range players {
					pl, exp := p.planned(), plannedFor(p, w)
					t.Errorf("%s: %s's planned image is not what it should be: %s (status %+v)", why, p.name, coDiff(&pl, &exp), p.s.r.Status())
				}
				t.Fatalf("no convergence\nhost:\n%s\nA:\n%s\nB:\n%s", h.rep, a.rep, b.rep)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	merged := func() world.Image {
		ia, ib := coWithin(a.image()), coWithin(b.image())
		return world.Join(&ia, &ib)
	}
	converge("after the play", merged, func(_ *coPlayer, w world.Image) world.Image { return w })

	// Each game clears progress of its own that the other lacks: the
	// world keeps it, the other still plans it, and the game that
	// cleared it does not get it planned back.
	before := h.world()
	cleared := map[*coPlayer]world.Image{} // the bits each game cleared
	ia, ib := a.image(), b.image()
	mine := map[*coPlayer]world.Image{a: coWithin(ia), b: coWithin(ib)}
	for _, pair := range [][2]*coPlayer{{a, b}, {b, a}} {
		p, other := pair[0], pair[1]
		var c world.Image
		n := 0
		for _, off := range or {
			bits := mine[p][off] &^ mine[other][off]
			if bits == 0 || n == 3 {
				continue
			}
			bit := bits & -bits
			p.put(off, p.get(off)&^bit)
			c[off], n = bit, n+1
		}
		if n == 0 {
			t.Fatalf("%s has no progress of its own to clear", p.name)
		}
		cleared[p] = c
	}
	converge("after the clears", func() world.Image { return before }, func(p *coPlayer, w world.Image) world.Image {
		out := w
		for off, bits := range cleared[p] {
			out[off] &^= bits
		}
		return out
	})

	// The host stops and starts on its world file once more: nothing is
	// lost, and the plans stand.
	drop(a, b)
	h.restart()
	converge("after the last restart", func() world.Image { return before }, func(p *coPlayer, w world.Image) world.Image {
		out := w
		for off, bits := range cleared[p] {
			out[off] &^= bits
		}
		return out
	})
	h.stop()
	saved, _, err := world.Load(h.env.worlds, world.ID(cs.world))
	if err != nil {
		t.Fatal(err)
	}
	if img := saved.Image(); img != before {
		t.Errorf("the world file is not the world: %s", coDiff(&img, &before))
	}
	if m, ok := readCoopMarker(world.Path(h.env.worlds, world.ID(cs.world))); ok {
		t.Errorf("the session marker outlived the session: %q", m)
	}

	// Dry run: still nothing but the tags was written.
	if n := len(a.tagWrites(t)); n != 1 {
		t.Errorf("A: %d writes after the play, want the one bind", n)
	}
	if n := len(b.tagWrites(t)); n > 2 {
		t.Errorf("B: %d writes after the play", n)
	}

	// The run did what it claims: both dropped and came back (a drop
	// while the last one is still redialling makes one reconnect of two),
	// and the wire was audited.
	for _, p := range players {
		if n := welcomes(p); n < 3 || !p.rep.has("armed offline") {
			t.Errorf("%s: %d welcomes, offline %v: the reconnects did not happen\n%s", p.name, n, p.rep.has("armed offline"), p.rep)
		}
		if st := p.s.r.Status(); len(st.Planned) == 0 {
			t.Errorf("%s plans nothing: the partner's progress never reached it", p.name)
		}
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, s := range audit.bad {
		t.Error(s)
	}
	if audit.worlds == 0 || audit.gains == 0 {
		t.Errorf("the audit saw %d WORLD and %d GAINS entries: the wire was not watched", audit.worlds, audit.gains)
	}
	if n := h.rep.count(fmt.Sprintf("Co-op world %s at seq", cs.world)); n != 3 || h.rep.has("marked in use") {
		t.Errorf("the host started %d times, not 3, or found its marker:\n%s", n, h.rep)
	}
	t.Logf("world seq %d; %d WORLD images and %d gains audited", saved.Seq(), audit.worlds, audit.gains)
}
