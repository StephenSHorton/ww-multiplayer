package audio

import (
	"testing"
	"time"
)

// callQuickly runs fn in a goroutine and fails the test if it doesn't
// return within the timeout -- the contract PlayJoin/PlayLeave must
// honor (fire-and-forget, never blocks the caller).
func callQuickly(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("call blocked for >2s; PlayJoin/PlayLeave must be fire-and-forget")
	}
}

func TestPlayJoinLeave_NoAudioEnv_NoopsQuickly(t *testing.T) {
	t.Setenv("WW_NO_AUDIO", "1")
	callQuickly(t, PlayJoin)
	callQuickly(t, PlayLeave)
}

func TestPlayJoinLeave_NeverBlocksOrPanics(t *testing.T) {
	// Deliberately NOT setting WW_NO_AUDIO here: even with audio enabled,
	// the platform backends must return promptly (async playback / a
	// non-waited Start()) and must never panic, since this exercises the
	// real playJoin/playLeave for whichever OS the test runs on. CI can't
	// assert actual sound output, but it can assert the call contract.
	t.Setenv("WW_NO_AUDIO", "")
	callQuickly(t, PlayJoin)
	callQuickly(t, PlayLeave)
}

func TestDisabled(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"1":     true,
		"true":  true,
		"True":  true,
		"false": false,
		"yes":   true, // non-bool truthy-ish value: still treated as opt-out
	}
	for v, want := range cases {
		t.Run(v, func(t *testing.T) {
			t.Setenv("WW_NO_AUDIO", v)
			if got := disabled(); got != want {
				t.Errorf("disabled() with WW_NO_AUDIO=%q = %v, want %v", v, got, want)
			}
		})
	}
}

func TestSafe_RecoversPanic(t *testing.T) {
	panicked := true
	func() {
		defer func() { panicked = recover() != nil }()
		safe(func() { panic("boom") })
	}()
	if panicked {
		t.Fatal("safe() let a panic escape")
	}
}
