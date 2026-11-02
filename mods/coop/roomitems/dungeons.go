package roomitems

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
)

// Dungeon is one dungeon's save table as the world holds it: its
// mDungeonItem flags (save.View.DungeonItems). Keys is its vanilla
// small-key count (keys.Dungeons), 0 for a dungeon without small keys
// (Forsaken Fortress, Ganon's Tower).
type Dungeon struct {
	Table       int
	Short, Name string
	Keys        int
	save.DungeonItems
}

// dungeonList is the seven dungeons in story order: Forsaken Fortress and
// Ganon's Tower around the five with small keys.
var dungeonList = func() []Dungeon {
	out := []Dungeon{{Table: addr.TableFF, Short: "FF", Name: "Forsaken Fortress"}}
	for _, d := range keys.Dungeons {
		out = append(out, Dungeon{Table: d.Table, Short: d.Short, Name: d.Name, Keys: d.Keys})
	}
	return append(out, Dungeon{Table: addr.TableGT, Short: "GT", Name: "Ganon's Tower"})
}()

// dungeons reads the seven dungeons' flags from v.
func dungeons(v save.View) []Dungeon {
	out := make([]Dungeon, len(dungeonList))
	for i, d := range dungeonList {
		d.DungeonItems = v.DungeonItems(d.Table)
		out[i] = d
	}
	return out
}

// dungeonStages are the stages of each dungeon's save table, for marking
// where a partner is (its PRESENCE names a stage, not a table). From the
// co-op slice audits, which read each stage's STAG save table off the
// disc: FF (coop-slice-ff.md "Stages": MajyuE ... M2ganon, table 2; ma3room
// is ma2room's post-Helmaroc files under the same name), DRC (M_NewD2,
// M_Dra09, M_DragB), FW (kindan, kinMB, kinBOSS), TotG (Siren, SirenMB,
// SirenB), ET (M_Dai, M_DaiMB, M_DaiB), WT (kaze, kazeMB, kazeB) and GT
// (coop-slice-gt.md "Stages, rooms and exits": all table 8). Subdungeon
// antechambers on other tables (Ekaze, Edaichi: table 11) are left out.
var dungeonStages = map[string]int{
	"MajyuE": addr.TableFF, "majroom": addr.TableFF, "Mjtower": addr.TableFF, "ma2room": addr.TableFF,
	"ma3room": addr.TableFF, "M2tower": addr.TableFF, "M2ganon": addr.TableFF,
	"M_NewD2": keys.TableDRC, "M_Dra09": keys.TableDRC, "M_DragB": keys.TableDRC,
	"kindan": keys.TableFW, "kinMB": keys.TableFW, "kinBOSS": keys.TableFW,
	"Siren": keys.TableTotG, "SirenMB": keys.TableTotG, "SirenB": keys.TableTotG,
	"M_Dai": keys.TableET, "M_DaiMB": keys.TableET, "M_DaiB": keys.TableET,
	"kaze": keys.TableWT, "kazeMB": keys.TableWT, "kazeB": keys.TableWT,
	"GanonA": addr.TableGT, "GanonB": addr.TableGT, "GanonC": addr.TableGT, "GanonD": addr.TableGT,
	"GanonE": addr.TableGT, "GanonJ": addr.TableGT, "GanonK": addr.TableGT, "GanonL": addr.TableGT,
	"GanonM": addr.TableGT, "GanonN": addr.TableGT, "GTower": addr.TableGT,
	"Xboss0": addr.TableGT, "Xboss1": addr.TableGT, "Xboss2": addr.TableGT, "Xboss3": addr.TableGT,
}

// DungeonOfStage is the dungeon save table of stage, if it is one of the
// seven dungeons' stages.
func DungeonOfStage(stage string) (table int, ok bool) {
	table, ok = dungeonStages[stage]
	return table, ok
}
