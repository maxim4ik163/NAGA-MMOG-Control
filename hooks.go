//go:build windows

package main

import (
	"runtime"
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
}

var hook *mouseHook

func newMouseHook() *mouseHook {
	h := &mouseHook{base: time.Now(), events: make(chan KeyEvent, 64)}
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
		if down {
			return !h.filter.down(now)
		}
		return !h.filter.up(now)
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
		var m MSG
		for {
			r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			pTranslateMessage.Call(ptr(&m))
			pDispatchMessageW.Call(ptr(&m))
		}
		pUnhookWindowsHookEx.Call(hh)
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
