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
	KindMacro    = "macro"     // a macro from the Macros tab
	KindText     = "text"      // type a text (optionally a protected secret), then a key
	KindRun      = "run"       // start a program, open a document, folder or URL
	KindCommand  = "command"   // a command line as in the Win+R box
)

// Ways of typing a text.
const (
	TextUnicode = "unicode" // characters as Unicode input: independent of the keyboard layout
	TextKeys    = "keys"    // real key presses in a given layout (for apps that ignore Unicode input)
)

// Window of a started program.
const (
	ShowNormal = ""
	ShowMin    = "min"
	ShowMax    = "max"
	ShowHidden = "hidden"
)

const (
	maxText      = 2000 // characters
	maxCharDelay = 500  // ms
)

type Binding struct {
	Kind  string `json:"kind"`
	Keys  string `json:"keys,omitempty"`  // "Ctrl+Win+Right"
	Stage int    `json:"stage,omitempty"` // DPI stage (1-based) for dpi-hold
	// Force (global bindings only): wins over any profile.
	Force bool `json:"force,omitempty"`

	MacroID string `json:"macroId,omitempty"` // KindMacro

	// KindText. A secret is kept only encrypted for the current Windows user
	// (Enc, DPAPI) and never sent back as plain text; Text then carries a new
	// value from the UI only.
	Text      string `json:"text,omitempty"`
	Enc       string `json:"enc,omitempty"`
	Secret    bool   `json:"secret,omitempty"`
	Method    string `json:"method,omitempty"`    // TextUnicode or TextKeys
	Layout    string `json:"layout,omitempty"`    // TextKeys: keyboard layout (HKL, hex); "" = the window's current one
	After     string `json:"after,omitempty"`     // key combination pressed after the text, e.g. "Enter"
	CharDelay int    `json:"charDelay,omitempty"` // ms between characters

	// KindRun: a program, document, folder or URL with arguments.
	Path string `json:"path,omitempty"`
	Args string `json:"args,omitempty"`
	// KindCommand: one line as typed into Win+R ("%LOCALAPPDATA%", "cmd /c …").
	Cmd string `json:"cmd,omitempty"`
	// KindRun, KindCommand: ShowNormal, ShowMin, ShowMax or ShowHidden.
	Show string `json:"show,omitempty"`
}

// ---------------------------------------------------------------------------
// Macros (played by the app)

// Macro modes, as in SM809 Control.
const (
	MacroHold   = 1 // repeat while the button is held
	MacroTimes  = 2 // run Repeat times
	MacroToggle = 3 // repeat until the button is pressed again
)

// Macro event types.
const (
	EvDown  = "keydown" // press a key or a mouse button (Key)
	EvUp    = "keyup"   // release it
	EvDelay = "delay"   // only wait (Delay)
	EvMove  = "move"    // move the cursor by DX, DY pixels
	EvWheel = "wheel"   // scroll Wheel notches (+ = up / away)
)

const (
	maxMacroEvents = 5000
	maxMacroDelay  = 60000 // ms
	maxMacroRepeat = 1000
)

// Mouse buttons a macro can press (names next to the key names).
var macroMouseButtons = []string{"MouseLeft", "MouseRight", "MouseMiddle", "MouseBack", "MouseForward"}

func isMacroMouse(name string) bool {
	for _, b := range macroMouseButtons {
		if strings.EqualFold(b, name) {
			return true
		}
	}
	return false
}

type MacroEvent struct {
	Type  string `json:"type"`
	Key   string `json:"key,omitempty"`   // key name (as in combinations) or Mouse*
	Delay int    `json:"delay"`           // ms before the event
	DX    int    `json:"dx,omitempty"`    // EvMove
	DY    int    `json:"dy,omitempty"`    // EvMove
	Wheel int    `json:"wheel,omitempty"` // EvWheel
}

