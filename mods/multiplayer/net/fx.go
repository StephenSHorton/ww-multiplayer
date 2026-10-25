package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

// Projectile events (protocol 6; ww-online review idea 5, design:
// ww-multiplayer-handoff/archive/ww-online-review/DESIGN-big-items.md
// section (a)): a player's bombs, boat-cannon shots and arrows, relayed so
// each other player's game makes world-only copies (inject/rel/fx.c,
// inject/include/wwfx.h). Travels on a player connection only, like
// MsgPose: broadcast-pose sends its game's outbox, the server stamps the
// sender's ID and relays it to every other player connection, and each
// puppet-sync scopes it to its own stage and room (mods/multiplayer/
// fxsync). The server cannot scope it itself: a pose carries no stage.
//
// Client -> server payload (FxWireSize bytes, big-endian):
//
//	[ver:1 = FxWireVersion][seq:4][kind:1][variant:1][room:1 s8]
//	[stage:8, NUL padded][id:4][x y z:3 f32][speedF speedY gravity:3 f32]
//	[angle x y z:3 s16][timer:2 s16]
//
// Server -> client: [senderID:1] + the same payload. seq counts the
// sender's events from 1 for its whole run (it survives reconnects), so
// a receiver drops repeats per sender.
const (
	MsgFx byte = 'X' // Both: one projectile event

	FxWireVersion = 1
	FxWireSize    = 52
	FxStageLen    = 8

	// FxKindMax is the last event kind (wwfx.h WWFX_KIND_MAX); kinds start at 1.
	FxKindMax = 6
	// FxArrowMax is the last arrow type (wwfx.h WWFX_ARROW_MAX).
	FxArrowMax = 3
	// FxKindArrow is the arrow event (wwfx.h WWFX_KIND_ARROW): the only
	// one with a variant.
	FxKindArrow = 6

	// The server's limits on a sender (the design's token bucket): a
	// burst of FxBurst events, refilled at FxRatePerSec.
	FxBurst      = 20
	FxRatePerSec = 10

	// FxMaxCoord, FxMaxSpeed and FxMaxGravity bound the floats (the REL
	// refuses the same, fx.c).
	FxMaxCoord   = 1.0e6
	FxMaxSpeed   = 1000.0
	FxMaxGravity = 50.0
)

// FxEvent is one projectile event on the wire.
type FxEvent struct {
	Seq     uint32
	Kind    uint8
	Variant uint8
	Room    int8
	Stage   string // the sender's stage name when the event happened (at most FxStageLen bytes)
	ID      uint32 // the sender's projectile (its process id in the sender's game)
	Pos     [3]float32
	SpeedF  float32
	SpeedY  float32
	Gravity float32
	Angle   [3]int16
	Timer   int16
}

// ErrFxEvent is wrapped by ParseFx for a payload it refuses.
var ErrFxEvent = errors.New("projectile event")

// Bytes encodes e as a client -> server payload.
func (e *FxEvent) Bytes() []byte {
	b := make([]byte, FxWireSize)
	be := binary.BigEndian
	b[0] = FxWireVersion
	be.PutUint32(b[1:], e.Seq)
	b[5], b[6], b[7] = e.Kind, e.Variant, byte(e.Room)
	copy(b[8:8+FxStageLen], e.Stage)
	be.PutUint32(b[16:], e.ID)
	for i, v := range e.Pos {
		be.PutUint32(b[20+4*i:], math.Float32bits(v))
	}
	be.PutUint32(b[32:], math.Float32bits(e.SpeedF))
	be.PutUint32(b[36:], math.Float32bits(e.SpeedY))
	be.PutUint32(b[40:], math.Float32bits(e.Gravity))
	for i, v := range e.Angle {
		be.PutUint16(b[44+2*i:], uint16(v))
	}
	be.PutUint16(b[50:], uint16(e.Timer))
	return b
}

