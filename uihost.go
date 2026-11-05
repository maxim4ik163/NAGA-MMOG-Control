//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
)

// runUI is the short-lived window process (exits when the window closes).
// Exit code 3 tells the background process to fall back to the browser.
func runUI(url string) {
	setDPIAware()
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	_, _, scale := monitorUnderCursor()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(base, "NagaControl", "WebView2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  uiTitle,
			Width:  uint(1200 * scale),
			Height: uint(840 * scale),
			Center: true,
			IconId: 1,
		},
	})
	if w == nil {
		os.Exit(3)
	}
	defer w.Destroy()
	darkTitleBar(uintptr(w.Window()))
	w.SetSize(int(900*scale), int(620*scale), webview2.HintMin)
	// size, position and maximized state are remembered between openings
	trackWindowState(uintptr(w.Window()), int32(900*scale), int32(620*scale))
	// file picker for "start a program": the dialog runs on its own thread
	// (a modal loop inside a WebView2 event handler is not allowed) and is
	// owned by this window, so it opens on top of it.
	_ = w.Bind("nagaPickFile", func(start, lang string) bool {
		setLang(lang)
		go func() {
			p := pickFile(uintptr(w.Window()), start)
			b, _ := json.Marshal(p)
			w.Dispatch(func() { w.Eval("window.nagaPickResult && window.nagaPickResult(" + string(b) + ")") })
		}()
		return true
	})
	w.Navigate(url)
	w.Run()
}

var pDwmSetWindowAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

// darkTitleBar paints the window caption in the UI's dark colours regardless
// of the Windows light/dark theme. Windows 11 accepts exact colours; Windows 10
// (1809+) only supports the generic dark mode flag.
func darkTitleBar(hwnd uintptr) {
	if hwnd == 0 || pDwmSetWindowAttribute.Find() != nil {
		return
	}
	set := func(attr uintptr, v uint32) bool {
		r, _, _ := pDwmSetWindowAttribute.Call(hwnd, attr, uintptr(unsafe.Pointer(&v)), 4)
		return r == 0
	}
	const (
		dwmwaDarkModeOld = 19 // Windows 10 1809–1909
		dwmwaDarkMode    = 20 // Windows 10 2004+ / 11
		dwmwaBorderColor = 34 // Windows 11
		dwmwaCaptionCol  = 35
		dwmwaTextColor   = 36
	)
	if !set(dwmwaDarkMode, 1) {
		set(dwmwaDarkModeOld, 1)
	}
	// COLORREF is 0x00BBGGRR; same colours as the page header (--panel / --text / --line)
	set(dwmwaCaptionCol, 0x00201C1A)
	set(dwmwaTextColor, 0x00EDEAE8)
	set(dwmwaBorderColor, 0x0038322E)
	// make Windows 10 redraw the non-client area with the new mode
	const swpFlags = 0x0001 | 0x0002 | 0x0004 | 0x0010 | 0x0020 // NOSIZE|NOMOVE|NOZORDER|NOACTIVATE|FRAMECHANGED
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpFlags)
}
