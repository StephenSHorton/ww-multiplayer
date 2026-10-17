// Package synctables is co-op's reading of the M1 stage tables
// (wwapi/tables; docs/08-coop-design.md §6.2-6.3, §7.2, §9 M1): the
// verdict on which switches and kills sync (SyncedSwitch,
// SyncedMemorySwitch, SyncedScopedSwitch, SyncedKill, KillMask), which of
// them sync only while no Game Boy Advance is linked (GBASwitch,
// GBAMemorySwitch, GBAScopedSwitch), and M1's owner acceptance and the
// coop-tables report (Acceptance, Report). Each is a function over
// *tables.Tables; a nil *tables.Tables, or one without the section asked,
// grants nothing.
//
// Citations are as in wwapi/tables.
package synctables
