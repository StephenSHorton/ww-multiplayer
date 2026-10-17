package tables

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
)

// identityDomain starts what Identity hashes, so the hash can't be taken
// for another FNV-1a over the same bytes (wire.ClassHash is one).
const identityDomain = "ww-multiplayer co-op stage tables\x00"

// Identity is the tables' identity, which a co-op replica sends as
// CO_HELLO's stageTblHash and the host's hub compares with its own (§4.4,
// §6.2): FNV-1a 64, as wire.ClassHash, over the schema and every section
// in the JSON the cache holds, leaving out the binary that built them
// (Builder) and the disc they were read from (ID).
//
// So two discs whose stages give the same tables have the same identity
// whatever their main.dol: a vanilla disc and its patched copy, an ISO and
// its CISO. Other stage data (a modded disc), or a build whose builders
// decide or explain anything differently, gives another; the hub then
// turns switch sync off for that player (hub.TableFeatures).
//
// It is never 0, which CO_HELLO keeps for "no tables". It fails for tables
// that lack a section, and for tables that don't encode as JSON (a NaN
// position in a damaged stage file), which can't be cached either.
func (t *Tables) Identity() (uint64, error) {
	if t == nil || !t.complete() {
		return 0, errors.New("tables: only whole tables have an identity")
	}
	c := *t
	c.Builder, c.ID = "", ID{}
	b, err := json.Marshal(&c)
	if err != nil {
		return 0, fmt.Errorf("tables: %w", err)
	}
	h := fnv.New64a()
	h.Write([]byte(identityDomain))
	h.Write(b)
	if id := h.Sum64(); id != 0 {
		return id, nil
	}
	return 1, nil
}
