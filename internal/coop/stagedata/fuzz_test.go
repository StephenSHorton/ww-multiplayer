package stagedata

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// Native fuzz targets for the stage readers, which parse bytes off the
// user's disc. The seed corpora (built in code: no game data) run as
// ordinary tests under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzParseArchive -fuzztime=30s ./internal/coop/stagedata/

func FuzzDecodeYaz0(f *testing.F) {
	for _, in := range yaz0Samples() {
		if len(in) <= 0x1000 {
			f.Add(stagetest.Yaz0(in))
		}
	}
	for _, seed := range [][]byte{
		yaz0Stream(8, lit('a'), lit('b'), ref(2, 6)),
		yaz0Stream(274, lit('z'), ref(1, 273)),
		yaz0Stream(5, lit('a'), lit('b'), ref(3, 3)),
		yaz0Stream(MaxYaz0Size+1, lit(0)),
		yaz0Stream(0xFFFFFFFF),
		nil, []byte("Yaz0"), []byte("Yay0\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x80A"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		out, err := DecodeYaz0(src)
		if err != nil {
			return
		}
		if want := binary.BigEndian.Uint32(src[4:]); uint32(len(out)) != want {
			t.Fatalf("decoded %d bytes, header declares %d", len(out), want)
		}
		if len(out) > (len(src)-yaz0HeaderSize)*yaz0MaxRatio {
			t.Fatalf("%d bytes from a %d-byte stream", len(out), len(src))
		}
		// What decodes re-encodes and decodes back to itself.
		again, err := DecodeYaz0(stagetest.Yaz0(out))
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("round trip of the output: %d bytes, %v", len(again), err)
		}
	})
}

// FuzzYaz0Encoder checks stagetest.Yaz0, which every later fixture is
// built with, against the decoder.
func FuzzYaz0Encoder(f *testing.F) {
	for _, in := range yaz0Samples() {
		f.Add(in)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeYaz0(stagetest.Yaz0(in))
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("round trip of %d bytes = %d bytes, %v", len(in), len(got), err)
		}
	})
}

func FuzzParseArchive(f *testing.F) {
	stage := stagetest.RARC("Stage", stageFiles()...)
	for _, seed := range [][]byte{
		stage,
		stagetest.Yaz0(stage),
		stagetest.RARC("Room0", stagetest.File{Path: "dzr/room.dzr", Data: []byte("room")}, stagetest.File{Path: "dzr/Room.dzr"}),
		stagetest.RARC("a", stagetest.File{Path: "x", Data: []byte("packed packed packed"), Yaz0: true}),
		stagetest.RARC("empty"),
		stage[:0x40], stage[:len(stage)/2], nil, []byte("RARC"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		a, err := ParseArchive(b)
		if err != nil {
			return
		}
		files := a.Files()
		for i, af := range files {
			if af.Path == "" || len(af.Path) > maxArchivePath {
				t.Fatalf("file %d has path %q", af.Index, af.Path)
			}
			if i > 0 && af.Index <= files[i-1].Index {
				t.Fatalf("files out of entry order: %d after %d", af.Index, files[i-1].Index)
			}
			if i >= 64 {
				continue // bound the work per input; the listing above is cheap
			}
			// File may find a same-named entry first, but what it finds is
			// spelled like the query.
			if got, ok := a.File(af.Path); ok && !asciiEqualFold(got.Path, af.Path) {
				t.Fatalf("File(%q) found %q", af.Path, got.Path)
			}
			base := af.Path[strings.LastIndexByte(af.Path, '/')+1:]
			if got, ok := a.FindName(base); !ok || got.Index > af.Index {
				t.Fatalf("FindName(%q) = %+v, %v; want entry %d or an earlier one", base, got, ok, af.Index)
			}
			data, err := a.Read(af)
			if err == nil && !af.Compressed() && len(data) != int(af.Size) {
				t.Fatalf("Read(%q) = %d bytes, size %d", af.Path, len(data), af.Size)
			}
		}
	})
}
