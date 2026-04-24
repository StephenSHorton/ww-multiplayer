// Package inject provides the standalone ISO patcher used by `ww-multiplayer.exe patch`
// to splice the multiplayer mod into a user-supplied vanilla Wind Waker ISO.
//
// The injected delta is captured at build time by scripts/extract_blob.py
// (which runs after Freighter produces inject/patched.dol) and emitted as
// internal/inject/blob.go. That blob contains ONLY our IP — the new T2
// code section bytes plus a list of in-DOL byte modifications (Freighter
// hook_branchlink targets + OSInit immediate patches). The user's vanilla
// DOL is the source of all original game-code bytes; nothing copyrighted
// ships in our binary.
package inject

import (
	"encoding/binary"
	"fmt"
)

// dolHeaderSize is the fixed 0x100 byte header at the start of every DOL.
const dolHeaderSize = 0x100

// dolNumText is the count of text-section slots in the DOL header.
// (DOL also has 11 data sections; we don't touch those.)
const dolNumText = 7

// PatchDOL splices our T2 section + applies every in-DOL byte patch from the
// blob into a copy of `vanilla` and returns the new DOL bytes. Caller is
// responsible for writing the result back into the ISO at the right offset.
//
// Idempotency: if the DOL already contains a T2 section pointing at our
// T2Address, it's treated as already-patched and we return ErrAlreadyPatched
// rather than corrupting the existing patch. Same for the in-DOL patches —
// if the bytes already match what we'd write, that patch is a no-op.
func PatchDOL(vanilla []byte) ([]byte, error) {
	if len(vanilla) < dolHeaderSize {
		return nil, fmt.Errorf("dol: input too small (%d bytes, need at least %d)",
			len(vanilla), dolHeaderSize)
	}

	out := make([]byte, len(vanilla))
	copy(out, vanilla)

	// Find a free text section slot, or detect we already patched this DOL.
	freeSlot := -1
	for i := 0; i < dolNumText; i++ {
		off := readU32BE(out, 0x00+4*i)
		addr := readU32BE(out, 0x48+4*i)
		size := readU32BE(out, 0x90+4*i)
		if off == 0 && addr == 0 && size == 0 {
			if freeSlot < 0 {
				freeSlot = i
			}
			continue
		}
		if addr == T2Address {
			return nil, ErrAlreadyPatched
		}
	}
	if freeSlot < 0 {
		return nil, fmt.Errorf("dol: no free text section slot (T0..T%d all in use)",
			dolNumText-1)
	}

	// Apply in-DOL byte patches (Freighter hook_branchlink targets + OSInit
	// immediates). Bounds-check each — a wildly stale blob shouldn't crash
	// the patcher silently.
	for _, p := range DOLPatches {
		end := int(p.FileOff) + len(p.Bytes)
		if end > len(out) {
			return nil, fmt.Errorf("dol: patch at 0x%x extends past end (DOL is %d bytes)",
				p.FileOff, len(out))
		}
		copy(out[p.FileOff:end], p.Bytes)
	}

	// Append T2 bytes and register the section in the header.
	t2FileOff := uint32(len(out))
	out = append(out, T2Bytes...)

	writeU32BE(out, 0x00+4*freeSlot, t2FileOff)        // file_offset[i]
	writeU32BE(out, 0x48+4*freeSlot, T2Address)        // mem_addr[i]
	writeU32BE(out, 0x90+4*freeSlot, uint32(len(T2Bytes))) // size[i]

	return out, nil
}

// ErrAlreadyPatched is returned by PatchDOL when the input DOL already has
// a section at T2Address — almost certainly because the user is running the
// patcher against an already-patched ISO. The caller should surface this as
// a friendly "looks like this ISO is already patched" message rather than
// a hard error.
var ErrAlreadyPatched = fmt.Errorf("dol: already patched (T2 section at 0x%08X exists)", T2Address)

func readU32BE(b []byte, off int) uint32 {
	return binary.BigEndian.Uint32(b[off : off+4])
}

func writeU32BE(b []byte, off int, v uint32) {
	binary.BigEndian.PutUint32(b[off:off+4], v)
}
