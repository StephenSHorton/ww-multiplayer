package dolphin

import (
	"encoding/binary"
	"math"
)

func putF32BE(b []byte, v float32) {
	binary.BigEndian.PutUint32(b, math.Float32bits(v))
}

func putS16BE(b []byte, v int16) {
	binary.BigEndian.PutUint16(b, uint16(v))
}
