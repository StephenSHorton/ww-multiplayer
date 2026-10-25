// Package gamefiles spots Nintendo game files and players' saves: a disc
// image, the DOL, a REL or archive from the disc, a model, a memory card
// or a save state. The repo and the releases must never carry one (the
// shipped binaries hold only our code and patch records; each player
// patches their own disc, release.yml's header), and a bug report must
// never pack one (internal/bugreport). tools/check_game_files runs it over
// the tracked files in CI and over the release assets in release.yml.
//
// A file is flagged by its name (main.dol, RELS.arc, bi2.bin, ...), its
// extension (.iso .ciso .rvz .dol .rel .arc .sav .gci .raw .s01, ...) or
// its first bytes (a RARC archive, Yaz0 data, a J3D model, a disc image),
// so a renamed file is caught too; a zip is opened and each entry checked
// the same way (Scan).
//
// Nothing is allowed by path: no file of ours has a game file's shape.
// Our REL is built on the player's machine at patch time from the bytes in
// internal/inject/blob.go (Go source), and the T2 blob ships the same way,
// so neither is ever a .rel or .dol file in the repo or a release. A file
// of ours that needed one would need an allowlist here first, checked by
// path and content (the idea of datstarkey/ww-online's
// scripts/check-no-nintendo-files.ps1, MIT; this is a clean-room rewrite).
package gamefiles

import (
	"bytes"
	"encoding/binary"
	"path"
	"regexp"
	"strings"
)

// HeaderLen is how many leading bytes Header checks: NKit's mark sits at
// 0x200.
const HeaderLen = nkitOffset + 4

// extensions are the file types of game data and of saves.
var extensions = map[string]string{
	// Disc images, in every format Dolphin plays.
	".iso": "disc image", ".gcm": "disc image", ".ciso": "disc image", ".rvz": "disc image",
	".wia": "disc image", ".gcz": "disc image", ".nkit": "disc image", ".wbfs": "disc image",
	".tgc": "disc image",
	// The disc's code and archives.
	".dol": "game executable", ".rel": "game module", ".arc": "game archive", ".szs": "game archive",
	// Models, textures, animations, layouts, fonts and messages.
	".bdl": "game model", ".bmd": "game model", ".bti": "game texture", ".bck": "game animation",
	".blo": "game layout", ".bfn": "game font", ".bmg": "game messages",
	// Saves: a memory card file, a whole card, a save state.
	".sav": "save state", ".gci": "memory card save", ".gcs": "memory card save",
	".raw": "memory card image",
}

// saveState is a Dolphin save-state slot file: .s01 to .s99 (the hotkey
// slots are .s01..s10).
var saveState = regexp.MustCompile(`^\.s[0-9]{2}$`)

// names are files from the disc's system area and the files we patch.
var names = map[string]string{
	"main.dol": "the disc's executable", "rels.arc": "the disc's module archive",
	"boot.bin": "the disc header", "bi2.bin": "the disc header", "apploader.img": "the disc's apploader",
	"fst.bin": "the disc's file table", "opening.bnr": "the disc banner",
}

// magic is a format's mark at an offset.
type magic struct {
	off  int
	mark []byte
	what string
}

const nkitOffset = 0x200

var magics = []magic{
	// JKRArchive's volume type (tww src/JSystem/JKernel/JKRArchivePub.cpp:23).
	{0, []byte("RARC"), "a RARC archive"},
	// JKRDecomp checks 'Y', 'a', '0' (JKRDecomp.cpp:273): Yaz0 and Yay0.
	{0, []byte("Yaz0"), "Yaz0-compressed game data"},
	{0, []byte("Yay0"), "Yay0-compressed game data"},
	// J3DModelLoader's magics (J3DModelLoader.cpp:24-57); animations too.
	{0, []byte("J3D1"), "a J3D model or animation"},
	{0, []byte("J3D2"), "a J3D model or animation"},
	// JMessage's signature and type (JMessage/resource.cpp:87-90).
	{0, []byte("MESGbmg1"), "a BMG message file"},
	// The disc ID at 0 of a GZLE01 disc (internal/inject/iso.go
	// isoExpectedGameID), of a GCI's header (gameName + company, card.h:45-46,
	// wwapi/savefile) and of a MEM1 dump (the ID sits at 0x80000000).
	{0, []byte("GZLE01"), "Wind Waker data (a disc, a save or a RAM dump)"},
	// Our patcher's input formats (internal/inject/ciso.go, format.go).
	{0, []byte("CISO"), "a CISO disc image"},
	{0, []byte("RVZ\x01"), "an RVZ disc image"},
	{0, []byte("WIA\x01"), "a WIA disc image"},
	{nkitOffset, []byte("NKIT"), "an NKit disc image"},
	// Any GameCube disc's magic word at 0x1C. Not in the decomp (DVDDiskID
	// leaves it in padding, dolphin/dvd/dvd.h:52) nor in our tables: it is
	// the documented disc-header word (Dolphin, YAGCD), unverified here.
	{0x1C, []byte{0xC2, 0x33, 0x9F, 0x3D}, "a GameCube disc image"},
}

// gczMagic is GCZ's little-endian u32 at 0 (internal/inject/format.go).
const gczMagic = 0xB10BC001

// Name says why the file at the slash path p is a game file by its name or
// extension, or "".
func Name(p string) string {
	base := strings.ToLower(path.Base(strings.ReplaceAll(p, `\`, "/")))
	if w, ok := names[base]; ok {
		return "named " + base + " (" + w + ")"
	}
	ext := path.Ext(base)
	if w, ok := extensions[ext]; ok {
		return "a " + ext + " file (" + w + ")"
	}
	if saveState.MatchString(ext) {
		return "a " + ext + " file (save state)"
	}
	if strings.Contains(base, ".nkit.") {
		return "an NKit disc image (by name)"
	}
	return ""
}

// Header says why a file whose first bytes are h (up to HeaderLen) is a
// game file by its format, or "".
func Header(h []byte) string {
	for _, m := range magics {
		if len(h) >= m.off+len(m.mark) && bytes.Equal(h[m.off:m.off+len(m.mark)], m.mark) {
			return "looks like " + m.what
		}
	}
	if len(h) >= 4 && binary.LittleEndian.Uint32(h) == gczMagic {
		return "looks like a GCZ disc image"
	}
	return ""
}

// Check is every reason the file at p with first bytes h is a game file
// (Name, then Header); nil when it is none. h may be nil (a file that
// can't be read is checked by name).
func Check(p string, h []byte) []string {
	var why []string
	if w := Name(p); w != "" {
		why = append(why, w)
	}
	if w := Header(h); w != "" {
		why = append(why, w)
	}
	return why
}
