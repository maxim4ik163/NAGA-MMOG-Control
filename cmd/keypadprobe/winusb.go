//go:build windows

package main

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	winusb                         = syscall.NewLazyDLL("winusb.dll")
	advapi32                       = syscall.NewLazyDLL("advapi32.dll")
	ole32                          = syscall.NewLazyDLL("ole32.dll")
	pWinUsb_Initialize             = winusb.NewProc("WinUsb_Initialize")
	pWinUsb_Free                   = winusb.NewProc("WinUsb_Free")
	pWinUsb_QueryInterfaceSettings = winusb.NewProc("WinUsb_QueryInterfaceSettings")
	pWinUsb_QueryPipe              = winusb.NewProc("WinUsb_QueryPipe")
	pWinUsb_ReadPipe               = winusb.NewProc("WinUsb_ReadPipe")
	pWinUsb_SetPipePolicy          = winusb.NewProc("WinUsb_SetPipePolicy")
	pSetupDiOpenDevRegKey          = setupapi.NewProc("SetupDiOpenDevRegKey")
	pRegQueryValueExW              = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey                   = advapi32.NewProc("RegCloseKey")
	pCLSIDFromString               = ole32.NewProc("CLSIDFromString")
)

const (
	FILE_FLAG_OVERLAPPED  = 0x40000000
	PIPE_TRANSFER_TIMEOUT = 0x03
	UsbdPipeTypeInterrupt = 3
)

type usbInterfaceDescriptor struct {
	BLength, BDescriptorType, BInterfaceNumber, BAlternateSetting, BNumEndpoints byte
	BInterfaceClass, BInterfaceSubClass, BInterfaceProtocol, IInterface          byte
}

type winusbPipeInfo struct {
	PipeType          int32
	PipeId            uint8
	_                 uint8
	MaximumPacketSize uint16
	Interval          uint8
	_                 [3]byte
}

