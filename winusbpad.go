//go:build windows

package main

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

// The side keypad is USB interface 1 of the mouse. With WinUSB installed on
// it (Zadig, "Razer Naga (Interface 1)") Windows no longer treats it as a
// keyboard and we read its interrupt endpoint directly.

type padStatus string

const (
	padAbsent   padStatus = "absent"   // the mouse (interface 1) is not connected
	padKeyboard padStatus = "keyboard" // still the Windows keyboard driver → needs WinUSB
	padReady    padStatus = "ready"    // WinUSB, reader running
	padError    padStatus = "error"    // WinUSB, but cannot open/read
)

type padInfo struct {
	Status  padStatus
	Service string
	Path    string
}

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

func regProp(h uintptr, d *SP_DEVINFO_DATA, prop uint32) string {
	buf := make([]uint16, 512)
	var typ, need uint32
	if r, _, _ := pSetupDiGetDeviceRegistryPropW.Call(h, ptr(d), uintptr(prop), ptr(&typ),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), ptr(&need)); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// findPad locates USB\VID_1532&PID_0015&MI_01 and, if it runs WinUSB, the
// path of its device interface.
func findPad() padInfo {
	info := padInfo{Status: padAbsent}
	h, _, _ := pSetupDiGetClassDevsW.Call(0, 0, 0, DIGCF_ALLCLASSES|DIGCF_PRESENT)
	if h == INVALID_HANDLE || h == 0 {
		return info
	}
	defer pSetupDiDestroyDeviceInfoList.Call(h)
	for i := uintptr(0); ; i++ {
		var d SP_DEVINFO_DATA
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
		info.Service = regProp(h, &d, 4) // SPDRP_SERVICE
		if !strings.EqualFold(info.Service, "WinUSB") {
			info.Status = padKeyboard
			return info
		}
		info.Status = padError
		key, _, _ := pSetupDiOpenDevRegKey.Call(h, ptr(&d), DICS_FLAG_GLOBAL, 0, DIREG_DEV, KEY_READ)
		if key == INVALID_HANDLE || key == 0 {
			return info
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
			n := int(size / 2)
			start := 0
			for j := 0; j <= n && j < len(vb); j++ {
				if j == n || vb[j] == 0 {
					if j > start {
						var g GUID
						if r, _, _ := pCLSIDFromString.Call(uintptr(unsafe.Pointer(u16(syscall.UTF16ToString(vb[start:j])))), ptr(&g)); r == 0 {
							if p := interfacePaths(g); len(p) > 0 {
								info.Path = p[0]
								return info
							}
						}
					}
					start = j + 1
				}
			}
		}
		return info
	}
	return info
}

type padReader struct {
	h, wh  uintptr
	pipe   uint8
	closed bool
}

func openPad(path string) (*padReader, error) {
	h := openPath(path, GENERIC_READ|GENERIC_WRITE, FILE_FLAG_OVERLAPPED)
	if h == INVALID_HANDLE {
		return nil, errors.New("cannot open the keypad")
	}
	var wh uintptr
	if r, _, err := pWinUsb_Initialize.Call(h, ptr(&wh)); r == 0 {
		pCloseHandle.Call(h)
		return nil, err
	}
	var id usbInterfaceDescriptor
	pWinUsb_QueryInterfaceSettings.Call(wh, 0, ptr(&id))
	p := &padReader{h: h, wh: wh}
	for i := byte(0); i < id.BNumEndpoints; i++ {
		var pi winusbPipeInfo
		if r, _, _ := pWinUsb_QueryPipe.Call(wh, 0, uintptr(i), ptr(&pi)); r == 0 {
			continue
		}
		if pi.PipeType == UsbdPipeTypeInterrupt && pi.PipeId&0x80 != 0 {
			p.pipe = pi.PipeId
			break
		}
	}
	if p.pipe == 0 {
		p.close()
		return nil, errors.New("the keypad has no interrupt IN endpoint")
	}
	return p, nil
}

// abort makes a pending read return (used to stop the reader goroutine,
// which then calls close itself).
func (p *padReader) abort() {
	if p.pipe != 0 {
		pWinUsb_AbortPipe.Call(p.wh, uintptr(p.pipe))
	}
}

// read blocks until a report arrives (abort() cancels it).
func (p *padReader) read(buf []byte) (int, error) {
	var n uint32
	r, _, err := pWinUsb_ReadPipe.Call(p.wh, uintptr(p.pipe), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), ptr(&n), 0)
	if r == 0 {
		return 0, err
	}
	return int(n), nil
}

func (p *padReader) close() {
	if p.closed {
		return
	}
	p.closed = true
	pWinUsb_Free.Call(p.wh)
	pCloseHandle.Call(p.h)
}
