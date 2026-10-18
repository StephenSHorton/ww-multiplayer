package fakegame

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Boot stores in m what booting this build's patched ISO leaves there
// for the contract check (wwapi.CheckContract): the disc ID at the start
// of MEM1, this build's T2 at T2Address, and the WWMB header and
// features word main01_init writes (Phase E1).
func Boot(m *mem.FakeMem) error {
	if err := m.Store(addr.MEM1Base, []byte("GZLE01")); err != nil {
		return err
	}
	if err := m.Store(inject.T2Address, inject.T2Bytes); err != nil {
		return err
	}
	return mailbox.WriteHeader(m, ThisHeader())
}

// ThisHeader is the WWMB header this build's main01_init writes.
func ThisHeader() mailbox.Header {
	return mailbox.Header{Present: true, Magic: mailbox.HeaderMagic, ABI: inject.ABI, Layout: inject.LayoutVersion, Features: inject.Features}
}
