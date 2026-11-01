//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	WM_TIMER = 0x0113
	WM_INPUT = 0x00FF

	RIDEV_INPUTSINK  = 0x00000100
	RID_INPUT        = 0x10000003
	RIDI_DEVICENAME  = 0x20000007
	RIM_TYPEKEYBOARD = 1

	RI_KEY_BREAK = 1
	RI_KEY_E0    = 2

	VK_NUMLOCK = 0x90
)

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type msg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       [2]int32
	LPrivate uint32
}

type rawInputDevice struct {
	UsagePage uint16
	Usage     uint16
	Flags     uint32
	Target    uintptr
}

type keyEvent struct {
	Device   string
	MakeCode uint16
	E0       bool
	Up       bool
	VKey     uint16
	NumLock  bool
}

var (
	devNames = map[uintptr]string{}
	onKey    func(keyEvent)
)

func rawDeviceName(h uintptr) string {
	if n, ok := devNames[h]; ok {
		return n
	}
	var size uint32
	pGetRawInputDeviceInfoW.Call(h, RIDI_DEVICENAME, 0, ptr(&size))
	name := ""
	if size > 0 && size < 2048 {
		buf := make([]uint16, size+1)
		if r, _, _ := pGetRawInputDeviceInfoW.Call(h, RIDI_DEVICENAME, uintptr(unsafe.Pointer(&buf[0])), ptr(&size)); int32(r) > 0 {
			name = syscall.UTF16ToString(buf)
		}
	}
	devNames[h] = name
	return name
}

func keyName(makeCode uint16, e0 bool) string {
	lp := uintptr(makeCode) << 16
	if e0 {
		lp |= 1 << 24
	}
	buf := make([]uint16, 64)
	if r, _, _ := pGetKeyNameTextW.Call(lp, uintptr(unsafe.Pointer(&buf[0])), 64); r > 0 {
		return syscall.UTF16ToString(buf)
	}
	return "?"
}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case WM_INPUT:
		var size uint32
		pGetRawInputData.Call(lp, RID_INPUT, 0, ptr(&size), 24)
		if size == 0 || size > 1024 {
			break
		}
		buf := make([]byte, size)
		if r, _, _ := pGetRawInputData.Call(lp, RID_INPUT, uintptr(unsafe.Pointer(&buf[0])), ptr(&size), 24); int32(r) <= 0 {
			break
		}
		if *(*uint32)(unsafe.Pointer(&buf[0])) != RIM_TYPEKEYBOARD || len(buf) < 24+12 {
			break
		}
		hDev := *(*uintptr)(unsafe.Pointer(&buf[8]))
		mk := *(*uint16)(unsafe.Pointer(&buf[24]))
		fl := *(*uint16)(unsafe.Pointer(&buf[26]))
		vk := *(*uint16)(unsafe.Pointer(&buf[30]))
		ks, _, _ := pGetKeyState.Call(VK_NUMLOCK)
		if onKey != nil {
			onKey(keyEvent{
				Device:   rawDeviceName(hDev),
				MakeCode: mk,
				E0:       fl&RI_KEY_E0 != 0,
				Up:       fl&RI_KEY_BREAK != 0,
				VKey:     vk,
				NumLock:  ks&1 != 0,
			})
		}
	case WM_TIMER:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

// listenKeys receives keyboard raw input from all keyboards for seconds
// seconds. It only observes; it never blocks or changes keys.
func listenKeys(seconds int, cb func(keyEvent)) error {
	onKey = cb
	hinst, _, _ := pGetModuleHnd.Call(0)
	cls := u16("NagaProbeRawInput")
	wc := wndClassEx{
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hinst,
		LpszClassName: cls,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(ptr(&wc)); r == 0 {
		return err
	}
	const HWND_MESSAGE = ^uintptr(2) // (HWND)-3
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, HWND_MESSAGE, 0, hinst, 0)
	if hwnd == 0 {
		return err
	}
	rid := rawInputDevice{UsagePage: 0x01, Usage: 0x06, Flags: RIDEV_INPUTSINK, Target: hwnd}
	if r, _, err := pRegisterRawInputDevices.Call(ptr(&rid), 1, unsafe.Sizeof(rid)); r == 0 {
		return err
	}
	pSetTimer.Call(hwnd, 1, uintptr(seconds*1000), 0)
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(ptr(&m))
		pDispatchMessageW.Call(ptr(&m))
	}
	return nil
}

func isRazer(path string) bool {
	return strings.Contains(strings.ToUpper(path), "VID_1532")
}
