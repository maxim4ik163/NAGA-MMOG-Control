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
- Remapping is software only: identify the device by Raw Input, key on scan code (independent of the switch and NumLock), suppress the original with a low-level hook. Buttons 4/5 need the same approach on the mouse side (a low-level mouse hook cannot tell which mouse sent a click; Raw Input can).
