//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type heldAction struct {
	kind  string
	combo Combo
	rep   *repeater
}

type Engine struct {
	mu    sync.Mutex
	store *Store
	data  *Data // validated copy of the store's data

	ownExe string
	fgExe  string
	active *Profile
	setKey string // "global" or the id of the profile whose DPI stages are used
	stage  int
	hold   int // DPI while a "hold" button is pressed, 0 = none

	held map[string]*heldAction

	// device
	ctl     *controlDev
	dev     deviceState
	devErr  string
	wake    chan struct{}
	pad     padInfo
	padRd   *padReader
	padDec  *keypadDecoder
	padErr  string
	padSeen bool // keypad events seen since start (for the status line)

	// calibration recordings
	calib      map[string][]clickEvent
	calibPhase string

	onChange func()
	onOSD    func(osdContent)
}

func newEngine(st *Store) *Engine {
	exe, _ := os.Executable()
	e := &Engine{
		store:  st,
		ownExe: normalizeExe(filepath.Base(exe)),
		held:   map[string]*heldAction{},
		wake:   make(chan struct{}, 1),
		padDec: newKeypadDecoder(),
		calib:  map[string][]clickEvent{},
		pad:    padInfo{Status: padAbsent},
	}
	st.mu.Lock()
	e.data = st.data.clone()
	st.mu.Unlock()
	_ = validateData(e.data)
	e.setKey = "global"
	e.stage = e.data.Settings.DPI.Start
	return e
}

func (e *Engine) changed() {
	if e.onChange != nil {
		e.onChange()
	}
}

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// ---------------------------------------------------------------------------
// Configuration

// reload takes new data from the store (after a save).
func (e *Engine) reload() {
	e.store.mu.Lock()
	d := e.store.data.clone()
	e.store.mu.Unlock()
	_ = validateData(d)
	e.mu.Lock()
	e.data = d
	e.applyContextLocked(false)
	e.mu.Unlock()
	e.kick()
	e.changed()
}

// applyContextLocked recomputes the active profile, the DPI stage set, the
// mouse-button remapping and the chatter filter. osd: show popups.
func (e *Engine) applyContextLocked(osd bool) {
	var prevID string
	if e.active != nil {
		prevID = e.active.ID
	}
	prev := e.active
	e.active = activeProfileFor(e.data, e.fgExe)
	key := "global"
	if e.active != nil && e.active.DPI != nil {
		key = e.active.ID
	}
	set := dpiSet(e.data, e.active)
	if key != e.setKey {
		e.setKey = key
		e.stage = set.Start
	}
	e.stage = clampInt(e.stage, 0, len(set.Stages)-1)
	if hook != nil {
		for i, slot := range mouseSlots {
			b, _ := resolve(e.data, e.active, slot)
			hook.remap[i+1].Store(b.Kind != KindDefault)
		}
		c := e.data.Settings.Chatter
		hook.filter.set(c.Enabled, c.params())
	}
	var newID string
	if e.active != nil {
		newID = e.active.ID
	}
	if osd && newID != prevID && e.data.Settings.OSDProfile && e.onOSD != nil {
		show := (e.active != nil && e.active.OSD) || (prev != nil && prev.OSD)
		if show {
			name := T("Без профиля", "No profile")
			if e.active != nil {
				name = e.active.Name
			}
			e.onOSD(osdContent{Caption: T("Профиль", "Profile"), Value: name, Accent: parseColor(e.data.Settings.Popup.AccentColor, 0x44d62c), Style: e.data.Settings.Popup})
		}
	}
}

// setForeground is called when another window comes to the front.
func (e *Engine) setForeground(exe string) {
	if exe == "" || exe == e.ownExe || exe == "msedgewebview2.exe" {
		return // our own settings window keeps the current profile
	}
	e.mu.Lock()
	if exe == e.fgExe {
		e.mu.Unlock()
		return
	}
	e.fgExe = exe
	before := e.active
	e.applyContextLocked(true)
	changedProfile := before != e.active
	e.mu.Unlock()
	if changedProfile {
		logf("foreground %s → profile %s", exe, e.profileName())
		e.kick()
	}
	e.changed()
}

