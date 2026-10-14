package tui

import (
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short, 10) = %q, want it unchanged", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("truncate(abcdefghij, 5) = %q, want %q", got, "abcd…")
	}
	if got := truncate("abc", 1); got != "…" {
		t.Errorf("truncate(abc, 1) = %q, want %q", got, "…")
	}
	if got := truncate("abc", 0); got != "" {
		t.Errorf("truncate(abc, 0) = %q, want empty", got)
	}

	// Styled and wide-character input: the visible width must fit and
	// the cut must land on a whole rune.
	for _, s := range []string{
		valueStyle.Render("192.168.1.10, 10.0.0.5, 172.20.0.1"),
		"日本語のプレイヤー名です",
		"Zoë Zoë Zoë Zoë Zoë",
	} {
		got := truncate(s, 7)
		if w := lipgloss.Width(got); w > 7 {
			t.Errorf("truncate(%q, 7) is %d cells wide: %q", s, w, got)
		}
		if !strings.HasSuffix(plain(got), "…") {
			t.Errorf("truncate(%q, 7) = %q, want a trailing ellipsis", s, got)
		}
		if strings.ContainsRune(got, unicode.ReplacementChar) {
			t.Errorf("truncate(%q, 7) split a rune: %q", s, got)
		}
	}
}

func TestDisplayText(t *testing.T) {
	got := displayText("line one\nline two\tend\x1b[31mred\r")
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("displayText left control rune %q in %q", r, got)
		}
	}
	if !strings.Contains(got, "line one line two end") {
		t.Errorf("displayText(...) = %q, want controls turned into spaces", got)
	}
}

func TestWrapTail(t *testing.T) {
	entries := []string{"old entry", "middle", strings.Repeat("x", 25)}

	// 25 x's at width 10 wrap to 3 rows; with 4 rows the middle entry
	// fits above them and the oldest one is scrolled off.
	rows := wrapTail(entries, 4, 10)
	if len(rows) != 4 {
		t.Fatalf("wrapTail returned %d rows, want 4: %q", len(rows), rows)
	}
	if !strings.HasPrefix(rows[0], "middle") {
		t.Errorf("row 0 = %q, want the middle entry", rows[0])
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > 10 {
			t.Errorf("row %d is %d cells wide, want <= 10: %q", i, w, r)
		}
	}

	// A newest entry taller than the window keeps its start.
	long := wrapTail([]string{"START " + strings.Repeat("y", 60)}, 2, 10)
	if len(long) != 2 || !strings.HasPrefix(long[0], "START") {
		t.Errorf("wrapTail of one long entry = %q, want 2 rows starting with START", long)
	}

	// Short history is blank-padded at the bottom.
	short := wrapTail([]string{"only"}, 3, 10)
	if len(short) != 3 || !strings.HasPrefix(short[0], "only") || short[2] != "" {
		t.Errorf("wrapTail short history = %q, want [only, \"\", \"\"]", short)
	}

	if got := wrapTail(entries, 0, 10); got != nil {
		t.Errorf("wrapTail with 0 rows = %q, want nil", got)
	}
}

func TestClipRows(t *testing.T) {
	if got := clipRows("a\nb\nc", 2); got != "a\nb" {
		t.Errorf("clipRows(a,b,c / 2) = %q, want %q", got, "a\nb")
	}
	if got := clipRows("a\nb", 5); got != "a\nb" {
		t.Errorf("clipRows(a,b / 5) = %q, want it unchanged", got)
	}
}
