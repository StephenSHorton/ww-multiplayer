package savefile

// A raw card image, as Dolphin keeps a memory card in one file
// (GC/MemoryCardA.USA.raw): the card's blocks back to back. The card SDK
// lays it out (CARDCheck.c, CARDDir.c, CARDBlock.c): block 0 the card ID,
// blocks 1-2 two copies of the directory, blocks 3-4 two copies of the
// block allocation table, files from block 5 (CARD_NUM_SYSTEM_BLOCK,
// card.h:199). Of each pair the SDK uses the copy whose checksum holds,
// and of two good ones the one with the larger check code (VerifyDir and
// VerifyFAT, CARDCheck.c:67-160).

import "fmt"

const (
	cardSystemBlocks = 5                   // CARD_NUM_SYSTEM_BLOCK (card.h:199)
	cardMaxFile      = 127                 // directory entries per block (CARD_MAX_FILE, card.h:12)
	cardMinBlocks    = 8                   // CARDMount asserts 8 <= cBlock (CARDMount.c:156)
	cardBlocksPerMb  = 16                  // cBlock = size Mbit * 1024 * 1024 / 8 / 0x2000 (CARDMount.c:155)
	cardMaxBlocks    = maxRead / BlockSize // the largest card, 128 Mbit: the block table's words index no further

	// CARDID (card.h:62-70).
	idOffDeviceID = 0x020
	idOffSize     = 0x022 // u16: the card's size in Mbit
	idOffSum      = 0x1FC // u16 checkSum, then u16 checkSumInv, over 0x000-0x1FB

	// CARDDirCheck, the directory block's last 0x40 bytes (card.h:120-126;
	// CARDGetDirCheck, card.h:233).
	dirOffCheckCode = cardMaxFile*HeaderSize + 0x3A // s16
	dirOffSum       = cardMaxFile*HeaderSize + 0x3C // u16 checkSum, u16 checkSumInv, over the block less them

	// The block allocation table: u16 words, [0] checkSum and [1]
	// checkSumInv over words 2 on, [2] the check code, [3] the free
	// count, then the next block of each block from 5, 0xFFFF the last
	// (CARD_FAT_*, card.h:186-190; __CARDAllocBlock, CARDBlock.c:94-116).
	fatOffCheckCode = 2 * 2
	fatOffFree      = 3 * 2
	fatAvail        = 0x0000
	fatLast         = 0xFFFF

	entryOffStartBlock = 0x36 // CARDDir.startBlock
)

// ParseCard finds the gczelda file of GZLE01 in a raw card image and
// returns it as a GCI would hold it: the directory entry and the file's
// blocks, in the order the allocation table chains them (CARDRead.c:
// 46-54, 89).
func ParseCard(b []byte) (*File, error) {
	if len(b)%BlockSize != 0 || len(b) < cardMinBlocks*BlockSize {
		return nil, formatErr("a raw card is a whole number of 0x2000-byte blocks, at least %d; this is %d bytes", cardMinBlocks, len(b))
	}
	block := func(i int) []byte { return b[i*BlockSize : (i+1)*BlockSize] }

	id := block(0)
	if v := be16(id[idOffDeviceID:]); v != 0 {
		return nil, formatErr("card ID: device %d, not 0 (VerifyID, CARDCheck.c:41)", v)
	}
	if s, inv := cardCheckSum(id[:idOffSum]); be16(id[idOffSum:]) != s || be16(id[idOffSum+2:]) != inv {
		return nil, formatErr("card ID checksum wrong (VerifyID, CARDCheck.c:44-46)")
	}
	cBlock := int(be16(id[idOffSize:])) * cardBlocksPerMb
	if cBlock < cardMinBlocks || cBlock > cardMaxBlocks || cBlock*BlockSize > len(b) {
		return nil, formatErr("card ID says %d blocks; the image has %d", cBlock, len(b)/BlockSize)
	}

	dir, err := current(block(1), block(2), "directory", func(blk []byte) (bool, int16) {
		s, inv := cardCheckSum(blk[:dirOffSum])
		return be16(blk[dirOffSum:]) == s && be16(blk[dirOffSum+2:]) == inv, int16(be16(blk[dirOffCheckCode:]))
	})
	if err != nil {
		return nil, err
	}
	fat, err := current(block(3), block(4), "block table", func(blk []byte) (bool, int16) {
		s, inv := cardCheckSum(blk[fatOffCheckCode:])
		ok := be16(blk[0:]) == s && be16(blk[2:]) == inv
		free := 0
		for n := cardSystemBlocks; n < cBlock; n++ {
			if be16(blk[2*n:]) == fatAvail {
				free++
			}
		}
		return ok && free == int(be16(blk[fatOffFree:])), int16(be16(blk[fatOffCheckCode:]))
	})
	if err != nil {
		return nil, err
	}

	for i := range cardMaxFile {
		var h Header
		copy(h[:], dir[i*HeaderSize:])
		if h[hdrOffGameName] == 0xFF || !h.isWindWaker() { // 0xFF: a free entry (__CARDAccess, CARDOpen.c:26)
			continue
		}
		if n := h.Length(); n != FileBlocks {
			return nil, formatErr("the card's %s is %d blocks; the game's is %d", FileName, n, FileBlocks)
		}
		f := &File{Header: h}
		seen := make(map[int]bool, FileBlocks)
		blk := int(be16(h[entryOffStartBlock:]))
		for k := range FileBlocks {
			if blk < cardSystemBlocks || blk >= cBlock || seen[blk] {
				return nil, formatErr("the card's %s chain is broken at its block %d (card block %d)", FileName, k, blk)
			}
			seen[blk] = true
			copy(f.block(k), block(blk))
			blk = int(be16(fat[2*blk:]))
		}
		if blk != fatLast {
			return nil, formatErr("the card's %s chain runs past its %d blocks", FileName, FileBlocks)
		}
		return f, nil
	}
	return nil, formatErr("the card holds no %s of %s%s", FileName, GameCode, Company)
}

// current picks the copy of a system block the SDK would use: the one
// whose check passes, of two the one with the larger check code (an equal
// pair: the first).
func current(a, b []byte, what string, check func([]byte) (bool, int16)) ([]byte, error) {
	okA, codeA := check(a)
	okB, codeB := check(b)
	switch {
	case okA && okB:
		if int(codeA)-int(codeB) < 0 {
			return b, nil
		}
		return a, nil
	case okA:
		return a, nil
	case okB:
		return b, nil
	}
	return nil, fmt.Errorf("%w: both copies of the card's %s fail their checksum", ErrFormat, what)
}
