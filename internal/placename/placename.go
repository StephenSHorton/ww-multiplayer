// Package placename turns a game place (a stage code and a room, as the
// position's place trailer carries them: mods/multiplayer/net/place.go)
// into words a player reads: "Dragon Roost Cavern, room 3", "Sea, D2
// (Windfall Island)". The app's Players rows, its "You are in" header,
// the co-op place chip and the sea chart share it, so one place reads the
// same everywhere.
//
// The names are hand-written. A stage this table doesn't know reads as its
// code ("Pdrgsh, room 0"), never as a guess.
package placename

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/seachart"
)

// Sea is the Great Sea's stage code: one stage whose rooms 1..49 are the
// chart's squares.
const Sea = "sea"

// stages are the display names of the stage codes (mCurStage.mName,
// d_stage.h:949; the list in JAIZelScene.cpp). A boss or miniboss stage
// names its dungeon.
var stages = map[string]string{
	Sea: "Sea",

	"M_NewD2": "Dragon Roost Cavern",
	"M_Dra09": "Dragon Roost Cavern",
	"M_DragB": "Dragon Roost Cavern (Gohma)",
	"Adanmae": "Dragon Roost Island",
	"Atorizk": "Dragon Roost Island",

	"kindan":  "Forbidden Woods",
	"kinMB":   "Forbidden Woods (Mothula)",
	"kinBOSS": "Forbidden Woods (Kalle Demos)",
	"Omori":   "Forest Haven",

	"Siren":   "Tower of the Gods",
	"SirenMB": "Tower of the Gods (Armos Knights)",
	"SirenB":  "Tower of the Gods (Gohdan)",

	"Edaichi": "Earth Temple entrance",
	"M_Dai":   "Earth Temple",
	"M_DaiMB": "Earth Temple (Stalfos)",
	"M_DaiB":  "Earth Temple (Jalhalla)",

	"Ekaze":  "Wind Temple entrance",
	"kaze":   "Wind Temple",
	"kazeMB": "Wind Temple (Wizzrobe)",
	"kazeB":  "Wind Temple (Molgera)",

	"MajyuE":  "Forsaken Fortress",
	"majroom": "Forsaken Fortress",
	"ma2room": "Forsaken Fortress",
	"ma3room": "Forsaken Fortress",
	"Mjtower": "Forsaken Fortress tower",
	"M2tower": "Forsaken Fortress tower",
	"M2ganon": "Forsaken Fortress",

	"Hyrule":  "Hyrule Castle",
	"Hyroom":  "Hyrule Castle",
	"kenroom": "Master Sword chamber",

	"GanonA": "Ganon's Tower",
	"GanonB": "Ganon's Tower",
	"GanonC": "Ganon's Tower",
	"GanonD": "Ganon's Tower",
	"GanonE": "Ganon's Tower",
	"GanonF": "Ganon's Tower",
	"GanonG": "Ganon's Tower",
	"GanonH": "Ganon's Tower",
	"GanonI": "Ganon's Tower",
	"GanonJ": "Ganon's Tower",
	"GanonK": "Ganon's Tower",
	"GanonL": "Ganon's Tower",
	"GanonM": "Ganon's Tower",
	"GanonN": "Ganon's Tower",
	"GTower": "Ganon's Tower",
	"Xboss0": "Ganon's Tower",
	"Xboss1": "Ganon's Tower",
	"Xboss2": "Ganon's Tower",
	"Xboss3": "Ganon's Tower",

	"LinkRM":  "Link's house",
	"A_mori":  "Forest of Fairies",
	"Pjavdou": "Jabun's cave",

	"Fairy01": "Great Fairy fountain",
	"Fairy02": "Great Fairy fountain",
	"Fairy03": "Great Fairy fountain",
	"Fairy04": "Great Fairy fountain",
	"Fairy05": "Great Fairy fountain",
	"Fairy06": "Great Fairy fountain",

	"Pfigure": "Nintendo Gallery",
	"figureA": "Nintendo Gallery",
	"figureB": "Nintendo Gallery",
	"figureC": "Nintendo Gallery",
	"figureD": "Nintendo Gallery",
	"figureE": "Nintendo Gallery",
	"figureF": "Nintendo Gallery",
	"figureG": "Nintendo Gallery",

	"ENDING": "Credits",
}

// Stage is the display name of a stage code, the code itself when the
// table doesn't know it, "" for none.
func Stage(code string) string {
	if n, ok := stages[code]; ok {
		return n
	}
	return code
}

// Known reports whether the table names the stage code.
func Known(code string) bool {
	_, ok := stages[code]
	return ok
}

// square is sea room n's chart square: rooms 1..49 run row by row from the
// north-west, as seachart's grid does.
func square(room int) (seachart.Square, bool) {
	if room < 1 || room > seachart.GridSide*seachart.GridSide {
		return seachart.Square{}, false
	}
	n := room - 1
	return seachart.Square{GX: n%seachart.GridSide + seachart.GridMin, GZ: n/seachart.GridSide + seachart.GridMin}, true
}

// Square is a sea room's chart square in the sea chart's own naming
// (seachart.Square.Name), so a row and the chart agree; ok is false
// outside 1..49.
func Square(room int) (string, bool) {
	sq, ok := square(room)
	if !ok {
		return "", false
	}
	return sq.Name(), true
}

// Island is a sea room's island ("Windfall Island"); "" outside 1..49.
func Island(room int) string {
	sq, ok := square(room)
	if !ok {
		return ""
	}
	return seachart.IslandName(sq)
}

// Describe is a place in words: "Sea, D2 (Windfall Island)" for a sea
// square, "Dragon Roost Cavern, room 3" for a room of another stage, the
// stage alone while the room isn't known (-1 at a stage's start), "" for
// no stage.
func Describe(stage string, room int) string {
	if stage == "" {
		return ""
	}
	name := Stage(stage)
	if stage == Sea {
		sq, ok := Square(room)
		if !ok {
			return name
		}
		return fmt.Sprintf("%s, %s (%s)", name, sq, Island(room))
	}
	if room < 0 {
		return name
	}
	return fmt.Sprintf("%s, room %d", name, room)
}
