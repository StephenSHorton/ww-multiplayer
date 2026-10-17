package backup

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Fuzz targets for the two files this package reads that anything may
// have written: a backup's manifest.json (Open, List, Restore) and
// Dolphin's Dolphin.ini (Locate). The seed corpora run as ordinary tests
// under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzDecodeManifest -fuzztime=20s ./wwapi/backup/

func FuzzDecodeManifest(f *testing.F) {
	src := filepath.Join(string(filepath.Separator)+"cards", "MemoryCardA.USA.raw")
	good, err := json.Marshal(Backup{
		Version:  ManifestVersion,
		UserDirs: []string{filepath.Dir(src)},
		Cards:    []Card{{Slot: "A", Kind: Raw, Path: src, From: "default"}},
		Files:    []File{{Source: src, Stored: "files/00-MemoryCardA.USA.raw", Size: 4, SHA256: strings.Repeat("ab", 32)}},
	})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{good, good[:len(good)/2], nil, []byte("{}"), []byte(`{"version":1,"files":[{}]}`), []byte("null")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := decodeManifest(data)
		if err != nil {
			return
		}
		// What decodes names only files under files/, from absolute
		// sources, each once.
		seen := map[string]bool{}
		for _, fl := range b.Files {
			if !validStored(fl.Stored) || !filepath.IsAbs(fl.Source) || fl.Card < 0 || fl.Card >= len(b.Cards) {
				t.Fatalf("decoded a bad entry %+v", fl)
			}
			if dir, name := path.Split(fl.Stored); dir != "files/" || name == ".." || path.Join("files", name) != fl.Stored {
				t.Fatalf("stored path %q escapes", fl.Stored)
			}
			if seen[fl.Source] {
				t.Fatalf("source %s twice", fl.Source)
			}
			seen[fl.Source] = true
		}
	})
}

func FuzzParseIni(f *testing.F) {
	for _, seed := range []string{
		"[Core]\nSlotA = 8\nMemcardAPath = C:\\x.raw\n",
		"\ufeff[core]\n\"\n=\n[",
		"",
		"[Core\nSlotA=1",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		ini := parseIni(strings.NewReader(src))
		for section, kv := range ini {
			if section != strings.ToLower(section) {
				t.Fatalf("section %q not folded", section)
			}
			for k := range kv {
				if k != strings.ToLower(k) || strings.TrimSpace(k) != k {
					t.Fatalf("key %q not folded and trimmed", k)
				}
			}
		}
	})
}
