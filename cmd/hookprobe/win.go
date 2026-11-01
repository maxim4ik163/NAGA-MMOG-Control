//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	pGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")
	pGetStdHandle            = kernel32.NewProc("GetStdHandle")
	pFlushConsoleInputBuffer = kernel32.NewProc("FlushConsoleInputBuffer")
	pGetConsoleMode          = kernel32.NewProc("GetConsoleMode")
	pSetConsoleMode          = kernel32.NewProc("SetConsoleMode")

	pRegisterClassExW        = user32.NewProc("RegisterClassExW")
	pCreateWindowExW         = user32.NewProc("CreateWindowExW")
	pDefWindowProcW          = user32.NewProc("DefWindowProcW")
	pGetMessageW             = user32.NewProc("GetMessageW")
	pTranslateMessage        = user32.NewProc("TranslateMessage")
	pDispatchMessageW        = user32.NewProc("DispatchMessageW")
	pPostQuitMessage         = user32.NewProc("PostQuitMessage")
	pSetTimer                = user32.NewProc("SetTimer")
	pRegisterRawInputDevices = user32.NewProc("RegisterRawInputDevices")
	pGetRawInputData         = user32.NewProc("GetRawInputData")
	pGetRawInputDeviceInfoW  = user32.NewProc("GetRawInputDeviceInfoW")
	pGetMessageTime          = user32.NewProc("GetMessageTime")
	pSetWindowsHookExW       = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx     = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx          = user32.NewProc("CallNextHookEx")
	pPeekMessageW            = user32.NewProc("PeekMessageW")
)

func ptr[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

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

// KBDLLHOOKSTRUCT
type kbdLL struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// MSLLHOOKSTRUCT
type msLL struct {
	Pt        [2]int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

var devNames = map[uintptr]string{}

func rawDeviceName(h uintptr) string {
	if n, ok := devNames[h]; ok {
		return n
	}
	const RIDI_DEVICENAME = 0x20000007
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

func shortDev(p string) string {
	u := strings.ToUpper(p)
	if i := strings.Index(u, "VID_"); i >= 0 {
		u = u[i:]
		if j := strings.Index(u, "#"); j >= 0 {
			u = u[:j]
		}
		return u
	}
	if u == "" {
		return "?"
	}
	if len(u) > 40 {
		u = u[len(u)-40:]
	}
	return u
}

func isNaga(dev string) bool {
	return strings.Contains(strings.ToUpper(dev), "VID_1532&PID_0015")
}

func flushConsoleInput() {
	h, _, _ := pGetStdHandle.Call(^uintptr(9)) // STD_INPUT_HANDLE
	if h != 0 && h != ^uintptr(0) {
		pFlushConsoleInputBuffer.Call(h)
	}
}

var syscallCallback = syscall.NewCallback

// disableQuickEdit turns off the console's QuickEdit mode. With it on, a
// click into the console window starts a selection and Windows pauses every
// write to the console until Enter/Esc, which looks like a hang.
func disableQuickEdit() {
	const ENABLE_QUICK_EDIT_MODE, ENABLE_EXTENDED_FLAGS = 0x40, 0x80
	h, _, _ := pGetStdHandle.Call(^uintptr(9)) // STD_INPUT_HANDLE
	var mode uint32
	if r, _, _ := pGetConsoleMode.Call(h, ptr(&mode)); r != 0 {
		pSetConsoleMode.Call(h, uintptr((mode&^ENABLE_QUICK_EDIT_MODE)|ENABLE_EXTENDED_FLAGS))
	}
}
