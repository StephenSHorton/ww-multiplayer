package hub

import (
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// The hub behind a real network.Server on loopback, with two dry-run
// replicas reduced to their image: each one's game sets (and now and then
// clears) bits, its differ proposes rising edges as GAINS, and its view of
// the world is the WORLD replies joined and the GAINS relays applied. They
// talk over real co-op connections (network.NewCoopClient), drop and come
// back (CO_HELLO, then WORLD both ways, §5.5), resend batches, and the host
// restarts on its world file mid-run. Once everything is delivered, every
// replica's view is the world, the world holds every bit a replica still
// has, nothing nobody proposed, and the world file says the same.

// host is the host side: a server and its hub over one world directory.
type host struct {
	t    *testing.T
	root string
	srv  *network.Server
	hub  *Hub
	addr string
}

func (hs *host) start(st *world.Store) {
	hs.t.Helper()
	h, err := New(Config{Store: st, StageTblHash: testTables, Features: DefaultFeatures, Policy: world.FullPolicy()})
	if err != nil {
		hs.t.Fatal(err)
	}
	srv := network.NewServer(0)
	srv.Coop = h
	if err := srv.Start(); err != nil {
		hs.t.Fatal(err)
	}
	hs.srv, hs.hub = srv, h
	hs.addr = fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port)
}

func (hs *host) stop() {
	hs.t.Helper()
	hs.srv.Stop()
	if err := hs.hub.Stop(); err != nil {
		hs.t.Fatalf("hub stop: %v", err)
	}
}

// restart stops the host and starts it again on the world file.
func (hs *host) restart() {
	hs.t.Helper()
	hs.stop()
	st, _, err := world.Open(hs.root, testWorld, world.Options{})
	if err != nil {
		hs.t.Fatalf("reopening the world: %v", err)
	}
	hs.start(st)
}

// model is one replica.
type model struct {
	t     *testing.T
	name  string
	nonce byte
	cc    *network.Client

	welcomes chan *wire.CoWelcome

	mu       sync.Mutex
	local    world.Image // what its game holds
	baseline world.Image // what its differ last looked at
	view     world.Image // the world as the hub told it
	sent     world.Image // the join of everything it ever proposed
	batch    uint32
	online   bool
	gen      int    // the connection; frames of an older one are ignored
	asked    []bool // the WORLDs this connection asked for, in order: true to join one into view
}

func newModel(t *testing.T, name string, nonce byte) *model {
	return &model{t: t, name: name, nonce: nonce, welcomes: make(chan *wire.CoWelcome, 16)}
}

func (m *model) onCoop(gen int, p []byte) {
	msg, err := wire.Decode(p, wire.Down)
	if err != nil {
		m.t.Errorf("%s: undecodable message from the hub: %v", m.name, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if gen != m.gen {
		return
	}
	switch x := msg.(type) {
	case *wire.CoWelcome:
		select {
		case m.welcomes <- x:
		default:
			m.t.Errorf("%s: CO_WELCOMEs pile up", m.name)
		}
	case *wire.World:
		if len(m.asked) == 0 {
			m.t.Errorf("%s: a WORLD nobody asked for", m.name)
			return
		}
		join := m.asked[0]
		m.asked = m.asked[1:]
		if join {
			img := world.Image(x.Image)
			m.view = world.Join(&m.view, &img) // a late WORLD reply never takes back a newer relay
		}
	case *wire.GainsDown:
		gs := make([]world.Gain, len(x.Gains))
		for i, g := range x.Gains {
			gs[i] = world.Gain(g)
		}
		world.Apply(&m.view, gs)
	}
}

func (m *model) send(msg wire.Msg) bool {
	b, err := wire.Encode(msg)
	if err != nil {
		m.t.Fatalf("%s: encoding %s: %v", m.name, msg.Sub(), err)
	}
	return m.cc.SendCoop(b) == nil
}

// connect dials the host, says hello and sends its whole image. Each
// connection gets a new client: on a Client dialled again at once, the old
// connection's read loop can still fail after the new one is up and clear
// the shared connected flag (its error path doesn't check that its conn is
// the current one), which RunCoop's backoff never lets happen.
func (m *model) connect(addr string) {
	m.t.Helper()
	if m.cc != nil {
		m.cc.Disconnect()
	}
	m.mu.Lock()
	m.gen++
	gen := m.gen
	m.asked = nil
	m.mu.Unlock()
	m.cc = network.NewCoopClient(m.name, 0)
	m.cc.OnCoop = func(p []byte) { m.onCoop(gen, p) }
	if err := m.cc.Connect(addr); err != nil {
		m.t.Fatalf("%s: connect: %v", m.name, err)
	}
	for len(m.welcomes) > 0 {
		<-m.welcomes
	}
	if !m.send(hello(wire.FileBound, testWorld, m.nonce)) {
		m.t.Fatalf("%s: sending CO_HELLO failed", m.name)
	}
	select {
	case w := <-m.welcomes:
		if w.Status != wire.StatusOK {
			m.t.Fatalf("%s: welcome %+v", m.name, w)
		}
	case <-time.After(5 * time.Second):
		m.t.Fatalf("%s: no CO_WELCOME", m.name)
	}
	m.mu.Lock()
	img := world.Shared(&m.local)
	m.baseline = m.local
	m.sent = world.Join(&m.sent, &img)
	m.online = true
	m.asked = append(m.asked, true)
	m.mu.Unlock()
	m.send(&wire.World{WorldID: wire.ID(testWorld), Image: wire.Image(img)})
}

// barrier returns once the hub has handled everything m sent: a RESYNC
// is answered with a WORLD, after every earlier frame on the connection.
// That WORLD is not joined into the view, which must converge through the
// relays alone.
func (m *model) barrier() {
	m.t.Helper()
	m.mu.Lock()
	m.asked = append(m.asked, false)
	m.mu.Unlock()
	m.send(&wire.Ctrl{Op: wire.CtrlResync})
	if !waitFor(5*time.Second, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.asked) == 0
	}) {
		m.t.Fatalf("%s: no WORLD answering RESYNC", m.name)
	}
}

