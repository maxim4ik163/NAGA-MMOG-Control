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
	if err := validateData(d); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(map[string]any{"data": d, "keys": keyDefs}, "", " ")
	if err := os.WriteFile(out, b, 0644); err != nil {
		t.Fatal(err)
	}
}
