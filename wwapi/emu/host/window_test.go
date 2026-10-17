package host

import "testing"

func TestPickGameWindow(t *testing.T) {
	popup := windowCandidate{hwnd: 1, title: "Update available", area: 400 * 200}
	gameList := windowCandidate{hwnd: 2, title: "Dolphin 2606", area: 1280 * 720}
	render := windowCandidate{hwnd: 3, title: "Dolphin 2606 | D3D11 | The Wind Waker (GZLE01)", area: 640 * 528}

	cases := []struct {
		name  string
		cands []windowCandidate
		want  uintptr
	}{
		{"none", nil, 0},
		{"render window wins even when smaller and enumerated last", []windowCandidate{popup, gameList, render}, 3},
		{"no render window: largest, not the popup", []windowCandidate{popup, gameList}, 2},
		{"render to main window: the main window carries the tag", []windowCandidate{popup,
			{hwnd: 4, title: "Dolphin 2606 | The Wind Waker (GZLE01)", area: 1280 * 720}}, 4},
	}
	for _, tc := range cases {
		if got := pickGameWindow(tc.cands); got != tc.want {
			t.Errorf("%s: pickGameWindow = %d, want %d", tc.name, got, tc.want)
		}
	}
}
