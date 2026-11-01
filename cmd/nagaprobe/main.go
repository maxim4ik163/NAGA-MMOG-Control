//go:build windows

// nagaprobe: read-only probe for the original Razer Naga (1532:0015).
//
// It lists the mouse's HID collections, sends only GET commands (firmware,
// serial, DPI, polling rate, LED states) and then records which keys the
// side keypad sends. Nothing is written to the mouse.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	razerVID = 0x1532
	nagaPID  = 0x0015
)

var out io.Writer = os.Stdout

func logf(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

func hexs(b []byte) string {
	var sb strings.Builder
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", x)
	}
	return sb.String()
}

type getCmd struct {
	name      string
	class, id byte
	size      byte
	args      []byte
	decode    func(a []byte) string
}

var pollNames = map[byte]string{1: "1000 Hz", 2: "500 Hz", 4: "250 Hz", 8: "125 Hz"}

var commands = []getCmd{
	{"firmware", 0x00, 0x81, 0x02, nil, func(a []byte) string { return fmt.Sprintf("v%d.%02d", a[0], a[1]) }},
	{"serial", 0x00, 0x82, 0x16, nil, func(a []byte) string { return strings.TrimRight(string(a[:22]), "\x00 ") }},
	{"device mode", 0x00, 0x84, 0x02, nil, func(a []byte) string { return fmt.Sprintf("%02X %02X", a[0], a[1]) }},
	{"polling rate", 0x00, 0x85, 0x01, nil, func(a []byte) string {
		if n, ok := pollNames[a[0]]; ok {
			return n
		}
		return fmt.Sprintf("code %d", a[0])
	}},
	{"DPI (byte form)", 0x04, 0x81, 0x03, nil, func(a []byte) string { return fmt.Sprintf("x=%d y=%d (raw bytes)", a[0], a[1]) }},
	{"DPI (u16 form)", 0x04, 0x85, 0x07, []byte{0x00}, func(a []byte) string {
		return fmt.Sprintf("x=%d y=%d", int(a[1])<<8|int(a[2]), int(a[3])<<8|int(a[4]))
	}},
	{"LED scroll wheel", 0x03, 0x80, 0x03, []byte{0x01, 0x01}, func(a []byte) string { return onOff(a[2]) }},
	{"LED logo", 0x03, 0x80, 0x03, []byte{0x01, 0x04}, func(a []byte) string { return onOff(a[2]) }},
	{"LED side panel", 0x03, 0x80, 0x03, []byte{0x01, 0x05}, func(a []byte) string { return onOff(a[2]) }},
}

func onOff(b byte) string {
	switch b {
	case 0:
		return "off"
	case 1:
		return "on"
	}
	return fmt.Sprintf("0x%02X", b)
}

var transactionIDs = []byte{0xFF, 0x3F, 0x1F}

// transact sends one request and reads the response. It never retries the
// request itself, only the read while the mouse reports "busy".
func transact(h uintptr, featLen int, req razerReport) (razerReport, error) {
	var resp razerReport
	n := razerReportLen + 1
	if featLen > n {
		n = featLen
	}
	buf := make([]byte, n)
	copy(buf[1:], req[:])
	if err := setFeature(h, buf); err != nil {
		return resp, fmt.Errorf("SetFeature: %v", err)
	}
	for i := 0; i < 10; i++ {
		time.Sleep(time.Duration(5+10*i) * time.Millisecond)
		rb := make([]byte, n)
		if err := getFeature(h, rb); err != nil {
			return resp, fmt.Errorf("GetFeature: %v", err)
		}
		copy(resp[:], rb[1:])
		if resp[0] != 1 { // not busy
			return resp, nil
		}
	}
	return resp, nil
}

