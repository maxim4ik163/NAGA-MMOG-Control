package main

// Side keypad reports (read through WinUSB from interface 1): 8-byte boot
// keyboard reports — modifiers, reserved, up to six HID key usages. The
// hardware switch on the bottom selects which usages the 12 buttons send:
// 123 → 1..0, -, = ; NUM → keypad 1..0, -, +.

var usageToButton = map[byte]struct {
	layer  string
	button int
}{}

func init() {
	l123 := []byte{0x1E, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x2D, 0x2E}
	lnum := []byte{0x59, 0x5A, 0x5B, 0x5C, 0x5D, 0x5E, 0x5F, 0x60, 0x61, 0x62, 0x56, 0x57}
	for i, u := range l123 {
		usageToButton[u] = struct {
			layer  string
			button int
		}{"123", i + 1}
	}
	for i, u := range lnum {
		usageToButton[u] = struct {
			layer  string
			button int
		}{"num", i + 1}
	}
}

// KeyEvent is a press or release of one slot ("123:5", "m:4", ...).
type KeyEvent struct {
	Slot string
	Down bool
}

// keypadDecoder turns successive reports into press/release events.
type keypadDecoder struct {
	held map[string]bool
}

func newKeypadDecoder() *keypadDecoder { return &keypadDecoder{held: map[string]bool{}} }

// decode returns the events for one report. Releases come first so that a
// fast roll from one button to another releases before it presses.
func (d *keypadDecoder) decode(rep []byte) []KeyEvent {
	if len(rep) == 9 { // with a report ID byte
		rep = rep[1:]
	}
	if len(rep) < 3 {
		return nil
	}
	now := map[string]bool{}
	end := len(rep)
	if end > 8 {
		end = 8
	}
	for _, u := range rep[2:end] {
		if b, ok := usageToButton[u]; ok {
			now[keypadSlot(b.layer, b.button)] = true
		}
	}
	var ev []KeyEvent
	for _, s := range sortedKeys(d.held) {
		if !now[s] {
			ev = append(ev, KeyEvent{s, false})
			delete(d.held, s)
		}
	}
	for _, s := range sortedKeys(now) {
		if !d.held[s] {
			ev = append(ev, KeyEvent{s, true})
			d.held[s] = true
		}
	}
	return ev
}

// releaseAll is used when the keypad disappears.
func (d *keypadDecoder) releaseAll() []KeyEvent {
	var ev []KeyEvent
	for _, s := range sortedKeys(d.held) {
		ev = append(ev, KeyEvent{s, false})
	}
	d.held = map[string]bool{}
	return ev
}

func sortedKeys(m map[string]bool) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	// small sets: insertion sort keeps this dependency-free and deterministic
	for i := 1; i < len(k); i++ {
		for j := i; j > 0 && k[j] < k[j-1]; j-- {
			k[j], k[j-1] = k[j-1], k[j]
		}
	}
	return k
}
