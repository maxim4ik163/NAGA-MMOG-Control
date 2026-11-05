//go:build windows

package main

// Actions beyond plain keys: macros played by the app, typing a text
// (optionally a DPAPI-protected secret) in a chosen keyboard layout, starting
// programs and Win+R style commands. Also the keyboard layout list and the
// file picker of the settings window. Ported from SM809 Control.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	crypt32  = syscall.NewLazyDLL("crypt32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	pCryptProtectData          = crypt32.NewProc("CryptProtectData")
	pCryptUnprotectData        = crypt32.NewProc("CryptUnprotectData")
	pLocalFree                 = kernel32.NewProc("LocalFree")
	pExpandEnvironmentStringsW = kernel32.NewProc("ExpandEnvironmentStringsW")
	pGetLocaleInfoW            = kernel32.NewProc("GetLocaleInfoW")
	pGetKeyboardLayoutList     = user32.NewProc("GetKeyboardLayoutList")
	pGetKeyboardLayout         = user32.NewProc("GetKeyboardLayout")
	pVkKeyScanExW              = user32.NewProc("VkKeyScanExW")
	pMapVirtualKeyExW          = user32.NewProc("MapVirtualKeyExW")
	pGetKeyState               = user32.NewProc("GetKeyState")
	pGetOpenFileNameW          = comdlg32.NewProc("GetOpenFileNameW")
	pCoInitializeEx            = ole32.NewProc("CoInitializeEx")
)

const (
	KEYEVENTF_UNICODE          = 0x0004
	MOUSEEVENTF_MOVE           = 0x0001
	MOUSEEVENTF_RIGHTDOWN      = 0x0008
	MOUSEEVENTF_RIGHTUP        = 0x0010
	MOUSEEVENTF_MIDDLEDOWN     = 0x0020
	MOUSEEVENTF_MIDDLEUP       = 0x0040
	MOUSEEVENTF_XDOWN          = 0x0080
	MOUSEEVENTF_XUP            = 0x0100
	MOUSEEVENTF_WHEEL          = 0x0800
	WM_INPUTLANGCHANGEREQUEST  = 0x0050
	CRYPTPROTECT_UI_FORBIDDEN  = 0x1
	LOCALE_SLOCALIZEDDISPLAYNM = 0x2
	secretPrefix               = "dpapi:"
)

// ---------------------------------------------------------------------------
// DPAPI: secrets can be decrypted only by the same Windows user on this PC.

type dataBlob struct {
	Size uint32
	Data *byte
}

func dpapiProtect(s string) (string, error) {
	b := []byte(s)
	in := dataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		in.Data = &b[0]
	}
	var out dataBlob
	r, _, err := pCryptProtectData.Call(ptr(&in), uintptr(unsafe.Pointer(u16("Naga Control"))), 0, 0, 0, CRYPTPROTECT_UI_FORBIDDEN, ptr(&out))
	if r == 0 {
		return "", err
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	enc := unsafe.Slice(out.Data, out.Size)
	return secretPrefix + base64.StdEncoding.EncodeToString(enc), nil
}

