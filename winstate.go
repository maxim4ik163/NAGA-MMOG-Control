//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Settings window size, position and maximized state, remembered between
// openings (the window process exits on close, the app stays in the tray).
// Stored in %APPDATA%\NagaControl\window.json; written by the window process
// itself whenever a move/resize ends, the window is maximized/restored, and on
// close — so it survives the background process killing the window on exit.

type windowState struct {
	X, Y, W, H int32 // normal (restored) rectangle, workspace coordinates
	Maximized  bool
}

type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    [2]int32
	PtMaxPosition    [2]int32
	RcNormalPosition RECT
}

const (
	swShowNormal         = 1
	swShowMinimized      = 2
	swShowMaximized      = 3
	wpfRestoreToMaximize = 0x0002
	gwlpWndProc          = ^uintptr(3) // -4
	wmSize               = 0x0005
	wmClose              = 0x0010
	wmEnterSizeMove      = 0x0231
	wmExitSizeMove       = 0x0232
	monitorDefaultToNull = 0
)

var (
	pGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	pSetWindowPlacement = user32.NewProc("SetWindowPlacement")
	pSetWindowLongPtrW  = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProcW    = user32.NewProc("CallWindowProcW")
	pMonitorFromRect    = user32.NewProc("MonitorFromRect")
)

func windowStatePath() string { return filepath.Join(appDataDir(), "window.json") }

func loadWindowState() (windowState, bool) {
	var s windowState
	b, err := os.ReadFile(windowStatePath())
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, false
	}
	return s, s.W > 0 && s.H > 0
}

func getPlacement(hwnd uintptr) (windowPlacement, bool) {
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	r, _, _ := pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp, r != 0
}

// restoreWindowState applies the saved rectangle and maximized state. A
// rectangle that is no longer on any monitor (monitor unplugged, resolution
// changed) is ignored, the window stays centered at its default size.
func restoreWindowState(hwnd uintptr, minW, minH int32) {
	s, ok := loadWindowState()
	if !ok || hwnd == 0 {
		return
	}
	if s.W < minW {
		s.W = minW
	}
	if s.H < minH {
		s.H = minH
	}
	rc := RECT{s.X, s.Y, s.X + s.W, s.Y + s.H}
	// the title bar must be reachable: check the top strip of the window
	top := RECT{rc.Left + 40, rc.Top, rc.Right - 40, rc.Top + 30}
	if m, _, _ := pMonitorFromRect.Call(uintptr(unsafe.Pointer(&top)), monitorDefaultToNull); m == 0 {
		return
	}
	wp, ok := getPlacement(hwnd)
	if !ok {
		return
	}
	wp.Flags = 0
	wp.RcNormalPosition = rc
	wp.ShowCmd = swShowNormal
	if s.Maximized {
		wp.ShowCmd = swShowMaximized
	}
	pSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
}

var lastSavedState windowState

func saveWindowState(hwnd uintptr) {
	wp, ok := getPlacement(hwnd)
	if !ok {
		return
	}
	rc := wp.RcNormalPosition
	s := windowState{X: rc.Left, Y: rc.Top, W: rc.Right - rc.Left, H: rc.Bottom - rc.Top}
	switch wp.ShowCmd {
	case swShowMaximized:
		s.Maximized = true
	case swShowMinimized:
		// closed from the taskbar while minimized: keep what it would restore to
		s.Maximized = wp.Flags&wpfRestoreToMaximize != 0
	}
	if s.W <= 0 || s.H <= 0 || s == lastSavedState {
		return
	}
	lastSavedState = s
	b, _ := json.Marshal(s)
	tmp := windowStatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = os.Rename(tmp, windowStatePath())
	}
}

var (
	inSizeMove  bool // live drag/resize: save once at the end, not on every WM_SIZE
	origWndProc uintptr
	stateProc   = syscall.NewCallback(stateWndProc)
)

// stateWndProc wraps the webview's window procedure to record the window state.
func stateWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case wmEnterSizeMove:
		inSizeMove = true
	case wmClose:
		saveWindowState(hwnd)
	case wmExitSizeMove:
		inSizeMove = false
		saveWindowState(hwnd)
	case wmSize:
		if inSizeMove {
			break
		}
		// maximize / restore (also Win+Up, double-click on the caption, snap)
		if wp == 0 || wp == 2 { // SIZE_RESTORED, SIZE_MAXIMIZED
			r, _, _ := pCallWindowProcW.Call(origWndProc, hwnd, msg, wp, lp)
			saveWindowState(hwnd)
			return r
		}
	}
	r, _, _ := pCallWindowProcW.Call(origWndProc, hwnd, msg, wp, lp)
	return r
}

// trackWindowState restores the saved state and starts recording changes.
func trackWindowState(hwnd uintptr, minW, minH int32) {
	if hwnd == 0 {
		return
	}
	restoreWindowState(hwnd, minW, minH)
	if s, ok := loadWindowState(); ok {
		lastSavedState = s
	}
	origWndProc, _, _ = pSetWindowLongPtrW.Call(hwnd, gwlpWndProc, stateProc)
}
