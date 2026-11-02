package seachart

import (
	"encoding/binary"
	"errors"
	"testing"
)

// ia4 builds a w x h IA4 BTI whose texels all hold v.
func ia4(w, h int, v byte) []byte {
	bw, bh := (w+7)/8, (h+3)/4
	b := make([]byte, btiHeaderLen+bw*bh*32)
	b[0] = gxIA4
	binary.BigEndian.PutUint16(b[2:], uint16(w))
	binary.BigEndian.PutUint16(b[4:], uint16(h))
	binary.BigEndian.PutUint32(b[0x1C:], btiHeaderLen)
	for i := btiHeaderLen; i < len(b); i++ {
		b[i] = v
	}
	return b
}

func TestDecodeIA4(t *testing.T) {
	b := ia4(10, 6, 0xF0) // opaque, intensity 0
	// The second block in the first row starts at byte 32: texel (8, 0).
	b[btiHeaderLen+32] = 0x8F
	img, err := decodeBTI(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Dx(); got != 10 {
		t.Fatalf("width %d", got)
	}
	if c := img.NRGBAAt(0, 0); c.R != 0 || c.A != 0xFF {
		t.Errorf("(0,0) = %v, want opaque black", c)
	}
	if c := img.NRGBAAt(8, 0); c.R != 0xFF || c.A != 0x88 {
		t.Errorf("(8,0) = %v, want white at alpha 0x88", c)
	}
}

func TestDecodeBTIRefuses(t *testing.T) {
	b := ia4(8, 4, 0)
	b[0] = 14 // CMPR
	if _, err := decodeBTI(b); !errors.Is(err, errFormat) {
		t.Errorf("CMPR: %v, want errFormat", err)
	}
	if _, err := decodeBTI(ia4(8, 4, 0)[:40]); err == nil {
		t.Error("truncated image data decoded")
	}
}

func TestUpscaleSharpens(t *testing.T) {
	img, _ := decodeBTI(ia4(8, 4, 0xFF))
	up := upscale(img, 4)
	if up.Bounds().Dx() != 32 || up.Bounds().Dy() != 16 {
		t.Fatalf("size %v", up.Bounds())
	}
	if c := up.NRGBAAt(16, 8); c.A != 0xFF || c.R != uint8(landColor[0]) {
		t.Errorf("land texel = %v", c)
	}
	clear, _ := decodeBTI(ia4(8, 4, 0x0F))
	if c := upscale(clear, 4).NRGBAAt(16, 8); c.A != 0 {
		t.Errorf("transparent texel = %v", c)
	}
}

func TestSquares(t *testing.T) {
	for _, c := range []struct {
		x, z float64
		want string
		grid int
	}{
		{0, 0, "D4", 24},
		{-200000, 320000, "G2", 43}, // Outset Island
		{-350000, -350000, "A1", 0},
		{49999, -50001, "C4", 17},
		{900000, 900000, "G7", 48}, // clamped, as dMap_point2Grid clamps
	} {
		s := SquareAt(c.x, c.z)
		if s.Name() != c.want || s.Grid() != c.grid {
			t.Errorf("SquareAt(%v, %v) = %s grid %d, want %s grid %d", c.x, c.z, s.Name(), s.Grid(), c.want, c.grid)
		}
	}
	if IslandName(Square{-2, 3}) != "Outset Island" || IslandName(Square{0, -2}) != "Windfall Island" || IslandName(Square{4, 0}) != "" {
		t.Error("IslandName")
	}
}

func TestParseCheckpoints(t *testing.T) {
	b := make([]byte, 4+3*chkPntLen)
	binary.BigEndian.PutUint32(b, 3)
	put := func(i int, gx, gz int8, dx, dz int16) {
		e := b[4+i*chkPntLen:]
		e[0], e[1] = byte(gx), byte(gz)
		binary.BigEndian.PutUint16(e[2:], uint16(dx))
		binary.BigEndian.PutUint16(e[4:], uint16(dz))
	}
	put(0, -2, 3, 105, 19713)
	put(1, -2, 3, 0, 0) // the same square again: the first wins
	put(2, 5, 0, 0, 0)  // off the grid
	cps, err := parseCheckpoints(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != 1 || cps[0].sq != (Square{-2, 3}) || cps[0].dx != 105 || cps[0].dz != 19713 {
		t.Errorf("got %+v", cps)
	}
	if _, err := parseCheckpoints(b[:10]); err == nil {
		t.Error("short file parsed")
	}
}

func TestWhere(t *testing.T) {
	c := &Chart{Stages: map[string]Square{"Omasao": {-2, 3}, "figureA": {2, 2}}}
	if sq, place, ok := c.Where("M_NewD2"); !ok || sq != (Square{2, -2}) || place != "Dragon Roost Cavern" {
		t.Errorf("M_NewD2: %v %q %v", sq, place, ok)
	}
	if sq, place, ok := c.Where("Omasao"); !ok || sq != (Square{-2, 3}) || place != "" {
		t.Errorf("Omasao: %v %q %v", sq, place, ok)
	}
	if sq, _, _ := c.Where("figureA"); sq != (Square{0, -2}) {
		t.Errorf("figureA: the gallery is on Windfall, got %v", sq)
	}
	if _, _, ok := c.Where("Nowhere"); ok {
		t.Error("an unknown stage was placed")
	}
	var nilChart *Chart
	if _, _, ok := nilChart.Where("kindan"); !ok {
		t.Error("a known stage needs no chart")
	}
	if !IsSea("sea") || !IsSea("SEA_T") || IsSea("LinkRM") {
		t.Error("IsSea")
	}
}
