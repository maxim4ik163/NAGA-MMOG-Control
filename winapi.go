//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	hid      = syscall.NewLazyDLL("hid.dll")
	setupapi = syscall.NewLazyDLL("setupapi.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")
	pCreateFileW      = kernel32.NewProc("CreateFileW")
	pReadFile         = kernel32.NewProc("ReadFile")
	pCloseHandle      = kernel32.NewProc("CloseHandle")

	pRegisterClassExW          = user32.NewProc("RegisterClassExW")
	pCreateWindowExW           = user32.NewProc("CreateWindowExW")
	pDefWindowProcW            = user32.NewProc("DefWindowProcW")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pShowWindow                = user32.NewProc("ShowWindow")
	pSetWindowPos              = user32.NewProc("SetWindowPos")
	pSetTimer                  = user32.NewProc("SetTimer")
	pKillTimer                 = user32.NewProc("KillTimer")
	pCreatePopupMenu           = user32.NewProc("CreatePopupMenu")
	pAppendMenuW               = user32.NewProc("AppendMenuW")
	pTrackPopupMenu            = user32.NewProc("TrackPopupMenu")
	pDestroyMenu               = user32.NewProc("DestroyMenu")
	pSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	pGetCursorPos              = user32.NewProc("GetCursorPos")
	pMonitorFromPoint          = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	pGetDC                     = user32.NewProc("GetDC")
	pReleaseDC                 = user32.NewProc("ReleaseDC")
	pUpdateLayeredWindow       = user32.NewProc("UpdateLayeredWindow")
	pCreateIconIndirect        = user32.NewProc("CreateIconIndirect")
	pDestroyIcon               = user32.NewProc("DestroyIcon")
	pRegisterWindowMessageW    = user32.NewProc("RegisterWindowMessageW")
	pSetProcessDPIAware        = user32.NewProc("SetProcessDPIAware")
	pSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForMonitor          = syscall.NewLazyDLL("shcore.dll").NewProc("GetDpiForMonitor")
	pMessageBoxW               = user32.NewProc("MessageBoxW")
	pDrawTextW                 = user32.NewProc("DrawTextW")

	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap          = gdi32.NewProc("CreateBitmap")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pCreateFontW           = gdi32.NewProc("CreateFontW")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	pGdiFlush              = gdi32.NewProc("GdiFlush")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW    = shell32.NewProc("ShellExecuteW")

	pHidD_GetHidGuid        = hid.NewProc("HidD_GetHidGuid")
	pHidD_GetAttributes     = hid.NewProc("HidD_GetAttributes")
	pHidD_GetPreparsedData  = hid.NewProc("HidD_GetPreparsedData")
	pHidD_FreePreparsedData = hid.NewProc("HidD_FreePreparsedData")
	pHidP_GetCaps           = hid.NewProc("HidP_GetCaps")
	pHidD_GetInputReport    = hid.NewProc("HidD_GetInputReport")

	pSetupDiGetClassDevsW             = setupapi.NewProc("SetupDiGetClassDevsW")
	pSetupDiEnumDeviceInterfaces      = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	pSetupDiGetDeviceInterfaceDetailW = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	pSetupDiDestroyDeviceInfoList     = setupapi.NewProc("SetupDiDestroyDeviceInfoList")

	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

const (
	WM_DESTROY      = 0x0002
	WM_TIMER        = 0x0113
	WM_COMMAND      = 0x0111
	WM_DEVICECHANGE = 0x0219
	WM_APP          = 0x8000
	WM_LBUTTONUP    = 0x0202
	WM_RBUTTONUP    = 0x0205
	WM_CONTEXTMENU  = 0x007B
	WM_NULL         = 0x0000

	WS_POPUP          = 0x80000000
	WS_EX_LAYERED     = 0x00080000
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_TRANSPARENT = 0x00000020
	WS_EX_NOACTIVATE  = 0x08000000

	SW_HIDE           = 0
	SW_SHOWNOACTIVATE = 4
	HWND_TOPMOST      = ^uintptr(0) // -1
	SWP_NOACTIVATE    = 0x0010
	SWP_SHOWWINDOW    = 0x0040

	MF_STRING       = 0x0000
	MF_GRAYED       = 0x0001
	MF_CHECKED      = 0x0008
	MF_SEPARATOR    = 0x0800
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100
	TPM_NONOTIFY    = 0x0080

	NIM_ADD        = 0
	NIM_MODIFY     = 1
	NIM_DELETE     = 2
	NIM_SETVERSION = 4
	NIF_MESSAGE    = 0x01
	NIF_ICON       = 0x02
	NIF_TIP        = 0x04
	NIF_INFO       = 0x10
	NIF_SHOWTIP    = 0x80
	NIIF_INFO      = 0x01
	NIIF_WARNING   = 0x02
	NIIF_ERROR     = 0x03

	ULW_ALPHA    = 0x02
	AC_SRC_OVER  = 0x00
	AC_SRC_ALPHA = 0x01

	GENERIC_READ     = 0x80000000
	GENERIC_WRITE    = 0x40000000
	FILE_SHARE_READ  = 0x1
	FILE_SHARE_WRITE = 0x2
	OPEN_EXISTING    = 3
	INVALID_HANDLE   = ^uintptr(0)

	DIGCF_PRESENT         = 0x02
	DIGCF_DEVICEINTERFACE = 0x10

	HIDP_STATUS_SUCCESS = 0x00110000

	HKEY_CURRENT_USER = 0x80000001
	KEY_READ          = 0x20019
	KEY_WRITE         = 0x20006
	REG_SZ            = 1

	FW_NORMAL           = 400
	FW_BOLD             = 700
	ANTIALIASED_QUALITY = 4
	DEFAULT_CHARSET     = 1
	TRANSPARENT_BK      = 1
	DT_CENTER           = 0x01
	DT_VCENTER          = 0x04
	DT_SINGLELINE       = 0x20
	DT_NOPREFIX         = 0x800
)

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type SIZE struct{ CX, CY int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	_       uint32
}

type WNDCLASSEXW struct {
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

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         GUID
	HBalloonIcon     uintptr
}

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type ICONINFO struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type SP_DEVICE_INTERFACE_DATA struct {
	CbSize             uint32
	InterfaceClassGuid GUID
	Flags              uint32
	Reserved           uintptr
}

type HIDD_ATTRIBUTES struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
	_             uint16
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyU16(dst []uint16, s string) {
	w, _ := syscall.UTF16FromString(s)
	if len(w) > len(dst) {
		w = w[:len(dst)]
		w[len(w)-1] = 0
	}
	copy(dst, w)
	for i := len(w); i < len(dst); i++ {
		dst[i] = 0
	}
}

func ptr[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

func rgb(r, g, b uint8) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

func ptrU16(s string) unsafe.Pointer       { return unsafe.Pointer(u16(s)) }
func unsafePtr[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) }
