//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// The low-level mouse hook runs on its own thread with its own message loop,
// so a busy UI thread can never delay mouse input. The callback only makes
// quick decisions; actions go to the engine through a channel.

type mouseHook struct {
	threadID uint32
	base     time.Time
	filter   *chatterFilter
	remap    [3]atomic.Bool // index 1 = button 4 (XBUTTON1), 2 = button 5
	swallow  [3]bool        // decision made at press time, reused for the release
	events   chan KeyEvent

	recMu  sync.Mutex
	rec    bool
	recT0  time.Duration
	recEvs []clickEvent

	alive atomic.Bool

	// left button state for drag detection (hook thread only)
	physDown   bool
	downPt     POINT
	dragMarked bool

	// key combination capture for the settings UI (state on the hook thread)
	capActive  atomic.Bool
	capResult  chan string
	capMods    [4]bool // Ctrl, Shift, Alt, Win
	capKey     bool    // a non-modifier key was pressed
	capBlocked map[uint32]bool
	kbdHook    uintptr

	dbg chan string // left-button trace for the debug log
}

var hook *mouseHook

const (
	msgKbdInstall  = WM_APP + 20
	msgKbdCancel   = WM_APP + 21
	WM_MOUSEMOVE   = 0x0200
	WH_KEYBOARD_LL = 13
	LLKHF_EXTENDED = 0x01
	LLKHF_INJECTED = 0x10
	LLKHF_UP       = 0x80
)

type KBDLLHOOKSTRUCT struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

func newMouseHook() *mouseHook {
	h := &mouseHook{base: time.Now(), events: make(chan KeyEvent, 64), dbg: make(chan string, 512), capBlocked: map[uint32]bool{}}
	go func() {
		for s := range h.dbg {
			logf("%s", s)
		}
	}()
	h.filter = newChatterFilter(sendLeftUp, func(d time.Duration, f func()) { time.AfterFunc(d, f) })
	return h
}

