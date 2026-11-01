package seachart

import "strings"

// islandNames are the chart's names of the 49 squares, row A (north) to G,
// column 1 (west) to 7.
var islandNames = [GridSide * GridSide]string{
	"Forsaken Fortress", "Star Island", "Northern Fairy Island", "Gale Isle", "Crescent Moon Island", "Seven-Star Isles", "Overlook Island",
	"Four-Eye Reef", "Mother and Child Isles", "Spectacle Island", "Windfall Island", "Pawprint Isle", "Dragon Roost Island", "Flight Control Platform",
	"Western Fairy Island", "Rock Spire Isle", "Tingle Island", "Northern Triangle Island", "Eastern Fairy Island", "Fire Mountain", "Star Belt Archipelago",
	"Three-Eye Reef", "Greatfish Isle", "Cyclops Reef", "Six-Eye Reef", "Tower of the Gods", "Eastern Triangle Island", "Thorned Fairy Island",
	"Needle Rock Isle", "Islet of Steel", "Stone Watcher Island", "Southern Triangle Island", "Private Oasis", "Bomb Island", "Bird's Peak Rock",
	"Diamond Steppe Island", "Five-Eye Reef", "Shark Island", "Southern Fairy Island", "Ice Ring Isle", "Forest Haven", "Cliff Plateau Isles",
	"Horseshoe Island", "Outset Island", "Headstone Island", "Two-Eye Reef", "Angular Isles", "Boating Course", "Five-Star Isles",
}

// IslandName is the square's island, "" off the grid.
func IslandName(s Square) string {
	if !s.Valid() {
		return ""
	}
	return islandNames[s.Grid()]
}

// knownStage is a stage this package names itself: the dungeons and
// other stages whose 2DMA names no square (or the wrong one for the chart
// a player wants: the Nintendo Gallery's rooms say Forest Haven), with
// the island whose square they are pinned to.
type knownStage struct {
	name string
	sq   Square
}

var (
	sqFF        = Square{-3, -3}
	sqWindfall  = Square{0, -2}
	sqDragon    = Square{2, -2}
	sqGale      = Square{0, -3}
	sqTower     = Square{1, 0}
	sqForest    = Square{2, 2}
	sqHeadstone = Square{-1, 3}
	sqOutset    = Square{-2, 3}
)

// knownStages are matched case-insensitively.
var knownStages = map[string]knownStage{
	"m_newd2": {"Dragon Roost Cavern", sqDragon}, "m_dragb": {"Gohma's lair", sqDragon}, "m_dra09": {"Dragon Roost Cavern", sqDragon},
	"kindan": {"Forbidden Woods", sqForest}, "kinboss": {"Kalle Demos' lair", sqForest}, "kinmb": {"Forbidden Woods", sqForest},
	"siren": {"Tower of the Gods", sqTower}, "sirenb": {"Gohdan's chamber", sqTower}, "sirenmb": {"Tower of the Gods", sqTower},
	"m_dai": {"Earth Temple", sqHeadstone}, "m_daib": {"Jalhalla's chamber", sqHeadstone}, "m_daimb": {"Earth Temple", sqHeadstone},
	"kaze": {"Wind Temple", sqGale}, "kazeb": {"Molgera's chamber", sqGale}, "kazemb": {"Wind Temple", sqGale},
	"majyue": {"Forsaken Fortress", sqFF}, "majroom": {"Forsaken Fortress", sqFF}, "ma2room": {"Forsaken Fortress", sqFF},
	"ma3room": {"Forsaken Fortress", sqFF}, "mjtower": {"Forsaken Fortress", sqFF}, "m2tower": {"Forsaken Fortress", sqFF},
	"m2ganon": {"Forsaken Fortress", sqFF},
	"hyrule":  {"Hyrule Castle", sqTower}, "hyroom": {"Hyrule Castle", sqTower}, "kenroom": {"Master Sword chamber", sqTower},
	"gtower": {"Ganon's Tower", sqTower}, "ganona": {"Ganon's Tower", sqTower}, "ganonb": {"Ganon's Tower", sqTower},
	"ganonc": {"Ganon's Tower", sqTower}, "ganond": {"Ganon's Tower", sqTower}, "ganone": {"Ganon's Tower", sqTower},
	"ganonj": {"Ganon's Tower", sqTower}, "ganonk": {"Ganon's Tower", sqTower}, "ganonl": {"Ganon's Tower", sqTower},
	"ganonm": {"Ganon's Tower", sqTower}, "ganonn": {"Ganon's Tower", sqTower},
	"figurea": {"Nintendo Gallery", sqWindfall}, "figureb": {"Nintendo Gallery", sqWindfall}, "figurec": {"Nintendo Gallery", sqWindfall},
	"figured": {"Nintendo Gallery", sqWindfall}, "figuree": {"Nintendo Gallery", sqWindfall}, "figuref": {"Nintendo Gallery", sqWindfall},
	"figureg": {"Nintendo Gallery", sqWindfall}, "pfigure": {"Nintendo Gallery", sqWindfall},
	"linkrm": {"Link's house", sqOutset}, "linkug": {"Under Link's house", sqOutset}, "a_mori": {"Forest of Fairies", sqOutset},
	"kaisen": {"Battleship game", sqWindfall}, "opub": {"Café Bar", sqWindfall}, "obombh": {"Bomb Shop", sqWindfall},
	"atorizk": {"Dragon Roost (Rito Aerie)", sqDragon}, "adanmae": {"Dragon Roost (cavern entrance)", sqDragon},
	"omori": {"Forest Haven (Deku Tree)", sqForest},
}

// seaStages are the stages whose coordinates are the sea's: a player's
// position there is drawn where it is.
var seaStages = map[string]bool{"sea": true, "sea_t": true, "sea_e": true}

// IsSea reports whether stage is the open sea (its coordinates are the
// chart's).
func IsSea(stage string) bool { return seaStages[strings.ToLower(stage)] }

// Where is a stage off the sea placed on the chart: its square, a name
// for the place ("" when only the island is known), and ok false when
// neither this package nor the disc's 2DMA table places it.
func (c *Chart) Where(stage string) (sq Square, place string, ok bool) {
	if k, found := knownStages[strings.ToLower(stage)]; found {
		return k.sq, k.name, true
	}
	if c != nil {
		if s, found := c.Stages[stage]; found {
			return s, "", true
		}
	}
	return Square{}, "", false
}
