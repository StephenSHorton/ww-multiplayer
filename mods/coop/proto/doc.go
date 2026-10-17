// Package proto is the vocabulary a co-op hub and its replicas share
// (docs/08 §4.4): the Feature bits of CO_HELLO, CO_WELCOME and CTRL
// COOP_OFF, the world policies a session merges by (DefaultPolicy,
// SessionPolicy, SwitchPolicy, IsSwitchByte) and the scoped stores'
// filters (ScopeFilter, KillFilter, Filter). The host decides what a
// session runs; package hub enforces it and names all of these under its
// own names, and package replica reads them from here, so a replica needs
// no hub to build.
package proto
