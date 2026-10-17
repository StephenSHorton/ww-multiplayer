// Package save is the game's save data as the modding API reads it: the
// 0x778-byte dSv_save_c image (Image), the decomp member each of its
// bytes belongs to (Layout), a tick's applied flags and counts (View), and
// the inventory and equipment slots the get-item and collect flags fill
// (ItemSlots, EquipSlots).
//
// It holds no co-op policy: how a byte merges between players is
// mods/coop/class's, a table keyed by the offsets Layout names.
//
// View reads the heart Catalogue's records and the item sources' flags
// through wwapi/tables.
//
// Decomp citations are zeldaret/tww at 4d78c994, as in wwapi/addr.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package save

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// ImageSize is the size of the image, dSv_save_c (d_save.h:916).
const ImageSize = addr.SaveImageSize

// Image is the canonical dSv_save_c image (d_save.h:908-916): the saved
// image with the current table's live memBit folded into its saved slot,
// as state.Snapshot.Image returns it, indexed by addr.Off* offsets.
type Image [ImageSize]byte