func (e *Engine) profileName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active == nil {
		return "-"
	}
	return e.active.Name
}

// ---------------------------------------------------------------------------
// Button events (keypad reader goroutine, mouse hook channel)

func (e *Engine) handleKey(ev KeyEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !ev.Down {
		h := e.held[ev.Slot]
		if h == nil {
			return
		}
		delete(e.held, ev.Slot)
		switch h.kind {
		case KindKeys, KindDefault:
			if h.rep != nil {
				h.rep.halt()
			}
			releaseCombo(h.combo)
		case KindDPIHold:
			e.hold = 0
			e.kick()
		}
		return
	}
	if e.held[ev.Slot] != nil {
		return // already down (repeated report)
	}
	b, _ := resolve(e.data, e.active, ev.Slot)
	h := &heldAction{kind: b.Kind}
	switch b.Kind {
	case KindDefault:
		c, ok := defaultCombo(ev.Slot)
		if !ok {
			return
		}
		h.combo = c
		pressCombo(c)
		if e.data.Settings.KeyRepeat && c.repeatable() {
			h.rep = &repeater{}
			h.rep.start(c)
		}
	case KindKeys:
		c, err := parseCombo(b.Keys)
		if err != nil {
			return
		}
		h.combo = c
		pressCombo(c)
		if e.data.Settings.KeyRepeat && c.repeatable() {
			h.rep = &repeater{}
			h.rep.start(c)
		}
	case KindDPIUp, KindDPIDown, KindDPICycle:
		set := dpiSet(e.data, e.active)
		e.stage = nextStage(b.Kind, e.stage, len(set.Stages))
		e.showDPILocked(set)
		e.kick()
	case KindDPIHold:
		set := dpiSet(e.data, e.active)
		if b.Stage >= 1 && b.Stage <= len(set.Stages) {
			e.hold = set.Stages[b.Stage-1]
			if e.data.Settings.OSDDPI && e.onOSD != nil {
				e.onOSD(osdContent{Caption: T("DPI (удержание)", "DPI (hold)"), Value: fmt.Sprint(e.hold),
					Accent: parseColor(e.data.Settings.Popup.AccentColor, 0x44d62c), Style: e.data.Settings.Popup})
			}
			e.kick()
		}
	}
	e.held[ev.Slot] = h
	if strings.HasPrefix(ev.Slot, "123:") || strings.HasPrefix(ev.Slot, "num:") {
		e.padSeen = true
	}
}

func (e *Engine) showDPILocked(set DPISet) {
	if !e.data.Settings.OSDDPI || e.onOSD == nil {
		return
	}
	e.onOSD(osdContent{Caption: "DPI", Value: fmt.Sprint(set.Stages[e.stage]), Dots: len(set.Stages), Active: e.stage,
		Accent: parseColor(e.data.Settings.Popup.AccentColor, 0x44d62c), Style: e.data.Settings.Popup})
}

// setStage is used by the tray menu and the UI.
func (e *Engine) setStage(i int) {
	e.mu.Lock()
	set := dpiSet(e.data, e.active)
	if i >= 0 && i < len(set.Stages) {
		e.stage = i
		e.showDPILocked(set)
	}
	e.mu.Unlock()
	e.kick()
	e.changed()
}

// releaseAll lets go of everything (keypad gone, exit).
func (e *Engine) releaseAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for slot, h := range e.held {
		if h.rep != nil {
			h.rep.halt()
		}
		if h.kind == KindKeys || h.kind == KindDefault {
			releaseCombo(h.combo)
		}
		delete(e.held, slot)
	}
	e.hold = 0
}

func (e *Engine) runHookEvents() {
	for ev := range hook.events {
		e.handleKey(ev)
	}
}

// ---------------------------------------------------------------------------
// Device worker: keeps the mouse in the wanted state and the keypad reader
// running. Woken by kick() (settings, DPI buttons, device change) and every
// few seconds.

