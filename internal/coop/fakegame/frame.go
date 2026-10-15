package fakegame

import (
	"context"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// Run runs frames every d until ctx ends, as the console's main loop
// does: a test can point a FrameWriter, whose writes wait for the
// heartbeat, at a game that keeps running. d <= 0 runs a frame every
// millisecond.
func (g *FakeGame) Run(ctx context.Context, d time.Duration) {
	if d <= 0 {
		d = time.Millisecond
	}
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Frame()
		}
	}
}

// BeginOnSwitch is the first half of dSv_memBit_c::onSwitch for memory
// switch no (0x00-0x7F) in the live memBit: the load of the u32 word that
// holds it (mSwitch[no >> 5], d_save.cpp:1084-1087). The store, with the
// bit ORed in, happens when the returned func is called. A tool byte
// written into that word in between is lost, as on the console, where
// the load, OR and store are three instructions (§4.2.1). A switch
// number outside 0x00-0x7F is ErrAssert, as onSwitch asserts it
// (d_save.cpp:1085).
func (g *FakeGame) BeginOnSwitch(no int) (store func(), err error) {
	if s := addr.ClassifySwitch(no); s != addr.SwitchMemory {
		return nil, assertf("memory switch %d is a %s switch", no, s)
	}
	_, bit, _ := addr.SwitchBit(no)
	at := addr.LiveMemBit + uint32(bit.Off)
	word := at &^ 3
	g.mu.Lock()
	b := g.load(word, 4)
	g.mu.Unlock()
	b[at-word] |= bit.Mask
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.store(word, b)
	}, nil
}
