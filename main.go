//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const appVersion = "0.2.0"

const (
	mainClass = "NagaControlMain"
	uiTitle   = "Naga Control"
	msgTray   = WM_APP + 1
	msgCall   = WM_APP + 2
	msgOpenUI = WM_APP + 3
	timerSoon = 2
	timerFG   = 3

	cmdStatus    = 100
	cmdOpenUI    = 101
	cmdAutostart = 102
	cmdChatter   = 103
	cmdExit      = 109
	cmdStage0    = 1000
)

var (
	hwndMain   uintptr
	hinst      uintptr
	nid        NOTIFYICONDATAW
	curIcon    uintptr
	curIconCol uint32
	msgTaskbar uintptr

	store *Store
	eng   *Engine
	api   *API

	callMu sync.Mutex
	calls  []func()
)

// uiCall runs f on the UI thread (tray, OSD, shell).
func uiCall(f func()) {
	callMu.Lock()
	calls = append(calls, f)
	callMu.Unlock()
	pPostMessageW.Call(hwndMain, msgCall, 0, 0)
}

func tooltipText() string {
	s := eng.status()
	if !s.Mouse {
		return T("Naga: мышь не найдена", "Naga: mouse not found")
	}
	dpi := s.DPI
	parts := []string{fmt.Sprintf("Naga: %d DPI", dpi)}
	switch s.Pad {
	case padKeyboard:
		parts = append(parts, T("панель не настроена (WinUSB)", "keypad not set up (WinUSB)"))
	case padError:
		parts = append(parts, T("ошибка панели", "keypad error"))
	}
	if s.ActiveName != "" {
		parts = append(parts, s.ActiveName)
	}
	t := strings.Join(parts, " · ")
	if len([]rune(t)) > 120 {
		t = string([]rune(t)[:120])
	}
	return t
}

func iconColour() uint32 {
	s := eng.status()
	switch {
	case !s.Mouse:
		return 0x9E9E9E
	case s.Pad != padReady:
		return 0xFFB300
	}
	return 0x44D62C
}

func updateTray() {
	col := iconColour()
	nid.UFlags = NIF_TIP | NIF_MESSAGE | NIF_SHOWTIP
	if col != curIconCol || curIcon == 0 {
		if ic := trayIcon(col); ic != 0 {
			nid.UFlags |= NIF_ICON
			nid.HIcon = ic
			if curIcon != 0 {
				defer pDestroyIcon.Call(curIcon)
			}
			curIcon, curIconCol = ic, col
		}
	}
	copyU16(nid.SzTip[:], tooltipText())
	pShellNotifyIconW.Call(NIM_MODIFY, ptr(&nid))
}

func notify(title, text string, warn bool) {
	nid.UFlags = NIF_INFO
	copyU16(nid.SzInfoTitle[:], title)
	copyU16(nid.SzInfo[:], text)
	nid.DwInfoFlags = NIIF_INFO
	if warn {
		nid.DwInfoFlags = NIIF_WARNING
	}
	pShellNotifyIconW.Call(NIM_MODIFY, ptr(&nid))
}

func addTray() {
	nid = NOTIFYICONDATAW{}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwndMain
	nid.UID = 1
	nid.UFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE | NIF_SHOWTIP
	nid.UCallbackMessage = msgTray
	curIconCol = 0x9E9E9E
	curIcon = trayIcon(curIconCol)
	nid.HIcon = curIcon
	copyU16(nid.SzTip[:], "Naga Control")
	pShellNotifyIconW.Call(NIM_ADD, ptr(&nid))
	nid.UVersion = 4
	pShellNotifyIconW.Call(NIM_SETVERSION, ptr(&nid))
	updateTray()
}

func showMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(menu uintptr, flags uint32, id int, text string) {
		pAppendMenuW.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(u16(text))))
	}
	st := eng.status()
	add(m, MF_STRING|MF_GRAYED, cmdStatus, tooltipText())
	add(m, MF_SEPARATOR, 0, "")
	add(m, MF_STRING, cmdOpenUI, T("Открыть Naga Control", "Open Naga Control"))

	sub, _, _ := pCreatePopupMenu.Call()
	for i, v := range st.Stages {
		fl := uint32(MF_STRING)
		if i == st.Stage {
			fl |= MF_CHECKED
		}
		add(sub, fl, cmdStage0+i, fmt.Sprintf("%d DPI", v))
	}
	pAppendMenuW.Call(m, MF_POPUP, sub, uintptr(unsafe.Pointer(u16("DPI"))))

	store.mu.Lock()
	chOn := store.data.Settings.Chatter.Enabled
	store.mu.Unlock()
	fl := uint32(MF_STRING)
	if chOn {
		fl |= MF_CHECKED
	}
	add(m, fl, cmdChatter, T("Фильтр дребезга ЛКМ", "Left-button chatter filter"))
	add(m, MF_SEPARATOR, 0, "")
	as := uint32(MF_STRING)
	if autostartEnabled() {
		as |= MF_CHECKED
	}
	add(m, as, cmdAutostart, T("Запускать вместе с Windows", "Start with Windows"))
	add(m, MF_SEPARATOR, 0, "")
	add(m, MF_STRING, cmdExit, T("Выход", "Exit"))

	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	pSetForegroundWindow.Call(hwndMain)
	cmd, _, _ := pTrackPopupMenu.Call(m, TPM_RIGHTBUTTON|TPM_RETURNCMD|TPM_NONOTIFY,
		uintptr(pt.X), uintptr(pt.Y), 0, hwndMain, 0)
	pPostMessageW.Call(hwndMain, WM_NULL, 0, 0)
	c := int(cmd)
	switch {
	case c == cmdOpenUI:
		openUI()
	case c == cmdAutostart:
		setAutostart(!autostartEnabled())
		api.notify()
	case c == cmdChatter:
		store.mu.Lock()
		store.data.Settings.Chatter.Enabled = !store.data.Settings.Chatter.Enabled
		_ = store.save()
		store.mu.Unlock()
		eng.reload()
	case c == cmdExit:
		pDestroyWindow.Call(hwndMain)
	case c >= cmdStage0 && c < cmdStage0+len(st.Stages):
		eng.setStage(c - cmdStage0)
	}
}

// ---------------------------------------------------------------------------
// UI process

var (
	uiMu   sync.Mutex
	uiProc *exec.Cmd
)

func openUI() {
	if h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("webview"))), uintptr(unsafe.Pointer(u16(uiTitle)))); h != 0 {
		if r, _, _ := pIsIconic.Call(h); r != 0 {
			pShowWindow.Call(h, SW_RESTORE)
		}
		pSetForegroundWindow.Call(h)
		return
	}
	uiMu.Lock()
	running := uiProc != nil
	uiMu.Unlock()
	if running {
		return
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--ui", api.url)
	if err := cmd.Start(); err != nil {
		shellOpen(api.url)
		return
	}
	pAllowSetForeground.Call(uintptr(cmd.Process.Pid))
	uiMu.Lock()
	uiProc = cmd
	uiMu.Unlock()
	go func() {
		start := time.Now()
		err := cmd.Wait()
		uiMu.Lock()
		uiProc = nil
		uiMu.Unlock()
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 && time.Since(start) < 60*time.Second {
			uiCall(func() { shellOpen(api.url) }) // no WebView2 runtime → default browser
		}
	}()
}

func shellOpen(path string) {
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(path))), 0, 0, 1)
}

// ---------------------------------------------------------------------------

func mainWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case msgTray:
		switch lp & 0xFFFF {
		case WM_LBUTTONUP:
			openUI()
		case WM_RBUTTONUP, WM_CONTEXTMENU:
			showMenu()
		}
		return 0
	case msgCall:
		callMu.Lock()
		q := calls
		calls = nil
		callMu.Unlock()
		for _, f := range q {
			f()
		}
		return 0
	case msgOpenUI:
		openUI()
		return 0
	case WM_DEVICECHANGE:
		pSetTimer.Call(hwnd, timerSoon, 800, 0)
		return 0
	case WM_TIMER:
		switch wp {
		case timerSoon:
			pKillTimer.Call(hwnd, timerSoon)
			eng.kick()
		case timerFG:
			// fallback for windows that do not raise the foreground event
			eng.setForeground(foregroundExe())
		}
		return 0
	case WM_DESTROY:
		pShellNotifyIconW.Call(NIM_DELETE, ptr(&nid))
		pPostQuitMessage.Call(0)
		return 0
	}
	if msgTaskbar != 0 && msg == msgTaskbar {
		addTray()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func fatal(text string) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16("Naga Control"))), 0x10)
	os.Exit(1)
}

func setDPIAware() {
	if pSetProcessDpiAwarenessCtx.Find() == nil {
		pSetProcessDpiAwarenessCtx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	} else {
		pSetProcessDPIAware.Call()
	}
}

