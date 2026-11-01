#!/usr/bin/env python3
"""Minimal mock of the Naga Control API for previewing web/ in a browser.
Usage: uimock.py <state.json> <port> [pad-status]"""
import json, sys, threading, time
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path

WEB = Path(__file__).resolve().parent.parent / "web"
seed = json.loads(Path(sys.argv[1]).read_text())
if len(sys.argv) > 4 and sys.argv[4] == "noprofiles":
    seed["data"]["profiles"] = None
PORT = int(sys.argv[2])
status = {"version": "0.1.0", "mouse": True, "fw": "1.13", "devErr": "", "dpi": 1400, "poll": 1000,
          "stage": 1, "stages": seed["data"]["settings"]["dpi"]["stages"], "stagesFrom": "global", "hold": 0,
          "pad": sys.argv[3] if len(sys.argv) > 3 else "ready", "padErr": "", "padService": "kbdhid",
          "foreground": "acad.exe", "active": "pcad", "activeName": "AutoCAD", "hook": True,
          "merged": 3, "presses": 41, "calibPhase": "", "calibCount": 0, "calibSingle": 0, "calibDouble": 0, "autostart": True}
state = {"data": seed["data"], "status": status, "keys": seed["keys"], "lang": "ru"}

class H(SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=str(WEB), **k)
    def log_message(self, *a): pass
    def send_json(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def do_GET(self):
        p = self.path.split("?")[0]
        if p == "/api/state": return self.send_json(state)
        if p == "/api/programs": return self.send_json(["acad.exe", "chrome.exe", "kompas.exe", "solidworks.exe"])
        if p == "/api/events":
            self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
            try:
                while True:
                    self.wfile.write(("data: " + json.dumps(status) + "\n\n").encode()); self.wfile.flush(); time.sleep(1)
            except Exception: return
        return super().do_GET()
    def do_POST(self):
        p = self.path.split("?")[0]
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n) or b"null")
        if p == "/api/save": state["data"] = body; state["lang"] = body["settings"].get("language", "ru"); return self.send_json(state)
        if p == "/api/calib/start": status["calibPhase"] = body["phase"]; status["calibCount"] = 4; return self.send_json(state)
        if p == "/api/calib/stop":
            ph = status["calibPhase"]; status["calibPhase"] = ""
            status["calibSingle" if ph == "single" else "calibDouble"] = 21 if ph == "single" else 34
            return self.send_json(state)
        if p == "/api/calib/reset":
            status["calibPhase"] = ""
            ph = (body or {}).get("phase", "")
            if ph in ("", "single"): status["calibSingle"] = 0
            if ph in ("", "double"): status["calibDouble"] = 0
            return self.send_json(state)
        if p == "/api/calib/result":
            if not status["calibSingle"] or not status["calibDouble"]:
                return self.send_json({"error": "сначала пройдите оба шага"}, 400)
            return self.send_json({"T": 45, "H": 30, "G": 100, "singlesOk": 15, "singles": 15, "doublesOk": 10, "doubles": 10,
                                   "rawSinglesOk": 9, "rawDoublesOk": 2, "chatterSeen": 7, "maxChatterMs": 81.2, "score": "одиночные 15/15, двойные 10/10"})
        return self.send_json(state)

ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
