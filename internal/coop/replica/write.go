package replica

import "github.com/StephenSHorton/ww-multiplayer/wwapi/write"

// Co-op's write engine is built on wwapi/write: the Breaker, co-op's off
// switch, is write.Breaker. These names keep the replica's API as it was.

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