func appDataDir() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	d := filepath.Join(base, "NagaControl")
	_ = os.MkdirAll(d, 0755)
	return d
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "--ui" {
		runUI(os.Args[2])
		return
	}
	runtime.LockOSThread()

	_, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16(`Local\NagaControlMutex`))))
	if errno, ok := err.(syscall.Errno); ok && errno == 183 {
		if h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16(mainClass))), 0); h != 0 {
			pAllowSetForeground.Call(^uintptr(0)) // ASFW_ANY
			pPostMessageW.Call(h, msgOpenUI, 0, 0)
		}
		return
	}
	setDPIAware()

	dir := appDataDir()
	logFile = filepath.Join(dir, "nagacontrol.log")
	setLang(systemLanguage())
	store = loadStore(dir)
	if l := store.data.Settings.Language; l == "ru" || l == "en" {
		setLang(l)
	} else {
		store.data.Settings.Language = currentLang()
		_ = store.save()
	}
	logEnabled = store.data.Settings.Debug
	logf("start %s, data: %s", appVersion, dir)

	hinst, _, _ = pGetModuleHandleW.Call(0)
	wc := WNDCLASSEXW{LpfnWndProc: syscall.NewCallback(mainWndProc), HInstance: hinst, LpszClassName: u16(mainClass)}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, _ := pRegisterClassExW.Call(ptr(&wc)); r == 0 {
		fatal(T("Не удалось зарегистрировать класс окна", "Cannot register the window class"))
	}
	hwndMain, _, _ = pCreateWindowExW.Call(WS_EX_TOOLWINDOW, uintptr(unsafe.Pointer(u16(mainClass))),
		uintptr(unsafe.Pointer(u16("Naga Control"))), WS_POPUP, 0, 0, 0, 0, 0, 0, hinst, 0)
	if hwndMain == 0 {
		fatal(T("Не удалось создать окно", "Cannot create the window"))
	}
	msgTaskbar, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))

	hook = newMouseHook()
	eng = newEngine(store)
	api, err = newAPI(eng, store, uiCall)
	if err != nil {
		fatal(T("Не удалось запустить локальный сервер: ", "Cannot start the local server: ") + err.Error())
	}
	var trayMu sync.Mutex
	trayPending := false
	eng.onChange = func() {
		api.notify()
		trayMu.Lock()
		if trayPending {
			trayMu.Unlock()
			return
		}
		trayPending = true
		trayMu.Unlock()
		uiCall(func() {
			trayMu.Lock()
			trayPending = false
			trayMu.Unlock()
			updateTray()
		})
	}
	eng.onOSD = func(c osdContent) { uiCall(func() { osdShow(c) }) }
	eng.onError = func(s string) { uiCall(func() { notify("Naga Control", s, true) }) }

	hook.run()
	if !hook.alive.Load() {
		notify("Naga Control", T("Не удалось установить хук мыши: кнопки 4/5 и фильтр дребезга не работают.",
			"Cannot install the mouse hook: buttons 4/5 and the chatter filter will not work."), true)
	}
	eng.mu.Lock()
	eng.fgExe = foregroundExe()
	eng.applyContextLocked(false)
	eng.mu.Unlock()
	go eng.runHookEvents()
	go eng.runDevice()

	refreshAutostart()
	osdCreate(hinst)
	addTray()
	watchForeground(eng.setForeground)
	applyFGTimer()

	if !startedByAutorun() {
		openUI()
	}

	var m MSG
	for {
		r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(ptr(&m))
		pDispatchMessageW.Call(ptr(&m))
	}
	hook.stop()
	eng.shutdown()
	uiMu.Lock()
	if uiProc != nil && uiProc.Process != nil {
		_ = uiProc.Process.Kill()
	}
	uiMu.Unlock()
	if curIcon != 0 {
		pDestroyIcon.Call(curIcon)
	}
}

// applyFGTimer (re)starts the foreground check with the interval from the
// settings (UI thread).
func applyFGTimer() {
	store.mu.Lock()
	ms := store.data.Settings.FGCheckMs
	store.mu.Unlock()
	ms = clampInt(ms, minFGCheckMs, maxFGCheckMs)
	pSetTimer.Call(hwndMain, timerFG, uintptr(ms), 0)
}

func startedByAutorun() bool {
	for _, a := range os.Args[1:] {
		if a == "--tray" {
			return true
		}
	}
	return false
}
