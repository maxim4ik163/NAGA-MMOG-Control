package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseCombo(t *testing.T) {
	cases := map[string]string{
		"win+ctrl+right": "Ctrl+Win+Right",
		"Ctrl+Win+→":     "Ctrl+Win+Right",
		"shift+f5":       "Shift+F5",
		"Num+":           "Num+",
		"Ctrl+Num+":      "Ctrl+Num+",
		"alt+=":          "Alt+=",
		"a":              "A",
	}
	for in, want := range cases {
		c, err := parseCombo(in)
		if err != nil || c.String() != want {
			t.Errorf("%q: got %q, %v; want %q", in, c.String(), err, want)
		}
	}
	for _, bad := range []string{"", "Ctrl+Nope", "+"} {
		if _, err := parseCombo(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	c, _ := parseCombo("Ctrl+Win+Right")
	if c.Keys[2].VK != 0x27 || !c.Keys[2].Ext || !c.Keys[1].Ext {
		t.Errorf("vk/ext wrong: %+v", c.Keys)
	}
}

func TestKeypadDecoder(t *testing.T) {
	d := newKeypadDecoder()
	ev := d.decode([]byte{0, 0, 0x1E, 0, 0, 0, 0, 0})
	if len(ev) != 1 || ev[0] != (KeyEvent{"123:1", true}) {
		t.Fatalf("press: %v", ev)
	}
	ev = d.decode([]byte{0, 0, 0x1E, 0x57, 0, 0, 0, 0})
	if len(ev) != 1 || ev[0] != (KeyEvent{"num:12", true}) {
		t.Fatalf("second key: %v", ev)
	}
	ev = d.decode(make([]byte, 8))
	if len(ev) != 2 || ev[0].Down || ev[1].Down {
		t.Fatalf("release: %v", ev)
	}
	d.decode([]byte{0, 0, 0x2E, 0, 0, 0, 0, 0})
	if ev := d.releaseAll(); len(ev) != 1 || ev[0] != (KeyEvent{"123:12", false}) {
		t.Fatalf("releaseAll: %v", ev)
	}
}

func TestResolve(t *testing.T) {
	d := defaultData()
	p := &Profile{ID: "cad", Name: "CAD", Enabled: true, Exes: []string{"acad.exe"}, Bindings: map[string]Binding{
		"123:1": {Kind: KindKeys, Keys: "Ctrl+Z"},
		"123:2": {Kind: KindKeys, Keys: "Ctrl+Y"},
	}}
	d.Profiles = []*Profile{p}
	d.Settings.Global["123:1"] = Binding{Kind: KindKeys, Keys: "F1"}
	d.Settings.Global["123:2"] = Binding{Kind: KindKeys, Keys: "Ctrl+Win+Right", Force: true}
	d.Settings.Global["123:3"] = Binding{Kind: KindKeys, Keys: "F3"}
	if err := validateData(d); err != nil {
		t.Fatal(err)
	}
	if activeProfileFor(d, `C:\Program Files\Autodesk\ACAD.EXE`) != p {
		t.Fatal("profile by exe")
	}
	check := func(prof *Profile, slot, keys, src string) {
		b, s := resolve(d, prof, slot)
		if b.Keys != keys || s != src {
			t.Errorf("%s: got %q from %s, want %q from %s", slot, b.Keys, s, keys, src)
		}
	}
	check(p, "123:1", "Ctrl+Z", "profile")
	check(p, "123:2", "Ctrl+Win+Right", "global") // forced global wins
	check(p, "123:3", "F3", "global")
	check(p, "123:4", "", "default")
	check(nil, "123:1", "F1", "global")
	if c, ok := defaultCombo("num:11"); !ok || c.String() != "Num-" {
		t.Errorf("default num:11 = %v", c)
	}
}

func TestValidate(t *testing.T) {
	d := defaultData()
	d.Profiles = []*Profile{
		{Name: "A", Enabled: true, Exes: []string{"x.exe"}},
		{Name: "B", Enabled: true, Exes: []string{"X.EXE"}},
	}
	if err := validateData(d); err == nil {
		t.Error("duplicate exe accepted")
	}
	d.Profiles[1].Enabled = false
	if err := validateData(d); err != nil {
		t.Error(err)
	}
	d.Settings.DPI.Stages = []int{150}
	if err := validateData(d); err == nil {
		t.Error("150 DPI accepted")
	}
}

func TestDPI(t *testing.T) {
	for dpi, b := range map[int]byte{100: 0, 400: 12, 1400: 52, 3200: 124, 5600: 220} {
		if dpiToByte(dpi) != b || byteToDPI(b) != dpi {
			t.Errorf("%d <-> %d", dpi, b)
		}
	}
	if nextStage(KindDPIUp, 3, 4) != 3 || nextStage(KindDPICycle, 3, 4) != 0 || nextStage(KindDPIDown, 0, 4) != 0 {
		t.Error("nextStage")
	}
}

// fake clock for the live filter
type fakeTimer struct {
	at time.Duration
	f  func()
}

func TestChatterFilterLive(t *testing.T) {
	var timers []fakeTimer
	now := time.Duration(0)
	ups := 0
	f := newChatterFilter(func() { ups++ }, func(d time.Duration, fn func()) { timers = append(timers, fakeTimer{now + d, fn}) })
	f.set(true, Chatter{T: 45, H: 15, G: 100}.params())
	advance := func(to time.Duration) {
		now = to
		var rest []fakeTimer
		for _, tm := range timers {
			if tm.at <= now {
				tm.f()
			} else {
				rest = append(rest, tm)
			}
		}
		timers = rest
	}
	ms := time.Millisecond
	// bounce: down 0, up 30, down 40 (gap 10 < T) → merged
	if !f.down(0) {
		t.Fatal("first press swallowed")
	}
	advance(30 * ms)
	if f.up(30 * ms) {
		t.Fatal("release not held back")
	}
	advance(40 * ms)
	if f.down(40 * ms) {
		t.Fatal("bounce press passed")
	}
	advance(120 * ms)
	if f.up(120*ms) || ups != 0 {
		t.Fatal("release should be held")
	}
	advance(170 * ms)
	if ups != 1 {
		t.Fatalf("held release not sent: %d", ups)
	}
	// a real double click: gap 60 ms after a 70 ms press → both presses pass
	if !f.down(1000 * ms) {
		t.Fatal("press")
	}
	advance(1070 * ms)
	f.up(1070 * ms)
	advance(1130 * ms) // 60 ms later; T elapsed at 1115 → release sent
	if ups != 2 || !f.down(1130*ms) {
		t.Fatalf("double click broken, ups=%d", ups)
	}
	// short fragment (5 ms) then 80 ms gap → rule B merges
	advance(1135 * ms)
	f.up(1135 * ms)
	advance(1215 * ms)
	if f.down(1215 * ms) {
		t.Fatal("rule B: press after a short fragment should merge")
	}
	// switching off sends the held release
	f.up(1300 * ms)
	f.set(false, chatterParams{})
	if ups != 3 {
		t.Fatalf("flush: ups=%d", ups)
	}
}

// loadRecorded reads phases 3 and 4 of the hook probe log.
func loadRecorded(t *testing.T) (single, double []clickEvent) {
	f, err := os.Open("testdata/hookprobe-2026-11-01.log")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	re := regexp.MustCompile(`^\s+([\d.]+) ms\s+t=\d+\s+HOOK (L down|L up)`)
	sc := bufio.NewScanner(f)
	in, part := false, 0
	var start float64 = -1
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "== Raw timelines of phases 3 and 4") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(l) == "----" {
			part, start = 1, -1
			continue
		}
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v, _ := strconv.ParseFloat(m[1], 64)
		if start < 0 {
			start = v
		}
		e := clickEvent{T: time.Duration((v - start) * float64(time.Millisecond)), Down: m[2] == "L down"}
		if part == 0 {
			single = append(single, e)
		} else {
			double = append(double, e)
		}
	}
	return
}

