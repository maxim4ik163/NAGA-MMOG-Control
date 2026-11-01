package main

import (
	"errors"
	"fmt"
	"strings"
)

// Key names shared with the UI (served by /api/keys), virtual-key codes and
// whether the key needs KEYEVENTF_EXTENDEDKEY.
type keyDef struct {
	Name string `json:"name"`
	VK   uint16 `json:"vk"`
	Ext  bool   `json:"ext"`
	Mod  bool   `json:"mod"`
	// Code is the browser KeyboardEvent.code for capturing in the UI.
	Code string `json:"code"`
}

var keyDefs []keyDef
var keyByName = map[string]keyDef{}
var keyByVK = map[uint16]keyDef{}

func addKey(name string, vk uint16, ext bool, code string, aliases ...string) {
	k := keyDef{Name: name, VK: vk, Ext: ext, Code: code}
	keyDefs = append(keyDefs, k)
	keyByName[strings.ToLower(name)] = k
	for _, a := range aliases {
		keyByName[strings.ToLower(a)] = k
	}
	if _, ok := keyByVK[vk]; !ok {
		keyByVK[vk] = k
	}
}

func addMod(name string, vk uint16, ext bool, code string, aliases ...string) {
	addKey(name, vk, ext, code, aliases...)
	keyDefs[len(keyDefs)-1].Mod = true
	k := keyDefs[len(keyDefs)-1]
	keyByName[strings.ToLower(name)] = k
	for _, a := range aliases {
		keyByName[strings.ToLower(a)] = k
	}
	keyByVK[vk] = k
}

// Order of modifiers in a normalised combo.
var modOrder = []string{"Ctrl", "Shift", "Alt", "Win"}

func init() {
	addMod("Ctrl", 0xA2, false, "ControlLeft", "control", "ctl", "lctrl")
	addMod("Shift", 0xA0, false, "ShiftLeft", "lshift")
	addMod("Alt", 0xA4, false, "AltLeft", "lalt", "menu")
	addMod("Win", 0x5B, true, "MetaLeft", "lwin", "meta", "super", "windows")
	for c := 'A'; c <= 'Z'; c++ {
		addKey(string(c), uint16(c), false, "Key"+string(c))
	}
	for c := '0'; c <= '9'; c++ {
		addKey(string(c), uint16(c), false, "Digit"+string(c))
	}
	for i := 1; i <= 24; i++ {
		addKey(fmt.Sprintf("F%d", i), uint16(0x70+i-1), false, fmt.Sprintf("F%d", i))
	}
	for i := 0; i <= 9; i++ {
		addKey(fmt.Sprintf("Num%d", i), uint16(0x60+i), false, fmt.Sprintf("Numpad%d", i), fmt.Sprintf("numpad%d", i))
	}
	addKey("Num*", 0x6A, false, "NumpadMultiply", "nummultiply")
	addKey("Num+", 0x6B, false, "NumpadAdd", "numadd", "numplus")
	addKey("Num-", 0x6D, false, "NumpadSubtract", "numsubtract", "numminus")
	addKey("Num.", 0x6E, false, "NumpadDecimal", "numdecimal")
	addKey("Num/", 0x6F, true, "NumpadDivide", "numdivide")
	addKey("NumEnter", 0x0D, true, "NumpadEnter")
	addKey("Enter", 0x0D, false, "Enter", "return")
	addKey("Esc", 0x1B, false, "Escape", "escape")
	addKey("Tab", 0x09, false, "Tab")
	addKey("Space", 0x20, false, "Space")
	addKey("Backspace", 0x08, false, "Backspace", "bksp")
	addKey("Delete", 0x2E, true, "Delete", "del")
	addKey("Insert", 0x2D, true, "Insert", "ins")
	addKey("Home", 0x24, true, "Home")
	addKey("End", 0x23, true, "End")
	addKey("PageUp", 0x21, true, "PageUp", "pgup")
	addKey("PageDown", 0x22, true, "PageDown", "pgdn")
	addKey("Left", 0x25, true, "ArrowLeft", "arrowleft", "←")
	addKey("Up", 0x26, true, "ArrowUp", "arrowup", "↑")
	addKey("Right", 0x27, true, "ArrowRight", "arrowright", "→")
	addKey("Down", 0x28, true, "ArrowDown", "arrowdown", "↓")
	addKey("PrintScreen", 0x2C, true, "PrintScreen", "prtsc", "printscr")
	addKey("ScrollLock", 0x91, false, "ScrollLock")
	addKey("Pause", 0x13, false, "Pause")
	addKey("CapsLock", 0x14, false, "CapsLock")
	addKey("NumLock", 0x90, true, "NumLock")
	addKey("Menu", 0x5D, true, "ContextMenu", "apps")
	addKey("-", 0xBD, false, "Minus", "minus")
	addKey("=", 0xBB, false, "Equal", "equal", "equals")
	addKey(",", 0xBC, false, "Comma", "comma")
	addKey(".", 0xBE, false, "Period", "period")
	addKey("/", 0xBF, false, "Slash", "slash")
	addKey("`", 0xC0, false, "Backquote", "backquote", "tilde")
	addKey("[", 0xDB, false, "BracketLeft", "bracketleft")
	addKey("]", 0xDD, false, "BracketRight", "bracketright")
	addKey("\\", 0xDC, false, "Backslash", "backslash")
	addKey(";", 0xBA, false, "Semicolon", "semicolon")
	addKey("'", 0xDE, false, "Quote", "quote")
	addKey("VolumeUp", 0xAF, true, "AudioVolumeUp")
	addKey("VolumeDown", 0xAE, true, "AudioVolumeDown")
	addKey("Mute", 0xAD, true, "AudioVolumeMute")
	addKey("PlayPause", 0xB3, true, "MediaPlayPause")
	addKey("NextTrack", 0xB0, true, "MediaTrackNext")
	addKey("PrevTrack", 0xB1, true, "MediaTrackPrevious")
	addKey("StopMedia", 0xB2, true, "MediaStop")
}

