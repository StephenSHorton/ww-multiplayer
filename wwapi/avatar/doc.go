// Package avatar drives the remote Links drawn on this Dolphin: the
// pose-feed slots the C side's mini-Links read (pose buffer, pose and
// face sequence bytes, shadow_mode), the guards on writing a pose from
// the network into emulated RAM, the pose math (localize, re-apply,
// lerp), and the tunic slots that recolor each Link's body texture
// (Tunics, over wwapi/gfx/texture).
//
// Who sits in which slot, and what the network sends, is the caller's
// business (the multiplayer mod's pose feed, mods/multiplayer/feed): this package
// only writes what it is told into the mailbox. Cap is the number of
// pose-feed slots, mailbox.MaxRemoteLinks (3 since WWMB_LAYOUT 3).
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package avatar
