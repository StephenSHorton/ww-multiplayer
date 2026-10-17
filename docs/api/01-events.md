# wwapi/events: the event bus

**Status: v0 UNSTABLE.** Phase B step B2 of [00-design.md](00-design.md). Its API can change in any 0.x minor version. The core watcher exists (C14): the `wwapi.Watcher` a `wwapi.Game` owns polls snapshot edges (10 Hz, `wwapi.FastInterval` for 30 Hz) and emits `StageChanged`, `ItemGained`, `SwitchSet`, `EventBitSet` and `HeartbeatLost`, defined in `wwapi/events/core.go`. Outside wwapi only the example mod (`mods/example`, [03-writing-a-mod.md](03-writing-a-mod.md)) uses it: the mod runtime takes a Game as `Options.Game` and forwards its core events to the mods (D3), and the app wires that in at D4.

Import path: `github.com/StephenSHorton/ww-multiplayer/wwapi/events`.

## Events

An event is a struct value that embeds `events.Base` and has an `EventName` method with a value receiver:

```go
type StageChanged struct {
    events.Base
    From, To events.Place // Place: Stage, Room, Table, File
    First    bool         // the watcher's first snapshot
}

func (StageChanged) EventName() string { return "core.stage_changed" }
```

That is the real `events.StageChanged` from `wwapi/events/core.go`. A mod's own events look the same, under its `mod.<id>.` names.

The bus stamps every event it emits with a `Meta`:

| Field | Meaning |
|---|---|
| `Seq` | Shared by the whole bus. It starts at 1 and goes up by one for each emitted event, in emit order. |
| `At` | When `Emit` ran (`Options.Now`, which is `time.Now` by default). |
| `Source` | `SourceGame` (the core view), `SourceMod` (a mod's view) or `SourceNet` (any view's `Net()`, for events relayed from another player). |
| `Origin` | `"core"` or the id of the mod that emitted it. |

`Emit` copies the event before it stamps it, so the caller's value stays as it was. Each name belongs to the first Go type that emits it. If another type emits the same name later, `Emit` returns `ErrNameTaken`.

## Namespaces

Event names are made of dot-separated segments. A segment uses lowercase ASCII letters, digits and `_`, and does not start with a digit. A name is at most 128 bytes long.

| View | Made by | May emit |
|---|---|---|
| core | `events.New(opts)` | `core.<name>` only. Mods can never emit under `core`. |
| mod | `core.Mod(id)` | `mod.<id>.<name>` only, for its own id |
| relay | `view.Net()` | the same names as `view`, stamped `SourceNet` |

Mod ids are lowercase. They start with a letter, optionally after one leading `_`, which marks a test fixture. An id is at most 64 bytes, and `core` cannot be one. Only the core view can call `Mod`. Any view can subscribe to any event.

## Subscribing

```go
off := events.On(bus, func(e StageChanged) { ... })            // a callback
ch, off := events.Chan[StageChanged](bus, 32)                   // a buffered channel
e, ok := events.Last[StageChanged](bus)                         // the latest value of a type
e, err := events.Wait(ctx, bus, func(e StageChanged) bool { return e.To.Stage == "M_NewD2" })
all, off := events.Chan[events.Event](bus, 64)                  // every event
```

- **The publisher never blocks.** Each subscription has its own buffer. When the buffer is full, the oldest queued event is dropped to make room. `Chan` takes the buffer size as an argument (at least 1); `On` and `Wait` use `DefaultBuffer` (256). `bus.Dropped()` counts the drops for the whole bus.
- **Order.** A subscriber gets the events it keeps in emit order, and `Seq` always goes up.
- **Interfaces.** If you subscribe with an interface type, you get every event whose type implements it. `Last` with an interface type also finds events emitted before anyone subscribed with that interface.
- **`On`** runs `fn` on its own goroutine, one call at a time. `off()` can be called more than once, including from inside `fn`. A call that has already started can still finish after `off` returns. `off` discards events still queued. A panic in `fn` is recovered, counted in `Bus.Panics()` and passed to `Options.OnPanic` with its stack, and the subscription stays. When `OnPanic` is nil the panic and stack are logged instead, so a handler panic is never silent. The B3 supervisor is expected to set `OnPanic` and close the owning mod's view.
- **`Wait`** only sees events emitted after it is called. It returns `ctx.Err()` when the context ends and `ErrClosed` when the bus or its view closes. Because it uses a drop-oldest buffer, it can miss an event during a flood.
- **Unsubscribe.** `off()` closes the channel. `Last` keeps working after `Close`.

## Close and quarantine

`Close` on the core view closes the whole bus. Every channel closes, and `Emit` and `Mod` return `ErrClosed`. `Close` on a mod view ends only the subscriptions made through that view, `Net()` views included, and the view can no longer emit. For `On` handlers, `Close` works like `off`: events still queued are discarded, and only a call already running can finish after `Close` returns. This is how the B3 supervisor will remove a quarantined mod's handlers. A later `core.Mod(id)` call gives a fresh view.

## Reminder

**Game events are polled snapshot edges, not in-game callbacks.** A short toggle that happens between two polls is never seen, and the order of events inside one poll tick is not defined.