func (h *mouseHook) proc(code int, wp, lp uintptr) uintptr {
	if code == 0 {
		m := (*MSLLHOOKSTRUCT)(unsafe.Pointer(lp))
		if m.Flags&LLMHF_INJECTED == 0 {
			if h.handle(wp, m) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}

// handle returns true to swallow the event.
func (h *mouseHook) handle(wp uintptr, m *MSLLHOOKSTRUCT) bool {
	now := time.Since(h.base)
	switch wp {
	case WM_LBUTTONDOWN, WM_LBUTTONUP_HOOK:
		down := wp == WM_LBUTTONDOWN
		h.recMu.Lock()
		rec := h.rec
		if rec {
			h.recEvs = append(h.recEvs, clickEvent{T: now - h.recT0, Down: down})
		}
		h.recMu.Unlock()
		if rec {
			return false // calibration needs the raw clicks, unfiltered
		}
		var pass bool
		if down {
			pass = h.filter.down(now)
			h.physDown = true
			if pass {
				h.downPt, h.dragMarked = m.Pt, false
			}
		} else {
			pass = h.filter.up(now)
			h.physDown = false
		}
		if logEnabled {
			what := map[bool]string{true: "down", false: "up"}[down]
			verdict := "passed"
			if !pass {
				verdict = map[bool]string{true: "merged (bounce)", false: "held back"}[down]
			}
			select {
			case h.dbg <- fmt.Sprintf("L %s %.1f ms: %s, drag=%v", what, ms(now), verdict, h.dragMarked):
			default:
			}
		}
		return !pass
	case WM_MOUSEMOVE:
		if h.physDown && !h.dragMarked {
			dx, dy := m.Pt.X-h.downPt.X, m.Pt.Y-h.downPt.Y
			if dx*dx+dy*dy > 16 { // moved more than 4 px with the button held
				h.dragMarked = true
				h.filter.setDragging()
			}
		}
		return false
	case WM_XBUTTONDOWN, WM_XBUTTONUP:
		b := int(m.MouseData >> 16) // 1 = XBUTTON1 (button 4), 2 = XBUTTON2 (button 5)
		if b != 1 && b != 2 {
			return false
		}
		down := wp == WM_XBUTTONDOWN
		if down {
			h.swallow[b] = h.remap[b].Load()
		}
		if !h.swallow[b] {
			return false
		}
		select {
		case h.events <- KeyEvent{Slot: mouseSlots[b-1], Down: down}:
		default:
		}
		if !down {
			h.swallow[b] = false
		}
		return true
	}
	return false
}

func (h *mouseHook) startRecording() {
	h.recMu.Lock()
	h.rec = true
	h.recT0 = time.Since(h.base)
	h.recEvs = nil
	h.recMu.Unlock()
	h.filter.flush()
}

// stopRecording returns the clicks and how long the recording ran.
func (h *mouseHook) stopRecording() ([]clickEvent, time.Duration) {
	h.recMu.Lock()
	defer h.recMu.Unlock()
	h.rec = false
	ev := h.recEvs
	h.recEvs = nil
	return ev, time.Since(h.base) - h.recT0
}

func (h *mouseHook) recordingCount() int {
	h.recMu.Lock()
	defer h.recMu.Unlock()
	n := 0
	for _, e := range h.recEvs {
		if e.Down {
			n++
		}
	}
	return n
}

// run installs the hook and pumps messages until stop().
func (h *mouseHook) run() {
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid, _, _ := pGetCurrentThreadId.Call()
		h.threadID = uint32(tid)
		cb := syscall.NewCallback(func(code int, wp, lp uintptr) uintptr { return h.proc(code, wp, lp) })
		hh, _, err := pSetWindowsHookExW.Call(WH_MOUSE_LL, cb, hinst, 0)
		if hh == 0 {
			logf("mouse hook: %v", err)
			close(ready)
			return
		}
		h.alive.Store(true)
		close(ready)
		kcb := syscall.NewCallback(func(code int, wp, lp uintptr) uintptr { return h.kbdProc(code, wp, lp) })
		var m MSG
		for {
			r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			if m.Hwnd == 0 && (m.Message == msgKbdInstall || m.Message == msgKbdCancel) {
				h.capControl(m.Message, kcb)
				continue
			}
			pTranslateMessage.Call(ptr(&m))
			pDispatchMessageW.Call(ptr(&m))
		}
		pUnhookWindowsHookEx.Call(hh)
		if h.kbdHook != 0 {
			pUnhookWindowsHookEx.Call(h.kbdHook)
		}
		h.alive.Store(false)
	}()
	<-ready
}

func (h *mouseHook) stop() {
	h.filter.flush()
	if h.threadID != 0 {
		pPostThreadMessageW.Call(uintptr(h.threadID), 0x0012, 0, 0) // WM_QUIT
	}
}

// ---------------------------------------------------------------------------
// Key combination capture
//
// The settings window cannot see combinations such as Ctrl+Win+Right:
// Windows acts on them before any window does. While capturing, a low-level
// keyboard hook takes every key press for itself (so Windows does not switch
// desktops), builds the combination and stays installed until the blocked
// keys have been released, so no key is left half-pressed.

func (h *mouseHook) capControl(msg uint32, kcb uintptr) {
	switch msg {
	case msgKbdInstall:
		h.capMods, h.capKey = [4]bool{}, false
		if h.kbdHook == 0 {
			hk, _, _ := pSetWindowsHookExW.Call(WH_KEYBOARD_LL, kcb, hinst, 0)
			if hk == 0 {
				h.capFinish("")
				return
			}
			h.kbdHook = hk
		}
	case msgKbdCancel:
		h.capFinish("")
	}
}

func (h *mouseHook) capFinish(combo string) {
	if h.capActive.Swap(false) {
		select {
		case h.capResult <- combo:
		default:
		}
	}
	h.capMaybeRemove()
}

func (h *mouseHook) capMaybeRemove() {
	if !h.capActive.Load() && len(h.capBlocked) == 0 && h.kbdHook != 0 {
		pUnhookWindowsHookEx.Call(h.kbdHook)
		h.kbdHook = 0
	}
}

func modIndex(vk uint32) int {
	switch vk {
	case 0x11, 0xA2, 0xA3:
		return 0
	case 0x10, 0xA0, 0xA1:
		return 1
	case 0x12, 0xA4, 0xA5:
		return 2
	case 0x5B, 0x5C:
		return 3
	}
	return -1
}

func (h *mouseHook) capCombo(key string) string {
	var parts []string
	for i, on := range h.capMods {
		if on {
			parts = append(parts, modOrder[i])
		}
	}
	if key != "" {
		parts = append(parts, key)
	}
	c, err := parseCombo(strings.Join(parts, "+"))
	if err != nil {
		return ""
	}
	return c.String()
}

func (h *mouseHook) kbdProc(code int, wp, lp uintptr) uintptr {
	if code == 0 {
		k := (*KBDLLHOOKSTRUCT)(unsafe.Pointer(lp))
		if k.Flags&LLKHF_INJECTED == 0 {
			vk := k.VkCode
			if k.Flags&LLKHF_UP != 0 {
				if h.capBlocked[vk] {
					delete(h.capBlocked, vk)
					// releasing a modifier with no other key: the combination is the modifiers
					if h.capActive.Load() && !h.capKey && modIndex(vk) >= 0 {
						h.capFinish(h.capCombo(""))
					}
					h.capMaybeRemove()
					return 1
				}
			} else if h.capActive.Load() {
				h.capBlocked[vk] = true
				if i := modIndex(vk); i >= 0 {
					h.capMods[i] = true
					return 1
				}
				if vk == 0x1B && h.capMods == [4]bool{} { // Esc alone cancels
					h.capFinish("")
					return 1
				}
				name := ""
				if vk == 0x0D && k.Flags&LLKHF_EXTENDED != 0 {
					name = "NumEnter"
				} else if kd, ok := keyByVK[uint16(vk)]; ok && !kd.Mod {
					name = kd.Name
				}
				if name != "" {
					h.capKey = true
					h.capFinish(h.capCombo(name))
				}
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}

// captureCombo waits for the user to press a combination (up to timeout).
// Returns "" if cancelled or timed out.
func (h *mouseHook) captureCombo(timeout time.Duration) (string, error) {
	if !h.alive.Load() {
		return "", errors.New(T("хук не работает", "the hook is not running"))
	}
	if !h.capActive.CompareAndSwap(false, true) {
		return "", errors.New(T("запись уже идёт", "already recording"))
	}
	ch := make(chan string, 1)
	h.capResult = ch
	pPostThreadMessageW.Call(uintptr(h.threadID), msgKbdInstall, 0, 0)
	select {
	case s := <-ch:
		return s, nil
	case <-time.After(timeout):
		pPostThreadMessageW.Call(uintptr(h.threadID), msgKbdCancel, 0, 0)
		return "", nil
	}
}

// ---------------------------------------------------------------------------
// Foreground window → exe name

var fgCallback uintptr

func exeOfWindow(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, ptr(&pid))
	if pid == 0 {
		return ""
	}
	h, _, _ := pOpenProcess.Call(PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer pCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := pQueryFullProcessImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), ptr(&n)); r == 0 {
		return ""
	}
	return normalizeExe(syscall.UTF16ToString(buf[:n]))
}

func foregroundExe() string {
	w, _, _ := pGetForegroundWindow.Call()
	return exeOfWindow(w)
}

// watchForeground must be called on a thread with a message loop.
func watchForeground(on func(exe string)) {
	fgCallback = syscall.NewCallback(func(hook, event, hwnd, idObject, idChild, thread, t uintptr) uintptr {
		on(exeOfWindow(hwnd))
		return 0
	})
	pSetWinEventHook.Call(EVENT_SYSTEM_FOREGROUND, EVENT_SYSTEM_FOREGROUND, 0, fgCallback, 0, 0, WINEVENT_OUTOFCONTEXT)
}
