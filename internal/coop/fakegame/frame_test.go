package fakegame

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

func TestBeginOnSwitchLosesAToolByteInItsWord(t *testing.T) {
	g, m := newGame(t)
	load(t, g, outset)
	_, b0, _ := addr.SwitchBit(0x00) // mSwitch[0] bits 0-7: the word's last byte
	_, b8, _ := addr.SwitchBit(0x08) // bits 8-15: the byte before it
	store, err := g.BeginOnSwitch(0x08)
	if err != nil {
		t.Fatal(err)
	}
	// A tool sets switch 0x00 between the game's load and store.
	tool := addr.LiveMemBit + uint32(b0.Off)
	if err := m.Write(tool, []byte{b0.Mask}); err != nil {
		t.Fatal(err)
	}
	store()
	if v := u8(t, m, tool); v&b0.Mask != 0 {
		t.Errorf("the tool's switch 0x00 survived the game's word store: 0x%02X", v)
	}
	if v := u8(t, m, addr.LiveMemBit+uint32(b8.Off)); v&b8.Mask == 0 {
		t.Errorf("the game's switch 0x08 is not set: 0x%02X", v)
	}

	// With nothing in between it is OnSwitch.
	store, _ = g.BeginOnSwitch(0x7F)
	store()
	_, b7f, _ := addr.SwitchBit(0x7F)
	if v := u8(t, m, addr.LiveMemBit+uint32(b7f.Off)); v&b7f.Mask == 0 {
		t.Errorf("switch 0x7F is not set: 0x%02X", v)
	}

	for _, no := range []int{-1, 0x80, 0xC0, 0xFF, 0x100} {
		if _, err := g.BeginOnSwitch(no); !errors.Is(err, ErrAssert) {
			t.Errorf("BeginOnSwitch(0x%X) = %v, want ErrAssert", no, err)
		}
	}
}

func TestRunBeatsUntilCancelled(t *testing.T) {
	g, m := newGame(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.Run(ctx, 0)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for u32(t, m, mailbox.SpawnTrigger) < 5 {
		if time.Now().After(deadline) {
			t.Fatal("the heartbeat did not move")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	n := g.FrameCount()
	time.Sleep(5 * time.Millisecond)
	if g.FrameCount() != n || uint64(u32(t, m, mailbox.SpawnTrigger)) != n {
		t.Fatalf("frames ran after Run returned: %d, then %d", n, g.FrameCount())
	}
}
