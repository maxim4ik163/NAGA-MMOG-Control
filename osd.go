//go:build windows

package main

import (
	"math"
	"strings"
	"syscall"
	"unsafe"
)

const (
	osdClass     = "NagaControlOSD"
	timerOSDHold = 10
	timerOSDFade = 11
)

type osdWindow struct {
	hwnd     uintptr
	alpha    int
	fadeStep int
	fadeMs   int
}

var osd osdWindow

// osdContent describes one popup.
type osdContent struct {
	Caption string
	Value   string
	Dots    int // 0 = no dot row
	Active  int
	Accent  uint32
	Style   PopupStyle
}

func parseHex(s string, def uint32) uint32 { return parseColor(s, def) }

func parseColor(s string, def uint32) uint32 {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return def
	}
	var v uint32
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint32(c-'A') + 10
		default:
			return def
		}
	}
	return v
}

func osdWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	if msg == WM_TIMER {
		switch wp {
		case timerOSDHold:
			pKillTimer.Call(hwnd, timerOSDHold)
			if osd.fadeMs <= 0 {
				osdHide()
			} else {
				steps := osd.fadeMs / 16
				if steps < 1 {
					steps = 1
				}
				osd.fadeStep = int(math.Ceil(255.0 / float64(steps)))
				osd.alpha = 255
				pSetTimer.Call(hwnd, timerOSDFade, 16, 0)
			}
			return 0
		case timerOSDFade:
			osd.alpha -= osd.fadeStep
			if osd.alpha <= 0 {
				osdHide()
				return 0
			}
			bf := BLENDFUNCTION{AC_SRC_OVER, 0, byte(osd.alpha), AC_SRC_ALPHA}
			pUpdateLayeredWindow.Call(hwnd, 0, 0, 0, 0, 0, 0, ptr(&bf), ULW_ALPHA)
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func osdCreate(hinst uintptr) {
	wc := WNDCLASSEXW{
		LpfnWndProc:   syscall.NewCallback(osdWndProc),
		HInstance:     hinst,
		LpszClassName: u16(osdClass),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(ptr(&wc))
	h, _, _ := pCreateWindowExW.Call(
		WS_EX_LAYERED|WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_TRANSPARENT|WS_EX_NOACTIVATE,
		uintptr(unsafe.Pointer(u16(osdClass))), uintptr(unsafe.Pointer(u16("Naga Control OSD"))),
		WS_POPUP, 0, 0, 10, 10, 0, 0, hinst, 0)
	osd.hwnd = h
}

func osdHide() {
	pKillTimer.Call(osd.hwnd, timerOSDFade)
	pKillTimer.Call(osd.hwnd, timerOSDHold)
	pShowWindow.Call(osd.hwnd, SW_HIDE)
}

func monitorUnderCursor() (RECT, POINT, float64) {
	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	mon, _, _ := pMonitorFromPoint.Call(uintptr(*(*uint64)(unsafe.Pointer(&pt))), 2)
	mi := MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(mon, ptr(&mi))
	scale := 1.0
	if pGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := pGetDpiForMonitor.Call(mon, 0, ptr(&dx), ptr(&dy)); r == 0 && dx > 0 {
			scale = float64(dx) / 96.0
		}
	}
	return mi.RcWork, pt, scale
}

// osdShow must be called on the UI thread.
func osdShow(c osdContent) {
	if osd.hwnd == 0 {
		return
	}
	st := c.Style
	work, cur, s := monitorUnderCursor()
	sc := func(v int) int { return int(math.Round(float64(v) * s)) }
	W, H := sc(clamp(st.Width, 60, 2000)), sc(clamp(st.Height, 30, 2000))
	R := float64(sc(clamp(st.CornerRadius, 0, 500)))
	bg := parseHex(st.Background, 0x1E1F22)
	textCol := parseHex(st.TextColor, 0xFFFFFF)
	capCol := parseHex(st.CaptionColor, 0x9AA0A6)
	dotCol := parseHex(st.DotColor, 0x4A4D52)
	accent := c.Accent

	cv := newCanvas(W, H)
	if cv == nil {
		return
	}
	defer cv.free()
	cv.fill(bg)
	fg := make([]float64, W*H)

	font := st.Font
	if font == "" {
		font = "Segoe UI"
	}
	capFont := createFont(font, sc(clamp(st.CaptionSize, 6, 200)), false)
	valFont := createFont(font, sc(clamp(st.FontSize, 6, 300)), st.Bold)
	defer pDeleteObject.Call(capFont)
	defer pDeleteObject.Call(valFont)

	// shrink the value font if the text does not fit
	val := c.Value
	valH := int(textSize(cv.DC, val, valFont).CY)
	if w := int(textSize(cv.DC, val, valFont).CX); w > W-sc(24) {
		ns := int(float64(clamp(st.FontSize, 6, 300)) * float64(W-sc(24)) / float64(w))
		pDeleteObject.Call(valFont)
		valFont = createFont(font, sc(clamp(ns, 6, 300)), st.Bold)
		valH = int(textSize(cv.DC, val, valFont).CY)
	}
	capH := 0
	if c.Caption != "" {
		capH = int(textSize(cv.DC, c.Caption, capFont).CY)
	}
	dotR := float64(sc(4))
	dotsH := 0
	if c.Dots > 0 {
		dotsH = int(dotR*2) + sc(4)
	}
	gap := sc(4)
	total := capH + valH + dotsH
	if capH > 0 {
		total += gap
	}
	if dotsH > 0 {
		total += gap
	}
	y := (H - total) / 2
	if capH > 0 {
		cv.drawText(c.Caption, capFont, capCol, RECT{0, int32(y), int32(W), int32(y + capH)})
		y += capH + gap
	}
	cv.drawText(val, valFont, textCol, RECT{0, int32(y), int32(W), int32(y + valH)})
	y += valH + gap
	if dotsH > 0 {
		n := c.Dots
		spacing := dotR * 3.2
		if maxSp := float64(W-sc(24)) / float64(n); spacing > maxSp {
			spacing = maxSp
		}
		x0 := float64(W)/2 - spacing*float64(n-1)/2
		cy := float64(y) + float64(dotsH)/2.0
		for i := 0; i < n; i++ {
			cx := x0 + spacing*float64(i)
			col, r := dotCol, dotR*0.8
			if i == c.Active {
				col, r = accent, dotR*1.15
			} else if i < c.Active {
				col = lerpRGB(dotCol, accent, 0.45)
			}
			cv.blendShape(int(cx-r-2), int(cy-r-2), int(cx+r+3), int(cy+r+3), col, fg,
				func(px, py float64) float64 { return circleCoverage(px, py, cx, cy, r) })
		}
	}

	bgA := float64(clamp(st.Opacity, 0, 255)) / 255.0
	tr, tg, tb := float64((textCol>>16)&255), float64((textCol>>8)&255), float64(textCol&255)
	br, bgc, bb := float64((bg>>16)&255), float64((bg>>8)&255), float64(bg&255)
	den := math.Max(math.Max(math.Abs(tr-br), math.Abs(tg-bgc)), math.Abs(tb-bb))
	if den < 1 {
		den = 1
	}
	for yy := 0; yy < H; yy++ {
		for xx := 0; xx < W; xx++ {
			i := yy*W + xx
			m := roundRectCoverage(float64(xx)+0.5, float64(yy)+0.5, 0, 0, float64(W), float64(H), R)
			if m <= 0 {
				cv.Pix[i] = 0
				continue
			}
			p := cv.Pix[i]
			pr, pg, pb := float64((p>>16)&255), float64((p>>8)&255), float64(p&255)
			t := math.Max(math.Max(math.Abs(pr-br), math.Abs(pg-bgc)), math.Abs(pb-bb)) / den
			if fg[i] > t {
				t = fg[i]
			}
			t = clampF(t, 0, 1)
			a := (bgA + (1-bgA)*t) * m
			A := uint32(a*255 + 0.5)
			cv.Pix[i] = A<<24 | uint32(pr*a+0.5)<<16 | uint32(pg*a+0.5)<<8 | uint32(pb*a+0.5)
		}
	}

	ox, oy := sc(st.OffsetX), sc(st.OffsetY)
	wl, wt, wr, wb := int(work.Left), int(work.Top), int(work.Right), int(work.Bottom)
	var x, yPos int
	switch st.Position {
	case "top-center":
		x, yPos = (wl+wr-W)/2+ox, wt+oy
	case "center":
		x, yPos = (wl+wr-W)/2+ox, (wt+wb-H)/2+oy
	case "top-left":
		x, yPos = wl+ox, wt+oy
	case "top-right":
		x, yPos = wr-W-ox, wt+oy
	case "bottom-left":
		x, yPos = wl+ox, wb-H-oy
	case "bottom-right":
		x, yPos = wr-W-ox, wb-H-oy
	case "cursor":
		x, yPos = int(cur.X)+ox, int(cur.Y)+oy
		if x+W > wr {
			x = wr - W
		}
		if yPos+H > wb {
			yPos = wb - H
		}
	default:
		x, yPos = (wl+wr-W)/2+ox, wb-H-oy
	}

	pKillTimer.Call(osd.hwnd, timerOSDFade)
	pKillTimer.Call(osd.hwnd, timerOSDHold)
	screen, _, _ := pGetDC.Call(0)
	pos := POINT{int32(x), int32(yPos)}
	size := SIZE{int32(W), int32(H)}
	src := POINT{}
	bf := BLENDFUNCTION{AC_SRC_OVER, 0, 255, AC_SRC_ALPHA}
	pUpdateLayeredWindow.Call(osd.hwnd, screen, ptr(&pos), ptr(&size), cv.DC, ptr(&src), 0, ptr(&bf), ULW_ALPHA)
	pReleaseDC.Call(0, screen)
	pSetWindowPos.Call(osd.hwnd, HWND_TOPMOST, uintptr(x), uintptr(yPos), uintptr(W), uintptr(H), SWP_NOACTIVATE|SWP_SHOWWINDOW)
	osd.alpha = 255
	osd.fadeMs = clamp(st.FadeMs, 0, 10000)
	pSetTimer.Call(osd.hwnd, timerOSDHold, uintptr(clamp(st.DurationMs, 100, 60000)), 0)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
