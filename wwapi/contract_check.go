package wwapi

import (
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// GameID is the one disc the API knows: Wind Waker, NTSC-U.
const GameID = "GZLE01"

// preE1Features is what every blob before E1 provides (design §5.1,
// §6.6): the mini-Link avatar slots, the remote faces, the tunic slots,
// the warp and input handshakes and the eye fix. The co-op op ring
// (CoopOps) does not exist yet.
var preE1Features = []string{"avatar", "eyefix", "face", "input", "tunic", "warp"}

// PreE1Features returns the fixed Features a blob without a WWMB header
// provides: every blob on main until E1 writes the header.
func PreE1Features() Features { return KnownFeatures(preE1Features...) }

// ContractMode is how the contract check reads the blob.
type ContractMode uint8

const (
	// PreE1 is the check until E1 lands (design decision 18): a missing
	// WWMB header is ABI 0 with PreE1Features, never a refusal, and
	// writes are gated by the game ID and the Go-side blob match alone.
	// E1 adds the strict mode, where a missing or wrong header refuses.
	PreE1 ContractMode = iota
)

func (m ContractMode) String() string {
	if m == PreE1 {
		return "pre-E1"
	}
	return fmt.Sprintf("ContractMode(%d)", uint8(m))
}

// BlobMatchMin is the share of this build's non-zero T2 bytes that must
// read back the same from RAM for the blob to match. It is the rule the
// app's own "is this build's mod in RAM" check uses (app_game.go
// hasThisMod): T2 holds .data and .bss too, which change at run time, so
// an exact match is not expected until E1 hashes .text alone.
const BlobMatchMin = 0.9

// ErrContract is matched (errors.Is) by the error a refused contract
// returns: the game in RAM is not one this build may write.
var ErrContract = errors.New("wwapi: this save state or ISO is from another patch; cold-boot or recapture")

// ContractOptions configure CheckContract. The zero value checks for
// GameID and this build's blob (internal/inject T2Bytes at T2Address).
type ContractOptions struct {
	GameID   string // "" means GameID
	Blob     []byte // nil means inject.T2Bytes
	BlobAddr uint32 // 0 means inject.T2Address
}

// Contract is the result of the contract check (design §6.4 step 5).
type Contract struct {
	Mode   ContractMode
	GameID string         // read from the start of MEM1
	Header mailbox.Header // the WWMB header; not Present before E1
	ABI    uint16         // the header's, 0 without one
	Layout uint16         // the header's, 0 without one
	// BlobHash is the CRC-32 (IEEE) of T2 as read from RAM. Before E1 it
	// covers all of T2, .data and .bss included, so it is a fingerprint
	// to log, not a value to compare: BlobMatches decides.
	BlobHash uint32
	// BlobMatch is the share of this build's non-zero T2 bytes that read
	// back the same; BlobMatches is BlobMatch >= BlobMatchMin.
	BlobMatch   float64
	BlobMatches bool
	Features    Features
	// WritesAllowed is the verdict; Why says why not ("" when allowed).
	WritesAllowed bool
	Why           string
}

// Err returns nil when writes are allowed, else an error matching
// ErrContract that carries Why.
func (c Contract) Err() error {
	if c.WritesAllowed {
		return nil
	}
	return fmt.Errorf("%w (%s)", ErrContract, c.Why)
}

// CheckContract reads the game ID, the WWMB header and T2 from m and
// decides whether this build may write to the game. It runs in PreE1
// mode. The error is for memory that could not be read; a refusal is a
// Contract with WritesAllowed false.
func CheckContract(m mem.Reader, o ContractOptions) (Contract, error) {
	want := o.GameID
	if want == "" {
		want = GameID
	}
	blob, at := o.Blob, o.BlobAddr
	if blob == nil {
		blob = inject.T2Bytes
	}
	if at == 0 {
		at = inject.T2Address
	}
	c := Contract{Mode: PreE1}

	id, err := m.Read(addr.MEM1Base, len(want))
	if err != nil {
		return c, fmt.Errorf("wwapi: contract: game ID: %w", err)
	}
	c.GameID = string(id)
	if c.Header, err = mailbox.ReadHeader(m); err != nil {
		return c, fmt.Errorf("wwapi: contract: %w", err)
	}
	live, err := m.Read(at, len(blob))
	if err != nil {
		return c, fmt.Errorf("wwapi: contract: T2: %w", err)
	}
	c.BlobHash = crc32.ChecksumIEEE(live)
	c.BlobMatch = blobMatch(live, blob)
	c.BlobMatches = c.BlobMatch >= BlobMatchMin

	// Pre-E1: no header is ABI 0 with the fixed features. A header that
	// is there (a blob newer than this build) still names its ABI and
	// layout; this build cannot read its Features word yet.
	if c.Header.Present {
		c.ABI, c.Layout = c.Header.ABI, c.Header.Layout
	}
	c.Features = PreE1Features()

	switch {
	case c.GameID != want:
		c.Why = fmt.Sprintf("game ID %q is not %s", c.GameID, want)
	case !c.BlobMatches:
		c.Why = fmt.Sprintf("the injected code in RAM is not this build's (%.0f%% of T2 matches)", c.BlobMatch*100)
	default:
		c.WritesAllowed = true
	}
	return c, nil
}

// blobMatch is the share of want's non-zero bytes that got has at the
// same place: app_game.go's modMatch.
func blobMatch(got, want []byte) float64 {
	n, same := 0, 0
	for i, b := range want {
		if b == 0 {
			continue
		}
		n++
		if i < len(got) && got[i] == b {
			same++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(same) / float64(n)
}
