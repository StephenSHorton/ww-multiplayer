package engine

import "testing"

// TestLiveState_ChatSink: incoming chat goes to the sink Engine set,
// is dropped without one, and survives Begin() (which runs after the engine
// set it).
func TestLiveState_ChatSink(t *testing.T) {
	ls := &LiveState{}
	ls.PushChat("Zelda", "dropped") // no sink: no panic

	var got []string
	ls.SetChat(func(from, text string) { got = append(got, from+": "+text) })
	ls.Begin("Join", "Link", nil)
	ls.PushChat("Zelda", "hi")
	ls.SetChat(nil)
	ls.PushChat("Zelda", "after")

	if len(got) != 1 || got[0] != "Zelda: hi" {
		t.Errorf("sink got %q, want only the line sent while set", got)
	}
}
