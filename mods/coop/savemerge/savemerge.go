// Package savemerge is co-op's offline save merge: Diff and Merge over two
// save images by the class table and the replica's planner and holds, and
// Derive, the DERIVED bytes the merged flags imply. It works on the
// images wwapi/savefile reads from a card file; it was savefile's
// merge.go and derive.go until the I/O half moved to wwapi/savefile.
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, §N a section of docs/08-coop-design.md.
package savemerge

import "encoding/binary"

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
