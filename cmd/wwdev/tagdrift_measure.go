package main

// tagdrift's frame analysis: where the remote's name tag and its body sit
// in a screenshot of the observer's game. Everything here is a pure
// function over images, so the tests drive it with synthetic frames.
//
// Three frames of the same camera are compared:
//
//   - N, no remote: mp-local is not running;
//   - F, fixed: mp-local runs and draws the remote and its tag with the
//     Widescreen Hack scale the app measures (PR #329);
//   - O, old: mp-local runs with WW_TAG_XSCALE=off, which writes scale 0
//     (1.0) into the visuals block, so the REL draws the tag exactly as
//     it did before #329 (visuals.c vis_xscale: q16 0 is 1.0).
//
// The tag is the near-white, dark-shadowed text that F has and N lacks;
// the body is the large F-N difference under it. Their x is measured from
// the game picture's centre c, and the verdict uses ratios, which cancel
// the perspective and pose offsets an absolute pixel tolerance would trip
// on: r = (tag - c) / (body - c) is 1 when the tag sits over the body.

import (
	"fmt"
	"image"
	"math"
)

// tagDriftLimits are the analyser's thresholds, in one place for the tests.
type tagDriftLimits struct {
	MinTagHits   int     // shadowed-white pixels a tag needs
	MinOffFrac   float64 // the body must sit this far from c, as a fraction of the game width
	PassRatioTol float64 // |r - 1| a fixed tag may show
}

var tagDriftDefaults = tagDriftLimits{MinTagHits: 15, MinOffFrac: 0.15, PassRatioTol: 0.10}

// tdBox is a found tag or body, in image coordinates.
type tdBox struct {
	OK     bool    `json:"ok"`
	CX     float64 `json:"cx"`
	Left   int     `json:"left"`
	Right  int     `json:"right"`
	Top    int     `json:"top"`
	Bottom int     `json:"bottom"`
	Hits   int     `json:"hits"`
}

func (b tdBox) width() int { return b.Right - b.Left + 1 }

