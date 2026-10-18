package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Install downloads rel's asset for goos next to the running program exe,
// verifies it (Download) and swaps it in: the exe on Windows (SwapExe),
// the .app bundle on macOS (Unzip next to it, FindApp, SwapBundle). The
// new version must run (c.Smoke, after the swap on Windows, where a
// failure puts the old exe back, and before it on macOS): else
// ErrBadBuild. It returns once the new version is in place; the caller
// relaunches it (RelaunchCmd) and exits. On an error nothing was replaced
// and the download is gone. Other systems, and a macOS binary outside its
// .app, get ErrUnsupported.
func (c *Client) Install(ctx context.Context, rel Release, goos, exe string, progress Progress) error {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	switch goos {
	case "windows":
		tmp, err := c.Download(ctx, rel, AssetWindows, filepath.Dir(exe), progress)
		if err != nil {
			return err
		}
		old, err := SwapExe(OS, exe, tmp)
		if err != nil {
			os.Remove(tmp)
			return err
		}
		if serr := c.smoke()(ctx, exe, rel.Tag); serr != nil {
			if rerr := Restore(OS, exe, old); rerr != nil {
				return fmt.Errorf("%w (%v), and %v", ErrBadBuild, serr, rerr)
			}
			return fmt.Errorf("%w (%v); the old one is back", ErrBadBuild, serr)
		}
		return nil
	case "darwin":
		bundle, ok := BundleOf(exe)
		if !ok {
			return ErrUnsupported
		}
		parent := filepath.Dir(bundle)
		zipTmp, err := c.Download(ctx, rel, AssetMac, parent, progress)
		if err != nil {
			return err
		}
		defer os.Remove(zipTmp)
		dir, err := os.MkdirTemp(parent, TempPrefix+"*")
		if err != nil {
			return fmt.Errorf("update: can't unpack next to the app: %w", err)
		}
		defer os.RemoveAll(dir)
		if err := Unzip(zipTmp, dir); err != nil {
			return err
		}
		app, err := FindApp(dir, filepath.Base(exe))
		if err != nil {
			return err
		}
		if serr := c.smoke()(ctx, filepath.Join(app, "Contents", "MacOS", filepath.Base(exe)), rel.Tag); serr != nil {
			return fmt.Errorf("%w (%v)", ErrBadBuild, serr)
		}
		return SwapBundle(OS, bundle, app)
	}
	return ErrUnsupported
}

func (c *Client) smoke() func(ctx context.Context, exe, tag string) error {
	if c.Smoke != nil {
		return c.Smoke
	}
	return Smoke
}

// CanInstall says Install can update exe on goos (Windows, or a macOS
// app bundle).
func CanInstall(goos, exe string) bool {
	switch goos {
	case "windows":
		return true
	case "darwin":
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		_, ok := BundleOf(exe)
		return ok
	}
	return false
}

// CachePath is the CLI's and the TUI's check cache:
// <os.UserCacheDir>/ww-multiplayer/update-check.json. (The app keeps its
// answer in its config; the CLI reads no config, owner decision 11.)
func CachePath() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "ww-multiplayer", "update-check.json"), nil
}

// LoadCache reads a cache file; a missing or bad one is the zero State
// (the next check is due).
func LoadCache(path string) State {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 64<<10 {
		return State{}
	}
	var st State
	if json.Unmarshal(b, &st) != nil {
		return State{}
	}
	return st
}

// SaveCache writes st to path (a temp file renamed over it), best effort.
func SaveCache(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// CLINotice is the CLI's and the TUI's check: the cached answer, asking
// GitHub first when the cache is due (bounded by timeout), and the one
// line to print ("" for none). It never fails: no network, a dev build,
// EnvNoCheck=1 or no cache directory just give "".
func CLINotice(ctx context.Context, getenv func(string) string, version string, timeout time.Duration) string {
	if IsDev(version) || (getenv != nil && getenv(EnvNoCheck) == "1") {
		return ""
	}
	path, err := CachePath()
	if err != nil {
		return ""
	}
	st := LoadCache(path)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, nst, _ := NewClient(getenv, version).Check(ctx, version, st, time.Now())
	if nst != st {
		_ = SaveCache(path, nst)
	}
	return Notice(r)
}
