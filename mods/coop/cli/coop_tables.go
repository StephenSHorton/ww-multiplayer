package coopcli

// coop-tables (#23; docs/08-coop-design.md §6.2, §6.4, §7.2, §9 M1, §11
// item 8): reads the user's own disc image and prints the co-op tables
// the later milestones sync by, small keys and key doors, heart sources,
// switch classes and push blocks, and enemy entries with their setIDs,
// with M1's owner acceptance on top. The tables are cached by DOL hash
// under the user's config directory (wwapi/tables), so only the
// first run reads the stage archives. Read-only on the disc; like the
// co-op diagnostics, it is not listed in printHelp.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

const coopTablesUsage = "coop-tables <iso|ciso> [--json] [--no-cache] [--section keys|hearts|switches|enemies]"

// coopTablesTarget is M1's acceptance time for building the tables from a
// 1.4 GB image (§9 M1).
const coopTablesTarget = 5 * time.Second

// coopTablesArgs are coop-tables' parsed arguments.
type coopTablesArgs struct {
	path    string
	json    bool           // print the tables as JSON instead of the report
	noCache bool           // build from the disc, and neither read nor write the cache
	section tables.Section // "" for every section
}

// parseCoopTablesArgs reads coop-tables' arguments: one image path and the
// flags, in any order; --section takes its value as the next argument or
// after "=".
func parseCoopTablesArgs(args []string) (coopTablesArgs, error) {
	var a coopTablesArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if a.path != "" {
				return a, fmt.Errorf("one disc image, not %q and %q", a.path, arg)
			}
			a.path = arg
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		switch {
		case !strings.HasPrefix(arg, "--"):
			return a, fmt.Errorf("unknown flag %s", arg)
		case name == "json" || name == "no-cache":
			if hasValue {
				return a, fmt.Errorf("--%s takes no value", name)
			}
			if name == "json" {
				a.json = true
			} else {
				a.noCache = true
			}
		case name == "section":
			if a.section != "" {
				return a, errors.New("--section given twice")
			}
			if !hasValue {
				if i+1 >= len(args) {
					return a, errors.New("--section needs keys, hearts, switches or enemies")
				}
				i++
				value = args[i]
			}
			s, err := tables.ParseSection(value)
			if err != nil {
				return a, fmt.Errorf("--section: %w", err)
			}
			a.section = s
		default:
			return a, fmt.Errorf("unknown flag %s", arg)
		}
	}
	if a.path == "" {
		return a, errors.New("give the path of your Wind Waker disc image (.iso or .ciso)")
	}
	return a, nil
}

// runCoopTables runs coop-tables with the arguments after it.
func runCoopTables(args []string) {
	a, err := parseCoopTablesArgs(args)
	if err != nil {
		exitUsage(err, coopTablesUsage)
	}
	if err := coopTables(os.Stdout, os.Stderr, a, tables.CacheDir); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// coopTables gets the tables of the image a names, through the cache in
// the directory cacheDir returns (tables.CacheDir in production; none with
// --no-cache, or when cacheDir fails, which status is warned of), and
// prints the report, or the JSON, to out. Where the tables came from and
// how long it took goes first: to out with the report, to status with
// JSON, so that out holds only the JSON.
func coopTables(out, status io.Writer, a coopTablesArgs, cacheDir func() (string, error)) error {
	dir := ""
	if !a.noCache {
		var err error
		if dir, err = cacheDir(); err != nil {
			fmt.Fprintf(status, "WARNING: %v; building without a cache.\n", err)
			dir = ""
		}
	}
	start := time.Now()
	d, err := inject.OpenDisc(a.path)
	if err != nil {
		return err
	}
	defer d.Close()
	t, st, err := tables.Get(d, dir)
	if err != nil {
		return err
	}
	log := out
	if a.json {
		log = status
	}
	writeCoopTablesStats(log, a.path, st, time.Since(start))

	var sections []tables.Section
	if a.section != "" {
		sections = []tables.Section{a.section}
	}
	if !a.json {
		fmt.Fprintf(out, "\n%s", synctables.Report(t, sections...))
		return nil
	}
	v := t
	if a.section != "" {
		v = t.Only(a.section)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = out.Write(append(b, '\n'))
	return err
}

// writeCoopTablesStats says where the tables came from: the cache, or the
// disc with the time each step took (the M1 acceptance is the total, under
// coopTablesTarget), and what happened to the cache.
func writeCoopTablesStats(w io.Writer, path string, st tables.Stats, total time.Duration) {
	fmt.Fprintf(w, "coop-tables: %s\n", path)
	secs := func(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }
	if st.Cached {
		fmt.Fprintf(w, "  read from the cache %s in %s (--no-cache rebuilds from the disc)\n", st.Path, secs(total))
		return
	}
	fmt.Fprintf(w, "  built from the disc in %s (M1 target: under %s): main.dol %s, %d stage archives (%.1f MiB) read and expanded %s, tables %s\n",
		secs(total), secs(coopTablesTarget), secs(st.Identify), st.Files, float64(st.Bytes)/(1<<20), secs(st.Read), secs(st.Build))
	switch {
	case st.Path == "":
		fmt.Fprintln(w, "  cache: off")
	case st.SaveErr != nil:
		fmt.Fprintf(w, "  cache: %s; not saved: %v\n", st.Why, st.SaveErr)
	default:
		fmt.Fprintf(w, "  cache: %s; saved to %s in %s\n", st.Why, st.Path, secs(st.Save))
	}
}
