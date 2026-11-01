package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Data model (persisted as data.json)

// Button slots. Keypad buttons exist in two layers chosen by the hardware
// switch on the bottom of the mouse (123 / NUM); mouse buttons 4 and 5 (the
// two next to the left button) have one layer.
const keypadButtons = 12

// Slot keys: "123:1".."123:12", "num:1".."num:12", "m:4", "m:5".
func keypadSlot(layer string, button int) string { return layer + ":" + strconv.Itoa(button) }

var mouseSlots = []string{"m:4", "m:5"}

func allSlots() []string {
	var s []string
	for _, l := range []string{"123", "num"} {
		for b := 1; b <= keypadButtons; b++ {
			s = append(s, keypadSlot(l, b))
		}
	}
	return append(s, mouseSlots...)
}

// Binding kinds.
const (
	KindInherit  = ""          // not set here: fall back (profile → global → default)
	KindDefault  = "default"   // what the button does without the app (original key / mouse button)
	KindKeys     = "keys"      // a key or a key combination, held while the button is held
	KindDisable  = "disable"   // nothing
	KindDPIUp    = "dpi-up"    // next DPI stage (stops at the last)
	KindDPIDown  = "dpi-down"  // previous DPI stage
	KindDPICycle = "dpi-cycle" // next stage, wraps around
	KindDPIHold  = "dpi-hold"  // sniper: stage Stage while held
)

type Binding struct {
	Kind  string `json:"kind"`
	Keys  string `json:"keys,omitempty"`  // "Ctrl+Win+Right"
	Stage int    `json:"stage,omitempty"` // DPI stage (1-based) for dpi-hold
	// Force (global bindings only): wins over any profile.
	Force bool `json:"force,omitempty"`
}

type DPISet struct {
	Stages []int `json:"stages"` // DPI values, 1..8 entries, 100..5600 step 100
	Start  int   `json:"start"`  // stage index (0-based) selected when this set becomes active
}

type Profile struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Exes     []string           `json:"exes"` // lower-case file names, e.g. "acad.exe"
	Enabled  bool               `json:"enabled"`
	Bindings map[string]Binding `json:"bindings"`
	DPI      *DPISet            `json:"dpi,omitempty"` // nil = global stages
	OSD      bool               `json:"osd"`           // popup when this profile becomes active
}

type Chatter struct {
	Enabled bool `json:"enabled"`
	// Release shorter than T ms → bounce (merged). Release shorter than G ms
	// after a press shorter than H ms → bounce too.
	T int `json:"t"`
	H int `json:"h"`
	G int `json:"g"`
	// Calibrated is the time of the last calibration (zero = never).
	Calibrated time.Time `json:"calibrated"`
	Score      string    `json:"score,omitempty"` // e.g. "singles 14/15, doubles 9/10"
}