func (m *model) disconnect() {
	m.cc.Disconnect()
	m.mu.Lock()
	m.online = false
	m.mu.Unlock()
}

// play is one step of its game: new bits, a raised counter, and now and
// then a cleared bit (the game's resets), which must never travel.
func (m *model) play(r *rand.Rand) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for n := r.IntN(4); n >= 0; n-- {
		off := r.IntN(world.ImageSize)
		if world.SharedMask(off) == 0 {
			continue
		}
		if isMaxByte(off) {
			m.local[off] = max(m.local[off], byte(r.IntN(int(world.SharedMask(off))+1)))
		} else {
			m.local[off] |= byte(r.Uint32()) & byte(r.Uint32())
		}
	}
	if r.IntN(5) == 0 {
		off := r.IntN(world.ImageSize)
		m.local[off] &^= byte(r.Uint32())
	}
}

// diff proposes the rising edges since its last look, sometimes twice
// (a repeat the hub drops) and sometimes again under a new batch (which
// merges to nothing).
func (m *model) diff(r *rand.Rand) {
	m.mu.Lock()
	gs := world.Diff(&m.baseline, &m.local)
	m.baseline = m.local
	online := m.online
	var up *wire.GainsUp
	if online && len(gs) > 0 {
		m.batch++
		up = &wire.GainsUp{Batch: m.batch, Gains: make([]wire.Gain, len(gs))}
		for i, g := range gs {
			up.Gains[i] = wire.Gain(g)
		}
		var im world.Image
		world.Apply(&im, gs)
		m.sent = world.Join(&m.sent, &im)
	}
	m.mu.Unlock()
	if up == nil {
		return // offline: the reconnect's WORLD carries them
	}
	m.send(up)
	switch r.IntN(6) {
	case 0:
		m.send(up)
	case 1:
		m.mu.Lock()
		m.batch++
		again := &wire.GainsUp{Batch: m.batch, Gains: up.Gains}
		m.mu.Unlock()
		m.send(again)
	}
}

func isMaxByte(off int) bool { return class.Of(off).Class == class.Max }

func TestLoopbackConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("real sockets")
	}
	for seed := uint64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) { loopbackRun(t, seed) })
	}
}

func loopbackRun(t *testing.T, seed uint64) {
	r := rand.New(rand.NewPCG(seed, 23))
	root := t.TempDir()
	st, err := world.Create(root, world.New(testWorld, time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	hs := &host{t: t, root: root}
	hs.start(st)
	reps := []*model{newModel(t, "alice", 1), newModel(t, "bob", 2)}
	defer func() {
		for _, m := range reps {
			if m.cc != nil {
				m.cc.Disconnect()
			}
		}
	}()
	for _, m := range reps {
		m.connect(hs.addr)
	}

	restarts, reconnects := 0, 0
	restart := func() {
		restarts++
		hs.restart()
		for _, m := range reps {
			m.disconnect()
		}
	}
	for step := 0; step < 300; step++ {
		if step == 150 {
			restart() // every run restarts the host at least once
		}
		m := reps[r.IntN(len(reps))]
		switch p := r.IntN(100); {
		case p < 40:
			m.play(r)
		case p < 80:
			m.diff(r)
		case p < 86:
			m.disconnect()
		case p < 92:
			reconnects++
			m.connect(hs.addr)
		case p < 93 && restarts < 3:
			restart()
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if reconnects == 0 {
		t.Fatalf("seed %d: the schedule had no reconnect; pick another seed", seed)
	}

	// Settle: everyone back online, then a few rounds of play that travel
	// as GAINS alone (each origin's view gets its own bits only from the
	// relay), then a barrier per replica so nothing is still in flight.
	for _, m := range reps {
		m.mu.Lock()
		online := m.online && m.cc.IsConnected()
		m.mu.Unlock()
		if !online {
			m.connect(hs.addr)
		}
	}
	for range 5 {
		for _, m := range reps {
			m.play(r)
			m.diff(r)
		}
	}
	for _, m := range reps {
		m.barrier()
	}
	converged := waitFor(10*time.Second, func() bool {
		w := hs.hub.store.Image()
		for _, m := range reps {
			m.mu.Lock()
			same := m.view == w
			m.mu.Unlock()
			if !same {
				return false
			}
		}
		return true
	})
	w := hs.hub.store.Image()
	var proposed world.Image
	for _, m := range reps {
		m.mu.Lock()
		if !converged && m.view != w {
			d := world.Diff(&m.view, &w)
			t.Errorf("%s's view lacks %d bytes of the world: %v", m.name, len(d), d)
		}
		if d := world.Diff(&w, &m.local); len(d) > 0 {
			t.Errorf("the world lacks %s's %v", m.name, d)
		}
		proposed = world.Join(&proposed, &m.sent)
		m.mu.Unlock()
	}
	if d := world.Diff(&proposed, &w); len(d) > 0 {
		t.Errorf("the world holds bits nobody proposed: %v", d)
	}
	seq := hs.hub.store.Seq()
	for _, m := range reps {
		m.cc.Disconnect()
	}
	hs.stop()
	onDisk, rep, err := world.Load(root, testWorld)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Image() != w || onDisk.Seq() != seq || rep.Replayed != 0 {
		t.Errorf("the world file (seq %d, %d replayed) differs from the world the hub served (seq %d)", onDisk.Seq(), rep.Replayed, seq)
	}
}
