package wwapi

import (
	"crypto/sha256"
	"slices"
	"strings"
)

// ModsID is what two players compare before they play together
// (docs/api/02-mods.md, "mods_hash"): the client_required mods that load,
// as sorted "id@version" strings, and Hash, the SHA-256 of those strings
// joined by newlines. Two builds with the same client_required mods at the
// same versions have the same Hash, whatever else they load.
type ModsID struct {
	List []string
	Hash [sha256.Size]byte
}

// ModsHash is the ModsID of infos: its mods that load (LOADED) with
// "multiplayer": "client_required". Use Plan.Infos (or the Runtime's
// Plan) for the mods this process runs.
func ModsHash(infos []ModInfo) ModsID {
	var list []string
	for _, mi := range infos {
		if mi.Status == Loaded && mi.Multiplayer == ClientRequired {
			list = append(list, mi.ID+"@"+mi.Version)
		}
	}
	return ModsIDOf(list)
}

// ModsIDOf is the ModsID of a list of "id@version" strings, in any order.
// The list is copied and sorted.
func ModsIDOf(list []string) ModsID {
	list = slices.Clone(list)
	slices.Sort(list)
	return ModsID{List: list, Hash: sha256.Sum256([]byte(strings.Join(list, "\n")))}
}

// ModsID is the ModsID of the plan's loading mods (ModsHash of Infos).
func (p *Plan) ModsID() ModsID { return ModsHash(p.Infos()) }
