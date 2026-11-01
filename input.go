//go:build windows

package main

import (
	"sync"
	"time"
)

// Key output through SendInput. Every injected event carries injectedMark
// in dwExtraInfo.

func comboInputs(c Combo, up bool) []INPUT {
	var in []INPUT
	n := len(c.Keys)
	for i := 0; i < n; i++ {
		k := c.Keys[i]
		if up {
			k = c.Keys[n-1-i] // release in reverse order
		}
		scan, _, _ := pMapVirtualKeyW.Call(uintptr(k.VK), 0) // MAPVK_VK_TO_VSC
		var fl uint32
		if k.Ext {
			fl |= KEYEVENTF_EXTENDEDKEY
		}
		if up {
			fl |= KEYEVENTF_KEYUP
		}
		in = append(in, keyInput(KEYBDINPUT{Vk: k.VK, Scan: uint16(scan), Flags: fl, ExtraInfo: injectedMark}))
	}
	return in
}

func pressCombo(c Combo)   { sendInputs(comboInputs(c, false)) }
func releaseCombo(c Combo) { sendInputs(comboInputs(c, true)) }

// repeatKey sends repeated key-down events for the last key of c while held,
// like a keyboard's typematic repeat (Windows does not repeat injected keys).
type repeater struct {
	mu   sync.Mutex
	stop chan struct{}
}

func (r *repeater) start(c Combo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		close(r.stop)
	}
	stop := make(chan struct{})
	r.stop = stop
	last := Combo{Keys: c.Keys[len(c.Keys)-1:]}
	go func() {
		select {
		case <-stop:
			return
		case <-time.After(500 * time.Millisecond):
		}
		t := time.NewTicker(33 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				pressCombo(last)
			}
		}
	}()
}

func (r *repeater) halt() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
}

func sendLeftUp() {
	sendInputs([]INPUT{mouseInput(MOUSEINPUT{Flags: MOUSEEVENTF_LEFTUP, ExtraInfo: injectedMark})})
}
