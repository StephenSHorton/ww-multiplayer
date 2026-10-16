package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/tunic"
)

// Tunic-color diagnostics (docs/09-player-colors.md §7, PR 2). Both act on
// the env-selected Dolphin (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
//
//	tunic-probe            read-only: Link's texture table, the two
//	                       linktexS3TC headers, the measured tunic green,
//	                       and which materials sample the texture.
//	tunic-recolor <color>  rewrite the hero's-clothes texture in place
//	                       (Link #1 AND every mini-Link on this Dolphin,
//	                       they share it). Proves the recolor and Dolphin's
//	                       texture-cache pickup with no blob change.
//	tunic-recolor reset    write the original bytes back.
//
// Offsets (tww 4d78c994):
//
//	daPy_lk_c +0x0328 mpCLModelData, +0x0338 mpCurrLinktex,
//	          +0x033C mOtherLinktex (ResTIMG, the clothes not worn),
//	          +0x02A0 mNoResetFlg1 (bit 0x8 = casual clothes)
//	          (d_a_player_main.h:1996-1997, d_a_player.h:202,486)
//	J3DModelData +0x58 J3DMaterialTable: +0x04 u16 count, +0x08 J3DMaterial**,
//	          +0x18 J3DTexture*, +0x1C JUTNameTab* textures
//	JUTNameTab +0x04 ResNTAB*, +0x0C u16 count (JUTNameTab.h:20-37)
//	          (J3DModelData.h:119, J3DMaterialAttach.h:56-63)
//	J3DTexture +0x0 u16 mNum, +0x4 ResTIMG* mpRes (J3DTexture.h:35-36)
//	J3DMaterial +0x2C J3DTevBlock*; J3DTevBlock +0x08 u16 mTexNo[]
const (
	playerPtrArray0      = 0x803CA754
	daPyMpCLModelDataOff = 0x0328
	daPyMpCurrLinktexOff = 0x0338
	daPyOtherLinktexOff  = 0x033C
	daPyNoResetFlg1Off   = 0x02A0
	daPyFlg1Casual       = 0x00000008
	modelDataMatTable    = 0x58
	maxLinkTextures      = 256
)

func inMEM1(a uint32) bool { return a >= 0x80000000 && a < 0x81800000 }

// linkTextures is Link's live texture table.
type linkTextures struct {
	link, data  uint32
	resArr      uint32 // ResTIMG[] base
	matArr      uint32
	matCount    int
	names       []string
	headers     []tunic.ResTIMG
	casual      bool
	currLinktex uint32
	spare, used int
	haveLinktex bool
}

func (lt *linkTextures) hdrAddr(i int) uint32 { return lt.resArr + uint32(i)*tunic.ResTIMGSize }

func readLinkTextures(d tunicMem) (*linkTextures, error) {
	lt := &linkTextures{}
	var err error
	if lt.link, err = d.ReadU32(playerPtrArray0); err != nil || !inMEM1(lt.link) {
		return nil, errors.New("Link not loaded (PLAYER_PTR_ARRAY[0] null); load a save first")
	}
	if lt.data, err = d.ReadU32(lt.link + daPyMpCLModelDataOff); err != nil || !inMEM1(lt.data) {
		return nil, fmt.Errorf("mpCLModelData invalid (0x%08X)", lt.data)
	}
	if flg, err := d.ReadU32(lt.link + daPyNoResetFlg1Off); err == nil {
		lt.casual = flg&daPyFlg1Casual != 0
	}
	lt.currLinktex, _ = d.ReadU32(lt.link + daPyMpCurrLinktexOff)

	mt := lt.data + modelDataMatTable
	if b, err := d.ReadAbsolute(mt+0x04, 2); err == nil && len(b) == 2 {
		lt.matCount = int(binary.BigEndian.Uint16(b))
	}
	lt.matArr, _ = d.ReadU32(mt + 0x08)
	texObj, err := d.ReadU32(mt + 0x18)
	if err != nil || !inMEM1(texObj) {
		return nil, fmt.Errorf("J3DTexture* invalid (0x%08X)", texObj)
	}
	nameTab, _ := d.ReadU32(mt + 0x1C)
	nb, err := d.ReadAbsolute(texObj, 8)
	if err != nil || len(nb) != 8 {
		return nil, errors.New("cannot read J3DTexture")
	}
	num := int(binary.BigEndian.Uint16(nb[0:2]))
	lt.resArr = binary.BigEndian.Uint32(nb[4:8])
	if num == 0 || num > maxLinkTextures || !inMEM1(lt.resArr) {
		return nil, fmt.Errorf("J3DTexture looks wrong: %d textures at 0x%08X", num, lt.resArr)
	}
	raw, err := d.ReadAbsolute(lt.resArr, num*tunic.ResTIMGSize)
	if err != nil || len(raw) != num*tunic.ResTIMGSize {
		return nil, errors.New("cannot read the ResTIMG array")
	}
	for i := 0; i < num; i++ {
		h, _ := tunic.ParseResTIMG(raw[i*tunic.ResTIMGSize:])
		lt.headers = append(lt.headers, h)
	}
	lt.names = readNameTab(d, nameTab, num)
	lt.spare, lt.used, lt.haveLinktex = tunic.FindLinktex(lt.names)
	return lt, nil
}