func (e *Engine) desiredLocked() deviceState {
	set := dpiSet(e.data, e.active)
	dpi := set.Stages[clampInt(e.stage, 0, len(set.Stages)-1)]
	if e.hold > 0 {
		dpi = e.hold
	}
	s := e.data.Settings
	return deviceState{DPI: dpiToByte(dpi), Poll: pollCode(s.PollRate), LEDs: [3]bool{s.LightWheel, s.LightLogo, s.LightPad}}
}

func (e *Engine) runDevice() {
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		e.syncDevice()
		e.syncPad()
		select {
		case <-e.wake:
		case <-t.C:
		}
	}
}

func (e *Engine) syncDevice() {
	e.mu.Lock()
	ctl := e.ctl
	e.mu.Unlock()
	if ctl != nil && findControlPath() != ctl.path {
		e.dropControl("the mouse was disconnected")
		ctl = nil
	}
	if ctl == nil {
		path := findControlPath()
		if path == "" {
			return
		}
		c, err := openControl(path)
		if err != nil {
			e.setDevErr(err.Error())
			return
		}
		st, err := c.readState()
		if err != nil {
			c.close()
			e.setDevErr(err.Error())
			return
		}
		logf("mouse connected: fw %s, DPI %d, poll %d Hz, LEDs %v", st.FW, byteToDPI(st.DPI), pollHz(st.Poll), st.LEDs)
		e.mu.Lock()
		e.ctl, e.dev, e.devErr = c, st, ""
		e.mu.Unlock()
		e.changed()
		ctl = c
	}
	e.mu.Lock()
	want := e.desiredLocked()
	have := e.dev
	e.mu.Unlock()
	var err error
	if want.DPI != have.DPI {
		if err = ctl.setDPI(want.DPI); err == nil {
			have.DPI = want.DPI
		}
	}
	if err == nil && want.Poll != have.Poll {
		if err = ctl.setPoll(want.Poll); err == nil {
			have.Poll = want.Poll
		}
	}
	for i := range ledIDs {
		if err == nil && want.LEDs[i] != have.LEDs[i] {
			if err = ctl.setLED(ledIDs[i], want.LEDs[i]); err == nil {
				have.LEDs[i] = want.LEDs[i]
			}
		}
	}
	e.mu.Lock()
	changed := e.dev != have
	e.dev = have
	e.mu.Unlock()
	if err != nil {
		logf("device: %v", err)
		e.dropControl(err.Error())
		return
	}
	if changed {
		e.changed()
	}
}

func (e *Engine) dropControl(reason string) {
	e.mu.Lock()
	if e.ctl != nil {
		e.ctl.close()
		e.ctl = nil
	}
	e.dev = deviceState{}
	e.devErr = reason
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) setDevErr(s string) {
	e.mu.Lock()
	e.devErr = s
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) syncPad() {
	e.mu.Lock()
	running := e.padRd != nil
	e.mu.Unlock()
	if running {
		return
	}
	info := findPad()
	e.mu.Lock()
	old := e.pad.Status
	e.pad = info
	e.mu.Unlock()
	if info.Path == "" {
		if old != info.Status {
			e.changed()
		}
		return
	}
	rd, err := openPad(info.Path)
	if err != nil {
		e.mu.Lock()
		e.pad.Status, e.padErr = padError, err.Error()
		e.mu.Unlock()
		e.changed()
		return
	}
	e.mu.Lock()
	e.padRd, e.pad.Status, e.padErr = rd, padReady, ""
	e.mu.Unlock()
	logf("keypad: reading %s", info.Path)
	e.changed()
	go e.readPad(rd)
}

func (e *Engine) readPad(rd *padReader) {
	buf := make([]byte, 64)
	for {
		n, err := rd.read(buf)
		if err != nil {
			logf("keypad: %v", err)
			break
		}
		for _, ev := range e.padDec.decode(buf[:n]) {
			e.handleKey(ev)
		}
	}
	for _, ev := range e.padDec.releaseAll() {
		e.handleKey(ev)
	}
	rd.close()
	e.mu.Lock()
	if e.padRd == rd {
		e.padRd = nil
		e.pad.Status = padAbsent
	}
	e.mu.Unlock()
	e.changed()
	e.kick()
}