type Macro struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Mode   int          `json:"mode"`   // MacroHold, MacroTimes, MacroToggle
	Repeat int          `json:"repeat"` // MacroTimes
	Events []MacroEvent `json:"events"`
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
	// D: while the cursor moves with the button held (a drag), a release is
	// held back this long. Ending a drag a little later is harmless, and
	// contact drops under load can be much longer than click bounces.
	D int `json:"d"`
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

	// FGCheckMs: how often the foreground window is checked in addition to
	// the Windows foreground event (for windows that do not raise it).
	FGCheckMs int `json:"fgCheckMs"`

	Language string `json:"language"`
	Debug    bool   `json:"debug"`
}

// dataVersion 2 added Chatter.D; 3 added macros, text/run/command actions,
// Settings.FGCheckMs.
const dataVersion = 3

type Data struct {
	Version  int        `json:"version"`
	Profiles []*Profile `json:"profiles"`
	Macros   []*Macro   `json:"macros"`
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
		Version: dataVersion,
		Settings: Settings{
			Global:     map[string]Binding{},
			DPI:        DPISet{Stages: []int{800, 1400, 2400, 3600}, Start: 1},
			PollRate:   1000,
			LightWheel: true, LightLogo: true, LightPad: true,
			Chatter: Chatter{T: 45, H: 15, G: 100, D: 250},
			OSDDPI:  true, OSDProfile: true,
			Popup:     defaultPopup(),
			KeyRepeat: true,
			FGCheckMs: defaultFGCheckMs,
		},
	}
}

// ---------------------------------------------------------------------------
// Validation