// readNameTab reads n names through a JUTNameTab object. The material
// table's mTextureName is a JUTNameTab*, a C++ object, not the resource:
// +0x00 vtable, +0x04 const ResNTAB* mNameTable, +0x08 mpStrData,
// +0x0C u16 mNameNum (tww JUTNameTab.h:20-37). The ResNTAB it points at
// is u16 count, u16 pad, then {u16 keyCode, u16 offset} per entry, the
// ASCIIZ name at ResNTAB+offset (ResNTAB::getName, JUTNameTab.h:13-15:
// mEntries + offs - 4, mEntries being at +4).
//
// The first version read the JUTNameTab object as if it were the
// ResNTAB, so its "count" was the vtable's high half and every name was
// read from vtable-relative garbage (live 2026-10-16: 41 garbage names).
func readNameTab(d tunicMem, tab uint32, n int) []string {
	names := make([]string, n)
	if !inMEM1(tab) {
		return names
	}
	obj, err := d.ReadAbsolute(tab, 0x10)
	if err != nil || len(obj) != 0x10 {
		return names
	}
	res, cnt := parseJUTNameTab(obj)
	if !inMEM1(res) || int(cnt) < n {
		return names
	}
	hdr, err := d.ReadAbsolute(res, 4+4*n)
	if err != nil || len(hdr) != 4+4*n {
		return names
	}
	if int(binary.BigEndian.Uint16(hdr[0:2])) < n {
		return names
	}
	for i := 0; i < n; i++ {
		off := binary.BigEndian.Uint16(hdr[4+4*i+2:])
		s, _ := d.ReadAbsolute(res+uint32(off), 64)
		names[i] = cString(s)
	}
	return names
}

// parseJUTNameTab returns a JUTNameTab object's ResNTAB pointer (+0x04)
// and name count (+0x0C).
func parseJUTNameTab(obj []byte) (res uint32, count uint16) {
	if len(obj) < 0x0E {
		return 0, 0
	}
	return binary.BigEndian.Uint32(obj[0x04:]), binary.BigEndian.Uint16(obj[0x0C:])
}

func cString(b []byte) string {
	if j := strings.IndexByte(string(b), 0); j >= 0 {
		b = b[:j]
	}
	return string(b)
}

