package seachart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// The chart is extracted once per disc into the app's config directory
// (ww-multiplayer/sea-chart/chart.json under os.UserConfigDir, beside the
// co-op tables), on the player's machine only.

// schema is the cache file's version: a build that draws the tiles
// differently (upscale, the palette) bumps it, and the next ensure
// extracts again.
const schema = 1

const (
	cacheSubdir = "ww-multiplayer/sea-chart"
	cacheFile   = "chart.json"
	maxCache    = 64 << 20
)

// cached is the cache file.
type cached struct {
	Schema int    `json:"schema"`
	Disc   string `json:"disc"` // the image it was read from
	Size   int64  `json:"size"` // its size then, which a re-patch changes
	*Chart
}

// Dir is the cache directory.
func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("seachart: no config directory: %w", err)
	}
	return filepath.Join(cfg, filepath.FromSlash(cacheSubdir)), nil
}

// Load reads the chart cached in dir, any disc's. A missing cache is an
// error matching fs.ErrNotExist.
func Load(dir string) (*Chart, error) {
	c, err := load(dir)
	if err != nil {
		return nil, err
	}
	return c.Chart, nil
}

func load(dir string) (*cached, error) {
	path := filepath.Join(dir, cacheFile)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxCache {
		return nil, fmt.Errorf("seachart: %s is %d bytes", path, fi.Size())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c cached
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("seachart: %s: %w", path, err)
	}
	if c.Schema != schema || c.Chart == nil || len(c.Islands) == 0 {
		return nil, errStale
	}
	return &c, nil
}

var errStale = errors.New("seachart: cache from another build")

// Ensure makes the cache in dir hold the chart of the disc image at path
// (an ISO or a CISO: the patched disc carries the vanilla chart): kept
// when it is already that image's at its current size, else extracted
// again. It returns the chart and whether it extracted.
func Ensure(dir, path string) (*Chart, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false, fmt.Errorf("seachart: %w", err)
	}
	abs, _ := filepath.Abs(path)
	if c, err := load(dir); err == nil && c.Disc == abs && c.Size == fi.Size() {
		return c.Chart, false, nil
	}
	d, err := inject.OpenDisc(path)
	if err != nil {
		return nil, false, fmt.Errorf("seachart: %w", err)
	}
	defer d.Close()
	ch, err := Extract(d)
	if err != nil {
		return nil, false, err
	}
	b, err := json.Marshal(cached{Schema: schema, Disc: abs, Size: fi.Size(), Chart: ch})
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ch, true, fmt.Errorf("seachart: %w", err)
	}
	tmp := filepath.Join(dir, cacheFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return ch, true, fmt.Errorf("seachart: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, cacheFile)); err != nil {
		os.Remove(tmp)
		return ch, true, fmt.Errorf("seachart: %w", err)
	}
	return ch, true, nil
}
