//go:build windows

// hookprobe: answers two design questions for Naga Control.
//
//  1. When the low-level keyboard hook sees a key, has Windows already told
//     us (via Raw Input) which keyboard it came from? If yes, the app can
//     suppress keys from the Naga keypad only and leave the main keyboard
//     alone. The hook peeks for a pending WM_INPUT at that moment.
//  2. Does the left button chatter (a release and press a few ms apart)?
//
// Phase 2 blocks the "1" key (scan code 02) for 15 seconds, from every
// keyboard, to see whether Raw Input still reports a blocked key. Nothing
// else is blocked and nothing is sent to the mouse.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unsafe"
)

func init() { runtime.LockOSThread() } // hooks, window and loop on one thread

const (
	WM_INPUT       = 0x00FF
	WM_TIMER       = 0x0113
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
	WM_RBUTTONDOWN = 0x0204
	WM_RBUTTONUP   = 0x0205
	WM_MBUTTONDOWN = 0x0207
	WM_MBUTTONUP   = 0x0208
	WM_XBUTTONDOWN = 0x020B
	WM_XBUTTONUP   = 0x020C

	WH_KEYBOARD_LL = 13
	WH_MOUSE_LL    = 14

	LLKHF_EXTENDED = 0x01
	LLKHF_INJECTED = 0x10
	LLKHF_UP       = 0x80

	RIDEV_REMOVE    = 0x00000001
	RIDEV_INPUTSINK = 0x00000100
	RID_INPUT       = 0x10000003
	PM_REMOVE       = 0x0001
)

type event struct {
	t       time.Duration
	src     string // HOOK or RAW
	what    string // key 02 down / L down ...
	dev     string // RAW: device; HOOK: device of the WM_INPUT found pending, if any
	msgTime uint32
	pending string // HOOK only: what a WM_INPUT peek found at hook time
	blocked bool
}

var (
	out     io.Writer = os.Stdout
	start             = time.Now()
	events  []event
	hwnd    uintptr
	until   time.Time
	blockSC uint32 // non-zero: block this scan code in the keyboard hook
	inPeek  int
)

