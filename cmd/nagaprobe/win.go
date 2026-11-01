//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	setupapi = syscall.NewLazyDLL("setupapi.dll")
	hidlib   = syscall.NewLazyDLL("hid.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	pSetupDiGetClassDevsW             = setupapi.NewProc("SetupDiGetClassDevsW")
	pSetupDiEnumDeviceInterfaces      = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	pSetupDiGetDeviceInterfaceDetailW = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	pSetupDiDestroyDeviceInfoList     = setupapi.NewProc("SetupDiDestroyDeviceInfoList")

	pHidD_GetHidGuid        = hidlib.NewProc("HidD_GetHidGuid")
	pHidD_GetAttributes     = hidlib.NewProc("HidD_GetAttributes")
	pHidD_GetPreparsedData  = hidlib.NewProc("HidD_GetPreparsedData")
	pHidD_FreePreparsedData = hidlib.NewProc("HidD_FreePreparsedData")
	pHidP_GetCaps           = hidlib.NewProc("HidP_GetCaps")
	pHidD_SetFeature        = hidlib.NewProc("HidD_SetFeature")
	pHidD_GetFeature        = hidlib.NewProc("HidD_GetFeature")

	pCreateFileW             = kernel32.NewProc("CreateFileW")
	pCloseHandle             = kernel32.NewProc("CloseHandle")
	pGetModuleHnd            = kernel32.NewProc("GetModuleHandleW")
	pGetStdHandle            = kernel32.NewProc("GetStdHandle")
	pFlushConsoleInputBuffer = kernel32.NewProc("FlushConsoleInputBuffer")

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
	pGetKeyState             = user32.NewProc("GetKeyState")
	pGetKeyNameTextW         = user32.NewProc("GetKeyNameTextW")
)

const (
	DIGCF_PRESENT         = 0x02
	DIGCF_DEVICEINTERFACE = 0x10

	GENERIC_READ     = 0x80000000
	GENERIC_WRITE    = 0x40000000
	FILE_SHARE_READ  = 1
	FILE_SHARE_WRITE = 2
	OPEN_EXISTING    = 3

	HIDP_STATUS_SUCCESS = 0x00110000

	invalidHandle = ^uintptr(0)
)

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type spDeviceInterfaceData struct {
	CbSize    uint32
	ClassGuid GUID
	Flags     uint32
	Reserved  uintptr
}

type hiddAttributes struct {
	Size      uint32
	VendorID  uint16
	ProductID uint16
	Version   uint16
}

func ptr[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

type hidDevice struct {
	Path                   string
	VID, PID, Ver          uint16
	UsagePage, Usage       uint16
	InLen, OutLen, FeatLen uint16
	Opened                 bool
}

func openHID(path string, access uint32) uintptr {
	h, _, _ := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), uintptr(access),
		FILE_SHARE_READ|FILE_SHARE_WRITE, 0, OPEN_EXISTING, 0, 0)
	return h
}

func closeHandle(h uintptr) { pCloseHandle.Call(h) }

func enumerateHID() []hidDevice {
	var g GUID
	pHidD_GetHidGuid.Call(ptr(&g))
	hdev, _, _ := pSetupDiGetClassDevsW.Call(ptr(&g), 0, 0, DIGCF_PRESENT|DIGCF_DEVICEINTERFACE)
	if hdev == invalidHandle || hdev == 0 {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(hdev)
	var out []hidDevice
	for i := uintptr(0); ; i++ {
		var ifd spDeviceInterfaceData
		ifd.CbSize = uint32(unsafe.Sizeof(ifd))
		if r, _, _ := pSetupDiEnumDeviceInterfaces.Call(hdev, 0, ptr(&g), i, ptr(&ifd)); r == 0 {
			break
		}
		var need uint32
		pSetupDiGetDeviceInterfaceDetailW.Call(hdev, ptr(&ifd), 0, 0, ptr(&need), 0)
		if need == 0 || need > 4096 {
			continue
		}
		buf := make([]byte, need+8)
		*(*uint32)(unsafe.Pointer(&buf[0])) = 8 // cbSize of SP_DEVICE_INTERFACE_DETAIL_DATA_W on x64
		if r, _, _ := pSetupDiGetDeviceInterfaceDetailW.Call(hdev, ptr(&ifd), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), 0, 0); r == 0 {
			continue
		}
		path := syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2))
		out = append(out, queryHID(path))
	}
	return out
}

func queryHID(path string) hidDevice {
	d := hidDevice{Path: path}
	h := openHID(path, 0)
	if h == invalidHandle {
		return d
	}
	defer closeHandle(h)
	d.Opened = true
	var a hiddAttributes
	a.Size = uint32(unsafe.Sizeof(a))
	if r, _, _ := pHidD_GetAttributes.Call(h, ptr(&a)); r != 0 {
		d.VID, d.PID, d.Ver = a.VendorID, a.ProductID, a.Version
	}
	var pp uintptr
	if r, _, _ := pHidD_GetPreparsedData.Call(h, ptr(&pp)); r == 0 {
		return d
	}
	defer pHidD_FreePreparsedData.Call(pp)
	var caps [64]byte
	if st, _, _ := pHidP_GetCaps.Call(pp, uintptr(unsafe.Pointer(&caps[0]))); uint32(st) == HIDP_STATUS_SUCCESS {
		d.Usage = *(*uint16)(unsafe.Pointer(&caps[0]))
		d.UsagePage = *(*uint16)(unsafe.Pointer(&caps[2]))
		d.InLen = *(*uint16)(unsafe.Pointer(&caps[4]))
		d.OutLen = *(*uint16)(unsafe.Pointer(&caps[6]))
		d.FeatLen = *(*uint16)(unsafe.Pointer(&caps[8]))
	}
	return d
}

func setFeature(h uintptr, buf []byte) error {
	r, _, err := pHidD_SetFeature.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return err
	}
	return nil
}

func getFeature(h uintptr, buf []byte) error {
	r, _, err := pHidD_GetFeature.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return err
	}
	return nil
}

func flushConsoleInput() {
	const STD_INPUT_HANDLE = ^uintptr(9) // (DWORD)-10
	h, _, _ := pGetStdHandle.Call(STD_INPUT_HANDLE)
	if h != 0 && h != invalidHandle {
		pFlushConsoleInputBuffer.Call(h)
	}
}
