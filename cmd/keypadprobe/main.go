//go:build windows

// keypadprobe: checks whether the Naga side keypad (interface MI_01) can be
// read directly by an app. That works only after its driver in Device
// Manager has been switched from "HID Keyboard Device" to "HID-compliant
// device"; then the keypad no longer types into Windows at all, and only
// Naga Control sees it. Read-only: nothing is sent to the mouse.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

var out io.Writer = os.Stdout

func logf(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

var usageNames = map[byte]string{
	0x1E: "1", 0x1F: "2", 0x20: "3", 0x21: "4", 0x22: "5", 0x23: "6", 0x24: "7", 0x25: "8",
	0x26: "9", 0x27: "0", 0x2D: "-", 0x2E: "=",
	0x59: "Num1", 0x5A: "Num2", 0x5B: "Num3", 0x5C: "Num4", 0x5D: "Num5", 0x5E: "Num6",
	0x5F: "Num7", 0x60: "Num8", 0x61: "Num9", 0x62: "Num0", 0x56: "Num-", 0x57: "Num+",
	0x53: "NumLock",
}

func decode(r []byte) string {
	// Boot-style keyboard report: [report id?] mods, reserved, up to 6 key usages.
	keys := r
	if len(keys) == 9 {
		keys = keys[1:]
	}
	var names []string
	if len(keys) >= 8 {
		if keys[0] != 0 {
			names = append(names, fmt.Sprintf("mods=%02X", keys[0]))
		}
		for _, u := range keys[2:8] {
			if u == 0 {
				continue
			}
			if n, ok := usageNames[u]; ok {
				names = append(names, n)
			} else {
				names = append(names, fmt.Sprintf("usage %02X", u))
			}
		}
	}
	if len(names) == 0 {
		return "(all released)"
	}
	return strings.Join(names, " + ")
}

func main() {
	exe, _ := os.Executable()
	logPath := filepath.Join(filepath.Dir(exe), "naga-keypad.log")
	if f, err := os.Create(logPath); err == nil {
		defer f.Close()
		out = io.MultiWriter(f, os.Stdout)
	}
	logf("Naga keypad probe. %s", time.Now().Format("2006-01-02 15:04:05"))
	logf("")
	logf("== Naga devices known to Windows")
	for _, n := range nagaDevNodes() {
		logf("  %s", n.ID)
		logf("     %q  class=%s  service=%s  status=%08X problem=%d (cm=%d)", n.Desc, n.Class, n.Service, n.Status, n.Problem, n.StatusErr)
	}
	logf("")
	logf("== HID interfaces of the Naga")
	var pad *hidDevice
	for _, d := range enumerateHID() {
		if !strings.Contains(strings.ToLower(d.Path), "vid_1532&pid_0015") {
			continue
		}
		logf("  %s", d.Path)
		logf("     usage %04X:%04X in=%d feature=%d attributes read=%v", d.UsagePage, d.Usage, d.InLen, d.FeatLen, d.Opened)
		for _, a := range []struct {
			name string
			acc  uint32
		}{{"query", 0}, {"read", GENERIC_READ}, {"read/write", GENERIC_READ | GENERIC_WRITE}} {
			h, err := openErr(d.Path, a.acc)
			if err != nil {
				logf("     open %-10s: %v", a.name, err)
			} else {
				logf("     open %-10s: ok", a.name)
				closeHandle(h)
			}
		}
		if strings.Contains(strings.ToLower(d.Path), "mi_01") {
			dd := d
			pad = &dd
		}
	}
	if pad == nil {
		logf("The keypad interface (MI_01) was not found.")
		finish(logPath)
		return
	}
	if strings.HasSuffix(strings.ToLower(pad.Path), `\kbd`) {
		logf("The keypad is still a system keyboard (path ends in \\kbd): Windows owns it,")
		logf("so an app cannot read it. Switch its driver as described, then run this again.")
	}
	h := openHID(pad.Path, GENERIC_READ|GENERIC_WRITE)
	if h == invalidHandle {
		h = openHID(pad.Path, GENERIC_READ)
	}
	if h == invalidHandle {
		logf("Cannot open the keypad for reading.")
		finish(logPath)
		return
	}
	defer closeHandle(h)
	logf("Opened the keypad for reading. Press side buttons for 30 s, in 123 and in NUM mode.")
	logf("Nothing should be typed anywhere while you press them.")
	go func() {
		buf := make([]byte, int(pad.InLen))
		if len(buf) == 0 {
			buf = make([]byte, 9)
		}
		for {
			var n uint32
			r, _, err := pReadFile.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), ptr(&n), 0)
			if r == 0 {
				logf("  read error: %v", err)
				return
			}
			rep := buf[:n]
			logf("  %s  report %X  -> %s", time.Now().Format("15:04:05.000"), rep, decode(rep))
		}
	}()
	time.Sleep(30 * time.Second)
	finish(logPath)
}

func finish(logPath string) {
	fmt.Printf("\nSaved to %s\nPress Enter to close.", logPath)
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}
