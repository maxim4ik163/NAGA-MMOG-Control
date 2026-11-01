//go:build windows

package main

import "unsafe"

// trayIcon draws the side keypad: a 3×4 grid of rounded keys. Colour shows
// the state: green = all working, amber = mouse works but the keypad is not
// set up (WinUSB missing), grey = mouse not found.
func trayIcon(col uint32) uintptr {
	n, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	S := int(n)
	if S < 16 {
		S = 16
	}
	cv := newCanvas(S, S)
	if cv == nil {
		return 0
	}
	defer cv.free()
	cv.fill(0)
	cv.TransparentBase = true
	alpha := make([]float64, S*S)
	f := float64(S) / 16.0
	// 3 columns × 4 rows of keys in a 16×16 box
	kw, kh := 4.0*f, 3.0*f
	gx, gy := 1.0*f, 0.8*f
	x0 := (float64(S) - (3*kw + 2*gx)) / 2
	y0 := (float64(S) - (4*kh + 3*gy)) / 2
	for r := 0; r < 4; r++ {
		for c := 0; c < 3; c++ {
			x := x0 + float64(c)*(kw+gx)
			y := y0 + float64(r)*(kh+gy)
			cv.blendShape(0, 0, S, S, col, alpha, func(px, py float64) float64 {
				return roundRectCoverage(px, py, x, y, x+kw, y+kh, 0.9*f)
			})
		}
	}
	for i := range cv.Pix {
		a := uint32(clampF(alpha[i], 0, 1)*255 + 0.5)
		cv.Pix[i] = a<<24 | (cv.Pix[i] & 0xFFFFFF)
	}
	mask, _, _ := pCreateBitmap.Call(uintptr(S), uintptr(S), 1, 1, 0)
	defer pDeleteObject.Call(mask)
	pSelectObject.Call(cv.DC, cv.old)
	ii := ICONINFO{FIcon: 1, HbmMask: mask, HbmColor: cv.BMP}
	h, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	pSelectObject.Call(cv.DC, cv.BMP)
	return h
}
