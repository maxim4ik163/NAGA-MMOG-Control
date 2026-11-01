# Razer Naga (original, 2009) — protocol notes

Re-implemented from public descriptions (OpenRazer, razercfg describe the Razer report format). No GPL code is copied. Everything below marked **confirmed** was seen on the user's mouse with `nagaprobe` (2026-11-01).

## Device (confirmed)
- USB `1532:0015`, rev 0x0200. Firmware v1.13. Wired only, no battery.
- HID collections:
  - `MI_00`, usage `0001:0002` (mouse), input 9, **feature 91** — the control channel. Opened with access 0 (the system owns the mouse); `HidD_SetFeature`/`HidD_GetFeature` work on that handle.
  - `MI_01`, usage `0001:0006` (keyboard), input 9, no feature — the side keypad.
- Synapse/Razer driver is not needed: plain Windows HID works.

## Report (confirmed)
90-byte feature report, report ID 0 (91-byte buffer on Windows):
`status, transaction_id, remaining_packets (u16 BE), protocol_type 0, data_size, class, id, args[80], crc, reserved`. CRC = XOR of bytes 2..87. Response status: 1 busy, 2 ok, 3 failure, 4 timeout, 5 not supported.
- transaction_id **0xFF** works for every command, reads and writes.

## Timing (confirmed)
Packets sent too close together make the firmware stall the control transfer; Windows reports `ERROR_GEN_FAILURE` ("A device attached to the system is not functioning") from SetFeature or GetFeature, and the next transfers fail for a while. Reading the response 5 ms after a SET failed this way (the SET itself still took effect).
Working scheme (probe v3, full write test with **0 retries**): at least **40 ms between any two transfers**; a stalled transfer is retried with back-off (it was not accepted, so repeating it is safe); while the status is "busy", re-read instead of re-sending.

## Commands (all confirmed)
| what | class/id | size | args |
|---|---|---|---|
| firmware | 00/81 | 2 | → major, minor (01 0D = v1.13) |
| serial | 00/82 | 0x16 | → ASCII |
| polling get | 00/85 | 1 | → code: 1 = 1000, 2 = 500, 8 = 125 Hz |
| polling set | 00/05 | 1 | code (500 and back to 1000 verified by read-back) |
| DPI get | 04/81 | 3 | → x byte, y byte |
| DPI set | 04/01 | 3 | x byte, y byte, 0 (takes effect immediately) |
| LED get | 03/80 | 3 | 01 (varstore), led → state 0/1 |
| LED set | 03/00 | 3 | 01, led, 0/1 (takes effect immediately) |
| device mode | 00/84 | — | not supported (status failure) |
| DPI u16 | 04/85 | — | not supported (status failure) |

- **Firmware quirk:** one run returned status ok with `00 00` (v0.00) as the very first command after the handle was opened. razercfg re-reads the firmware version up to 5 times, 250 ms apart. Treat 0.00 as "not ready" and re-read.
- DPI byte: `DPI = 100 + 25 × byte` (razercfg: byte = (DPI/100 − 1) × 4 → 100..5600 = 0..220). Verified: byte 12 = 400 (slow), byte 124 = 3200 (fast), byte 52 = 1400 (the user's original value). Whether bytes that are not multiples of 4 give 25-DPI steps is untested; the app uses 100-DPI steps.
- LEDs: 0x01 scroll wheel, 0x04 logo, 0x05 side keypad. All three switch on and off independently, GET returns the real state. No colours or effects.
- No DPI stages, no sleep, no battery, no onboard profiles.
- Persistence across replug/reboot is not yet known (the report has a varstore byte for LEDs; DPI has none). The app should re-apply its settings when the mouse appears.

## Buttons (confirmed)
- Mouse collection `MI_00`: left, right, middle, wheel, and the **two buttons next to the left button = mouse buttons 4 and 5** (XBUTTON1/XBUTTON2).
- Side keypad: hardware switch on the bottom, 123 / NUM. The keypad is a separate keyboard (`MI_01`), keys arrive as normal keyboard input:
  - 123: scan codes 02..0D = `1 2 3 4 5 6 7 8 9 0 - =`.
  - NUM: scan codes `4F 50 51 4B 4C 4D 47 48 49 52 4A 4E` = Num 1, 2, 3, 4, 5, 6, 7, 8, 9, 0, Num −, Num + (seen with NumLock on).
- Telling the keypad from the main keyboard in a low-level hook is **impossible** (hookprobe, 49/49 events): the WH_KEYBOARD_LL hook always runs ~1 ms *before* Raw Input reports the device, and a key blocked in the hook never reaches Raw Input at all.
- Switching the keypad's HID collection to the generic "HID-compliant device" driver does not work: the node stays not started (DN_STARTED missing, no HID interface) and Windows puts `kbdhid` back on replug.
- **Working solution (confirmed): WinUSB on the USB interface `USB\VID_1532&PID_0015&MI_01`** (installed with Zadig, "Razer Naga (Interface 1)"). Windows no longer sees the keypad as a keyboard; the app reads it directly:
  - device interface GUID from the device's registry value `DeviceInterfaceGUIDs` (Zadig generates it), open with `FILE_FLAG_OVERLAPPED`, `WinUsb_Initialize`;
  - interface 1, class 03/00/01, one endpoint: **interrupt IN 0x82**, max packet 9, interval 1;
  - reports are 8-byte boot-keyboard reports, no report ID: `mods, 0, key1..key6` (HID usages). 123 mode: `1E..27` (1..0), `2D` (-), `2E` (=). NUM mode: `59..62` (Num1..Num0), `56` (Num−), `57` (Num+). All zero = released.
  - Without the app running the keypad does nothing. Undo: Device Manager → Razer Naga (Interface 1) → uninstall with "delete driver" → replug.
- The mouse interface `MI_00` stays on the stock HID mouse driver; the control channel (feature report) is unaffected. Buttons 4/5 and the left button are handled with a low-level mouse hook (single-mouse assumption; the hook cannot tell mice apart either).

## Left button chatter (measured 2026-11-01)
The user's left switch is worn. Single clicks (15, ~1/s): 6 of 15 arrived as 2–3 presses; chatter releases 6–81 ms. Intentional double-click gap: 46–92 ms — overlaps the chatter, so a release-time-only filter cannot work (T=85 ms: singles 15/15 but doubles 2/10).
Two-rule filter, release held back by the app: merge if the release was shorter than **T≈45 ms**, or shorter than **G≈100 ms** when the preceding press lasted less than **H≈15–20 ms** (real presses are 50–115 ms). On the recorded data: singles 14/15, doubles 9/10 (no filter: 9/15 and 2/10). Data: `testdata/hookprobe-2026-11-01.log`, simulator `tools/chatter_sim.py`. The app calibrates T/H/G from 15 single + 8 double clicks.
Drags (v0.1.2): with only T/H/G a drag broke — during a long hold only T applies, and contact drops while moving the mouse are longer than click bounces. While the cursor has moved more than 4 px with the button held, a release is held back for **D (default 250 ms)**; a merged bounce keeps the drag state, a new press resets it.