func runTunicProbe() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	lt, err := readLinkTextures(d)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Link 0x%08X  mpCLModelData 0x%08X  casual clothes: %v  mpCurrLinktex 0x%08X\n",
		lt.link, lt.data, lt.casual, lt.currLinktex)
	fmt.Printf("%d textures, ResTIMG[] at 0x%08X\n\n", len(lt.headers), lt.resArr)
	fmt.Printf("%-4s %-20s %-10s %-4s %-9s %-4s %-10s %s\n", "idx", "name", "hdr", "fmt", "size", "mips", "image", "bytes")
	for i, h := range lt.headers {
		n := "?"
		if sz, ok := h.ImageSize(); ok {
			n = fmt.Sprint(sz)
		}
		mark := ""
		switch {
		case lt.haveLinktex && i == lt.spare:
			mark = "  <- linktexS3TC spare (hero, never swapped)"
		case lt.haveLinktex && i == lt.used:
			mark = "  <- linktexS3TC used by the materials"
		}
		fmt.Printf("[%2d] %-20s 0x%08X 0x%02X %4dx%-4d %-4d 0x%08X %s%s\n",
			i, truncateName(lt.names[i], 20), lt.hdrAddr(i), h.Format, h.Width, h.Height,
			h.MipmapCount, h.ImageAddr(lt.hdrAddr(i)), n, mark)
	}
	if !lt.haveLinktex {
		fmt.Println("\nlinktexS3TC not found twice: the recolor plan's assumption is wrong (docs/09 §2).")
		return
	}
	spare, used := lt.headers[lt.spare], lt.headers[lt.used]
	fmt.Printf("\nspare image 0x%08X, used image 0x%08X (same when wearing the hero's clothes: %v)\n",
		spare.ImageAddr(lt.hdrAddr(lt.spare)), used.ImageAddr(lt.hdrAddr(lt.used)),
		spare.ImageAddr(lt.hdrAddr(lt.spare)) == used.ImageAddr(lt.hdrAddr(lt.used)))
	fmt.Printf("mpCurrLinktex == used header: %v\n", lt.currLinktex == lt.hdrAddr(lt.used))

	if sz, ok := spare.ImageSize(); ok {
		img, err := d.ReadAbsolute(spare.ImageAddr(lt.hdrAddr(lt.spare)), sz)
		if err == nil && len(img) == sz {
			m, ref := tunic.Calibrate(img, tunic.DefaultMask)
			_, st := tunic.Recolor(img, playercolor.Palette()[1].Color, m, ref)
			fmt.Printf("tunic green: hue %.1f sat %.2f val %.2f from %d endpoints; a recolor changes %d of %d blocks\n",
				ref.H, ref.S, ref.V, ref.Samples, st.Changed, st.Blocks)
		}
	} else {
		fmt.Printf("spare header format 0x%02X is not CMPR: the recolor needs another codec (docs/09 §2)\n", spare.Format)
	}

	fmt.Println("\nmaterials sampling linktexS3TC (texNo[0..7]):")
	for i := 0; i < lt.matCount && i < 64; i++ {
		mat, err := d.ReadU32(lt.matArr + uint32(i)*4)
		if err != nil || !inMEM1(mat) {
			continue
		}
		tev, err := d.ReadU32(mat + 0x2C)
		if err != nil || !inMEM1(tev) {
			continue
		}
		tn, err := d.ReadAbsolute(tev+0x08, 16)
		if err != nil || len(tn) != 16 {
			continue
		}
		var hits []string
		for s := 0; s < 8; s++ {
			v := int(binary.BigEndian.Uint16(tn[2*s:]))
			if v == lt.spare || v == lt.used {
				hits = append(hits, fmt.Sprintf("stage %d = tex %d", s, v))
			}
		}
		if len(hits) > 0 {
			fmt.Printf("  mat[%2d] tevBlock 0x%08X: %s\n", i, tev, strings.Join(hits, ", "))
		}
	}
}

// tunicBackupPath is where tunic-recolor keeps the original image bytes,
// per Dolphin process and image address.
func tunicBackupPath(pid, img uint32) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("ww-tunic-orig-%d-%08X.bin", pid, img))
}

func runTunicRecolor(arg string) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	lt, err := readLinkTextures(d)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if !lt.haveLinktex {
		fmt.Println("linktexS3TC not found twice; run tunic-probe")
		os.Exit(1)
	}
	// In the hero's clothes the materials draw the used header's image.
	// In the casual clothes that header holds the casual texture, so take
	// the spare header, which the game never swaps (inferred to share the
	// used header's image data; tunic-probe prints both addresses).
	idx := lt.used
	if lt.casual {
		idx = lt.spare
	}
	hdr := lt.headers[idx]
	size, ok := hdr.ImageSize()
	if !ok {
		fmt.Printf("hero texture format 0x%02X is not CMPR; run tunic-probe\n", hdr.Format)
		os.Exit(1)
	}
	img := hdr.ImageAddr(lt.hdrAddr(idx))
	if !inMEM1(img) || !inMEM1(img+uint32(size)-1) {
		fmt.Printf("hero image 0x%08X+0x%X is outside MEM1\n", img, size)
		os.Exit(1)
	}

	path := tunicBackupPath(d.PID(), img)
	orig, err := os.ReadFile(path)
	if err != nil || len(orig) != size {
		orig, err = d.ReadAbsolute(img, size)
		if err != nil || len(orig) != size {
			fmt.Println("cannot read the hero texture")
			os.Exit(1)
		}
		if err := os.WriteFile(path, orig, 0o644); err != nil {
			fmt.Printf("cannot save the original texture to %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("saved the original texture to %s\n", path)
	}

	if arg == "reset" {
		if err := d.WriteAbsolute(img, orig); err != nil {
			fmt.Printf("write failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("restored %d bytes at 0x%08X\n", size, img)
		return
	}
	c, err := playercolor.Parse(arg)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	m, ref := tunic.Calibrate(orig, tunic.DefaultMask)
	out, st := tunic.Recolor(orig, c, m, ref)
	if err := d.WriteAbsolute(img, out); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("hero texture 0x%08X (%dx%d, %d bytes): %d of %d blocks recolored to %s (green measured at hue %.1f from %d endpoints)\n",
		img, hdr.Width, hdr.Height, size, st.Changed, st.Blocks, c, ref.H, ref.Samples)
	if lt.casual {
		fmt.Println("note: Link is wearing the casual clothes, so the change shows only in the hero's clothes")
	}
	fmt.Printf("undo with: tunic-recolor reset\n")
}
