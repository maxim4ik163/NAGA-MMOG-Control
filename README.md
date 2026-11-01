# Naga Control

A lightweight Windows app for the original **Razer Naga** (2009, USB `1532:0015`), so Razer Synapse is not needed. Built in the spirit of [SM809 Control](https://github.com/maxim4ik163/SM809-Control).

## What it does

- **Side keypad remapping** — keys and key combinations on all 12 buttons, in **two layers** chosen by the 123/NUM switch under the mouse (24 bindings). Held buttons hold the keys; single keys auto-repeat like a keyboard.
- **Buttons 4 and 5** (next to the left button) — the same actions.
- **Global bindings and program profiles.** A profile switches on when a window of one of its programs becomes active (e.g. a CAD program) and off when you switch away. Global bindings fill in what a profile leaves unset; global bindings marked **“always, over profiles”** win everywhere (e.g. Ctrl+Win+←/→ for virtual desktops).
- **DPI stages** (1–8, 100–5600 DPI): next / previous / cycle / hold-for-sniper, with an on-screen popup. Profiles can have their own stages.
- **Polling rate** (125/500/1000 Hz) and the three **lights** (wheel, logo, keypad: on/off).
- **Left-button chatter filter** for worn switches, **calibrated from your own clicks**: 15 single clicks + 8 double clicks, then the app picks the parameters that turn the most of them into the right number of clicks. Drags never break: the filter holds a release back for a few ms instead of dropping presses.
- Tray icon (keypad grid: green = all working, amber = keypad not set up, grey = mouse not found), DPI menu, autostart, RU/EN.

## One-time setup: WinUSB on the keypad

Windows sees the side keypad as a second keyboard, and a low-level hook cannot tell which keyboard a key came from (measured: the hook always runs before Raw Input reports the device, and a blocked key never reaches Raw Input). So the keypad interface is switched to Microsoft's **WinUSB** driver, and Naga Control reads it directly:

1. Run [Zadig](https://zadig.akeo.ie/), menu **Options → List All Devices**.
2. Pick **Razer Naga (Interface 1)** — Interface 1 exactly; Interface 0 is the mouse.
3. With **WinUSB** on the right, press **Replace Driver**.

Without Naga Control running the keypad is then silent. To undo: Device Manager → *Razer Naga (Interface 1)* → *Uninstall device* with *Delete the driver* ticked → reconnect the mouse.

## Notes

- Programs running as administrator do not accept input from a normal app (Windows UIPI). If a CAD program runs elevated, run Naga Control elevated too.
- Settings: `%APPDATA%\NagaControl\data.json`.
- The mouse stores no profiles; the app re-applies DPI, polling rate and lights whenever the mouse is connected.

## Protocol and research

`docs/PROTOCOL.md` — control protocol (confirmed on the real mouse), keypad reports, timing, chatter measurements. `cmd/` holds the probes used to establish it. The Razer report format is re-implemented from public descriptions (OpenRazer, razercfg); no GPL code is included.

## Building

```
go test ./...
cd res && llvm-windres-18 --target=pe-x86-64 -i app.rc -o ../rsrc_windows_amd64.syso && cd ..
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o NagaControl.exe .
```

Dependencies are vendored in `third_party/` (go-webview2). UI preview without Windows: `linuxtest/uimock.py`.

Licence: MIT.
