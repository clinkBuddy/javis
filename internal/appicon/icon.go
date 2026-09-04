package appicon

import (
	"encoding/binary"
)

// ICO is the 32×32 product image as a complete ICO file. The installer writes
// this next to jarvis.exe so the desktop shortcut can use it.
func ICO() []byte { return buildIcon() }

// buildIcon returns a 32×32 ICO in memory. The image is the same blue mark
// the admin UI uses, so the shortcut matches the browser tab.
func buildIcon() []byte {
	const size = 32
	pixels := make([]byte, size*size*4) // BGRA, top-down for us; BMP is bottom-up

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := (y*size + x) * 4
			if inMark(x, y, size) {
				// Accent blue #4C8DFF.
				pixels[i+0] = 0xFF
				pixels[i+1] = 0x8D
				pixels[i+2] = 0x4C
				pixels[i+3] = 0xFF
				continue
			}
			pixels[i+3] = 0x00
		}
	}
	drawJ(pixels, size)

	return encodeICO(pixels, size)
}

func inMark(x, y, size int) bool {
	m := 2
	if x < m || y < m || x >= size-m || y >= size-m {
		return false
	}
	r := 4
	cx, cy := x, y
	if cx < m+r && cy < m+r {
		return dist2(cx, cy, m+r, m+r) <= r*r
	}
	if cx >= size-m-r && cy < m+r {
		return dist2(cx, cy, size-m-r-1, m+r) <= r*r
	}
	if cx < m+r && cy >= size-m-r {
		return dist2(cx, cy, m+r, size-m-r-1) <= r*r
	}
	if cx >= size-m-r && cy >= size-m-r {
		return dist2(cx, cy, size-m-r-1, size-m-r-1) <= r*r
	}
	return true
}

func dist2(x, y, cx, cy int) int {
	dx, dy := x-cx, y-cy
	return dx*dx + dy*dy
}

func drawJ(px []byte, size int) {
	set := func(x, y int) {
		if x < 0 || y < 0 || x >= size || y >= size {
			return
		}
		i := (y*size + x) * 4
		px[i+0], px[i+1], px[i+2], px[i+3] = 0xFF, 0xFF, 0xFF, 0xFF
	}
	for x := 11; x <= 21; x++ {
		set(x, 8)
		set(x, 9)
	}
	for y := 8; y <= 21; y++ {
		set(19, y)
		set(20, y)
	}
	for x := 12; x <= 20; x++ {
		set(x, 21)
		set(x, 22)
	}
	for y := 18; y <= 21; y++ {
		set(11, y)
		set(12, y)
	}
}

func encodeICO(topDownBGRA []byte, size int) []byte {
	xorSize := size * size * 4
	andRow := (size + 31) / 32 * 4
	andSize := andRow * size
	dibSize := 40 + xorSize + andSize

	out := make([]byte, 6+16+dibSize)
	binary.LittleEndian.PutUint16(out[2:], 1) // type = icon
	binary.LittleEndian.PutUint16(out[4:], 1) // one image
	out[6] = byte(size)
	out[7] = byte(size)
	binary.LittleEndian.PutUint16(out[10:], 1)  // planes
	binary.LittleEndian.PutUint16(out[12:], 32) // bit count
	binary.LittleEndian.PutUint32(out[14:], uint32(dibSize))
	binary.LittleEndian.PutUint32(out[18:], 22) // offset of DIB

	hdr := out[22:]
	binary.LittleEndian.PutUint32(hdr[0:], 40)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(size))
	// Height is doubled: XOR bitmap plus AND mask.
	binary.LittleEndian.PutUint32(hdr[8:], uint32(size*2))
	binary.LittleEndian.PutUint16(hdr[12:], 1)
	binary.LittleEndian.PutUint16(hdr[14:], 32)
	binary.LittleEndian.PutUint32(hdr[20:], uint32(xorSize))

	xor := hdr[40:]
	for y := 0; y < size; y++ {
		src := (size - 1 - y) * size * 4
		dst := y * size * 4
		copy(xor[dst:dst+size*4], topDownBGRA[src:src+size*4])
	}
	return out
}
