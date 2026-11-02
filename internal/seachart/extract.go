// Package seachart is the app's Great Sea chart: the island art read from
// the player's own disc (never from this repository: the repo is public,
// and the art is Nintendo's), the sea's grid, and where each stage lies
// on it.
//
// The geometry is the game's (d_menu_fmap.cpp, d_map.cpp in the tww
// decomp): the sea is 7x7 squares of 100000 units, square (gx, gz) with gx
// and gz in -3..3 centred on (gx*100000, gz*100000), +X east and +Z south.
// The squares are named by column letter A..G (gx -3..3, west to east) and
// row number 1..7 (gz -3..3, north to south), as the guides name them
// (Prima's: Windfall Island D2, Outset Island B7); the game draws no labels. Each square has one island tile,
// res/Fmap/Fmap.arc fm/rXX_b.bti with XX = grid+1 in hex (grid = gx+3 +
// (gz+3)*7), drawn over 20000x20000 units centred on the square's
// checkpoint, res/FmapDat/FmapDat.bin's offset from the square's centre
// (dMenu_Fmap_c::changeFmapTexture, the R01G pane's 20000-unit scale at
// d_menu_fmap.cpp:1671-1685). Each stage's stage.dzs 2DMA chunk names the
// square the game's chart opens on there (dStage_mapInfo_GetOceanX/Z).
package seachart

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"math"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/disc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Sea geometry, in world units.
const (
	SquareUnits = 100000 // one square's side
	TileUnits   = 20000  // one island tile's side, centred on the checkpoint
	GridMin     = -3
	GridMax     = 3
	GridSide    = GridMax - GridMin + 1
)

// Files on the disc the chart is read from.
const (
	fmapArc     = "res/Fmap/Fmap.arc"
	fmapDatFile = "res/FmapDat/FmapDat.bin"
	stageDZS    = "dzs/stage.dzs"
	mapInfoTag  = "2DMA"
	mapInfoLen  = 0x38 // stage_map_info_class
	mapInfoXZ   = 0x36 // its mOceanXZ: X in the low nibble, Z in the high, signed
	chkPntLen   = 8    // FmapChkPnt: s8 sector X, s8 sector Z, s16 X, s16 Z, u8
)

// Square is one square of the sea grid.
type Square struct {
	GX int `json:"gx"` // -3..3, west to east
	GZ int `json:"gz"` // -3..3, north to south
}

// Grid is the square's index 0..48, row by row from the north-west.
func (s Square) Grid() int { return (s.GX - GridMin) + (s.GZ-GridMin)*GridSide }

// Valid reports whether s is on the sea grid.
func (s Square) Valid() bool {
	return s.GX >= GridMin && s.GX <= GridMax && s.GZ >= GridMin && s.GZ <= GridMax
}

// Name is the chart's name of the square: "B7" for Outset Island.
func (s Square) Name() string {
	if !s.Valid() {
		return ""
	}
	return fmt.Sprintf("%c%d", 'A'+(s.GX-GridMin), s.GZ-GridMin+1)
}

// SquareAt is the square holding world point (x, z), clamped to the grid
// as dMap_point2Grid clamps it.
func SquareAt(x, z float64) Square {
	f := func(v float64) int {
		g := int(math.Floor((v + SquareUnits/2) / SquareUnits))
		return min(max(g, GridMin), GridMax)
	}
	return Square{GX: f(x), GZ: f(z)}
}

// Island is one square's island tile.
type Island struct {
	Square
	// CX, CZ is the tile's centre in world units: the square's centre
	// plus its checkpoint offset.
	CX int `json:"cx"`
	CZ int `json:"cz"`
	// PNG is the upscaled tile (upscale), TileUnits across.
	PNG []byte `json:"png"`
}

// Chart is what Extract reads from a disc.
type Chart struct {
	Islands []Island `json:"islands"`
	// Stages maps a stage name to the square its 2DMA chunk names, for
	// the stages that name one on the grid.
	Stages map[string]Square `json:"stages"`
}

