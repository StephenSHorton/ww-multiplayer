package stagetest

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The DZS/DZR chunk files stagedata's dzx.go documents and cites: an s32
// chunk count, 0xC-byte chunk headers (tag, s32 entry count, u32 offset
// from the file start), then the chunks' entries.

// Chunk is one chunk of a file DZx builds.
type Chunk struct {
	Tag   string // four bytes: shorter is NUL-padded, longer is cut
	Count int    // the header's entry count
	Data  []byte // the entries, written after the chunk table
}

// Actor is one entry of an actor chunk.
type Actor struct {
	Name   string // name[8]: shorter is NUL-padded, longer is cut
	Params uint32
	Pos    [3]float32
	Angle  [3]int16
	SetID  uint16
	Scale  [3]uint8 // written in the 0x24-byte entries only
}

// Scaled reports whether the game reads tag's entries as 0x24-byte
// stage_tgsc_data_class entries, with a scale: TGSC, SCOB, DOOR, TGDR and
// the layer chunks SCO0..SCOb. Every other actor chunk has 0x20-byte
// entries.
func Scaled(tag string) bool {
	switch tag {
	case "TGSC", "SCOB", "DOOR", "TGDR":
		return true
	}
	return len(tag) == 4 && tag[:3] == "SCO"
}

// LayerTag is the tag of layer n's (0-11) chunk of kind "ACT", "SCO" or
// "TRE": "ACT0".."ACT9", "ACTa", "ACTb".
func LayerTag(kind string, n int) string {
	if n < 0 || n >= 12 {
		panic(fmt.Sprintf("stagetest: layer %d out of range", n))
	}
	return kind + "0123456789ab"[n:n+1]
}

// Actors returns an actor chunk tagged tag holding actors, in the entry
// size Scaled picks.
func Actors(tag string, actors ...Actor) Chunk {
	size := 0x20
	if Scaled(tag) {
		size = 0x24
	}
	data := make([]byte, 0, len(actors)*size)
	for _, a := range actors {
		e := make([]byte, size)
		copy(e[:8], a.Name)
		be := binary.BigEndian
		be.PutUint32(e[0x08:], a.Params)
		for i, f := range a.Pos {
			be.PutUint32(e[0x0C+4*i:], math.Float32bits(f))
		}
		for i, v := range a.Angle {
			be.PutUint16(e[0x18+2*i:], uint16(v))
		}
		be.PutUint16(e[0x1E:], a.SetID)
		if size == 0x24 {
			copy(e[0x20:], a.Scale[:])
		}
		data = append(data, e...)
	}
	return Chunk{Tag: tag, Count: len(actors), Data: data}
}

// Stag is a STAG entry, field for field (stage_stag_info_class).
type Stag struct {
	Near, Far     float32
	CameraType    uint8
	Prop          uint8 // key HUD bit 0, save table bits 1-7
	ParticleScene uint16
	TypeAndSchbit uint32 // stage type in bits 16-18
	SchbitAndFar  uint32
	Rest          [3]uint32
}

// StagInfo returns a Stag for save table saveTable (0-127) and stage type
// stageType (0-7), with the small-key HUD on or off.
func StagInfo(saveTable, stageType int, keyHUD bool) Stag {
	s := Stag{Near: 1, Far: 100000, Prop: uint8(saveTable&0x7F) << 1, TypeAndSchbit: uint32(stageType&7) << 16}
	if keyHUD {
		s.Prop |= 1
	}
	return s
}

// StagChunk returns a STAG chunk holding s.
func StagChunk(s Stag) Chunk {
	e := make([]byte, 0x20)
	be := binary.BigEndian
	be.PutUint32(e[0x00:], math.Float32bits(s.Near))
	be.PutUint32(e[0x04:], math.Float32bits(s.Far))
	e[0x08], e[0x09] = s.CameraType, s.Prop
	be.PutUint16(e[0x0A:], s.ParticleScene)
	be.PutUint32(e[0x0C:], s.TypeAndSchbit)
	be.PutUint32(e[0x10:], s.SchbitAndFar)
	for i, v := range s.Rest {
		be.PutUint32(e[0x14+4*i:], v)
	}
	return Chunk{Tag: "STAG", Count: 1, Data: e}
}

// DZx builds a DZS/DZR file of chunks, in order: the chunk table, then
// each chunk's data, 4-aligned, at the offset its header gives.
func DZx(chunks ...Chunk) []byte {
	be := binary.BigEndian
	pos := align(4+len(chunks)*0xC, 4)
	out := make([]byte, pos)
	be.PutUint32(out, uint32(len(chunks)))
	for i, c := range chunks {
		h := out[4+i*0xC:]
		tag := []byte(c.Tag + "\x00\x00\x00\x00")[:4]
		copy(h, tag)
		be.PutUint32(h[4:], uint32(int32(c.Count)))
		be.PutUint32(h[8:], uint32(pos))
		out = append(out, c.Data...)
		pos = align(len(out), 4)
		out = append(out, make([]byte, pos-len(out))...)
	}
	return out
}
