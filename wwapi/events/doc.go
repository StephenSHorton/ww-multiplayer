// Package events is the Wind Waker modding API's event bus.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version, without a deprecation period.
//
// # Model
//
// An event is a plain Go struct value that embeds [Base] and has an
// EventName method with a value receiver:
//
//	type DoorOpened struct {
//		events.Base
//		Room int
//	}
//
//	func (DoorOpened) EventName() string { return "mod.mydoors.door_opened" }
//
// The core game events (StageChanged, ItemGained, ...) are defined the
// same way in this package and emitted by wwapi.Game's watcher.
//
// The bus stamps every emitted event with a [Meta] (sequence number, time,
// source and origin), so a publisher never fills Base in itself.
//
// Subscribers pick events by Go type: [On] runs a callback, [Chan] returns a
// buffered channel, [Last] returns the latest value of a type and [Wait]
// blocks until a matching event arrives. Subscribing to an interface type,
// [Event] included, receives every emitted event whose type implements it.
//
// # Delivery
//
// Emit never blocks on a subscriber. Every subscription has its own buffer;
// when it is full the oldest queued event is dropped to make room (see
// [Bus.Dropped]). A subscriber receives the events it keeps in emit order,
// with strictly increasing Meta.Seq. Game events are polled snapshot edges,
// not in-game callbacks, so a short toggle between two polls is never seen.
//
// # Namespaces
//
// Names are dot-separated lowercase segments. The core bus from [New] emits
// only core.* names; core is reserved for it. A mod's view from [Bus.Mod]
// emits only mod.<id>.* names for its own id. Anyone may subscribe to
// anything. Closing a mod's view ends every subscription made through it,
// which is how a quarantined mod's handlers are removed: queued events
// are discarded and only an On call already running may finish.
package events