func TestFitChatterRecorded(t *testing.T) {
	single, double := loadRecorded(t)
	if len(single) < 30 || len(double) < 30 {
		t.Fatalf("recording too short: %d %d", len(single), len(double))
	}
	r := fitChatter(single, double)
	t.Logf("best T=%d H=%d G=%d: %s; chatter %d, max %.1f ms", r.T, r.H, r.G, r.Score, r.ChatterSeen, r.MaxChatterMs)
	if r.Singles != 15 || r.Doubles != 10 {
		t.Fatalf("grouping: %d singles, %d doubles", r.Singles, r.Doubles)
	}
	if r.RawSinglesOK != 9 || r.RawDoublesOK != 2 {
		t.Errorf("raw: %d/%d", r.RawSinglesOK, r.RawDoublesOK)
	}
	if r.SinglesOK+r.DoublesOK < 23 {
		t.Errorf("fit too weak: %s", r.Score)
	}
}

func TestTrimRecording(t *testing.T) {
	ms := time.Millisecond
	ev := []clickEvent{
		{40 * ms, false},                  // tail of the Start click
		{60 * ms, true}, {70 * ms, false}, // its bounce
		{1000 * ms, true}, {1080 * ms, false},
		{2000 * ms, true}, {2010 * ms, false}, {2030 * ms, true}, {2100 * ms, false}, // a click with chatter
		{3500 * ms, true}, {3580 * ms, false}, // the Done click
	}
	got := trimRecording(ev, 3600*ms)
	if len(got) != 6 || got[0].T != 1000*ms || got[len(got)-1].T != 2100*ms {
		t.Fatalf("trim: %v", got)
	}
	// stopped long after the last click (e.g. with the keyboard): keep it
	if got := trimRecording(ev, 9000*ms); len(got) != 8 {
		t.Fatalf("keep last group: %d", len(got))
	}
}

