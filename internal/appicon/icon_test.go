package appicon

import (
	"encoding/binary"
	"testing"
)

func TestBuildIconIsAValidICO(t *testing.T) {
	ico := buildIcon()
	if len(ico) < 22+40 {
		t.Fatalf("icon is only %d bytes", len(ico))
	}
	if binary.LittleEndian.Uint16(ico[2:]) != 1 {
		t.Fatal("ICO type is not icon")
	}
	if binary.LittleEndian.Uint16(ico[4:]) != 1 {
		t.Fatal("expected a single image")
	}
	if ico[6] != 32 || ico[7] != 32 {
		t.Errorf("image size = %dx%d, want 32x32", ico[6], ico[7])
	}
	if binary.LittleEndian.Uint32(ico[18:]) != 22 {
		t.Errorf("DIB offset = %d, want 22", binary.LittleEndian.Uint32(ico[18:]))
	}
	if binary.LittleEndian.Uint32(ico[22:]) != 40 {
		t.Fatal("DIB header is not BITMAPINFOHEADER")
	}
}
