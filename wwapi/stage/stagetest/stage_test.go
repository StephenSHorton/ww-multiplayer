package stagetest

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"testing"
)

func TestDiscReadFile(t *testing.T) {
	var d Disc
	d.Add("res/Stage/sea/Stage.arc", []byte("stage"))
	d.Add("res/Stage/SEA/Room1.arc", []byte("shadowed"))
	d.Add("res/Stage/sea/Room2.arc", []byte("room2"))
	d.Add("Res/x", []byte("other top"))
	for path, want := range map[string]string{
		"res/Stage/sea/Stage.arc":  "stage",
		"/RES/stage/SEA/stage.ARC": "stage",
		"res/Stage/sea/room2.arc":  "room2",
		"res/Stage/sea/Room1.arc":  "", // "sea" comes first, and has no Room1.arc
		"res/Stage/SEA/Room1.arc":  "", // the same lookup
		"res/x":                    "", // "res" comes first, and has no x
		"res/Stage/sea":            "", // a directory
		"res/Stage/sea/Stage.arc/": "",
		"":                         "",
	} {
		got, err := d.ReadFile(path)
		if want == "" {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("ReadFile(%q) = %q, %v; want fs.ErrNotExist", path, got, err)
			}
			continue
		}
		if err != nil || string(got) != want {
			t.Errorf("ReadFile(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	got, _ := d.ReadFile("res/Stage/sea/Stage.arc")
	got[0] = 'X'
	if again, _ := d.ReadFile("res/Stage/sea/Stage.arc"); string(again) != "stage" {
		t.Error("ReadFile shares its buffer")
	}
	files := d.Files()
	if len(files) != 4 || files[2].Offset != 13 || files[2].Size != 5 {
		t.Errorf("Files() = %+v", files)
	}
}

func TestDZxLayout(t *testing.T) {
	b := DZx(Chunk{Tag: "AB", Count: -2, Data: []byte{1, 2, 3}}, Chunk{Tag: "TOOLONG", Count: 1})
	be := binary.BigEndian
	if be.Uint32(b) != 2 || string(b[4:8]) != "AB\x00\x00" || int32(be.Uint32(b[8:])) != -2 || be.Uint32(b[12:]) != 28 ||
		string(b[16:20]) != "TOOL" || be.Uint32(b[24:]) != 32 || len(b) != 32 || !bytes.Equal(b[28:31], []byte{1, 2, 3}) {
		t.Errorf("DZx = % x", b)
	}
	if !Scaled("SCOb") || !Scaled("DOOR") || Scaled("ACTR") || Scaled("TRE0") || LayerTag("SCO", 10) != "SCOa" {
		t.Error("Scaled or LayerTag")
	}
	if a := Actors("TGDR", Actor{Name: "door12AB9", Scale: [3]uint8{4, 5, 6}}); a.Count != 1 || len(a.Data) != 0x24 ||
		string(a.Data[:8]) != "door12AB" || !bytes.Equal(a.Data[0x20:], []byte{4, 5, 6, 0}) {
		t.Errorf("Actors(TGDR) = %+v", a)
	}
	if s := StagInfo(130, 9, true); s.Prop != 0x05 || s.TypeAndSchbit != 1<<16 {
		t.Errorf("StagInfo masks: %+v", s)
	}
}

func TestMainDOL(t *testing.T) {
	dol := MainDOL(ObjectName{Name: "door10", Proc: 0x12E, Subtype: -1, GBAName: 5})
	be := binary.BigEndian
	dataOff, dataAddr := be.Uint32(dol[0x1C:]), be.Uint32(dol[0x64:])
	tab := dol[dataOff+ObjectNameAddr-dataAddr:]
	if string(tab[:8]) != "door10\x00\x00" || be.Uint16(tab[8:]) != 0x12E || tab[10] != 0xFF || tab[11] != 5 ||
		string(tab[12:20]) != "Pad000\x00\x00" {
		t.Errorf("table starts % x", tab[:24])
	}
	last := tab[(ObjectNameCount-1)*objectNameEntry:]
	if string(last[:8]) != "EndCode\x00" || be.Uint16(last[8:]) != 7 {
		t.Errorf("last entry % x", last[:12])
	}
	defer func() {
		if recover() == nil {
			t.Error("ObjectNameTable took 825 names")
		}
	}()
	ObjectNameTable(make([]ObjectName, ObjectNameCount)...)
}
