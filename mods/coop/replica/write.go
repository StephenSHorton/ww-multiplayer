package replica

import "github.com/StephenSHorton/ww-multiplayer/wwapi/write"

// Co-op's write engine is built on wwapi/write: the Breaker, co-op's off
// switch, is write.Breaker; the Guard's static allowlist is a
// write.Allowlist of co-op's byte rules; a FrameWriter follows the
// heartbeat and rechecks the stage through a write.FrameWriter, and makes
// its escapes (Fields, StageRequest) with write.FrameWriter.WriteFields.
// These names keep the replica's API as it was.

// FrameWriter makes escapes as a write.Writer.
var _ write.Writer = (*FrameWriter)(nil)

// BeatTimeout is write.BeatTimeout.
const BeatTimeout = write.BeatTimeout

// maxBeatRate is write.MaxBeatRate.
const maxBeatRate = write.MaxBeatRate

// The writer's errors (write.ErrDryRun, write.ErrClosed, write.ErrStall,
// write.ErrAborted).
var (
	ErrDryRun  = write.ErrDryRun
	ErrClosed  = write.ErrClosed
	ErrStall   = write.ErrStall
	ErrAborted = write.ErrAborted
)

// The escapes' bounds (write.MaxStageLayer, write.MaxWipe).
const (
	MaxStageLayer = write.MaxStageLayer
	MaxWipe       = write.MaxWipe
)

type (
	// ByteWrite is write.ByteWrite.
	ByteWrite = write.ByteWrite
	// Result is write.Result: what one Apply did.
	Result = write.Result
	// Field is write.Field: one write of an escape.
	Field = write.Field
	// StageRequest is write.StageRequest.
	StageRequest = write.StageRequest
)

// The auto-disable thresholds (write.MaxFaultsPerMinute,
// write.MaxRegressionsPerMinute).
const (
	MaxFaultsPerMinute      = write.MaxFaultsPerMinute
	MaxRegressionsPerMinute = write.MaxRegressionsPerMinute
)

// ErrOff is write.ErrOff.
var ErrOff = write.ErrOff

type (
	// DisabledError is write.DisabledError.
	DisabledError = write.DisabledError
	// FaultKind is write.FaultKind.
	FaultKind = write.FaultKind
	// BreakerConfig is write.BreakerConfig.
	BreakerConfig = write.BreakerConfig
	// Breaker is write.Breaker: co-op's off switch.
	Breaker = write.Breaker
)

// The kinds of write fault (write.FaultVerify, write.FaultRefusal).
const (
	FaultVerify  = write.FaultVerify
	FaultRefusal = write.FaultRefusal
)

// NewBreaker is write.NewBreaker.
func NewBreaker(cfg BreakerConfig) *Breaker { return write.NewBreaker(cfg) }
