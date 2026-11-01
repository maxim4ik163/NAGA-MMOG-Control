# Naga Control

A lightweight Windows app for the original Razer Naga (USB 1532:0015), so Razer Synapse is not needed.

Status: protocol research. `cmd/nagaprobe` is a read-only console probe: it lists the mouse's HID collections, sends only GET commands (firmware, serial, DPI, polling rate, LED states) and records which keys the side keypad sends.

Protocol notes are re-implemented from public descriptions (OpenRazer documents the Razer report format); no GPL code is included. Licence: MIT.
