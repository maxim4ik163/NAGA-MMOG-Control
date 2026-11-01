//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	cfgmgr32                       = syscall.NewLazyDLL("cfgmgr32.dll")
	pCM_Get_DevNode_Status         = cfgmgr32.NewProc("CM_Get_DevNode_Status")
	pSetupDiEnumDeviceInfo         = setupapi.NewProc("SetupDiEnumDeviceInfo")
	pSetupDiGetDeviceInstanceIdW   = setupapi.NewProc("SetupDiGetDeviceInstanceIdW")
	pSetupDiGetDeviceRegistryPropW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
)

type spDevinfoData struct {
	CbSize    uint32
	ClassGuid GUID
	DevInst   uint32
	Reserved  uintptr
}

type devNode struct {
	ID, Desc, Service, Class string
	Status, Problem          uint32
	StatusErr                uintptr
}

func regProp(h uintptr, d *spDevinfoData, prop uint32) string {
	buf := make([]uint16, 512)
	var typ, need uint32
	if r, _, _ := pSetupDiGetDeviceRegistryPropW.Call(h, ptr(d), uintptr(prop), ptr(&typ),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), ptr(&need)); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// nagaDevNodes lists every present device whose instance ID belongs to the Naga.
func nagaDevNodes() []devNode {
	const DIGCF_ALLCLASSES = 0x04
	h, _, _ := pSetupDiGetClassDevsW.Call(0, 0, 0, DIGCF_ALLCLASSES|DIGCF_PRESENT)
	if h == invalidHandle || h == 0 {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(h)
	var out []devNode
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
		id := syscall.UTF16ToString(buf)
		if !strings.Contains(strings.ToUpper(id), "VID_1532&PID_0015") {
			continue
		}
		n := devNode{ID: id, Desc: regProp(h, &d, 0), Service: regProp(h, &d, 4), Class: regProp(h, &d, 7)}
		n.StatusErr, _, _ = pCM_Get_DevNode_Status.Call(ptr(&n.Status), ptr(&n.Problem), uintptr(d.DevInst), 0)
		out = append(out, n)
	}
	return out
}

// openErr tries CreateFile with the given access and returns the Windows error.
func openErr(path string, access uint32) (uintptr, error) {
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), uintptr(access),
		FILE_SHARE_READ|FILE_SHARE_WRITE, 0, OPEN_EXISTING, 0, 0)
	if h == invalidHandle {
		return h, err
	}
	return h, nil
}
