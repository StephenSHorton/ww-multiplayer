package gui

import (
	"encoding/base64"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/seachart"
)

// The sea chart's island art (wind#51) is read from the player's own disc
// (internal/seachart) into the config directory: after each patch, and,
// for a disc patched by an earlier build, the first time the chart is
// shown. Until it is there, the chart draws its grid alone.

// chartState is the Bridge's chart.
type chartState struct {
	mu       sync.Mutex
	chart    *seachart.Chart
	disc     string // the image chart was read from
	rev      int    // bumped when chart changes
	building bool
	failed   string // the image whose extraction failed this run: not retried until it changes
}

// SeaChartView is the island art for the frontend: one tile per square,
// drawn seachart.TileUnits across centred on (cx, cz).
type SeaChartView struct {
	Rev       int             `json:"rev"`
	Islands   []SeaIslandView `json:"islands"`
	TileUnits int             `json:"tileUnits"`
	Squares   int             `json:"squareUnits"`
	Names     [][7]string     `json:"names"` // row by row, the squares' island names
}

// SeaIslandView is one island tile.
type SeaIslandView struct {
	GX  int    `json:"gx"`
	GZ  int    `json:"gz"`
	CX  int    `json:"cx"`
	CZ  int    `json:"cz"`
	Src string `json:"src"` // a data: URL of the PNG
}

// SeaChart returns the island art (no islands while there is none yet,
// and then it starts reading it from the patched disc).
func (b *Bridge) SeaChart() SeaChartView {
	ch, rev := b.seaChart()
	v := SeaChartView{Rev: rev, Islands: []SeaIslandView{}, TileUnits: seachart.TileUnits, Squares: seachart.SquareUnits}
	for gz := seachart.GridMin; gz <= seachart.GridMax; gz++ {
		var row [7]string
		for gx := seachart.GridMin; gx <= seachart.GridMax; gx++ {
			row[gx-seachart.GridMin] = seachart.IslandName(seachart.Square{GX: gx, GZ: gz})
		}
		v.Names = append(v.Names, row)
	}
	if ch == nil {
		return v
	}
	for _, is := range ch.Islands {
		v.Islands = append(v.Islands, SeaIslandView{GX: is.GX, GZ: is.GZ, CX: is.CX, CZ: is.CZ,
			Src: "data:image/png;base64," + base64.StdEncoding.EncodeToString(is.PNG)})
	}
	return v
}

// seaChart is the chart and its revision, starting a read of the patched
// disc's when there is none for it.
func (b *Bridge) seaChart() (*seachart.Chart, int) {
	b.mu.Lock()
	disc := b.cfg.ISO.Patched
	if disc == "" {
		disc = b.cfg.ISO.Vanilla
	}
	b.mu.Unlock()
	c := &b.chart
	c.mu.Lock()
	defer c.mu.Unlock()
	if disc != "" && disc != c.disc && disc != c.failed && !c.building && b.chartDir != nil {
		c.building = true
		go b.buildChart(disc)
	}
	return c.chart, c.rev
}

// buildChart reads disc's chart (from the cache when it is that disc's).
func (b *Bridge) buildChart(disc string) {
	dir, err := b.chartDir()
	var ch *seachart.Chart
	if err == nil {
		ch, _, err = b.chartEnsure(dir, disc)
	}
	c := &b.chart
	c.mu.Lock()
	defer c.mu.Unlock()
	c.building = false
	if err != nil {
		c.failed = disc
		return
	}
	c.chart, c.disc, c.failed = ch, disc, ""
	c.rev++
}

// refreshChart reads the chart again after a patch (the disc's path may
// be the same, its contents not).
func (b *Bridge) refreshChart() {
	c := &b.chart
	c.mu.Lock()
	c.disc, c.failed = "", ""
	c.mu.Unlock()
	b.seaChart()
}