func rgbAt(img *image.RGBA, x, y int) (r, g, b int) {
	i := img.PixOffset(x, y)
	p := img.Pix[i : i+3 : i+3]
	return int(p[0]), int(p[1]), int(p[2])
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// pixDiff is the summed absolute RGB difference of two images at (x, y).
func pixDiff(a, b *image.RGBA, x, y int) int {
	r1, g1, b1 := rgbAt(a, x, y)
	r2, g2, b2 := rgbAt(b, x, y)
	return absInt(r1-r2) + absInt(g1-g2) + absInt(b1-b2)
}

// gameArea is the game picture inside a client-area capture: the columns
// and rows where more than 2% of the pixels are not black, so Force
// 16:9's pillarbox bars (and any letterbox) fall outside it.
func gameArea(img *image.RGBA) image.Rectangle {
	bnd := img.Bounds()
	lit := func(x, y int) bool {
		r, g, b := rgbAt(img, x, y)
		return max(r, g, b) > 20
	}
	colOK := func(x int) bool {
		n := 0
		for y := bnd.Min.Y; y < bnd.Max.Y; y++ {
			if lit(x, y) {
				n++
			}
		}
		return n*50 > bnd.Dy()
	}
	rowOK := func(y int) bool {
		n := 0
		for x := bnd.Min.X; x < bnd.Max.X; x++ {
			if lit(x, y) {
				n++
			}
		}
		return n*50 > bnd.Dx()
	}
	r := bnd
	for r.Min.X < r.Max.X && !colOK(r.Min.X) {
		r.Min.X++
	}
	for r.Max.X > r.Min.X && !colOK(r.Max.X-1) {
		r.Max.X--
	}
	for r.Min.Y < r.Max.Y && !rowOK(r.Min.Y) {
		r.Min.Y++
	}
	for r.Max.Y > r.Min.Y && !rowOK(r.Max.Y-1) {
		r.Max.Y--
	}
	return r
}

// isTagPixel says whether (x, y) of f looks like the tag's text: near
// white, with a dark pixel down and to the right (the tag's black
// shadow, drawn 1.5 virtual pixels off, so 1-3 real ones), where the
// text or its shadow is new against n (white text on a pale wall differs
// little from it, but its shadow does).
func isTagPixel(f, n *image.RGBA, area image.Rectangle, x, y int) bool {
	r, g, b := rgbAt(f, x, y)
	if min(r, g, b) < 215 {
		return false
	}
	newText := pixDiff(f, n, x, y) >= 150
	for dy := 1; dy <= 3; dy++ {
		for dx := 1; dx <= 3; dx++ {
			px, py := x+dx, y+dy
			if px >= area.Max.X || py >= area.Max.Y {
				continue
			}
			if r, g, b := rgbAt(f, px, py); max(r, g, b) <= 80 && (newText || pixDiff(f, n, px, py) >= 120) {
				return true
			}
		}
	}
	return false
}

// findTag finds the name tag in f against n: the row band with the most
// tag pixels, then within it the widest run of columns with them.
func findTag(f, n *image.RGBA, area image.Rectangle, lim tagDriftLimits) tdBox {
	h, w := area.Dy(), area.Dx()
	hits := make([][]bool, h)
	rowN := make([]int, h)
	for y := 0; y < h; y++ {
		hits[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			if isTagPixel(f, n, area, area.Min.X+x, area.Min.Y+y) {
				hits[y][x] = true
				rowN[y]++
			}
		}
	}
	band := max(8, h/30)
	best, bestY := 0, -1
	sum := 0
	for y := 0; y < h; y++ {
		sum += rowN[y]
		if y >= band {
			sum -= rowN[y-band]
		}
		if sum > best {
			best, bestY = sum, y-band+1
		}
	}
	if bestY < 0 || best < lim.MinTagHits {
		return tdBox{Hits: best}
	}
	y0, y1 := max(bestY, 0), min(bestY+band, h)
	colN := make([]int, w)
	for y := y0; y < y1; y++ {
		for x := 0; x < w; x++ {
			if hits[y][x] {
				colN[x]++
			}
		}
	}
	// Runs of columns with hits, joined across gaps narrower than a
	// letter space; the run with the most hits is the tag.
	gap := max(6, w/60)
	type run struct{ l, r, n int }
	var runs []run
	cur := run{l: -1}
	last := -1
	for x := 0; x < w; x++ {
		if colN[x] == 0 {
			continue
		}
		if cur.l < 0 || x-last > gap {
			if cur.l >= 0 {
				runs = append(runs, cur)
			}
			cur = run{l: x}
		}
		cur.r, cur.n, last = x, cur.n+colN[x], x
	}
	if cur.l >= 0 {
		runs = append(runs, cur)
	}
	var top run
	for _, r := range runs {
		if r.n > top.n {
			top = r
		}
	}
	if top.n < lim.MinTagHits {
		return tdBox{Hits: top.n}
	}
	b := tdBox{OK: true, Left: area.Min.X + top.l, Right: area.Min.X + top.r, Top: -1, Hits: top.n}
	for y := y0; y < y1; y++ {
		for x := top.l; x <= top.r; x++ {
			if hits[y][x] {
				if b.Top < 0 {
					b.Top = area.Min.Y + y
				}
				b.Bottom = area.Min.Y + y
				break
			}
		}
	}
	b.CX = float64(b.Left+b.Right) / 2
	return b
}

// findBody finds the remote's body in f against n: the heaviest run of
// changed columns in the rows from the tag down to six tag heights below
// it (the tag hangs at the root joint + 90, above the head), then the
// centroid of the top quarter of that run, the head and shoulders,
// nearest the tag's anchor.
func findBody(f, n *image.RGBA, noise []bool, area image.Rectangle, tag tdBox) tdBox {
	if !tag.OK {
		return tdBox{}
	}
	const diffMin = 90
	th := max(tag.Bottom-tag.Top+1, 8)
	y0 := tag.Bottom + 2
	y1 := min(area.Max.Y, y0+8*th)
	inTag := func(x, y int) bool {
		return x >= tag.Left-4 && x <= tag.Right+4 && y >= tag.Top-4 && y <= tag.Bottom+4
	}
	changed := func(x, y int) bool {
		if inTag(x, y) || pixDiff(f, n, x, y) < diffMin {
			return false
		}
		return noise == nil || !noise[(y-area.Min.Y)*area.Dx()+(x-area.Min.X)]
	}
	w := area.Dx()
	colN := make([]int, w)
	for y := y0; y < y1; y++ {
		for x := 0; x < w; x++ {
			if changed(area.Min.X+x, y) {
				colN[x]++
			}
		}
	}
	// A body column has several changed pixels; shimmer has one or two.
	const colMin = 4
	type run struct{ l, r, n int }
	var best, cur run
	cur.l = -1
	last := -1
	flush := func() {
		if cur.l >= 0 && cur.n > best.n {
			best = cur
		}
	}
	for x := 0; x < w; x++ {
		if colN[x] < colMin {
			continue
		}
		if cur.l < 0 || x-last > 6 {
			flush()
			cur = run{l: x}
		}
		cur.r, cur.n, last = x, cur.n+colN[x], x
	}
	flush()
	if best.n == 0 {
		return tdBox{}
	}
	b := tdBox{OK: true, Left: area.Min.X + best.l, Right: area.Min.X + best.r, Top: -1, Hits: best.n}
	for y := y0; y < y1; y++ {
		for x := b.Left; x <= b.Right; x++ {
			if changed(x, y) {
				if b.Top < 0 {
					b.Top = y
				}
				b.Bottom = y
				break
			}
		}
	}
	if b.Top < 0 {
		return tdBox{}
	}
	head := b.Top + max(1, (b.Bottom-b.Top+1)/4)
	sx, k := 0.0, 0
	for y := b.Top; y <= head; y++ {
		for x := b.Left; x <= b.Right; x++ {
			if changed(x, y) {
				sx += float64(x)
				k++
			}
		}
	}
	if k == 0 {
		return tdBox{}
	}
	b.CX = sx / float64(k)
	return b
}

// noiseMask marks, over area, the pixels that change between frames
// with no remote in them (A's own Link idling, its sword's glow, water,
// grass), grown by grow pixels: the body search ignores them, so only
// what the remote adds is left. nil with fewer than two frames.
func noiseMask(ns []*image.RGBA, area image.Rectangle, grow int) []bool {
	if len(ns) < 2 {
		return nil
	}
	w, h := area.Dx(), area.Dy()
	raw := make([]bool, w*h)
	for i := 0; i < len(ns); i++ {
		for j := i + 1; j < len(ns); j++ {
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if !raw[y*w+x] && pixDiff(ns[i], ns[j], area.Min.X+x, area.Min.Y+y) >= 60 {
						raw[y*w+x] = true
					}
				}
			}
		}
	}
	// Grow: a horizontal then a vertical pass of a running window.
	tmp := make([]bool, w*h)
	for y := 0; y < h; y++ {
		last := -1 << 30
		for x := 0; x < w; x++ {
			if raw[y*w+x] {
				last = x
			}
			tmp[y*w+x] = x-last <= grow
		}
		last = 1 << 30
		for x := w - 1; x >= 0; x-- {
			if raw[y*w+x] {
				last = x
			}
			tmp[y*w+x] = tmp[y*w+x] || last-x <= grow
		}
	}
	out := make([]bool, w*h)
	for x := 0; x < w; x++ {
		last := -1 << 30
		for y := 0; y < h; y++ {
			if tmp[y*w+x] {
				last = y
			}
			out[y*w+x] = y-last <= grow
		}
		last = 1 << 30
		for y := h - 1; y >= 0; y-- {
			if tmp[y*w+x] {
				last = y
			}
			out[y*w+x] = out[y*w+x] || last-y <= grow
		}
	}
	return out
}

