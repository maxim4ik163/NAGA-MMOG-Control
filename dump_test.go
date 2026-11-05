package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDumpUIState writes the default data and key table for the UI preview
// mock (linuxtest/uimock.py). Runs only with NAGA_DUMP set.
func TestDumpUIState(t *testing.T) {
	out := os.Getenv("NAGA_DUMP")
	if out == "" {
		t.Skip("set NAGA_DUMP=path")
	}
	d := defaultData()
	d.Settings.Language = "ru"
	d.Profiles = []*Profile{{ID: "pcad", Name: "AutoCAD", Exes: []string{"acad.exe"}, Enabled: true, OSD: true,
		Bindings: map[string]Binding{"123:1": {Kind: KindKeys, Keys: "Ctrl+Z"}, "123:2": {Kind: KindKeys, Keys: "Ctrl+Y"}, "m:4": {Kind: KindDPIHold, Stage: 1}}}}
	d.Settings.Global["123:11"] = Binding{Kind: KindKeys, Keys: "Ctrl+Win+Left", Force: true}
	d.Settings.Global["123:12"] = Binding{Kind: KindKeys, Keys: "Ctrl+Win+Right", Force: true}
	d.Settings.Global["num:5"] = Binding{Kind: KindDPICycle}
	d.Macros = []*Macro{{ID: "mcombo", Name: "Комбо 1-2-3", Mode: MacroTimes, Repeat: 2, Events: []MacroEvent{
		{Type: EvDown, Key: "1"}, {Type: EvUp, Key: "1", Delay: 40}, {Type: EvDown, Key: "MouseLeft", Delay: 120}, {Type: EvUp, Key: "MouseLeft", Delay: 30},
		{Type: EvMove, DX: 40, DY: -10, Delay: 10}, {Type: EvWheel, Wheel: -2, Delay: 10}}}}
	d.Settings.Global["123:5"] = Binding{Kind: KindMacro, MacroID: "mcombo"}
	d.Settings.Global["123:6"] = Binding{Kind: KindText, Text: "С уважением,\nМаксим", Method: TextUnicode}
	d.Settings.Global["123:7"] = Binding{Kind: KindText, Secret: true, Enc: "dpapi:AAAA", Method: TextKeys, Layout: "4090409", After: "Enter"}
	d.Settings.Global["123:8"] = Binding{Kind: KindRun, Path: `C:\Windows\notepad.exe`}
	d.Settings.Global["123:9"] = Binding{Kind: KindCommand, Cmd: "%LOCALAPPDATA%"}
	if err := validateData(d); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(map[string]any{"data": d, "keys": keyDefs}, "", " ")
	if err := os.WriteFile(out, b, 0644); err != nil {
		t.Fatal(err)
	}
}
