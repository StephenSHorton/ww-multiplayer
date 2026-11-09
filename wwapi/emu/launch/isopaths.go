package launch

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ISOPaths returns the game folders Dolphin's game list shows for
// userDir: Config/Dolphin.ini's [General] ISOPath0, ISOPath1, ... in
// order (every ISOPath<n> key, whatever ISOPaths counts), relative ones
// taken against userDir, and RecursiveISOPaths (the list also looks in
// their subfolders). None when the file can't be read.
func ISOPaths(userDir string) (dirs []string, recursive bool) {
	if userDir == "" {
		return nil, false
	}
	type entry struct {
		n   int
		dir string
	}
	var es []entry
	iniSection(filepath.Join(userDir, "Config", "Dolphin.ini"), "General", func(k, v string) {
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		lk := strings.ToLower(k)
		switch {
		case lk == "recursiveisopaths":
			recursive = strings.EqualFold(v, "true") || v == "1"
		case strings.HasPrefix(lk, "isopath") && lk != "isopaths":
			n, err := strconv.Atoi(lk[len("isopath"):])
			if err != nil || v == "" {
				return
			}
			d := filepath.Clean(filepath.FromSlash(v))
			if !filepath.IsAbs(d) {
				d = filepath.Join(userDir, d)
			}
			es = append(es, entry{n, d})
		}
	})
	sort.SliceStable(es, func(i, j int) bool { return es[i].n < es[j].n })
	for _, e := range es {
		dirs = append(dirs, e.dir)
	}
	return dirs, recursive
}