// tagDriftMeasure is one case's numbers.
type tagDriftMeasure struct {
	Area     [4]int  `json:"area"` // x0, y0, x1, y1 of the game picture
	Centre   float64 `json:"centre"`
	XScaleIm float64 `json:"xscale_image"` // (4/3) / the picture's aspect: the hack's scale when it is on
	TagF     tdBox   `json:"tag_fixed"`
	TagO     tdBox   `json:"tag_old,omitempty"`
	Body     tdBox   `json:"body"`
	BodyOff  float64 `json:"body_offset_frac"` // |body - c| / width
	RatioF   float64 `json:"ratio_fixed"`      // (tagF - c) / (body - c): 1 = on the body
	RatioO   float64 `json:"ratio_old,omitempty"`
	OldOverF float64 `json:"old_over_fixed,omitempty"` // (tagO - c) / (tagF - c): 1/xscale when the fix moved the tag
	WidthOF  float64 `json:"width_old_over_fixed,omitempty"`
	DyF      int     `json:"tag_body_dy_fixed"` // body top - tag bottom
}

// measureTagDrift measures one case: ns are frames with no remote (the
// first is the reference, the others only show what moves by itself), f
// and (optionally) o the fixed and old frames.
func measureTagDrift(ns []*image.RGBA, f, o *image.RGBA, lim tagDriftLimits) tagDriftMeasure {
	n := ns[0]
	area := gameArea(n)
	var m tagDriftMeasure
	m.Area = [4]int{area.Min.X, area.Min.Y, area.Max.X, area.Max.Y}
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return m
	}
	c := float64(area.Min.X+area.Max.X-1) / 2
	m.Centre = c
	m.XScaleIm = 1
	if asp := float64(area.Dx()) / float64(area.Dy()); asp > 4.0/3.0 {
		m.XScaleIm = (4.0 / 3.0) / asp
	}
	m.TagF = findTag(f, n, area, lim)
	m.Body = findBody(f, n, noiseMask(ns, area, 6), area, m.TagF)
	if m.Body.OK {
		m.BodyOff = math.Abs(m.Body.CX-c) / float64(area.Dx())
		if d := m.Body.CX - c; d != 0 && m.TagF.OK {
			m.RatioF = (m.TagF.CX - c) / d
		}
		m.DyF = m.Body.Top - m.TagF.Bottom
	}
	if o != nil {
		m.TagO = findTag(o, n, area, lim)
		if m.TagO.OK && m.Body.OK {
			if d := m.Body.CX - c; d != 0 {
				m.RatioO = (m.TagO.CX - c) / d
			}
		}
		if m.TagO.OK && m.TagF.OK {
			if d := m.TagF.CX - c; d != 0 {
				m.OldOverF = (m.TagO.CX - c) / d
			}
			m.WidthOF = float64(m.TagO.width()) / float64(m.TagF.width())
		}
	}
	return m
}

