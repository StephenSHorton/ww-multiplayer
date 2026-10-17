// Package disc is the API's view of the user's own Wind Waker disc: the
// file entry its FST lists (DiscFile) and the interface the stage and
// table readers take a disc through (FileSource). The reader itself,
// *inject.Disc (internal/inject), implements FileSource; tests use
// stagetest.Disc.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package disc

// DiscFile is one file the FST lists.
type DiscFile struct {
	Path   string // from the disc root, slash-separated: "res/Stage/sea/Stage.arc"
	Offset uint32 // disc offset of its first byte
	Size   uint32
}

// FileSource is a disc listed and read by path as *inject.Disc does:
// Files lists every file; Stat and ReadFile resolve a path the way the
// game's DVD library does (case-insensitively, the first match winning),
// Stat returning the entry ReadFile would read, and both return an error
// matching fs.ErrNotExist for a missing file. stage.Load Stats each
// archive before reading it, and calls both from several goroutines at
// once.
type FileSource interface {
	Files() []DiscFile
	Stat(path string) (DiscFile, error)
	ReadFile(path string) ([]byte, error)
}