func TestChatterDrag(t *testing.T) {
	var timers []fakeTimer
	now := time.Duration(0)
	ups := 0
	f := newChatterFilter(func() { ups++ }, func(d time.Duration, fn func()) { timers = append(timers, fakeTimer{now + d, fn}) })
	f.set(true, Chatter{T: 45, H: 15, G: 100, D: 250}.params())
	run := func(to time.Duration) {
		now = to
		var rest []fakeTimer
		for _, tm := range timers {
			if tm.at <= now {
				tm.f()
			} else {
				rest = append(rest, tm)
			}
		}
		timers = rest
	}
	ms := time.Millisecond
	f.down(0)
	f.setDragging()
	run(800 * ms)
	f.up(800 * ms) // contact drop mid-drag
	run(950 * ms)  // 150 ms later: longer than T and G, shorter than D
	if f.down(950 * ms) {
		t.Fatal("drag drop of 150 ms not merged")
	}
	run(2000 * ms)
	f.up(2000 * ms) // real end of the drag
	run(2200 * ms)
	if ups != 0 {
		t.Fatal("released too early")
	}
	run(2260 * ms)
	if ups != 1 {
		t.Fatalf("drag end not sent: %d", ups)
	}
	// a new click is no longer a drag: short window again
	f.down(3000 * ms)
	run(3080 * ms)
	f.up(3080 * ms)
	run(3130 * ms)
	if ups != 2 {
		t.Fatalf("click release delayed like a drag: %d", ups)
	}
}

func TestDragDefaultAndMigration(t *testing.T) {
	if defaultData().Settings.Chatter.D != 250 {
		t.Fatal("default D")
	}
	dir := t.TempDir()
	old := `{"version":1,"profiles":[],"settings":{"global":{},"dpi":{"stages":[800],"start":0},"pollRate":1000,"chatter":{"enabled":true,"t":45,"h":30,"g":100}}}`
	if err := os.WriteFile(dir+"/data.json", []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	s := loadStore(dir)
	c := s.data.Settings.Chatter
	if c.D != 250 || c.H != 30 || !c.Enabled || s.data.Version != dataVersion {
		t.Fatalf("migration: %+v v%d", c, s.data.Version)
	}
}