const (
	minDPI = 100
	maxDPI = 5600

	defaultFGCheckMs = 2000
	minFGCheckMs     = 250
	maxFGCheckMs     = 30000
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
	at := slotName(slot)
	switch b.Kind {
	case KindInherit, KindDefault, KindDisable, KindDPIUp, KindDPIDown, KindDPICycle:
	case KindKeys:
		if _, err := parseCombo(b.Keys); err != nil {
			return fmt.Errorf("%s: %v", at, err)
		}
	case KindDPIHold:
		if b.Stage < 1 || b.Stage > 8 {
			return fmt.Errorf(T("%s: неверная ступень DPI", "%s: invalid DPI stage"), at)
		}
	case KindMacro:
		if b.MacroID == "" {
			return fmt.Errorf(T("%s: макрос не выбран", "%s: no macro selected"), at)
		}
	case KindText:
		if err := validateText(at, b); err != nil {
			return err
		}
	case KindRun:
		b.Path = strings.Trim(strings.TrimSpace(b.Path), `"`)
		b.Args = strings.TrimSpace(b.Args)
		if b.Path == "" {
			return fmt.Errorf(T("%s: не указано, что запускать", "%s: nothing to start"), at)
		}
	case KindCommand:
		b.Cmd = strings.TrimSpace(b.Cmd)
		if b.Cmd == "" {
			return fmt.Errorf(T("%s: команда не задана", "%s: no command"), at)
		}
		if len([]rune(b.Cmd)) > 2000 {
			return fmt.Errorf(T("%s: команда слишком длинная", "%s: the command is too long"), at)
		}
	default:
		return fmt.Errorf(T("%s: неизвестное действие %q", "%s: unknown action %q"), at, b.Kind)
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
	if b.Kind != KindDPIHold {
		b.Stage = 0
	}
	if b.Kind != KindMacro {
		b.MacroID = ""
	}
	if b.Kind != KindText {
		b.Text, b.Enc, b.Secret, b.Method, b.Layout, b.After, b.CharDelay = "", "", false, "", "", "", 0
	}
	if b.Kind != KindRun {
		b.Path, b.Args = "", ""
	}
	if b.Kind != KindCommand {
		b.Cmd = ""
	}
	if b.Kind == KindRun || b.Kind == KindCommand {
		switch b.Show {
		case ShowNormal, ShowMin, ShowMax, ShowHidden:
		default:
			b.Show = ShowNormal
		}
	} else {
		b.Show = ""
	}
	return nil
}

// validateText checks and normalises a text binding.
func validateText(at string, b *Binding) error {
	if b.Text == "" && b.Enc == "" {
		return fmt.Errorf(T("%s: текст не задан", "%s: no text"), at)
	}
	if len([]rune(b.Text)) > maxText {
		return fmt.Errorf(T("%s: текст длиннее %d символов", "%s: the text is longer than %d characters"), at, maxText)
	}
	switch b.Method {
	case "":
		b.Method = TextUnicode
	case TextUnicode, TextKeys:
	default:
		return fmt.Errorf(T("%s: неизвестный способ ввода %q", "%s: unknown input method %q"), at, b.Method)
	}
	if b.Method != TextKeys {
		b.Layout = ""
	} else if b.Layout != "" {
		if _, err := strconv.ParseUint(b.Layout, 16, 64); err != nil || len(b.Layout) > 16 {
			return fmt.Errorf(T("%s: неверная раскладка %q", "%s: invalid layout %q"), at, b.Layout)
		}
		b.Layout = strings.ToUpper(b.Layout)
	}
	if strings.TrimSpace(b.After) != "" {
		c, err := parseCombo(b.After)
		if err != nil {
			return fmt.Errorf(T("%s: клавиша после текста: %v", "%s: key after the text: %v"), at, err)
		}
		b.After = c.String()
	} else {
		b.After = ""
	}
	b.CharDelay = clampInt(b.CharDelay, 0, maxCharDelay)
	if !b.Secret && b.Text == "" { // a secret was turned off without a new text
		return fmt.Errorf(T("%s: введите текст заново — сохранённый секрет не показывается", "%s: enter the text again — a stored secret is never shown"), at)
	}
	if !b.Secret {
		b.Enc = ""
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
	c.T, c.H, c.G, c.D = clampInt(c.T, 0, 150), clampInt(c.H, 0, 60), clampInt(c.G, 0, 250), clampInt(c.D, 0, 1000)
	if c.G < c.T {
		c.G = c.T
	}
	if s.Popup.Width == 0 {
		s.Popup = defaultPopup()
	}
	validatePopup(&s.Popup)
	if s.FGCheckMs == 0 {
		s.FGCheckMs = defaultFGCheckMs
	}
	s.FGCheckMs = clampInt(s.FGCheckMs, minFGCheckMs, maxFGCheckMs)
	return nil
}

var popupPositions = []string{"bottom-center", "top-center", "center", "top-left", "top-right", "bottom-left", "bottom-right", "cursor"}

// validatePopup keeps the popup style within sane limits.
func validatePopup(p *PopupStyle) {
	ok := false
	for _, v := range popupPositions {
		ok = ok || p.Position == v
	}
	if !ok {
		p.Position = "bottom-center"
	}
	p.OffsetX, p.OffsetY = clampInt(p.OffsetX, -4000, 4000), clampInt(p.OffsetY, -4000, 4000)
	p.Width, p.Height = clampInt(p.Width, 60, 2000), clampInt(p.Height, 30, 2000)
	p.CornerRadius = clampInt(p.CornerRadius, 0, 500)
	p.Opacity = clampInt(p.Opacity, 0, 255)
	p.FontSize, p.CaptionSize = clampInt(p.FontSize, 6, 300), clampInt(p.CaptionSize, 6, 200)
	p.DurationMs, p.FadeMs = clampInt(p.DurationMs, 100, 60000), clampInt(p.FadeMs, 0, 10000)
	if strings.TrimSpace(p.Font) == "" {
		p.Font = "Segoe UI"
	}
	d := defaultPopup()
	for _, c := range []struct {
		v   *string
		def string
	}{{&p.Background, d.Background}, {&p.TextColor, d.TextColor}, {&p.CaptionColor, d.CaptionColor}, {&p.AccentColor, d.AccentColor}, {&p.DotColor, d.DotColor}} {
		if !validColor(*c.v) {
			*c.v = c.def
		}
	}
}

func validColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

// validateMacro normalises a macro.
func validateMacro(m *Macro) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return errors.New(T("у макроса должно быть имя", "the macro needs a name"))
	}
	switch m.Mode {
	case MacroHold, MacroTimes, MacroToggle:
	default:
		m.Mode = MacroTimes
	}
	m.Repeat = clampInt(m.Repeat, 1, maxMacroRepeat)
	if len(m.Events) > maxMacroEvents {
		return fmt.Errorf(T("«%s»: больше %d действий", "%q: more than %d actions"), m.Name, maxMacroEvents)
	}
	if m.Events == nil {
		m.Events = []MacroEvent{}
	}
	for i := range m.Events {
		e := &m.Events[i]
		e.Delay = clampInt(e.Delay, 0, maxMacroDelay)
		switch e.Type {
		case EvDown, EvUp:
			name, ok := macroKeyName(e.Key)
			if !ok {
				return fmt.Errorf(T("«%s», действие %d: неизвестная клавиша %q", "%q, action %d: unknown key %q"), m.Name, i+1, e.Key)
			}
			e.Key = name
			e.DX, e.DY, e.Wheel = 0, 0, 0
		case EvDelay:
			e.Key, e.DX, e.DY, e.Wheel = "", 0, 0, 0
		case EvMove:
			e.Key, e.Wheel = "", 0
			e.DX, e.DY = clampInt(e.DX, -10000, 10000), clampInt(e.DY, -10000, 10000)
		case EvWheel:
			e.Key, e.DX, e.DY = "", 0, 0
			e.Wheel = clampInt(e.Wheel, -100, 100)
		default:
			return fmt.Errorf(T("«%s», действие %d: неизвестный тип %q", "%q, action %d: unknown type %q"), m.Name, i+1, e.Type)
		}
	}
	return nil
}