func probeCollection(d hidDevice) bool {
	h := openHID(d.Path, GENERIC_READ|GENERIC_WRITE)
	mode := "read/write"
	if h == invalidHandle {
		h = openHID(d.Path, 0)
		mode = "query-only (system owns it)"
	}
	if h == invalidHandle {
		logf("  cannot open this collection")
		return false
	}
	defer closeHandle(h)
	logf("  opened %s", mode)

	anyOK := false
	for _, c := range commands {
		var lastErr error
		done := false
		for _, tid := range transactionIDs {
			req := newRazerReport(tid, c.class, c.id, c.size, c.args...)
			resp, err := transact(h, int(d.FeatLen), req)
			if err != nil {
				lastErr = err
				break // the transport itself failed; another tid will not help
			}
			match := resp[6] == c.class && resp[7] == c.id
			line := fmt.Sprintf("  %-17s tid=%02X status=%-13s", c.name, tid, statusName(resp[0]))
			if resp[0] == 2 && match {
				logf("%s -> %s", line, c.decode(resp.args()))
				logf("  %17s resp: %s | args: %s", "", hexs(resp[:8]), hexs(resp.args()[:int(c.size)+2]))
				anyOK, done = true, true
				break
			}
			logf("%s echo=%v resp: %s", line, match, hexs(resp[:12]))
		}
		if lastErr != nil {
			logf("  %-17s error: %v", c.name, lastErr)
			if !anyOK && c.name == "firmware" {
				return false // this collection does not take Razer reports
			}
		}
		_ = done
	}
	return anyOK
}

func main() {
	exe, _ := os.Executable()
	logPath := filepath.Join(filepath.Dir(exe), "naga-probe.log")
	if f, err := os.Create(logPath); err == nil {
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
	} else {
		logPath = ""
	}

	logf("Naga probe — read-only. %s", time.Now().Format("2006-01-02 15:04:05"))
	logf("")
	all := enumerateHID()
	var razer []hidDevice
	for _, d := range all {
		if d.VID == razerVID || isRazer(d.Path) {
			razer = append(razer, d)
		}
	}
	logf("== Razer HID collections (%d of %d HID total)", len(razer), len(all))
	for _, d := range razer {
		logf("%04X:%04X ver %04X  usage %04X:%04X  in=%d out=%d feature=%d  open=%v",
			d.VID, d.PID, d.Ver, d.UsagePage, d.Usage, d.InLen, d.OutLen, d.FeatLen, d.Opened)
		logf("  %s", d.Path)
	}
	if len(razer) == 0 {
		logf("No Razer device found. Is the Naga plugged in?")
	}

	// Collections that declare a feature report, the 91-byte ones first.
	var cands []hidDevice
	for _, d := range razer {
		if d.PID == nagaPID && d.FeatLen > 0 {
			cands = append(cands, d)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return (cands[i].FeatLen == 91) && (cands[j].FeatLen != 91)
	})
	logf("")
	logf("== Control commands (GET only)")
	if len(cands) == 0 {
		logf("No Naga collection declares a feature report.")
	}
	found := false
	for _, d := range cands {
		logf("-- usage %04X:%04X feature=%d", d.UsagePage, d.Usage, d.FeatLen)
		logf("  %s", d.Path)
		if probeCollection(d) {
			found = true
			logf("  => this collection answers Razer commands")
			break
		}
	}
	if !found {
		logf("No collection answered.")
	}

	const secs = 60
	logf("")
	logf("== Side keypad (%d s, observation only)", secs)
	logf("Switch on the bottom to 123: press side buttons 1..12 in order.")
	logf("Then switch to NUM: press 1..12 again. Also press the two thumb buttons above the keypad, if any.")
	seen := 0
	err := listenKeys(secs, func(e keyEvent) {
		if !isRazer(e.Device) || e.Up {
			return
		}
		seen++
		logf("  key scan=%02X%s vk=%02X %-12q numlock=%v  dev=%s",
			e.MakeCode, map[bool]string{true: " E0", false: ""}[e.E0], e.VKey, keyName(e.MakeCode, e.E0), e.NumLock, shortDev(e.Device))
	})
	if err != nil {
		logf("raw input error: %v", err)
	}
	logf("%d key presses from Razer devices.", seen)
	logf("")
	if logPath != "" {
		fmt.Printf("Saved to %s\n", logPath)
	}
	fmt.Print("Press Enter to close.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func shortDev(p string) string {
	u := strings.ToUpper(p)
	if i := strings.Index(u, "VID_"); i >= 0 {
		u = u[i:]
		if j := strings.Index(u, "#"); j >= 0 {
			u = u[:j]
		}
	}
	return u
}
