package placename

import "testing"

func TestDescribe(t *testing.T) {
	for _, c := range []struct {
		stage string
		room  int
		want  string
	}{
		{"M_NewD2", 3, "Dragon Roost Cavern, room 3"},
		{"M_NewD2", -1, "Dragon Roost Cavern"},
		{"sea", 11, "Sea, B4 (Windfall Island)"},
		{"sea", 44, "Sea, G2 (Outset Island)"},
		{"sea", 1, "Sea, A1 (Forsaken Fortress)"},
		{"sea", 49, "Sea, G7 (Five-Star Isles)"},
		{"sea", 0, "Sea"},
		{"sea", -1, "Sea"},
		{"sea", 50, "Sea"},
		{"Pdrgsh", 0, "Pdrgsh, room 0"},
		{"", 3, ""},
		{"GanonK", 0, "Ganon's Tower, room 0"},
	} {
		if got := Describe(c.stage, c.room); got != c.want {
			t.Errorf("Describe(%q, %d) = %q, want %q", c.stage, c.room, got, c.want)
		}
	}
}

func TestSquare(t *testing.T) {
	// In seachart's naming (row letter A-G north to south, column 1-7):
	// Windfall B4, Dragon Roost B6, Tower of the Gods D5, Forest Haven F6,
	// Outset G2.
	for room, want := range map[int]string{11: "B4", 13: "B6", 26: "D5", 41: "F6", 44: "G2"} {
		if got, ok := Square(room); !ok || got != want {
			t.Errorf("Square(%d) = %q, %v; want %q", room, got, ok, want)
		}
	}
	for room, want := range map[int]string{13: "Dragon Roost Island", 26: "Tower of the Gods", 41: "Forest Haven"} {
		if got := Island(room); got != want {
			t.Errorf("Island(%d) = %q, want %q", room, got, want)
		}
	}
	if _, ok := Square(0); ok {
		t.Error("Square(0) should not be a square")
	}
}

func TestStage(t *testing.T) {
	if Stage("kazeB") != "Wind Temple (Molgera)" || !Known("kazeB") {
		t.Error("kazeB")
	}
	if Stage("Xyz") != "Xyz" || Known("Xyz") {
		t.Error("an unknown code should read as itself")
	}
}