// Extract reads the chart from src, a Wind Waker (GZLE01) disc. A tile
// the disc lacks or can't be read leaves its square without an island;
// a disc with no tile or no checkpoints at all is an error.
func Extract(src disc.FileSource) (*Chart, error) {
	chk, err := checkpoints(src)
	if err != nil {
		return nil, err
	}
	arc, err := readArc(src, fmapArc)
	if err != nil {
		return nil, err
	}
	c := &Chart{Stages: stageSquares(src)}
	for _, cp := range chk {
		name := fmt.Sprintf("r%02x_b.bti", cp.sq.Grid()+1)
		f, ok := arc.FindName(name)
		if !ok {
			continue
		}
		b, err := arc.Read(f)
		if err != nil {
			continue
		}
		img, err := decodeBTI(b)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, upscale(img, upscaleFactor)); err != nil {
			return nil, err
		}
		c.Islands = append(c.Islands, Island{Square: cp.sq,
			CX: cp.sq.GX*SquareUnits + cp.dx, CZ: cp.sq.GZ*SquareUnits + cp.dz, PNG: buf.Bytes()})
	}
	if len(c.Islands) == 0 {
		return nil, fmt.Errorf("seachart: no island tile could be read from %s", fmapArc)
	}
	sort.Slice(c.Islands, func(i, j int) bool { return c.Islands[i].Grid() < c.Islands[j].Grid() })
	return c, nil
}

type checkpoint struct {
	sq     Square
	dx, dz int
}

// checkpoints reads FmapDat.bin: a u32 count, then FmapChkPnt entries.
func checkpoints(src disc.FileSource) ([]checkpoint, error) {
	b, err := src.ReadFile(fmapDatFile)
	if err != nil {
		return nil, fmt.Errorf("seachart: %w", err)
	}
	if stage.IsYaz0(b) {
		if b, err = stage.DecodeYaz0(b); err != nil {
			return nil, fmt.Errorf("seachart: %s: %w", fmapDatFile, err)
		}
	}
	return parseCheckpoints(b)
}

func parseCheckpoints(b []byte) ([]checkpoint, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("seachart: %s is too short", fmapDatFile)
	}
	n := int(binary.BigEndian.Uint32(b))
	if n <= 0 || n > GridSide*GridSide || len(b) < 4+n*chkPntLen {
		return nil, fmt.Errorf("seachart: %s lists %d checkpoints in %d bytes", fmapDatFile, n, len(b))
	}
	var out []checkpoint
	seen := map[int]bool{}
	for i := 0; i < n; i++ {
		e := b[4+i*chkPntLen:]
		cp := checkpoint{sq: Square{GX: int(int8(e[0])), GZ: int(int8(e[1]))},
			dx: int(int16(binary.BigEndian.Uint16(e[2:]))), dz: int(int16(binary.BigEndian.Uint16(e[4:])))}
		if !cp.sq.Valid() || seen[cp.sq.Grid()] || abs(cp.dx) > SquareUnits/2 || abs(cp.dz) > SquareUnits/2 {
			continue
		}
		seen[cp.sq.Grid()] = true
		out = append(out, cp)
	}
	if len(out) == 0 {
		return nil, errors.New("seachart: no usable checkpoint in " + fmapDatFile)
	}
	return out, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// stageSquares reads every stage's 2DMA square. Stages that can't be read
// or name none (the dungeons, the sea itself) are left out.
func stageSquares(src disc.FileSource) map[string]Square {
	out := map[string]Square{}
	for _, f := range src.Files() {
		parts := strings.Split(f.Path, "/")
		if len(parts) != 4 || !strings.EqualFold(parts[0]+"/"+parts[1], stage.StageRoot) || !strings.EqualFold(parts[3], "Stage.arc") {
			continue
		}
		a, err := readArc(src, f.Path)
		if err != nil {
			continue
		}
		dzs, err := a.ReadFile(stageDZS)
		if err != nil {
			continue
		}
		if sq, ok := mapInfoSquare(dzs); ok {
			out[parts[2]] = sq
		}
	}
	return out
}

// mapInfoSquare is the first 2DMA entry's square in a stage.dzs.
func mapInfoSquare(dzs []byte) (Square, bool) {
	z, err := stage.ParseDZS(dzs)
	if err != nil {
		return Square{}, false
	}
	c, ok := z.Chunk(mapInfoTag)
	if !ok {
		if c, ok = z.Chunk("2Dma"); !ok {
			return Square{}, false
		}
	}
	at := int(c.Offset) + mapInfoXZ
	if c.Count < 1 || at >= len(dzs) {
		return Square{}, false
	}
	nib := func(v byte) int {
		n := int(v & 0x0F)
		if n&8 != 0 {
			n -= 16
		}
		return n
	}
	sq := Square{GX: nib(dzs[at]), GZ: nib(dzs[at] >> 4)}
	return sq, sq.Valid()
}

func readArc(src disc.FileSource, path string) (*stage.Archive, error) {
	b, err := src.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("seachart: %w", err)
	}
	if stage.IsYaz0(b) {
		if b, err = stage.DecodeYaz0(b); err != nil {
			return nil, fmt.Errorf("seachart: %s: %w", path, err)
		}
	}
	a, err := stage.ParseArchive(b)
	if err != nil {
		return nil, fmt.Errorf("seachart: %s: %w", path, err)
	}
	return a, nil
}