// ParseFx decodes and checks a client -> server payload: its version and
// size, a known kind, a variant only on an arrow, finite floats in range
// and a printable stage name. Bytes past FxWireSize are ignored, so a
// later version can append fields.
func ParseFx(b []byte) (FxEvent, error) {
	var e FxEvent
	if len(b) < FxWireSize {
		return e, fmt.Errorf("%w: %d bytes, want %d", ErrFxEvent, len(b), FxWireSize)
	}
	if b[0] != FxWireVersion {
		return e, fmt.Errorf("%w: version %d", ErrFxEvent, b[0])
	}
	be := binary.BigEndian
	e.Seq = be.Uint32(b[1:])
	e.Kind, e.Variant, e.Room = b[5], b[6], int8(b[7])
	st := b[8 : 8+FxStageLen]
	n := 0
	for n < len(st) && st[n] != 0 {
		if st[n] < 0x21 || st[n] > 0x7E {
			return e, fmt.Errorf("%w: stage name byte 0x%02X", ErrFxEvent, st[n])
		}
		n++
	}
	for _, c := range st[n:] {
		if c != 0 {
			return e, fmt.Errorf("%w: stage name not NUL padded", ErrFxEvent)
		}
	}
	e.Stage = string(st[:n])
	e.ID = be.Uint32(b[16:])
	for i := range e.Pos {
		e.Pos[i] = math.Float32frombits(be.Uint32(b[20+4*i:]))
	}
	e.SpeedF = math.Float32frombits(be.Uint32(b[32:]))
	e.SpeedY = math.Float32frombits(be.Uint32(b[36:]))
	e.Gravity = math.Float32frombits(be.Uint32(b[40:]))
	for i := range e.Angle {
		e.Angle[i] = int16(be.Uint16(b[44+2*i:]))
	}
	e.Timer = int16(be.Uint16(b[50:]))
	if e.Kind < 1 || e.Kind > FxKindMax {
		return e, fmt.Errorf("%w: kind %d", ErrFxEvent, e.Kind)
	}
	if e.Kind == FxKindArrow && e.Variant > FxArrowMax || e.Kind != FxKindArrow && e.Variant != 0 {
		return e, fmt.Errorf("%w: variant %d for kind %d", ErrFxEvent, e.Variant, e.Kind)
	}
	if e.Stage == "" {
		return e, fmt.Errorf("%w: no stage", ErrFxEvent)
	}
	for _, v := range e.Pos {
		if !fxIn(v, FxMaxCoord) {
			return e, fmt.Errorf("%w: position %v", ErrFxEvent, e.Pos)
		}
	}
	if !fxIn(e.SpeedF, FxMaxSpeed) || !fxIn(e.SpeedY, FxMaxSpeed) || !fxIn(e.Gravity, FxMaxGravity) {
		return e, fmt.Errorf("%w: speeds %v %v, gravity %v", ErrFxEvent, e.SpeedF, e.SpeedY, e.Gravity)
	}
	return e, nil
}

// fxIn is false for NaN, infinity and anything at or past limit.
func fxIn(v float32, limit float64) bool {
	f := float64(v)
	return f > -limit && f < limit
}

// FxRelayMessage builds the server -> client payload: the sender's ID,
// then its payload's first FxWireSize bytes.
func FxRelayMessage(senderID byte, payload []byte) []byte {
	out := make([]byte, 1+FxWireSize)
	out[0] = senderID
	copy(out[1:], payload[:FxWireSize])
	return out
}

// ParseFxRelay splits a server -> client payload into the sender's ID and
// its checked event.
func ParseFxRelay(b []byte) (senderID byte, e FxEvent, err error) {
	if len(b) < 1 {
		return 0, e, fmt.Errorf("%w: empty relay", ErrFxEvent)
	}
	e, err = ParseFx(b[1:])
	return b[0], e, err
}

// fxBucket is the server's token bucket for one player connection.
type fxBucket struct {
	tokens float64
	last   time.Time
}

// take spends one token at now, refilling FxRatePerSec per second up to
// FxBurst; false when the sender is over its rate.
func (b *fxBucket) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = FxBurst
	} else if dt := now.Sub(b.last).Seconds(); dt > 0 {
		b.tokens = math.Min(FxBurst, b.tokens+dt*FxRatePerSec)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// SendFx sends one projectile event (a player client: broadcast-pose).
func (c *Client) SendFx(e *FxEvent) error {
	return c.send(MsgFx, e.Bytes())
}
