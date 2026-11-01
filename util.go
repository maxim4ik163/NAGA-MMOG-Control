//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func syscallUTF16(s string) ([]uint16, error) { return syscall.UTF16FromString(s) }

var (
	logMu      sync.Mutex
	logEnabled bool
	logFile    string
)

func logf(format string, a ...any) {
	if !logEnabled || logFile == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	if fi, err := os.Stat(logFile); err == nil && fi.Size() > 2<<20 {
		_ = os.Rename(logFile, logFile+".old")
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, a...))
}

func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(p)
}

// ---- autostart (HKCU\...\Run) ----

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runName = "NagaControl"

func autostartEnabled() bool {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(ptrU16(runKey)), 0, KEY_READ, ptr(&k)); r != 0 {
		return false
	}
	defer pRegCloseKey.Call(k)
	r, _, _ := pRegQueryValueExW.Call(k, uintptr(ptrU16(runName)), 0, 0, 0, 0)
	return r == 0
}

// refreshAutostart rewrites the Run value (exe may have moved, old value lacked --tray).
func refreshAutostart() {
	if autostartEnabled() {
		setAutostart(true)
	}
}

func setAutostart(on bool) {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(ptrU16(runKey)), 0, KEY_WRITE, ptr(&k)); r != 0 {
		return
	}
	defer pRegCloseKey.Call(k)
	if !on {
		pRegDeleteValueW.Call(k, uintptr(ptrU16(runName)))
		return
	}
	exe, _ := os.Executable()
	v, _ := syscall.UTF16FromString(`"` + exe + `" --tray`)
	pRegSetValueExW.Call(k, uintptr(ptrU16(runName)), 0, REG_SZ, uintptr(unsafePtr(&v[0])), uintptr(len(v)*2))
}
