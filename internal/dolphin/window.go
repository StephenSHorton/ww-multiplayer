package dolphin

import "strings"

// windowCandidate is one visible top-level window of a Dolphin process,
// as FindWindowByPID sees it.
type windowCandidate struct {
	hwnd  uintptr
	title string
	area  int64 // width * height in pixels
}

// gameWindowTag is in the title of the window that renders the game
// ("Dolphin ... | The Wind Waker (GZLE01)").
const gameWindowTag = "GZLE01"

// pickGameWindow chooses the window that shows the game: the largest one
// whose title names the game, else the largest window overall (the main
// window, never a small popup such as "Update available"). It returns 0
// when there are no candidates. Kept free of Win32 calls so it is
// testable on every platform.
func pickGameWindow(cands []windowCandidate) uintptr {
	var best, bestTagged *windowCandidate
	for i := range cands {
		c := &cands[i]
		if best == nil || c.area > best.area {
			best = c
		}
		if strings.Contains(c.title, gameWindowTag) && (bestTagged == nil || c.area > bestTagged.area) {
			bestTagged = c
		}
	}
	switch {
	case bestTagged != nil:
		return bestTagged.hwnd
	case best != nil:
		return best.hwnd
	}
	return 0
}
