package wwapi

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// GameID is the one disc the API knows: Wind Waker, NTSC-U.
const GameID = "GZLE01"

// ContractMode is how the contract check reads the blob.
type ContractMode uint8

const (
	// Strict is the check from Phase E1 on (design decision 18): the
	// game must carry the WWMB header main01_init writes, with this
	// build's ABI and layout version, and the CRC-32 of the blob's
	// read-only sections (inject.HashRanges: .rodata, .text, .init,
	// .fini) read back from RAM must be this build's BlobHash. A missing
	// header (every blob before E1, an unpatched game, or a save state
	// taken on either) refuses writes. The pre-E1 mode, where a missing
	// header was ABI 0 and allowed, ended with E1.
	Strict ContractMode = iota + 1
)

func (m ContractMode) String() string {
	if m == Strict {
		return "strict"
	}
	return fmt.Sprintf("ContractMode(%d)", uint8(m))
}

// ErrContract is matched (errors.Is) by the error a refused contract
// returns: the game in RAM is not one this build may write.
var ErrContract = errors.New("wwapi: this save state or ISO is from another patch; cold-boot or recapture")

// RepatchAdvice is what to tell a player whose game the contract check
// refused: the words the app, the TUI and the CLI print.
const RepatchAdvice = "Patch your Wind Waker ISO again with this version (`ww-multiplayer patch`, or Setup in the app) and cold-boot it from Dolphin's game list. A save state taken on another version cannot be used: boot the game and recapture it."

// ContractOptions configure CheckContract. The zero value checks for
// GameID and this build's blob (internal/inject: ABI, LayoutVersion,
// BlobHash over HashRanges).
type ContractOptions struct {
	GameID string // "" means GameID
	// ABI and Layout are the header values to require; 0 means this
	// build's (inject.ABI, inject.LayoutVersion).
	ABI    uint16
	Layout uint16
	// BlobHash is the CRC-32 to require over HashRanges (the blob's
	// read-only sections); both zero mean this build's.
	BlobHash   uint32
	HashRanges [][2]uint32
}

func (o ContractOptions) withDefaults() ContractOptions {
	if o.GameID == "" {
		o.GameID = GameID
	}
	if o.ABI == 0 {
		o.ABI = inject.ABI
	}
	if o.Layout == 0 {
		o.Layout = inject.LayoutVersion
	}
	if o.BlobHash == 0 && len(o.HashRanges) == 0 {
		o.BlobHash, o.HashRanges = inject.BlobHash, inject.HashRanges
	}
	return o
}

// Contract is the result of the contract check (design §6.4 step 5).
type Contract struct {
	Mode   ContractMode
	GameID string         // read from the start of MEM1
	Header mailbox.Header // the WWMB header; not Present before E1
	ABI    uint16         // the header's, 0 without one
	Layout uint16         // the header's, 0 without one
	// BlobHash is the CRC-32 (IEEE) of the blob's read-only sections
	// (.rodata, .text, .init, .fini: inject.HashRanges) as read from RAM,
	// and WantHash this build's (blob.go BlobHash). BlobMatches says they
	// are equal.
	BlobHash    uint32
	WantHash    uint32
	BlobMatches bool
	// Features is what the header's features word says the blob
	// provides when writes are allowed; a known, empty set otherwise
	// (no header, another ABI or layout, or another blob's code).
	Features Features
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

// CheckContract reads the game ID, the WWMB header and the blob's code
// from m and decides, in Strict mode, whether this build may write to
// the game. The error is for memory that could not be read; a refusal is
// a Contract with WritesAllowed false.
func CheckContract(m mem.Reader, o ContractOptions) (Contract, error) {
	o = o.withDefaults()
	c := Contract{Mode: Strict, WantHash: o.BlobHash, Features: KnownFeatures()}

	id, err := m.Read(addr.MEM1Base, len(o.GameID))
	if err != nil {
		return c, fmt.Errorf("wwapi: contract: game ID: %w", err)
	}
	c.GameID = string(id)
	if c.Header, err = mailbox.ReadHeader(m); err != nil {
		return c, fmt.Errorf("wwapi: contract: %w", err)
	}
	if c.BlobHash, err = mailbox.BlobHash(m, o.HashRanges); err != nil {
		return c, fmt.Errorf("wwapi: contract: %w", err)
	}
	c.BlobMatches = c.BlobHash == o.BlobHash
	if c.Header.Present {
		c.ABI, c.Layout = c.Header.ABI, c.Header.Layout
	}

	switch {
	case c.GameID != o.GameID:
		c.Why = fmt.Sprintf("game ID %q is not %s", c.GameID, o.GameID)
	case !c.Header.Present:
		c.Why = "the game has no WWMB header: it runs a patch from before this version, or none (an old patched ISO, or a save state taken on one)"
	case c.ABI != o.ABI || c.Layout != o.Layout:
		c.Why = fmt.Sprintf("the game's patch is ABI %d layout %d, this version's is ABI %d layout %d", c.ABI, c.Layout, o.ABI, o.Layout)
	case !c.BlobMatches:
		c.Why = fmt.Sprintf("the injected code in RAM is not this version's (code hash %08X, want %08X)", c.BlobHash, o.BlobHash)
	default:
		c.WritesAllowed = true
		// The features word only means what this build's WW_FEAT_* names
		// say when the whole contract holds; a refused game provides none,
		// so a mod with needs is NEEDS_REPATCH, not ready.
		c.Features = FeaturesOf(c.Header.Features)
	}
	return c, nil
}