type PopupStyle struct {
	Position     string `json:"position"`
	OffsetX      int    `json:"offsetX"`
	OffsetY      int    `json:"offsetY"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	CornerRadius int    `json:"cornerRadius"`
	Background   string `json:"background"`
	Opacity      int    `json:"opacity"`
	TextColor    string `json:"textColor"`
	CaptionColor string `json:"captionColor"`
	AccentColor  string `json:"accentColor"`
	DotColor     string `json:"dotColor"`
	Font         string `json:"font"`
	FontSize     int    `json:"fontSize"`
	CaptionSize  int    `json:"captionSize"`
	Bold         bool   `json:"bold"`
	DurationMs   int    `json:"durationMs"`
	FadeMs       int    `json:"fadeMs"`
}

type Settings struct {
	Global   map[string]Binding `json:"global"`
	DPI      DPISet             `json:"dpi"`
	PollRate int                `json:"pollRate"` // Hz: 125, 500, 1000
	// Lights: scroll wheel, logo, side keypad.
	LightWheel bool `json:"lightWheel"`
	LightLogo  bool `json:"lightLogo"`
	LightPad   bool `json:"lightPad"`

	Chatter Chatter `json:"chatter"`

	OSDDPI     bool       `json:"osdDpi"`
	OSDProfile bool       `json:"osdProfile"`
	Popup      PopupStyle `json:"popup"`

	KeyRepeat bool `json:"keyRepeat"` // typematic repeat for held single keys

	Language string `json:"language"`
	Debug    bool   `json:"debug"`
}

type Data struct {
	Version  int        `json:"version"`
	Profiles []*Profile `json:"profiles"`
	Settings Settings   `json:"settings"`
}

// ---------------------------------------------------------------------------
// Defaults

func defaultPopup() PopupStyle {
	return PopupStyle{
		Position: "bottom-center", OffsetY: 120, Width: 240, Height: 120, CornerRadius: 18,
		Background: "#1e1f22", Opacity: 225, TextColor: "#ffffff", CaptionColor: "#9aa0a6",
		AccentColor: "#44d62c", DotColor: "#4a4d52", Font: "Segoe UI", FontSize: 40, CaptionSize: 13,
		Bold: true, DurationMs: 1200, FadeMs: 350,
	}
}

func defaultData() *Data {
	return &Data{
		Version: 1,
		Settings: Settings{
			Global:     map[string]Binding{},
			DPI:        DPISet{Stages: []int{800, 1400, 2400, 3600}, Start: 1},
			PollRate:   1000,
			LightWheel: true, LightLogo: true, LightPad: true,
			Chatter: Chatter{T: 45, H: 15, G: 100},
			OSDDPI:  true, OSDProfile: true,
			Popup:     defaultPopup(),
			KeyRepeat: true,
		},
	}
}

// ---------------------------------------------------------------------------
// Validation

const (
	minDPI = 100
	maxDPI = 5600
)

func validateDPISet(s *DPISet) error {
	if len(s.Stages) < 1 || len(s.Stages) > 8 {
		return errors.New(T("ступеней DPI должно быть от 1 до 8", "there must be 1 to 8 DPI stages"))
	}
	for i, v := range s.Stages {
		if v < minDPI || v > maxDPI || v%100 != 0 {
			return fmt.Errorf(T("ступень %d: %d DPI — допустимо 100..5600 с шагом 100", "stage %d: %d DPI — allowed 100..5600 in steps of 100"), i+1, v)
		}
	}
	if s.Start < 0 || s.Start >= len(s.Stages) {
		s.Start = 0
	}
	return nil
}

func validateBinding(slot string, b *Binding, global bool) error {
	switch b.Kind {
	case KindInherit, KindDefault, KindDisable, KindDPIUp, KindDPIDown, KindDPICycle:
	case KindKeys:
		if _, err := parseCombo(b.Keys); err != nil {
			return fmt.Errorf("%s: %v", slotName(slot), err)
		}
	case KindDPIHold:
		if b.Stage < 1 || b.Stage > 8 {
			return fmt.Errorf(T("%s: неверная ступень DPI", "%s: invalid DPI stage"), slotName(slot))
		}
	default:
		return fmt.Errorf(T("%s: неизвестное действие %q", "%s: unknown action %q"), slotName(slot), b.Kind)
	}
	if !global {
		b.Force = false
	}
	if b.Kind != KindKeys {
		b.Keys = ""
	} else {
		c, _ := parseCombo(b.Keys)
		b.Keys = c.String()
	}
	return nil
}

func validateBindings(m map[string]Binding, global bool) error {
	valid := map[string]bool{}
	for _, s := range allSlots() {
		valid[s] = true
	}
	for slot, b := range m {
		if !valid[slot] {
			delete(m, slot)
			continue
		}
		if err := validateBinding(slot, &b, global); err != nil {
			return err
		}
		if b.Kind == KindInherit && !b.Force {
			delete(m, slot)
			continue
		}
		m[slot] = b
	}
	return nil
}

func normalizeExe(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, `"`)
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func validateProfile(p *Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New(T("у профиля должно быть имя", "the profile needs a name"))
	}
	exes := []string{}
	seen := map[string]bool{}
	for _, e := range p.Exes {
		e = normalizeExe(e)
		if e == "" || seen[e] {
			continue
		}
		if !strings.HasSuffix(e, ".exe") {
			return fmt.Errorf(T("%q: укажите имя файла программы, например acad.exe", "%q: give the program's file name, e.g. acad.exe"), e)
		}
		seen[e] = true
		exes = append(exes, e)
	}
	p.Exes = exes
	if p.Bindings == nil {
		p.Bindings = map[string]Binding{}
	}
	if err := validateBindings(p.Bindings, false); err != nil {
		return err
	}
	if p.DPI != nil {
		if err := validateDPISet(p.DPI); err != nil {
			return err
		}
	}
	return nil
}