// keypadUSBInterfaceGUIDs finds the USB interface MI_01 of the Naga and reads
// the device interface GUIDs its (WinUSB) driver registered.
func keypadUSBInterfaceGUIDs() (service string, guids []GUID) {
	const DIGCF_ALLCLASSES, DICS_FLAG_GLOBAL, DIREG_DEV, KEY_READ = 0x04, 1, 1, 0x20019
	h, _, _ := pSetupDiGetClassDevsW.Call(0, 0, 0, DIGCF_ALLCLASSES|DIGCF_PRESENT)
	if h == invalidHandle || h == 0 {
		return
	}
	defer pSetupDiDestroyDeviceInfoList.Call(h)
	for i := uintptr(0); ; i++ {
		var d spDevinfoData
		d.CbSize = uint32(unsafe.Sizeof(d))
		if r, _, _ := pSetupDiEnumDeviceInfo.Call(h, i, ptr(&d)); r == 0 {
			break
		}
		buf := make([]uint16, 512)
		if r, _, _ := pSetupDiGetDeviceInstanceIdW.Call(h, ptr(&d), uintptr(unsafe.Pointer(&buf[0])), 512, 0); r == 0 {
			continue
		}
		id := strings.ToUpper(syscall.UTF16ToString(buf))
		if !strings.HasPrefix(id, `USB\VID_1532&PID_0015&MI_01\`) {
			continue
		}
		service = regProp(h, &d, 4)
		key, _, _ := pSetupDiOpenDevRegKey.Call(h, ptr(&d), DICS_FLAG_GLOBAL, 0, DIREG_DEV, KEY_READ)
		if key == invalidHandle || key == 0 {
			return
		}
		defer pRegCloseKey.Call(key)
		for _, name := range []string{"DeviceInterfaceGUIDs", "DeviceInterfaceGUID"} {
			vb := make([]uint16, 1024)
			size := uint32(len(vb) * 2)
			var typ uint32
			if r, _, _ := pRegQueryValueExW.Call(key, uintptr(unsafe.Pointer(u16(name))), 0, ptr(&typ),
				uintptr(unsafe.Pointer(&vb[0])), ptr(&size)); r != 0 {
				continue
			}
			// REG_MULTI_SZ: strings separated by NULs
			start := 0
			for j := 0; j <= int(size/2) && j < len(vb); j++ {
				if j == int(size/2) || vb[j] == 0 {
					if j > start {
						str := syscall.UTF16ToString(vb[start:j])
						var g GUID
						if r, _, _ := pCLSIDFromString.Call(uintptr(unsafe.Pointer(u16(str))), ptr(&g)); r == 0 {
							guids = append(guids, g)
						}
					}
					start = j + 1
				}
			}
			if len(guids) > 0 {
				return
			}
		}
		return
	}
	return
}

func interfacePaths(g GUID) []string {
	hdev, _, _ := pSetupDiGetClassDevsW.Call(ptr(&g), 0, 0, DIGCF_PRESENT|DIGCF_DEVICEINTERFACE)
	if hdev == invalidHandle || hdev == 0 {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(hdev)
	var out []string
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
		*(*uint32)(unsafe.Pointer(&buf[0])) = 8
		if r, _, _ := pSetupDiGetDeviceInterfaceDetailW.Call(hdev, ptr(&ifd), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), 0, 0); r == 0 {
			continue
		}
		out = append(out, syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2)))
	}
	return out
}

// readWinUSB reads the keypad's interrupt endpoint for d. Returns false if
// the keypad is not on WinUSB.
func readWinUSB(d time.Duration) bool {
	service, guids := keypadUSBInterfaceGUIDs()
	logf("")
	logf("== USB interface 1 (keypad): driver service = %q", service)
	if !strings.EqualFold(service, "WinUSB") {
		logf("  not on WinUSB")
		return false
	}
	if len(guids) == 0 {
		logf("  WinUSB, but no DeviceInterfaceGUIDs in the registry")
		return true
	}
	var path string
	for _, g := range guids {
		if p := interfacePaths(g); len(p) > 0 {
			path = p[0]
			break
		}
	}
	if path == "" {
		logf("  no device interface found for its GUIDs")
		return true
	}
	logf("  %s", path)
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), GENERIC_READ|GENERIC_WRITE,
		FILE_SHARE_READ|FILE_SHARE_WRITE, 0, OPEN_EXISTING, FILE_FLAG_OVERLAPPED, 0)
	if h == invalidHandle {
		logf("  open: %v", err)
		return true
	}
	defer closeHandle(h)
	var wh uintptr
	if r, _, err := pWinUsb_Initialize.Call(h, ptr(&wh)); r == 0 {
		logf("  WinUsb_Initialize: %v", err)
		return true
	}
	defer pWinUsb_Free.Call(wh)
	var id usbInterfaceDescriptor
	pWinUsb_QueryInterfaceSettings.Call(wh, 0, ptr(&id))
	logf("  interface %d class %02X/%02X/%02X, %d endpoint(s)", id.BInterfaceNumber, id.BInterfaceClass,
		id.BInterfaceSubClass, id.BInterfaceProtocol, id.BNumEndpoints)
	var in winusbPipeInfo
	found := false
	for p := byte(0); p < id.BNumEndpoints; p++ {
		var pi winusbPipeInfo
		if r, _, _ := pWinUsb_QueryPipe.Call(wh, 0, uintptr(p), ptr(&pi)); r == 0 {
			continue
		}
		logf("  pipe %02X type %d max %d interval %d", pi.PipeId, pi.PipeType, pi.MaximumPacketSize, pi.Interval)
		if pi.PipeType == UsbdPipeTypeInterrupt && pi.PipeId&0x80 != 0 && !found {
			in, found = pi, true
		}
	}
	if !found {
		logf("  no interrupt IN pipe")
		return true
	}
	timeout := uint32(500)
	pWinUsb_SetPipePolicy.Call(wh, uintptr(in.PipeId), PIPE_TRANSFER_TIMEOUT, 4, ptr(&timeout))
	logf("Reading the keypad for %v. Press side buttons in 123 and NUM mode.", d)
	logf("Nothing should be typed anywhere while you press them.")
	end := time.Now().Add(d)
	buf := make([]byte, 64)
	for time.Now().Before(end) {
		var n uint32
		r, _, err := pWinUsb_ReadPipe.Call(wh, uintptr(in.PipeId), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), ptr(&n), 0)
		if r == 0 {
			if errno, ok := err.(syscall.Errno); ok && errno == 121 { // ERROR_SEM_TIMEOUT
				continue
			}
			logf("  read: %v", err)
			return true
		}
		rep := buf[:n]
		logf("  %s  report %X  -> %s", time.Now().Format("15:04:05.000"), rep, decode(rep))
	}
	return true
}
