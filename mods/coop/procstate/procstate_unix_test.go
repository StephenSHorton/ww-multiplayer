//go:build unix

package procstate

import "testing"

func TestStatStartTime(t *testing.T) {
	tests := []struct{ stat, want string }{
		{"1234 (ww-multiplayer) S 1 1234 1234 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 98765 1000 10\n", "98765"},
		// A command name with spaces and parentheses.
		{"1234 (a) (b c) S 1 1234 1234 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 98765 1000 10\n", "98765"},
		{"1234 (short) S 1 2 3\n", ""},
		{"1234 no name S 1 1234 1234 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 98765\n", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := statStartTime([]byte(tt.stat)); got != tt.want {
			t.Errorf("statStartTime(%q) = %q, want %q", tt.stat, got, tt.want)
		}
	}
}