func logf(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

// decodeRaw reads one WM_INPUT. Returns "" for events we do not log (moves).
func decodeRaw(lp uintptr) (what, dev string) {
	var size uint32
	pGetRawInputData.Call(lp, RID_INPUT, 0, ptr(&size), 24)
	if size == 0 || size > 1024 {
		return "", ""
	}
	buf := make([]byte, size)
	if r, _, _ := pGetRawInputData.Call(lp, RID_INPUT, uintptr(unsafe.Pointer(&buf[0])), ptr(&size), 24); int32(r) <= 0 {
		return "", ""
	}
	typ := *(*uint32)(unsafe.Pointer(&buf[0]))
	dev = rawDeviceName(*(*uintptr)(unsafe.Pointer(&buf[8])))
	switch typ {
	case 1: // keyboard
		mk := *(*uint16)(unsafe.Pointer(&buf[24]))
		fl := *(*uint16)(unsafe.Pointer(&buf[26]))
		return keyWhat(uint32(mk), fl&2 != 0, fl&1 != 0), dev
	case 0: // mouse
		bf := *(*uint16)(unsafe.Pointer(&buf[28]))
		names := []struct {
			bit  uint16
			name string
		}{{0x1, "L down"}, {0x2, "L up"}, {0x4, "R down"}, {0x8, "R up"}, {0x10, "M down"}, {0x20, "M up"},
			{0x40, "X1 down"}, {0x80, "X1 up"}, {0x100, "X2 down"}, {0x200, "X2 up"}}
		for _, n := range names {
			if bf&n.bit != 0 {
				return n.name, dev
			}
		}
	}
	return "", dev
}

func keyWhat(sc uint32, e0, up bool) string {
	s := fmt.Sprintf("key %02X", sc)
	if e0 {
		s += " E0"
	}
	if up {
		return s + " up"
	}
	return s + " down"
}

func msgTime() uint32 {
	r, _, _ := pGetMessageTime.Call()
	return uint32(r)
}

// peekRaw removes pending WM_INPUT messages and logs them. It is called from
// the hooks too: that is the test — is the raw event already there?
func peekRaw() (found []event) {
	inPeek++
	defer func() { inPeek-- }()
	var m msg
	for {
		r, _, _ := pPeekMessageW.Call(ptr(&m), hwnd, WM_INPUT, WM_INPUT, PM_REMOVE)
		if r == 0 {
			return
		}
		what, dev := decodeRaw(m.LParam)
		pDefWindowProcW.Call(m.Hwnd, uintptr(m.Message), m.WParam, m.LParam) // let the system clean up
		if what == "" {
			continue
		}
		e := event{t: time.Since(start), src: "RAW", what: what, dev: dev, msgTime: m.Time}
		events = append(events, e)
		found = append(found, e)
	}
}

func describePending(found []event, what string) string {
	if len(found) == 0 {
		return "none pending"
	}
	for _, f := range found {
		if f.what == what {
			return "match from " + shortDev(f.dev)
		}
	}
	return fmt.Sprintf("%d other raw event(s)", len(found))
}

func kbdHook(code int, wp, lp uintptr) uintptr {
	if code == 0 {
		k := (*kbdLL)(unsafe.Pointer(lp))
		if k.Flags&LLKHF_INJECTED == 0 {
			what := keyWhat(k.ScanCode, k.Flags&LLKHF_EXTENDED != 0, k.Flags&LLKHF_UP != 0)
			t := time.Since(start)
			var found []event
			if inPeek == 0 {
				found = peekRaw()
			}
			e := event{t: t, src: "HOOK", what: what, msgTime: k.Time, pending: describePending(found, what)}
			for _, f := range found {
				if f.what == what {
					e.dev = f.dev
				}
			}
			e.blocked = blockSC != 0 && k.ScanCode == blockSC
			events = append(events, e)
			if e.blocked {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}

func mouseHook(code int, wp, lp uintptr) uintptr {
	if code == 0 {
		m := (*msLL)(unsafe.Pointer(lp))
		what := ""
		switch wp {
		case WM_LBUTTONDOWN:
			what = "L down"
		case WM_LBUTTONUP:
			what = "L up"
		case WM_RBUTTONDOWN:
			what = "R down"
		case WM_RBUTTONUP:
			what = "R up"
		case WM_MBUTTONDOWN:
			what = "M down"
		case WM_MBUTTONUP:
			what = "M up"
		case WM_XBUTTONDOWN, WM_XBUTTONUP:
			what = fmt.Sprintf("X%d", m.MouseData>>16)
			if wp == WM_XBUTTONDOWN {
				what += " down"
			} else {
				what += " up"
			}
		}
		if what != "" && m.Flags&1 == 0 { // skip injected
			t := time.Since(start)
			var found []event
			if inPeek == 0 {
				found = peekRaw()
			}
			e := event{t: t, src: "HOOK", what: what, msgTime: m.Time, pending: describePending(found, what)}
			for _, f := range found {
				if f.what == what {
					e.dev = f.dev
				}
			}
			events = append(events, e)
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}

func wndProc(h, m, wp, lp uintptr) uintptr {
	switch m {
	case WM_INPUT:
		what, dev := decodeRaw(lp)
		if what != "" {
			events = append(events, event{t: time.Since(start), src: "RAW", what: what, dev: dev, msgTime: msgTime()})
		}
	case WM_TIMER:
		return 0 // only wakes the loop so it can check the deadline
	}
	r, _, _ := pDefWindowProcW.Call(h, m, wp, lp)
	return r
}

var (
	kbdCB   = syscallCallback(kbdHook)
	mouseCB = syscallCallback(mouseHook)
)

func setupWindow() error {
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cls := u16("NagaHookProbe")
	wc := wndClassEx{LpfnWndProc: syscallCallback(wndProc), HInstance: hinst, LpszClassName: cls}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(ptr(&wc)); r == 0 {
		return err
	}
	h, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, hinst, 0)
	if h == 0 {
		return err
	}
	hwnd = h
	return nil
}

func registerRaw(on bool) {
	flags, target := uint32(RIDEV_INPUTSINK), hwnd
	if !on {
		flags, target = RIDEV_REMOVE, 0
	}
	rid := [2]rawInputDevice{{0x01, 0x06, flags, target}, {0x01, 0x02, flags, target}}
	pRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&rid[0])), 2, unsafe.Sizeof(rid[0]))
}

// phase installs the hooks, runs the message loop for secs seconds and
// removes the hooks again (so the console prompts never stall input).
func phase(secs int) []event {
	events = nil
	registerRaw(true)
	hinst, _, _ := pGetModuleHandleW.Call(0)
	hk, _, _ := pSetWindowsHookExW.Call(WH_KEYBOARD_LL, kbdCB, hinst, 0)
	hm, _, _ := pSetWindowsHookExW.Call(WH_MOUSE_LL, mouseCB, hinst, 0)
	if hk == 0 || hm == 0 {
		logf("  could not install the hooks")
	}
	until = time.Now().Add(time.Duration(secs) * time.Second)
	pSetTimer.Call(hwnd, 1, 200, 0)
	var m msg
	for time.Now().Before(until) {
		r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(ptr(&m))
		pDispatchMessageW.Call(ptr(&m))
	}
	pUnhookWindowsHookEx.Call(hk)
	pUnhookWindowsHookEx.Call(hm)
	registerRaw(false)
	peekRaw()
	ev := events
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].t < ev[j].t })
	return ev
}