// tagDriftVerdicts turns one case's measure into checks. label names the
// case; xs is the scale the case should run at (launch.GFXAspect.XScale).
// The old frame must fail where the fix moves the tag (xs < 1) and agree
// with the fixed one where it doesn't: that is the proof the check
// catches the #1157 drift rather than passing on anything.
func tagDriftVerdicts(label string, m tagDriftMeasure, xs float64, haveOld bool, lim tagDriftLimits) (checks []smokeCheck, cannot string) {
	vals := map[string]any{"measure": m, "expected_xscale": xs}
	switch {
	case !m.TagF.OK:
		return nil, label + ": the remote's tag was not found in the fixed frame"
	case !m.Body.OK:
		return nil, label + ": the remote's body was not found under its tag"
	case m.BodyOff < lim.MinOffFrac:
		return nil, label + ": the remote is too near the picture's centre to measure drift (V4's blind spot): move it further aside"
	}
	checks = append(checks, passIf(math.Abs(m.RatioF-1) <= lim.PassRatioTol, label+" tag on body",
		fmt.Sprintf("tag at %.0f, body at %.0f, centre %.0f: ratio %.3f (pass within %.2f of 1)", m.TagF.CX, m.Body.CX, m.Centre, m.RatioF, lim.PassRatioTol), vals))
	if !haveOld {
		return checks, ""
	}
	if !m.TagO.OK {
		return checks, label + ": the remote's tag was not found in the old (WW_TAG_XSCALE=off) frame"
	}
	if xs < 0.98 {
		// The pre-#329 tag must miss the body by about 1/xs: the check
		// must fail on it, and by the predicted amount.
		oldFails := math.Abs(m.RatioO-1) > lim.PassRatioTol
		want := 1 / xs
		checks = append(checks, passIf(oldFails && math.Abs(m.OldOverF-want) <= 0.06*want, label+" old tag drifts",
			fmt.Sprintf("with WW_TAG_XSCALE=off the tag is at %.0f: ratio %.3f to the body (the check fails it: %v); old/fixed offset %.3f, predicted 1/xs = %.3f", m.TagO.CX, m.RatioO, oldFails, m.OldOverF, want), vals))
	} else {
		checks = append(checks, passIf(math.Abs(m.OldOverF-1) <= 0.03, label+" old tag unchanged",
			fmt.Sprintf("scale 1: the tag is at %.0f fixed and %.0f old (offset ratio %.3f, within 3%% of 1 wanted)", m.TagF.CX, m.TagO.CX, m.OldOverF), vals))
	}
	return checks, ""
}