// macroKeyName: the normalised name of a key or mouse button for macros.
func macroKeyName(s string) (string, bool) {
	for _, b := range macroMouseButtons {
		if strings.EqualFold(b, strings.TrimSpace(s)) {
			return b, true
		}
	}
	k, ok := keyByName[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return "", false
	}
	return k.Name, true
}

// macroUse: where a macro is assigned ("" = nowhere).
func macroUse(d *Data, id string) string {
	for _, slot := range allSlots() {
		if b, ok := d.Settings.Global[slot]; ok && b.Kind == KindMacro && b.MacroID == id {
			return T("Глобальные", "Global") + ", " + slotName(slot)
		}
	}
	for _, p := range d.Profiles {
		for _, slot := range allSlots() {
			if b, ok := p.Bindings[slot]; ok && b.Kind == KindMacro && b.MacroID == id {
				return p.Name + ", " + slotName(slot)
			}
		}
	}
	return ""
}

func (d *Data) macro(id string) *Macro {
	for _, m := range d.Macros {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// checkMacroRefs: every macro binding must point to an existing macro.
func checkMacroRefs(d *Data) error {
	check := func(owner string, m map[string]Binding) error {
		for _, slot := range allSlots() {
			if b, ok := m[slot]; ok && b.Kind == KindMacro && d.macro(b.MacroID) == nil {
				return fmt.Errorf(T("%s, %s: макрос не найден", "%s, %s: the macro does not exist"), owner, slotName(slot))
			}
		}
		return nil
	}
	if err := check(T("Глобальные", "Global"), d.Settings.Global); err != nil {
		return err
	}
	for _, p := range d.Profiles {
		if err := check(p.Name, p.Bindings); err != nil {
			return err
		}
	}
	return nil
}

// exe → profile: every exe may belong to one enabled profile only.
func validateData(d *Data) error {
	if d.Profiles == nil {
		d.Profiles = []*Profile{} // the UI expects a list, never null
	}
	if d.Macros == nil {
		d.Macros = []*Macro{}
	}
	mids := map[string]bool{}
	for _, m := range d.Macros {
		if m == nil {
			return errors.New(T("пустой макрос", "empty macro"))
		}
		if err := validateMacro(m); err != nil {
			return err
		}
		if m.ID == "" || mids[m.ID] {
			m.ID = newID("m")
		}
		mids[m.ID] = true
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
	if err := validateSettings(&d.Settings); err != nil {
		return err
	}
	return checkMacroRefs(d)
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
			if d.Version < 2 {
				d.Settings.Chatter.D = 250
			}
			d.Version = dataVersion
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