func printTimeline(ev []event) {
	for _, e := range ev {
		line := fmt.Sprintf("  %9.3f ms  t=%d  %-4s %-14s", float64(e.t.Microseconds())/1000, e.msgTime, e.src, e.what)
		if e.src == "RAW" {
			line += "  dev=" + shortDev(e.dev)
		} else {
			line += "  at hook: " + e.pending
			if e.blocked {
				line += "  [BLOCKED]"
			}
		}
		logf("%s", line)
	}
}

// summary: for every hook event, did we know the device at hook time?
func summary(ev []event) {
	known, unknown := 0, 0
	for _, e := range ev {
		if e.src != "HOOK" {
			continue
		}
		if e.dev != "" {
			known++
		} else {
			unknown++
		}
	}
	logf("  hook events: device known at hook time %d, unknown %d", known, unknown)
}

// leftGaps returns, for every left press after the first, how long the
// button had been released before it (hook timing).
func leftGaps(ev []event) (gaps []time.Duration, holds []time.Duration) {
	var lastDown, lastUp time.Duration = -1, -1
	for _, e := range ev {
		if e.src != "HOOK" {
			continue
		}
		switch e.what {
		case "L down":
			if lastUp >= 0 {
				gaps = append(gaps, e.t-lastUp)
			}
			lastDown = e.t
		case "L up":
			if lastDown >= 0 {
				holds = append(holds, e.t-lastDown)
			}
			lastUp = e.t
		}
	}
	return
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// calibrate: in phase 3 the user clicked about once a second, so any release
// shorter than 250 ms there is chatter. In phase 4 the gap inside a real
// double click is intentional. The filter threshold must sit above the
// longest chatter and well below the shortest intentional gap.
func calibrate(single, double []event) {
	const sep = 250 * time.Millisecond
	logf("== Anti-chatter calibration")
	g1, _ := leftGaps(single)
	var chatter []time.Duration
	for _, g := range g1 {
		if g < sep {
			chatter = append(chatter, g)
		}
	}
	presses := len(g1) + 1
	if len(g1) == 0 {
		presses = 0
	}
	logf("  phase 3: %d presses reported, %d of them were chatter", presses, len(chatter))
	var maxChatter time.Duration
	for _, c := range chatter {
		if c > maxChatter {
			maxChatter = c
		}
	}
	if len(chatter) > 0 {
		sort.Slice(chatter, func(i, j int) bool { return chatter[i] < chatter[j] })
		var parts []string
		for _, c := range chatter {
			parts = append(parts, fmt.Sprintf("%.1f", ms(c)))
		}
		logf("  chatter releases, ms: %s", strings.Join(parts, " "))
	}
	g2, _ := leftGaps(double)
	var intent []time.Duration
	floor := maxChatter * 3 / 2
	if floor < 15*time.Millisecond {
		floor = 15 * time.Millisecond
	}
	for _, g := range g2 {
		if g < sep && g > floor {
			intent = append(intent, g)
		}
	}
	var minIntent time.Duration
	if len(intent) > 0 {
		sort.Slice(intent, func(i, j int) bool { return intent[i] < intent[j] })
		minIntent = intent[0]
		var parts []string
		for _, c := range intent {
			parts = append(parts, fmt.Sprintf("%.1f", ms(c)))
		}
		logf("  phase 4: intentional double-click gaps, ms: %s", strings.Join(parts, " "))
	} else {
		logf("  phase 4: no intentional double-click gaps recognised")
	}
	if len(chatter) == 0 {
		logf("  no chatter seen in phase 3; a filter would not be needed right now")
		return
	}
	th := maxChatter + maxChatter/2
	if th < maxChatter+5*time.Millisecond {
		th = maxChatter + 5*time.Millisecond
	}
	if minIntent > 0 && th > minIntent*6/10 {
		logf("  WARNING: chatter (%.1f ms) is close to your double-click gap (%.1f ms);", ms(maxChatter), ms(minIntent))
		logf("  a time-only filter may eat some double clicks")
		th = minIntent * 6 / 10
	}
	logf("  suggested threshold: ignore a press if the button was released less than %.0f ms ago", ms(th))
}

func clickStats(ev []event) {
	var lastDown, lastUp time.Duration = -1, -1
	n := 0
	for _, e := range ev {
		if e.src != "HOOK" {
			continue
		}
		switch e.what {
		case "L down":
			n++
			gap := ""
			if lastUp >= 0 {
				g := e.t - lastUp
				gap = fmt.Sprintf("released for %6.1f ms", float64(g.Microseconds())/1000)
				if g < 80*time.Millisecond {
					gap += "  <-- possible chatter"
				}
			}
			logf("  press %2d  %s", n, gap)
			lastDown = e.t
		case "L up":
			if lastDown >= 0 {
				h := e.t - lastDown
				s := fmt.Sprintf("           held for %6.1f ms", float64(h.Microseconds())/1000)
				if h < 20*time.Millisecond {
					s += "  <-- very short"
				}
				logf("%s", s)
			}
			lastUp = e.t
		}
	}
}

var stdin = bufio.NewReader(os.Stdin)

func waitEnter(prompt string) {
	flushConsoleInput()
	fmt.Print(prompt)
	stdin.ReadString('\n')
}

func main() {
	exe, _ := os.Executable()
	logPath := filepath.Join(filepath.Dir(exe), "naga-hookprobe.log")
	if f, err := os.Create(logPath); err == nil {
		defer f.Close()
		out = io.MultiWriter(f, os.Stdout) // file first: the console can pause output
	}
	disableQuickEdit()
	logf("Naga hook probe v2. %s", time.Now().Format("2006-01-02 15:04:05"))
	if err := setupWindow(); err != nil {
		logf("window: %v", err)
		return
	}

	logf("")
	logf("== Phase 1 (20 s): which keyboard? Nothing is blocked.")
	logf("Switch the Naga to 123. Press Naga side buttons 1, 2, 3, then the keys")
	logf("1, 2, 3 on your main keyboard, then a few Naga buttons again.")
	waitEnter("Press Enter to start phase 1...")
	ev := phase(20)
	printTimeline(ev)
	summary(ev)

	logf("")
	logf("== Phase 2 (15 s): the key \"1\" is BLOCKED from every keyboard.")
	logf("Press Naga side button 1 a few times and the main keyboard 1 a few times.")
	logf("Nothing will be typed; that is expected. Everything returns to normal after 15 s.")
	waitEnter("Press Enter to start phase 2...")
	blockSC = 0x02
	ev = phase(15)
	blockSC = 0
	printTimeline(ev)
	summary(ev)

	logf("")
	logf("== Phase 3 (20 s): single clicks.")
	logf("Click the left button ONCE at a time, slowly (about one click per second),")
	logf("15 times. Click on an empty spot (desktop or this window's title).")
	waitEnter("Press Enter to start phase 3...")
	single := phase(20)
	logf("  -- left button, single clicks")
	clickStats(single)

	logf("")
	logf("== Phase 4 (20 s): double clicks.")
	logf("Double-click the way you normally do, 8 times, pausing a second between them.")
	waitEnter("Press Enter to start phase 4...")
	double := phase(20)
	logf("  -- left button, double clicks")
	clickStats(double)

	logf("")
	calibrate(single, double)
	logf("")
	logf("== Raw timelines of phases 3 and 4")
	printTimeline(single)
	logf("  ----")
	printTimeline(double)

	logf("")
	fmt.Printf("Saved to %s\n", logPath)
	waitEnter("Press Enter to close.")
}
