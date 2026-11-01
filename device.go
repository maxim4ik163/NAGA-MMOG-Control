//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	razerVID = 0x1532
	nagaPID  = 0x0015
)

// ---------------------------------------------------------------------------
// Enumeration helpers

func interfacePaths(g GUID) []string {
	hdev, _, _ := pSetupDiGetClassDevsW.Call(ptr(&g), 0, 0, DIGCF_PRESENT|DIGCF_DEVICEINTERFACE)
	if hdev == INVALID_HANDLE || hdev == 0 {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(hdev)
	var out []string
	for i := uintptr(0); ; i++ {
		var ifd SP_DEVICE_INTERFACE_DATA
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
		out = append(out, syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2)))
	}
	return out
}

func openPath(path string, access uint32, flags uint32) uintptr {
	h, _, _ := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), uintptr(access),
		FILE_SHARE_READ|FILE_SHARE_WRITE, 0, OPEN_EXISTING, uintptr(flags), 0)
	return h
}

// findControlPath returns the Naga's mouse collection with the 91-byte
// feature report (the control channel), or "".
func findControlPath() string {
	var g GUID
	pHidD_GetHidGuid.Call(ptr(&g))
	for _, path := range interfacePaths(g) {
		if !strings.Contains(strings.ToLower(path), "vid_1532&pid_0015") {
			continue
		}
		h := openPath(path, 0, 0)
		if h == INVALID_HANDLE {
			continue
		}
		var pp uintptr
		feat := 0
		if r, _, _ := pHidD_GetPreparsedData.Call(h, ptr(&pp)); r != 0 {
			var caps [64]byte
			if st, _, _ := pHidP_GetCaps.Call(pp, uintptr(unsafe.Pointer(&caps[0]))); uint32(st) == HIDP_STATUS_SUCCESS {
				feat = int(*(*uint16)(unsafe.Pointer(&caps[8])))
			}
			pHidD_FreePreparsedData.Call(pp)
		}
		pCloseHandle.Call(h)
		if feat == razerReportLen+1 {
			return path
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Control channel: 90-byte Razer reports as feature report 0.

// Old Razer firmware stalls (ERROR_GEN_FAILURE) when packets come too close
// together; 40 ms between any two transfers made a full write test clean.
const packetGap = 40 * time.Millisecond

type controlDev struct {
	mu   sync.Mutex
	path string
	h    uintptr
	last time.Time
}

func openControl(path string) (*controlDev, error) {
	h := openPath(path, 0, 0) // the system owns the mouse; feature reports work with access 0
	if h == INVALID_HANDLE {
		return nil, errors.New("cannot open the control channel")
	}
	return &controlDev{path: path, h: h}, nil
}

func (c *controlDev) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.h != 0 && c.h != INVALID_HANDLE {
		pCloseHandle.Call(c.h)
		c.h = 0
	}
}

func (c *controlDev) xfer(get bool, buf []byte) error {
	var err error
	for i := 0; i < 8; i++ {
		if i > 0 {
			time.Sleep(time.Duration(50*i) * time.Millisecond)
		}
		if d := time.Until(c.last.Add(packetGap)); d > 0 {
			time.Sleep(d)
		}
		var r uintptr
		if get {
			r, _, err = pHidD_GetFeature.Call(c.h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		} else {
			r, _, err = pHidD_SetFeature.Call(c.h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		}
		c.last = time.Now()
		if r != 0 {
			return nil
		}
	}
	return err
}

// do sends one command (transaction id 0xFF) and returns the response.
func (c *controlDev) do(class, id, size byte, args ...byte) (razerReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var resp razerReport
	if c.h == 0 {
		return resp, errors.New("closed")
	}
	req := newRazerReport(0xFF, class, id, size, args...)
	buf := make([]byte, razerReportLen+1)
	copy(buf[1:], req[:])
	if err := c.xfer(false, buf); err != nil {
		return resp, fmt.Errorf("SetFeature %02X/%02X: %v", class, id, err)
	}
	for i := 0; i < 10; i++ {
		rb := make([]byte, razerReportLen+1)
		if err := c.xfer(true, rb); err != nil {
			return resp, fmt.Errorf("GetFeature %02X/%02X: %v", class, id, err)
		}
		copy(resp[:], rb[1:])
		if resp[0] != 1 { // not busy
			break
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	if resp[0] != 2 || resp[6] != class || resp[7] != id {
		return resp, fmt.Errorf("%02X/%02X: status %s", class, id, statusName(resp[0]))
	}
	return resp, nil
}

func (c *controlDev) firmware() string {
	for i := 0; i < 5; i++ {
		r, err := c.do(0x00, 0x81, 0x02)
		if err == nil && (r[8] != 0 || r[9] != 0) {
			return fmt.Sprintf("%d.%02d", r[8], r[9])
		}
		time.Sleep(250 * time.Millisecond) // the first read after opening can return 0.00
	}
	return ""
}

func (c *controlDev) getDPI() (byte, error) {
	r, err := c.do(0x04, 0x81, 0x03)
	return r[8], err
}

func (c *controlDev) setDPI(b byte) error {
	_, err := c.do(0x04, 0x01, 0x03, b, b, 0x00)
	return err
}

func (c *controlDev) getPoll() (byte, error) {
	r, err := c.do(0x00, 0x85, 0x01)
	return r[8], err
}

func (c *controlDev) setPoll(code byte) error {
	_, err := c.do(0x00, 0x05, 0x01, code)
	return err
}

// LED ids: 0x01 scroll wheel, 0x04 logo, 0x05 side keypad.
var ledIDs = [3]byte{0x01, 0x04, 0x05}

func (c *controlDev) getLED(id byte) (bool, error) {
	r, err := c.do(0x03, 0x80, 0x03, 0x01, id)
	return r[10] == 1, err
}

func (c *controlDev) setLED(id byte, on bool) error {
	v := byte(0)
	if on {
		v = 1
	}
	_, err := c.do(0x03, 0x00, 0x03, 0x01, id, v)
	return err
}

// deviceState is what the mouse currently has (read at connect, then
// updated with every successful write).
type deviceState struct {
	DPI    byte
	Poll   byte
	LEDs   [3]bool
	Known  bool
	FW     string
	Serial string
}

func (c *controlDev) readState() (deviceState, error) {
	var s deviceState
	var err error
	s.FW = c.firmware()
	if s.DPI, err = c.getDPI(); err != nil {
		return s, err
	}
	if s.Poll, err = c.getPoll(); err != nil {
		return s, err
	}
	for i, id := range ledIDs {
		if s.LEDs[i], err = c.getLED(id); err != nil {
			return s, err
		}
	}
	s.Known = true
	return s, nil
}