func dpapiUnprotect(s string) (string, error) {
	if !strings.HasPrefix(s, secretPrefix) {
		return "", errors.New("not a DPAPI secret")
	}
	b, err := base64.StdEncoding.DecodeString(s[len(secretPrefix):])
	if err != nil || len(b) == 0 {
		return "", errors.New("damaged secret")
	}
	in := dataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out dataBlob
	r, _, e := pCryptUnprotectData.Call(ptr(&in), 0, 0, 0, 0, CRYPTPROTECT_UI_FORBIDDEN, ptr(&out))
	if r == 0 {
		return "", e
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

// sealSecrets moves new secret texts of all bindings into Enc.
func sealSecrets(d *Data) error {
	seal := func(m map[string]Binding) error {
		for slot, b := range m {
			if b.Kind != KindText || !b.Secret || b.Text == "" {
				continue
			}
			enc, err := dpapiProtect(b.Text)
			if err != nil {
				return fmt.Errorf("%s: %v", slotName(slot), err)
			}
			b.Enc, b.Text = enc, ""
			m[slot] = b
		}
		return nil
	}
	if err := seal(d.Settings.Global); err != nil {
		return err
	}
	for _, p := range d.Profiles {
		if err := seal(p.Bindings); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Keyboard layouts

type keyboardLayout struct {
	ID   string `json:"id"`   // HKL, hex
	Name string `json:"name"` // language name
}

func installedLayouts() []uintptr {
	n, _, _ := pGetKeyboardLayoutList.Call(0, 0)
	if n == 0 || n > 64 {
		return nil
	}
	buf := make([]uintptr, n)
	n, _, _ = pGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&buf[0])))
	return buf[:n]
}

func hklID(h uintptr) string { return strings.ToUpper(strconv.FormatUint(uint64(h), 16)) }

func keyboardLayouts() []keyboardLayout {
	out := []keyboardLayout{}
	for _, h := range installedLayouts() {
		lang := h & 0xFFFF
		buf := make([]uint16, 128)
		name := ""
		if n, _, _ := pGetLocaleInfoW.Call(lang, LOCALE_SLOCALIZEDDISPLAYNM, uintptr(unsafe.Pointer(&buf[0])), 128); n > 0 {
			name = syscall.UTF16ToString(buf)
		}
		if name == "" {
			name = fmt.Sprintf("%04X", lang)
		}
		if (h>>16)&0xFFFF != lang { // a variant (e.g. Dvorak, typewriter)
			name += fmt.Sprintf(" (%X)", (h>>16)&0xFFFF)
		}
		out = append(out, keyboardLayout{ID: hklID(h), Name: name})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

func findLayout(id string) uintptr {
	for _, h := range installedLayouts() {
		if strings.EqualFold(hklID(h), id) {
			return h
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Typing a text

var typingMu sync.Mutex // one text at a time

func unicodeChar(u uint16) []INPUT {
	return []INPUT{
		keyInput(KEYBDINPUT{Scan: u, Flags: KEYEVENTF_UNICODE, ExtraInfo: injectedMark}),
		keyInput(KEYBDINPUT{Scan: u, Flags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP, ExtraInfo: injectedMark}),
	}
}

func vkTap(vk uint16, ext bool, hkl uintptr) []INPUT {
	scan, _, _ := pMapVirtualKeyExW.Call(uintptr(vk), 0, hkl)
	var fl uint32
	if ext {
		fl = KEYEVENTF_EXTENDEDKEY
	}
	return []INPUT{
		keyInput(KEYBDINPUT{Vk: vk, Scan: uint16(scan), Flags: fl, ExtraInfo: injectedMark}),
		keyInput(KEYBDINPUT{Vk: vk, Scan: uint16(scan), Flags: fl | KEYEVENTF_KEYUP, ExtraInfo: injectedMark}),
	}
}

// keysForChar: the key presses that produce ch in layout hkl, or nil.
func keysForChar(ch rune, hkl uintptr) []INPUT {
	if ch > 0xFFFF {
		return nil
	}
	res, _, _ := pVkKeyScanExW.Call(uintptr(ch), hkl)
	v := int16(res)
	if v == -1 {
		return nil
	}
	vk, shift := uint16(v&0xFF), uint16(v>>8)&0xFF
	if shift&0x38 != 0 { // Hankaku and other special shift states
		return nil
	}
	mods := []uint16{}
	if shift&2 != 0 {
		mods = append(mods, 0xA2) // Ctrl (with Alt: AltGr)
	}
	if shift&4 != 0 {
		mods = append(mods, 0xA4) // Alt
	}
	if shift&1 != 0 {
		mods = append(mods, 0xA0) // Shift
	}
	var in []INPUT
	for _, m := range mods {
		sc, _, _ := pMapVirtualKeyExW.Call(uintptr(m), 0, hkl)
		in = append(in, keyInput(KEYBDINPUT{Vk: m, Scan: uint16(sc), ExtraInfo: injectedMark}))
	}
	in = append(in, vkTap(vk, false, hkl)...)
	for i := len(mods) - 1; i >= 0; i-- {
		sc, _, _ := pMapVirtualKeyExW.Call(uintptr(mods[i]), 0, hkl)
		in = append(in, keyInput(KEYBDINPUT{Vk: mods[i], Scan: uint16(sc), Flags: KEYEVENTF_KEYUP, ExtraInfo: injectedMark}))
	}
	return in
}

// typeText types b's text into the window in front, then presses b.After.
// report shows a problem to the user.
func typeText(b Binding, report func(string)) {
	text := b.Text
	if b.Secret {
		t, err := dpapiUnprotect(b.Enc)
		if err != nil {
			report(T("Текст не расшифрован (другой пользователь Windows или ПК?) — задайте его заново", "Could not decrypt the text (another Windows user or PC?) — enter it again"))
			logf("secret not decrypted: %v", err)
			return
		}
		text = t
	}
	typingMu.Lock()
	defer typingMu.Unlock()
	wait := func() {
		if b.CharDelay > 0 {
			time.Sleep(time.Duration(b.CharDelay) * time.Millisecond)
		}
	}
	special := func(ch rune, hkl uintptr) []INPUT {
		switch ch {
		case '\n':
			return vkTap(0x0D, false, hkl)
		case '\t':
			return vkTap(0x09, false, hkl)
		case '\r':
			return []INPUT{}
		}
		return nil
	}

	if b.Method != TextKeys {
		for _, ch := range text {
			if in := special(ch, 0); in != nil {
				sendInputs(in)
			} else {
				for _, u := range utf16.Encode([]rune{ch}) {
					sendInputs(unicodeChar(u))
				}
			}
			wait()
		}
		pressAfter(b.After)
		return
	}

	fg, _, _ := pGetForegroundWindow.Call()
	tid, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	cur, _, _ := pGetKeyboardLayout.Call(tid)
	hkl, switched := cur, false
	if b.Layout != "" {
		if want := findLayout(b.Layout); want == 0 {
			report(T("Раскладка для ввода текста не установлена в Windows — печатаю в текущей", "The layout for typing is not installed in Windows — typing in the current one"))
		} else if want != cur && fg != 0 {
			pPostMessageW.Call(fg, WM_INPUTLANGCHANGEREQUEST, 0, want)
			for i := 0; i < 30; i++ { // up to 300 ms for the window to switch
				time.Sleep(10 * time.Millisecond)
				if h, _, _ := pGetKeyboardLayout.Call(tid); h == want {
					break
				}
			}
			hkl, switched = want, true
		}
	}
	caps := false
	if s, _, _ := pGetKeyState.Call(0x14); s&1 != 0 { // Caps Lock would invert letters
		caps = true
		sendInputs(vkTap(0x14, false, hkl))
	}
	for _, ch := range text {
		in := special(ch, hkl)
		if in == nil {
			in = keysForChar(ch, hkl)
		}
		if in == nil { // not on this layout: Unicode input for this character
			for _, u := range utf16.Encode([]rune{ch}) {
				in = append(in, unicodeChar(u)...)
			}
		}
		sendInputs(in)
		wait()
	}
	if caps {
		sendInputs(vkTap(0x14, false, hkl))
	}
	pressAfter(b.After)
	if switched {
		// posted messages overtake keyboard input in the target's queue:
		// give it time to process the keys before switching back
		time.Sleep(250 * time.Millisecond)
		pPostMessageW.Call(fg, WM_INPUTLANGCHANGEREQUEST, 0, cur)
	}
}

func pressAfter(after string) {
	if after == "" {
		return
	}
	c, err := parseCombo(after)
	if err != nil {
		return
	}
	time.Sleep(30 * time.Millisecond)
	pressCombo(c)
	releaseCombo(c)
}

// ---------------------------------------------------------------------------
// Starting programs and commands
//
// ShellExecute may load shell extensions, so it runs on one thread with COM
// initialised; a slow start (network path) never blocks the buttons.

var launchQ = make(chan func(), 16)

func init() {
	go func() {
		runtime.LockOSThread()
		pCoInitializeEx.Call(0, 0x2|0x4) // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE
		for f := range launchQ {
			f()
		}
	}()
}

func launch(f func()) {
	select {
	case launchQ <- f:
	default: // a dozen starts already queued: drop this one
	}
}

func expandEnv(s string) string {
	src := u16(s)
	buf := make([]uint16, 8192)
	n, _, _ := pExpandEnvironmentStringsW.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) > len(buf) {
		return s
	}
	return syscall.UTF16ToString(buf)
}

func showCmd(show string) uintptr {
	switch show {
	case ShowMin:
		return 7 // SW_SHOWMINNOACTIVE
	case ShowMax:
		return 3 // SW_SHOWMAXIMIZED
	case ShowHidden:
		return 0 // SW_HIDE
	}
	return 1 // SW_SHOWNORMAL
}

// shellRun starts file with params; returns the ShellExecute result (> 32 = ok).
func shellRun(file, params, dir string, show string) uintptr {
	var args, wd uintptr
	if params != "" {
		args = uintptr(unsafe.Pointer(u16(params)))
	}
	if dir != "" {
		wd = uintptr(unsafe.Pointer(u16(dir)))
	}
	pAllowSetForeground.Call(^uintptr(0)) // ASFW_ANY: the started program may come to the front
	res, _, _ := pShellExecuteW.Call(0, 0, uintptr(unsafe.Pointer(u16(file))), args, wd, showCmd(show))
	return res
}

func shellError(code uintptr) string {
	switch code {
	case 2:
		return T("файл не найден", "file not found")
	case 3:
		return T("путь не найден", "path not found")
	case 5:
		return T("доступ запрещён", "access denied")
	case 31:
		return T("нет программы для этого типа файлов", "no program is associated with this file type")
	}
	return fmt.Sprintf(T("код %d", "code %d"), code)
}

func runProgram(b Binding, report func(string)) {
	path := expandEnv(b.Path)
	dir := ""
	if filepath.IsAbs(path) {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			dir = filepath.Dir(path)
		}
	}
	if res := shellRun(path, expandEnv(b.Args), dir, b.Show); res <= 32 {
		report(fmt.Sprintf(T("Не удалось запустить %s: %s", "Could not start %s: %s"), b.Path, shellError(res)))
		logf("run %q: ShellExecute %d", path, res)
		return
	}
	logf("started: %s %s", path, b.Args)
}

func pathExists(p string) bool {
	if _, err := os.Stat(p); err == nil {
		return true
	}
	_, err := os.Stat(p + ".exe")
	return err == nil
}

// runCommand performs a line as the Win+R box does, without showing the box:
// folders and files open (also "%LOCALAPPDATA%"), URLs and shell: links open,
// programs start with their arguments ("cmd /k ipconfig").
func runCommand(b Binding, report func(string)) {
	line := expandEnv(strings.TrimSpace(b.Cmd))
	file, params := line, ""
	// a whole line that is a path, URL or shell: link opens as it is
	if !(pathExists(line) || strings.Contains(line, "://") || strings.HasPrefix(strings.ToLower(line), "shell:")) {
		file, params = splitCommand(line, pathExists)
	}
	if res := shellRun(file, params, "", b.Show); res <= 32 {
		report(fmt.Sprintf(T("Не удалось выполнить «%s»: %s", "Could not run “%s”: %s"), b.Cmd, shellError(res)))
		logf("command %q → %q %q: ShellExecute %d", b.Cmd, file, params, res)
		return
	}
	logf("command: %q → %q %q", b.Cmd, file, params)
}

// ---------------------------------------------------------------------------
// Mouse buttons and macros

func mouseButtonInput(name string, up bool) (INPUT, bool) {
	var fl, data uint32
	switch name {
	case "MouseLeft":
		fl = MOUSEEVENTF_LEFTDOWN
		if up {
			fl = MOUSEEVENTF_LEFTUP
		}
	case "MouseRight":
		fl = MOUSEEVENTF_RIGHTDOWN
		if up {
			fl = MOUSEEVENTF_RIGHTUP
		}
	case "MouseMiddle":
		fl = MOUSEEVENTF_MIDDLEDOWN
		if up {
			fl = MOUSEEVENTF_MIDDLEUP
		}
	case "MouseBack", "MouseForward":
		fl = MOUSEEVENTF_XDOWN
		if up {
			fl = MOUSEEVENTF_XUP
		}
		data = 1
		if name == "MouseForward" {
			data = 2
		}
	default:
		return INPUT{}, false
	}
	return mouseInput(MOUSEINPUT{Flags: fl, MouseData: data, ExtraInfo: injectedMark}), true
}

// macroRun is one playback of a macro started by a button.
type macroRun struct {
	mode int
	stop chan struct{}
	once sync.Once
	done chan struct{}
}

func newMacroRun(mode int) *macroRun {
	return &macroRun{mode: mode, stop: make(chan struct{}), done: make(chan struct{})}
}

func (m *macroRun) halt() { m.once.Do(func() { close(m.stop) }) }

func (m *macroRun) running() bool {
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

func macroEvent(e MacroEvent) {
	switch e.Type {
	case EvDown, EvUp:
		up := e.Type == EvUp
		if in, ok := mouseButtonInput(e.Key, up); ok {
			sendInputs([]INPUT{in})
			return
		}
		if k, ok := keyByName[strings.ToLower(e.Key)]; ok {
			c := Combo{Keys: []keyDef{k}}
			if up {
				releaseCombo(c)
			} else {
				pressCombo(c)
			}
		}
	case EvMove:
		sendInputs([]INPUT{mouseInput(MOUSEINPUT{Dx: int32(e.DX), Dy: int32(e.DY), Flags: MOUSEEVENTF_MOVE, ExtraInfo: injectedMark})})
	case EvWheel:
		sendInputs([]INPUT{mouseInput(MOUSEINPUT{MouseData: uint32(int32(e.Wheel * 120)), Flags: MOUSEEVENTF_WHEEL, ExtraInfo: injectedMark})})
	}
}

// playMacro plays m in the run's mode until it ends or is halted. Keys and
// buttons the macro left pressed are released at the end.
func playMacro(m *Macro, run *macroRun) {
	defer close(run.done)
	down := map[string]bool{}
	defer func() {
		for k := range down {
			macroEvent(MacroEvent{Type: EvUp, Key: k})
		}
	}()
	sleep := func(ms int) bool {
		if ms <= 0 {
			select {
			case <-run.stop:
				return false
			default:
				return true
			}
		}
		t := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer t.Stop()
		select {
		case <-run.stop:
			return false
		case <-t.C:
			return true
		}
	}
	total := 0
	for _, e := range m.Events {
		total += e.Delay
	}
	n := 1
	if run.mode == MacroTimes {
		n = max(m.Repeat, 1)
	}
	for it := 0; run.mode != MacroTimes || it < n; it++ {
		for _, e := range m.Events {
			if !sleep(e.Delay) {
				return
			}
			macroEvent(e)
			switch e.Type {
			case EvDown:
				down[e.Key] = true
			case EvUp:
				delete(down, e.Key)
			}
		}
		if total < 10 && !sleep(10) { // no busy loop for a macro without delays
			return
		}
	}
}

// ---------------------------------------------------------------------------
// File picker (settings window process)

type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	FnHook        uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	FlagsEx       uint32
}

// pickFile shows the Open dialog on a dedicated thread; "" = cancelled.
func pickFile(owner uintptr, start string) string {
	res := make(chan string, 1)
	go func() {
		runtime.LockOSThread() // the thread ends with the goroutine
		buf := make([]uint16, 1024)
		filter := utf16.Encode([]rune(T("Программы и ярлыки\x00*.exe;*.lnk;*.bat;*.cmd;*.ps1;*.url\x00Все файлы\x00*.*\x00\x00",
			"Programs and shortcuts\x00*.exe;*.lnk;*.bat;*.cmd;*.ps1;*.url\x00All files\x00*.*\x00\x00")))
		ofn := openFileName{Owner: owner, Filter: &filter[0], File: &buf[0], MaxFile: uint32(len(buf)),
			// FILEMUSTEXIST | PATHMUSTEXIST | NOCHANGEDIR | EXPLORER | NODEREFERENCELINKS
			Flags: 0x1000 | 0x800 | 0x8 | 0x80000 | 0x100000}
		ofn.StructSize = uint32(unsafe.Sizeof(ofn))
		if start != "" {
			if d := filepath.Dir(expandEnv(start)); d != "." {
				ofn.InitialDir = u16(d)
			}
		}
		if r, _, _ := pGetOpenFileNameW.Call(ptr(&ofn)); r == 0 {
			res <- ""
			return
		}
		res <- syscall.UTF16ToString(buf)
	}()
	return <-res
}
