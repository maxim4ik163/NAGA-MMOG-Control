//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var stdin = bufio.NewReader(os.Stdin)

// ask prints a question, waits for Enter and logs the answer.
func ask(q string) string {
	flushConsoleInput() // drop anything typed while the probe was busy
	fmt.Print(q + " ")
	a, _ := stdin.ReadString('\n')
	a = strings.TrimSpace(a)
	fmt.Fprintf(logFile, "%s %s\n", q, a)
	return a
}

// do sends one command, trying the transaction IDs in order until the mouse
// answers "ok". It returns the response and the tid that worked.
func do(h uintptr, featLen int, class, id, size byte, args ...byte) (razerReport, byte, bool) {
	var resp razerReport
	for _, tid := range transactionIDs {
		r, err := transact(h, featLen, newRazerReport(tid, class, id, size, args...))
		if err != nil {
			logf("    %02X/%02X tid=%02X error: %v", class, id, tid, err)
			return resp, tid, false
		}
		resp = r
		if r[0] == 2 && r[6] == class && r[7] == id {
			return r, tid, true
		}
		logf("    %02X/%02X tid=%02X status=%s", class, id, tid, statusName(r[0]))
	}
	return resp, 0, false
}

func dpiFromByte(b byte) int { return 100 + 25*int(b) }

func writeTest(d hidDevice) {
	logf("== Write test (optional, everything is restored)")
	logf("It changes DPI, the LEDs and the polling rate one at a time, asks what you")
	logf("saw, and puts the old value back.")
	if a := ask("Type y and press Enter to run it, or just Enter to skip:"); !strings.EqualFold(a, "y") {
		logf("  skipped")
		return
	}
	h := openHID(d.Path, 0)
	if h == invalidHandle {
		logf("  cannot open the control collection")
		return
	}
	defer closeHandle(h)
	fl := int(d.FeatLen)

	// --- DPI
	r, _, ok := do(h, fl, 0x04, 0x81, 0x03)
	if !ok {
		logf("  cannot read DPI, stopping")
		return
	}
	ox, oy := r[8], r[9]
	logf("  DPI now: bytes %d/%d = %d/%d DPI", ox, oy, dpiFromByte(ox), dpiFromByte(oy))
	final := ox
	a := ask(fmt.Sprintf("  DPI to keep after the test (100..5600, Enter = %d):", dpiFromByte(ox)))
	if v, err := strconv.Atoi(a); err == nil && v >= 100 && v <= 5600 {
		final = byte((v - 100 + 12) / 25)
	}
	// Test with a value far from the current one so the change is obvious.
	test, testName := byte(12), "much slower (400 DPI)"
	if ox <= 36 {
		test, testName = byte(124), "much faster (3200 DPI)"
	}
	_, tid, ok := do(h, fl, 0x04, 0x01, 0x03, test, test, 0x00)
	logf("  set DPI bytes %d/%d (%d DPI): ok=%v tid=%02X", test, test, dpiFromByte(test), ok, tid)
	if r, _, ok := do(h, fl, 0x04, 0x81, 0x03); ok {
		logf("  read back: %d/%d", r[8], r[9])
	}
	ask("  Move the mouse: is the cursor " + testName + " now? y/n:")
	_, _, ok = do(h, fl, 0x04, 0x01, 0x03, final, final, 0x00)
	if r, _, ok2 := do(h, fl, 0x04, 0x81, 0x03); ok2 {
		logf("  DPI set to %d (byte %d): ok=%v, read back %d/%d", dpiFromByte(final), final, ok, r[8], r[9])
	}

	// --- LEDs
	for _, led := range []struct {
		id   byte
		name string
	}{{0x01, "scroll wheel"}, {0x04, "logo"}, {0x05, "side keypad"}} {
		r, _, ok := do(h, fl, 0x03, 0x80, 0x03, 0x01, led.id)
		if !ok {
			logf("  LED %s: cannot read state", led.name)
			continue
		}
		orig := r[10]
		ask(fmt.Sprintf("  LED %s reads %s. Is it actually lit right now? y/n:", led.name, onOff(orig)))
		want := byte(1)
		if orig == 1 {
			want = 0
		}
		_, tid, ok := do(h, fl, 0x03, 0x00, 0x03, 0x01, led.id, want)
		rb := byte(0xEE)
		if r, _, ok := do(h, fl, 0x03, 0x80, 0x03, 0x01, led.id); ok {
			rb = r[10]
		}
		logf("  LED %s set %s: ok=%v tid=%02X, read back %s", led.name, onOff(want), ok, tid, onOff(rb))
		ask(fmt.Sprintf("  Did the %s light change? y/n:", led.name))
		_, _, ok = do(h, fl, 0x03, 0x00, 0x03, 0x01, led.id, orig)
		logf("  LED %s restored to %s: ok=%v", led.name, onOff(orig), ok)
	}

	// --- polling rate (no visible effect; check by read-back only)
	if r, _, ok := do(h, fl, 0x00, 0x85, 0x01); ok {
		orig := r[8]
		test := byte(2)
		if orig == 2 {
			test = 1
		}
		_, tid, ok := do(h, fl, 0x00, 0x05, 0x01, test)
		time.Sleep(200 * time.Millisecond)
		rb := byte(0)
		if r, _, ok := do(h, fl, 0x00, 0x85, 0x01); ok {
			rb = r[8]
		}
		logf("  polling set %s: ok=%v tid=%02X, read back %s", pollNames[test], ok, tid, pollNames[rb])
		_, _, ok = do(h, fl, 0x00, 0x05, 0x01, orig)
		time.Sleep(200 * time.Millisecond)
		if r, _, ok2 := do(h, fl, 0x00, 0x85, 0x01); ok2 {
			logf("  polling restored: ok=%v, read back %s", ok, pollNames[r[8]])
		}
	}
	logf("  write test done, transfer retries: %d", transferRetries)
}
