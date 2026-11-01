//go:build windows

package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var webFS embed.FS

type API struct {
	eng   *Engine
	store *Store
	token string
	url   string

	subMu sync.Mutex
	subs  map[chan struct{}]bool

	uiCall func(func())
}

func newAPI(eng *Engine, st *Store, uiCall func(func())) (*API, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	a := &API{eng: eng, store: st, token: hex.EncodeToString(b), subs: map[chan struct{}]bool{}, uiCall: uiCall}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	a.url = fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), a.token)
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/api/", a.dispatch)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return a, nil
}

func (a *API) notify() {
	a.subMu.Lock()
	for c := range a.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	a.subMu.Unlock()
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) dispatch(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get("X-Token")
	if tok == "" {
		tok = r.URL.Query().Get("t")
	}
	if tok != a.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if path == "events" {
		a.events(w, r)
		return
	}
	var body json.RawMessage
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
			w.WriteHeader(400)
			writeJSON(w, apiError{T("неверный запрос: ", "bad request: ") + err.Error()})
			return
		}
	}
	res, err := a.handle(path, body)
	if err != nil {
		w.WriteHeader(400)
		writeJSON(w, apiError{err.Error()})
		return
	}
	writeJSON(w, res)
	a.notify()
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	c := make(chan struct{}, 1)
	a.subMu.Lock()
	a.subs[c] = true
	a.subMu.Unlock()
	defer func() {
		a.subMu.Lock()
		delete(a.subs, c)
		a.subMu.Unlock()
	}()
	send := func() bool {
		st := a.eng.status()
		st.Autostart = autostartEnabled()
		b, _ := json.Marshal(st)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c:
			if !send() {
				return
			}
		case <-tick.C:
			// calibration counters change without other events
			if a.eng.status().CalibPhase != "" && !send() {
				return
			}
		}
	}
}

type stateReply struct {
	Data   *Data    `json:"data"`
	Status Status   `json:"status"`
	Keys   []keyDef `json:"keys"`
	Lang   string   `json:"lang"`
}

func (a *API) state() stateReply {
	a.store.mu.Lock()
	d := a.store.data.clone()
	a.store.mu.Unlock()
	st := a.eng.status()
	st.Autostart = autostartEnabled()
	return stateReply{Data: d, Status: st, Keys: keyDefs, Lang: currentLang()}
}

func (a *API) handle(path string, body json.RawMessage) (any, error) {
	switch path {
	case "state":
		return a.state(), nil

	case "save":
		var d Data
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, err
		}
		if err := validateData(&d); err != nil {
			return nil, err
		}
		a.store.mu.Lock()
		d.Version = 1
		a.store.data = &d
		err := a.store.save()
		a.store.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if l := d.Settings.Language; l == "ru" || l == "en" {
			setLang(l)
		}
		logEnabled = d.Settings.Debug
		a.eng.reload()
		if a.uiCall != nil {
			a.uiCall(updateTray)
		}
		return a.state(), nil

	case "stage":
		var v struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal(body, &v)
		a.eng.setStage(v.Index)
		return a.state(), nil

	case "autostart":
		var v struct {
			On bool `json:"on"`
		}
		_ = json.Unmarshal(body, &v)
		setAutostart(v.On)
		return a.state(), nil

	case "calib/start":
		var v struct {
			Phase string `json:"phase"`
		}
		_ = json.Unmarshal(body, &v)
		if err := a.eng.calibStart(v.Phase); err != nil {
			return nil, err
		}
		return a.state(), nil

	case "calib/stop":
		a.eng.calibStop()
		return a.state(), nil

	case "calib/result":
		return a.eng.calibResult()

	case "osd-test":
		a.eng.mu.Lock()
		set := dpiSet(a.eng.data, a.eng.active)
		a.eng.showDPILocked(set)
		a.eng.mu.Unlock()
		return a.state(), nil

	case "programs":
		return visiblePrograms(), nil

	case "open":
		var v struct {
			What string `json:"what"`
		}
		_ = json.Unmarshal(body, &v)
		targets := map[string]string{
			"zadig":  "https://zadig.akeo.ie/",
			"repo":   "https://github.com/maxim4ik163/NAGA-MMOG-Control",
			"data":   a.store.dir,
			"devmgr": "devmgmt.msc",
		}
		t, ok := targets[v.What]
		if !ok {
			return nil, errors.New("unknown target")
		}
		if a.uiCall != nil {
			a.uiCall(func() { shellOpen(t) })
		}
		return a.state(), nil
	}
	return nil, fmt.Errorf("unknown request %q", path)
}

// visiblePrograms lists the exe names of visible top-level windows (to pick
// programs for a profile).
var (
	pEnumWindows       = user32.NewProc("EnumWindows")
	pIsWindowVisible   = user32.NewProc("IsWindowVisible")
	pGetWindowTextLenW = user32.NewProc("GetWindowTextLengthW")
)

var (
	progMu   sync.Mutex
	progSeen map[string]bool
	progCB   = syscall.NewCallback(func(hwnd, lp uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if n, _, _ := pGetWindowTextLenW.Call(hwnd); n == 0 {
			return 1
		}
		if exe := exeOfWindow(hwnd); exe != "" {
			progSeen[exe] = true
		}
		return 1
	})
)

func visiblePrograms() []string {
	progMu.Lock()
	defer progMu.Unlock()
	progSeen = map[string]bool{}
	pEnumWindows.Call(progCB, 0)
	var out []string
	for e := range progSeen {
		switch e {
		case "explorer.exe", "textinputhost.exe", "applicationframehost.exe", "shellexperiencehost.exe", "searchhost.exe":
			continue
		}
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
