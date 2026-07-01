package network

import (
	"os"
	"strconv"
	"time"
)

// Tunable network timing. These are package VARS (not consts) on purpose:
// tests reassign them to small values to exercise the heartbeat /
// read-deadline machinery without real-world waits, and several are
// env-overridable at process start for field tuning.
//
//	WW_HEARTBEAT_SECS   heartbeat ping cadence (both directions)   default 5s
//	WW_READ_TIMEOUT_SECS read deadline before a peer is declared    default 15s
//	                     ungracefully gone (server + client share it)
//	WW_BACKOFF_MAX_SECS  upper bound on reconnect backoff           default 8s
//	WW_FREEZE_SECS       silent-sender freeze threshold (receiver)  default 2s
//	WW_DESPAWN_SECS      silent-sender despawn threshold (receiver) default 5s
var (
	// heartbeatInterval is how often each side sends a MsgPing. The ping
	// doubles as a keepalive (resets the peer's read deadline) and, on the
	// client, as an RTT probe (the server echoes it as MsgPong).
	heartbeatInterval = envSeconds("WW_HEARTBEAT_SECS", 5*time.Second)

	// clientReadTimeout / serverReadTimeout are the read deadlines that turn
	// a stalled/ungracefully-dropped peer into an ordinary read error. They
	// must comfortably exceed heartbeatInterval so a live-but-quiet peer
	// (paused game, menu) is kept alive by heartbeats rather than reaped.
	clientReadTimeout = envSeconds("WW_READ_TIMEOUT_SECS", 15*time.Second)
	serverReadTimeout = envSeconds("WW_READ_TIMEOUT_SECS", 15*time.Second)

	// backoffBase / backoffCap bound the exponential reconnect backoff.
	backoffBase = 100 * time.Millisecond
	backoffCap  = envSeconds("WW_BACKOFF_MAX_SECS", 8*time.Second)

	// poseFreezeAfter / poseDespawnAfter drive the receiver-side
	// vanished-sender UX (a remote Link freezes, then despawns, when its
	// pose stream goes silent — a faster, local safety net than waiting for
	// the server's read-timeout MsgLeave).
	poseFreezeAfter  = envSeconds("WW_FREEZE_SECS", 2*time.Second)
	poseDespawnAfter = envSeconds("WW_DESPAWN_SECS", 5*time.Second)
)

// PoseLivenessDefaults exposes the (env-configured) freeze/despawn
// thresholds so the receiver loop in package main can feed them to
// PoseLiveness without re-reading the environment.
func PoseLivenessDefaults() (freeze, despawn time.Duration) {
	return poseFreezeAfter, poseDespawnAfter
}

// envSeconds reads an env var as a (possibly fractional) number of seconds,
// falling back to def when unset, unparseable, or non-positive.
func envSeconds(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs <= 0 {
		return def
	}
	return time.Duration(secs * float64(time.Second))
}

// procStartMono anchors a process-local monotonic clock. monotonicNanos()
// returns nanoseconds since this point using Go's monotonic reading (immune
// to wall-clock jumps), which is exactly what we want for RTT math: the
// client stamps a ping with monotonicNanos(), the server echoes the bytes
// verbatim, and the client subtracts to get a real elapsed duration.
var procStartMono = time.Now()

func monotonicNanos() int64 { return int64(time.Since(procStartMono)) }