func (e *Engine) shutdown() {
	e.releaseAll()
	e.mu.Lock()
	rd := e.padRd
	e.mu.Unlock()
	if rd != nil {
		rd.abort()
	}
}

// ---------------------------------------------------------------------------
// Calibration of the chatter filter

func (e *Engine) calibStart(phase string) error {
	if phase != "single" && phase != "double" {
		return fmt.Errorf("unknown phase %q", phase)
	}
	if hook == nil || !hook.alive.Load() {
		return errors.New(T("хук мыши не работает", "the mouse hook is not running"))
	}
	e.mu.Lock()
	e.calibPhase = phase
	e.mu.Unlock()
	hook.startRecording()
	return nil
}

func (e *Engine) calibStop() {
	ev, dur := hook.stopRecording()
	e.mu.Lock()
	if e.calibPhase != "" {
		e.calib[e.calibPhase] = trimRecording(ev, dur)
	}
	e.calibPhase = ""
	e.mu.Unlock()
}

// calibReset drops a recording ("" = both) and stops a running one.
func (e *Engine) calibReset(phase string) {
	e.mu.Lock()
	running := e.calibPhase != ""
	e.calibPhase = ""
	if phase == "" {
		e.calib = map[string][]clickEvent{}
	} else {
		delete(e.calib, phase)
	}
	e.mu.Unlock()
	if running {
		hook.stopRecording()
	}
}

func (e *Engine) calibResult() (calibResult, error) {
	e.mu.Lock()
	s, d := e.calib["single"], e.calib["double"]
	e.mu.Unlock()
	if len(s) < 6 || len(d) < 6 {
		return calibResult{}, errors.New(T("сначала пройдите оба шага", "complete both steps first"))
	}
	return fitChatter(s, d), nil
}

// ---------------------------------------------------------------------------
// Snapshot for the UI and the tray

type Status struct {
	Version     string    `json:"version"`
	Mouse       bool      `json:"mouse"`
	FW          string    `json:"fw"`
	DevErr      string    `json:"devErr"`
	DPI         int       `json:"dpi"`
	Poll        int       `json:"poll"`
	Stage       int       `json:"stage"`
	Stages      []int     `json:"stages"`
	StagesFrom  string    `json:"stagesFrom"`
	Hold        int       `json:"hold"`
	Pad         padStatus `json:"pad"`
	PadErr      string    `json:"padErr"`
	PadService  string    `json:"padService"`
	Foreground  string    `json:"foreground"`
	Active      string    `json:"active"`
	ActiveName  string    `json:"activeName"`
	Hook        bool      `json:"hook"`
	Merged      int       `json:"merged"`
	Presses     int       `json:"presses"`
	CalibPhase  string    `json:"calibPhase"`
	CalibCount  int       `json:"calibCount"`
	CalibSingle int       `json:"calibSingle"`
	CalibDouble int       `json:"calibDouble"`
	Autostart   bool      `json:"autostart"`
}

func countDowns(ev []clickEvent) int {
	n := 0
	for _, e := range ev {
		if e.Down {
			n++
		}
	}
	return n
}

func (e *Engine) status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := dpiSet(e.data, e.active)
	s := Status{
		Version: appVersion, Mouse: e.ctl != nil, FW: e.dev.FW, DevErr: e.devErr,
		Stage: e.stage, Stages: set.Stages, StagesFrom: e.setKey, Hold: e.hold,
		Pad: e.pad.Status, PadErr: e.padErr, PadService: e.pad.Service,
		Foreground: e.fgExe, CalibPhase: e.calibPhase,
		CalibSingle: countDowns(e.calib["single"]), CalibDouble: countDowns(e.calib["double"]),
	}
	if e.ctl != nil {
		s.DPI, s.Poll = byteToDPI(e.dev.DPI), pollHz(e.dev.Poll)
	}
	if e.active != nil {
		s.Active, s.ActiveName = e.active.ID, e.active.Name
	}
	if hook != nil {
		s.Hook = hook.alive.Load()
		s.Merged, s.Presses = hook.filter.stats()
		if e.calibPhase != "" {
			s.CalibCount = hook.recordingCount()
		}
	}
	return s
}
