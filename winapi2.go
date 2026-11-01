//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	winusbDLL = syscall.NewLazyDLL("winusb.dll")
	ole32     = syscall.NewLazyDLL("ole32.dll")
	cfgmgr32  = syscall.NewLazyDLL("cfgmgr32.dll")

	pHidD_SetFeature = hid.NewProc("HidD_SetFeature")
	pHidD_GetFeature = hid.NewProc("HidD_GetFeature")

	pFindWindowW               = user32.NewProc("FindWindowW")
	pIsIconic                  = user32.NewProc("IsIconic")
	pAllowSetForeground        = user32.NewProc("AllowSetForegroundWindow")
	pSetWindowsHookExW         = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx            = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx       = user32.NewProc("UnhookWindowsHookEx")
	pSendInput                 = user32.NewProc("SendInput")
	pMapVirtualKeyW            = user32.NewProc("MapVirtualKeyW")
	pSetWinEventHook           = user32.NewProc("SetWinEventHook")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pOpenProcess               = kernel32.NewProc("OpenProcess")
	pQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	pGetCurrentThreadId        = kernel32.NewProc("GetCurrentThreadId")
	pPostThreadMessageW        = user32.NewProc("PostThreadMessageW")
	pGetUserDefaultUILanguage  = kernel32.NewProc("GetUserDefaultUILanguage")

	pSetupDiEnumDeviceInfo         = setupapi.NewProc("SetupDiEnumDeviceInfo")
	pSetupDiGetDeviceInstanceIdW   = setupapi.NewProc("SetupDiGetDeviceInstanceIdW")
	pSetupDiGetDeviceRegistryPropW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	pSetupDiOpenDevRegKey          = setupapi.NewProc("SetupDiOpenDevRegKey")
	pCLSIDFromString               = ole32.NewProc("CLSIDFromString")
	pCM_Get_DevNode_Status         = cfgmgr32.NewProc("CM_Get_DevNode_Status")
	pWinUsb_Initialize             = winusbDLL.NewProc("WinUsb_Initialize")
	pWinUsb_Free                   = winusbDLL.NewProc("WinUsb_Free")
	pWinUsb_QueryInterfaceSettings = winusbDLL.NewProc("WinUsb_QueryInterfaceSettings")
	pWinUsb_QueryPipe              = winusbDLL.NewProc("WinUsb_QueryPipe")
	pWinUsb_ReadPipe               = winusbDLL.NewProc("WinUsb_ReadPipe")
	pWinUsb_SetPipePolicy          = winusbDLL.NewProc("WinUsb_SetPipePolicy")
	pWinUsb_AbortPipe              = winusbDLL.NewProc("WinUsb_AbortPipe")
)

const (
	MF_POPUP   = 0x0010
	MF_RADIO   = 0x0200
	SW_RESTORE = 9

	WH_MOUSE_LL       = 14
	WM_LBUTTONDOWN    = 0x0201
	WM_LBUTTONUP_HOOK = 0x0202
	WM_XBUTTONDOWN    = 0x020B
	WM_XBUTTONUP      = 0x020C
	LLMHF_INJECTED    = 0x01

	INPUT_MOUSE           = 0
	INPUT_KEYBOARD        = 1
	KEYEVENTF_EXTENDEDKEY = 0x0001
	KEYEVENTF_KEYUP       = 0x0002
	MOUSEEVENTF_LEFTDOWN  = 0x0002
	MOUSEEVENTF_LEFTUP    = 0x0004

	EVENT_SYSTEM_FOREGROUND = 0x0003
	WINEVENT_OUTOFCONTEXT   = 0x0000

	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

	DIGCF_ALLCLASSES = 0x04
	DICS_FLAG_GLOBAL = 1
	DIREG_DEV        = 1

	FILE_FLAG_OVERLAPPED  = 0x40000000
	PIPE_TRANSFER_TIMEOUT = 0x03
	UsbdPipeTypeInterrupt = 3
)

// injectedMark is put into dwExtraInfo of every event we inject, so our own
// hook can recognise them.
const injectedMark = 0x4E414741 // "NAGA"

type MSLLHOOKSTRUCT struct {
	Pt        POINT
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// INPUT for SendInput (x64: 4-byte type, 4 padding, 32-byte union).
type INPUT struct {
	Type  uint32
	_     uint32
	Union [32]byte
}

type KEYBDINPUT struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type MOUSEINPUT struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type SP_DEVINFO_DATA struct {
	CbSize    uint32
	ClassGuid GUID
	DevInst   uint32
	Reserved  uintptr
}

func keyInput(k KEYBDINPUT) INPUT {
	in := INPUT{Type: INPUT_KEYBOARD}
	*(*KEYBDINPUT)(unsafe.Pointer(&in.Union[0])) = k
	return in
}

func mouseInput(m MOUSEINPUT) INPUT {
	in := INPUT{Type: INPUT_MOUSE}
	*(*MOUSEINPUT)(unsafe.Pointer(&in.Union[0])) = m
	return in
}

func sendInputs(in []INPUT) {
	if len(in) == 0 {
		return
	}
	pSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

// systemLanguage: Russian UI language → "ru", anything else → "en".
func systemLanguage() string {
	id, _, _ := pGetUserDefaultUILanguage.Call()
	if id&0x3FF == 0x19 {
		return "ru"
	}
	return "en"
}
