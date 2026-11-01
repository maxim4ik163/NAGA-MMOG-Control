//go:build windows

package main

import (
	"math"
	"unsafe"
)

// canvas is a top-down 32bpp DIB section. Pixels are stored as 0xAARRGGBB
// (BGRA in memory). Drawing helpers work on straight (non-premultiplied)
// colour; alpha is finalised separately.
type canvas struct {
	W, H int
	DC   uintptr
	BMP  uintptr
	old  uintptr
	Pix  []uint32
	// TransparentBase: canvas starts fully transparent (icons). Painting on a
	// pixel with no coverage yet takes the colour directly instead of mixing
	// with black, which avoids dark fringes with straight alpha.
	TransparentBase bool
}

func newCanvas(w, h int) *canvas {
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	bi := BITMAPINFOHEADER{
		BiWidth: int32(w), BiHeight: -int32(h), BiPlanes: 1, BiBitCount: 32,
	}
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, ptr(&bi), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		pDeleteDC.Call(dc)
		return nil
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	return &canvas{W: w, H: h, DC: dc, BMP: bmp, old: old, Pix: unsafe.Slice((*uint32)(bits), w*h)}
}

func (c *canvas) free() {
	if c == nil {
		return
	}
	pSelectObject.Call(c.DC, c.old)
	pDeleteObject.Call(c.BMP)
	pDeleteDC.Call(c.DC)
}

func (c *canvas) fill(col uint32) {
	v := col & 0xFFFFFF
	for i := range c.Pix {
		c.Pix[i] = v
	}
}

func lerpRGB(a, b uint32, t float64) uint32 {
	if t <= 0 {
		return a & 0xFFFFFF
	}
	if t >= 1 {
		return b & 0xFFFFFF
	}
	ch := func(s uint) uint32 {
		x := float64((a>>s)&0xFF)*(1-t) + float64((b>>s)&0xFF)*t
		return uint32(x+0.5) << s
	}
	return ch(16) | ch(8) | ch(0)
}

// roundRectCoverage returns the antialiased coverage (0..1) of pixel centre
// (px,py) by a rounded rectangle [x0,x1)x[y0,y1) with corner radius r.
func roundRectCoverage(px, py, x0, y0, x1, y1, r float64) float64 {
	cx := (x0 + x1) / 2
	cy := (y0 + y1) / 2
	hw := (x1-x0)/2 - r
	hh := (y1-y0)/2 - r
	dx := math.Abs(px-cx) - hw
	dy := math.Abs(py-cy) - hh
	ox := math.Max(dx, 0)
	oy := math.Max(dy, 0)
	dist := math.Hypot(ox, oy) + math.Min(math.Max(dx, dy), 0) - r
	return clampF(0.5-dist, 0, 1)
}

func circleCoverage(px, py, cx, cy, r float64) float64 {
	return clampF(r-math.Hypot(px-cx, py-cy)+0.5, 0, 1)
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// blendShape paints colour col over the canvas with per-pixel coverage given by fn,
// also recording the coverage into fg (foreground opacity mask) if non-nil.
func (c *canvas) blendShape(x0, y0, x1, y1 int, col uint32, fg []float64, fn func(px, py float64) float64) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > c.W {
		x1 = c.W
	}
	if y1 > c.H {
		y1 = c.H
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			t := fn(float64(x)+0.5, float64(y)+0.5)
			if t <= 0 {
				continue
			}
			i := y*c.W + x
			if c.TransparentBase && fg != nil && fg[i] == 0 {
				c.Pix[i] = col & 0xFFFFFF
			} else {
				c.Pix[i] = lerpRGB(c.Pix[i], col, t)
			}
			if fg != nil && t > fg[i] {
				fg[i] = t
			}
		}
	}
}

func createFont(name string, px int, bold bool) uintptr {
	w := FW_NORMAL
	if bold {
		w = FW_BOLD
	}
	f, _, _ := pCreateFontW.Call(uintptr(-int32(px)), 0, 0, 0, uintptr(w), 0, 0, 0,
		DEFAULT_CHARSET, 0, 0, ANTIALIASED_QUALITY, 0, uintptr(unsafe.Pointer(u16(name))))
	return f
}

// drawText renders text centred in rc with GDI. Returns false if nothing drawn.
func (c *canvas) drawText(s string, font uintptr, col uint32, rc RECT) {
	if s == "" {
		return
	}
	old, _, _ := pSelectObject.Call(c.DC, font)
	pSetBkMode.Call(c.DC, TRANSPARENT_BK)
	pSetTextColor.Call(c.DC, uintptr(rgb(uint8(col>>16), uint8(col>>8), uint8(col))))
	w, _ := syscallUTF16(s)
	pDrawTextW.Call(c.DC, uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)-1), ptr(&rc), DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_NOPREFIX)
	pGdiFlush.Call()
	pSelectObject.Call(c.DC, old)
}

func textSize(dc uintptr, s string, font uintptr) SIZE {
	var sz SIZE
	if s == "" {
		return sz
	}
	old, _, _ := pSelectObject.Call(dc, font)
	w, _ := syscallUTF16(s)
	pGetTextExtentPoint32W.Call(dc, uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)-1), ptr(&sz))
	pSelectObject.Call(dc, old)
	return sz
}
