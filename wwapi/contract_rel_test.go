package wwapi_test

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestContractREL: the contract check reports the REL module's slack
// words without enforcing them. A booted game without the module (the
// words zero) is allowed and says not linked; with the module's prolog
// words it says linked and which build, whether or not that is this
// build's; and Info carries both.
func TestContractREL(t *testing.T) {
	m, _ := bootedRAM(t)
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !c.WritesAllowed || c.REL.Linked || c.REL.BuildID != 0 {
		t.Errorf("no module: %+v", c)
	}
	g := newGame(t, m)
	if info := g.Info(); info.RELLinked || info.RELBuildID != 0 {
		t.Errorf("no module: Info %+v", info)
	}

	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 7}); err != nil {
		t.Fatal(err)
	}
	if c, err = wwapi.CheckContract(m, wwapi.ContractOptions{}); err != nil {
		t.Fatal(err)
	}
	if !c.WritesAllowed || !c.REL.Linked || c.REL.BuildID != inject.RELBuildID || c.REL.Frames != 7 {
		t.Errorf("this build's module: %+v", c)
	}
	// A Game checks the contract when made (and again on a save-state
	// load), so a Game attached to the linked module reports it.
	g.Close()
	if info := newGame(t, m).Info(); !info.RELLinked || info.RELBuildID != inject.RELBuildID {
		t.Errorf("this build's module: Info %+v", info)
	}

	// Another build's module is reported, not refused.
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID ^ 0xFFFF}); err != nil {
		t.Fatal(err)
	}
	if c, err = wwapi.CheckContract(m, wwapi.ContractOptions{}); err != nil {
		t.Fatal(err)
	}
	if !c.WritesAllowed || !c.REL.Linked || c.REL.BuildID != inject.RELBuildID^0xFFFF {
		t.Errorf("another build's module: %+v", c)
	}
	// The epilog's clear: not linked again.
	if err := mailbox.WriteREL(m, mailbox.REL{BuildID: inject.RELBuildID}); err != nil {
		t.Fatal(err)
	}
	if c, err = wwapi.CheckContract(m, wwapi.ContractOptions{}); err != nil || c.REL.Linked || c.REL.BuildID != 0 {
		t.Errorf("after the epilog: %+v, %v", c, err)
	}
}