func validateSettings(s *Settings) error {
	if s.Global == nil {
		s.Global = map[string]Binding{}
	}
	if err := validateBindings(s.Global, true); err != nil {
		return err
	}
	if err := validateDPISet(&s.DPI); err != nil {
		return err
	}
	switch s.PollRate {
	case 125, 500, 1000:
	default:
		return errors.New(T("частота опроса: 125, 500 или 1000 Гц", "polling rate must be 125, 500 or 1000 Hz"))
	}
	c := &s.Chatter
	c.T, c.H, c.G = clampInt(c.T, 0, 150), clampInt(c.H, 0, 60), clampInt(c.G, 0, 250)
	if c.G < c.T {
		c.G = c.T
	}
	if s.Popup.Width == 0 {
		s.Popup = defaultPopup()
	}
	return nil
}

// exe → profile: every exe may belong to one enabled profile only.
func validateData(d *Data) error {
	if d.Profiles == nil {
		d.Profiles = []*Profile{} // the UI expects a list, never null
	}
	owner := map[string]string{}
	ids := map[string]bool{}
	for _, p := range d.Profiles {
		if err := validateProfile(p); err != nil {
			return fmt.Errorf("%s: %v", p.Name, err)
		}
		if p.ID == "" || ids[p.ID] {
			p.ID = newID("p")
		}
		ids[p.ID] = true
		if !p.Enabled {
			continue
		}
		for _, e := range p.Exes {
			if o, ok := owner[e]; ok {
				return fmt.Errorf(T("%s указан и в «%s», и в «%s»", "%s is listed in both %q and %q"), e, o, p.Name)
			}
			owner[e] = p.Name
		}
	}
	return validateSettings(&d.Settings)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func slotName(slot string) string {
	l, b, _ := strings.Cut(slot, ":")
	switch l {
	case "123":
		return T("кнопка ", "button ") + b + " (123)"
	case "num":
		return T("кнопка ", "button ") + b + " (NUM)"
	case "m":
		return T("кнопка мыши ", "mouse button ") + b
	}
	return slot
}

// ---------------------------------------------------------------------------
// Persistence

type Store struct {
	mu   sync.Mutex
	dir  string
	data *Data
}

var (
	idMu      sync.Mutex
	idCounter int64
)

func newID(prefix string) string {
	idMu.Lock()
	idCounter++
	n := idCounter
	idMu.Unlock()
	return prefix + strconv.FormatInt(time.Now().UnixNano()/1000+n, 36)
}

func loadStore(dir string) *Store {
	s := &Store{dir: dir, data: defaultData()}
	if b, err := os.ReadFile(filepath.Join(dir, "data.json")); err == nil {
		d := defaultData()
		if err := json.Unmarshal(b, d); err == nil {
			if err := validateData(d); err == nil {
				s.data = d
			} else {
				_ = os.WriteFile(filepath.Join(dir, "data.invalid.json"), b, 0644)
			}
		} else {
			_ = os.WriteFile(filepath.Join(dir, "data.broken.json"), b, 0644)
		}
	}
	_ = validateData(s.data)
	return s
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// save must be called with mu held.
func (s *Store) save() error { return writeJSONAtomic(filepath.Join(s.dir, "data.json"), s.data) }

func (s *Store) profile(id string) *Profile {
	for _, p := range s.data.Profiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// clone returns a deep copy of the data (for handing to the engine / UI).
func (d *Data) clone() *Data {
	b, _ := json.Marshal(d)
	c := &Data{}
	_ = json.Unmarshal(b, c)
	return c
}
