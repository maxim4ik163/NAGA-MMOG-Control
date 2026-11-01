package main

// Platform-independent decisions: which binding a slot has right now, which
// profile is active, DPI stages and values.

// activeProfileFor returns the enabled profile that lists exe, or nil.
func activeProfileFor(d *Data, exe string) *Profile {
	exe = normalizeExe(exe)
	if exe == "" {
		return nil
	}
	for _, p := range d.Profiles {
		if !p.Enabled {
			continue
		}
		for _, e := range p.Exes {
			if e == exe {
				return p
			}
		}
	}
	return nil
}

// resolve: forced global → profile → global → default.
func resolve(d *Data, p *Profile, slot string) (Binding, string) {
	g, hasG := d.Settings.Global[slot]
	hasG = hasG && g.Kind != KindInherit
	if hasG && g.Force {
		return g, "global"
	}
	if p != nil {
		if b, ok := p.Bindings[slot]; ok && b.Kind != KindInherit {
			return b, "profile"
		}
	}
	if hasG {
		return g, "global"
	}
	return Binding{Kind: KindDefault}, "default"
}

// defaultCombo is what a keypad button types without the app.
func defaultCombo(slot string) (Combo, bool) {
	layer, num := slotParts(slot)
	keys, ok := defaultKeypadKeys[layer]
	if !ok || num < 1 || num > keypadButtons {
		return Combo{}, false
	}
	c, err := parseCombo(keys[num-1])
	return c, err == nil
}

func slotParts(slot string) (string, int) {
	for i := 0; i < len(slot); i++ {
		if slot[i] == ':' {
			n := 0
			for _, ch := range slot[i+1:] {
				if ch < '0' || ch > '9' {
					return slot[:i], -1
				}
				n = n*10 + int(ch-'0')
			}
			return slot[:i], n
		}
	}
	return slot, -1
}

// dpiSet: the profile's own stages or the global ones.
func dpiSet(d *Data, p *Profile) DPISet {
	if p != nil && p.DPI != nil && len(p.DPI.Stages) > 0 {
		return *p.DPI
	}
	return d.Settings.DPI
}

// DPI byte on the wire: DPI = 100 + 25 × byte (100 DPI steps = multiples of 4).
func dpiToByte(dpi int) byte {
	dpi = clampInt(dpi, minDPI, maxDPI)
	return byte((dpi - 100) / 25)
}

func byteToDPI(b byte) int { return 100 + 25*int(b) }

func pollCode(hz int) byte {
	switch hz {
	case 125:
		return 8
	case 500:
		return 2
	}
	return 1
}

func pollHz(code byte) int {
	switch code {
	case 8:
		return 125
	case 2:
		return 500
	case 1:
		return 1000
	}
	return 0
}

// nextStage implements the DPI actions; returns the new stage index.
func nextStage(kind string, cur, n int) int {
	if n <= 0 {
		return 0
	}
	switch kind {
	case KindDPIUp:
		if cur < n-1 {
			return cur + 1
		}
		return n - 1
	case KindDPIDown:
		if cur > 0 {
			return cur - 1
		}
		return 0
	case KindDPICycle:
		return (cur + 1) % n
	}
	return clampInt(cur, 0, n-1)
}
