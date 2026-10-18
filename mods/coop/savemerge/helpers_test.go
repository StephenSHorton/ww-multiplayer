package savemerge

import (
	"math/rand/v2"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// randImage is a random image with the padding the card does not hold
// zeroed and a player name, so it is a slot in use (savefile's own test
// helper of the same name).
func randImage(seed uint64, name string) save.Image {
	r := rand.New(rand.NewPCG(seed, 1))
	var img save.Image
	for i := range img {
		img[i] = byte(r.Uint32())
	}
	p := savefile.Pack(&img)
	img = savefile.Unpack(&p)
	n := img[addr.OffPlayerName : addr.OffPlayerName+17]
	clear(n)
	copy(n, name)
	img[addr.OffClearCount] = 0
	return img
}
