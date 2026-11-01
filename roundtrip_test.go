package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestUIRoundTrip validates data saved by the UI through the preview mock
// (linuxtest/uimock.py). Runs only with NAGA_SAVED set.
func TestUIRoundTrip(t *testing.T) {
	path := os.Getenv("NAGA_SAVED")
	if path == "" {
		t.Skip("set NAGA_SAVED=path")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if err := validateData(&d); err != nil {
		t.Fatal(err)
	}
	g := d.Settings.Global
	if g["123:3"].Keys != "Ctrl+Win+Right" || !g["123:3"].Force || g["123:4"].Keys != "Ctrl+Shift+S" {
		t.Errorf("global: %+v", g)
	}
	if len(d.Profiles) != 2 || d.Profiles[1].Exes[0] != "kompas.exe" || d.Profiles[1].DPI == nil || d.Profiles[1].ID == "" {
		t.Errorf("profiles: %+v", d.Profiles[1])
	}
	if d.Settings.PollRate != 500 || d.Settings.LightLogo {
		t.Error("mouse settings")
	}
}