// Combo is a key combination: modifiers plus one or more keys, pressed in
// order and released in reverse order.
type Combo struct {
	Keys []keyDef
}

func parseCombo(s string) (Combo, error) {
	var c Combo
	s = strings.TrimSpace(s)
	if s == "" {
		return c, errors.New(T("сочетание не задано", "no key combination"))
	}
	// "+" itself can be a key name only as "Num+"; split on '+' but keep "Num+".
	var parts []string
	cur := ""
	for _, ch := range s {
		if ch == '+' && cur != "" && !strings.EqualFold(strings.TrimSpace(cur), "num") {
			parts = append(parts, strings.TrimSpace(cur))
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if strings.TrimSpace(cur) != "" {
		parts = append(parts, strings.TrimSpace(cur))
	}
	var mods [4]bool
	seen := map[uint16]bool{}
	for _, p := range parts {
		k, ok := keyByName[strings.ToLower(p)]
		if !ok {
			return c, fmt.Errorf(T("неизвестная клавиша %q", "unknown key %q"), p)
		}
		if k.Mod {
			for i, m := range modOrder {
				if m == k.Name {
					mods[i] = true
				}
			}
			continue
		}
		id := k.VK
		if k.Ext {
			id |= 0x8000
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		c.Keys = append(c.Keys, k)
	}
	var out []keyDef
	for i, on := range mods {
		if on {
			out = append(out, keyByName[strings.ToLower(modOrder[i])])
		}
	}
	c.Keys = append(out, c.Keys...)
	if len(c.Keys) == 0 {
		return c, errors.New(T("сочетание не задано", "no key combination"))
	}
	if len(c.Keys) > 6 {
		return c, errors.New(T("слишком много клавиш в сочетании (до 6)", "too many keys in the combination (up to 6)"))
	}
	return c, nil
}

func (c Combo) String() string {
	var n []string
	for _, k := range c.Keys {
		n = append(n, k.Name)
	}
	return strings.Join(n, "+")
}

// repeatable: a single non-modifier key (typematic repeat while held).
func (c Combo) repeatable() bool {
	n := 0
	for _, k := range c.Keys {
		if !k.Mod {
			n++
		}
	}
	return n == 1 && !c.Keys[len(c.Keys)-1].Mod && c.Keys[len(c.Keys)-1].Name != "PrintScreen"
}

// Default output of keypad buttons (what the keypad types without the app).
var defaultKeypadKeys = map[string][keypadButtons]string{
	"123": {"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "="},
	"num": {"Num1", "Num2", "Num3", "Num4", "Num5", "Num6", "Num7", "Num8", "Num9", "Num0", "Num-", "Num+"},
}
