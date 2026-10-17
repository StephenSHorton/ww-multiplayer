package coop

import (
	"errors"

	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// coopEnv is an Env whose Dolphin is never found.
func coopEnv() coopsession.Env {
	return coopsession.Env{Open: func(uint32) (*emu.Dolphin, error) { return nil, errors.New("no Dolphin in tests") }}
}
