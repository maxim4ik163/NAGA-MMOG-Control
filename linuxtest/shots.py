#!/usr/bin/env python3
"""Screenshots of every tab via the mock (headless Chrome)."""
import sys
from playwright.sync_api import sync_playwright
url, out = sys.argv[1], sys.argv[2]
errors = []
with sync_playwright() as p:
    b = p.chromium.launch(executable_path="/usr/bin/google-chrome", args=["--no-sandbox"])
    pg = b.new_page(viewport={"width": 1200, "height": 840})
    pg.on("pageerror", lambda e: errors.append(str(e)))
    pg.on("console", lambda m: m.type == "error" and errors.append(m.text))
    pg.goto(url); pg.wait_for_timeout(800)
    tabs = ["buttons", "profiles", "macros", "mouse", "chatter", "settings"]
    for i, t in enumerate(tabs):
        pg.locator("#tabs button").nth(i).click(); pg.wait_for_timeout(300)
        pg.screenshot(path=f"{out}/{i}-{t}.png", full_page=False)
    # buttons: switch scope to the profile, select a key, NUM layer
    pg.locator("#tabs button").nth(0).click()
    pg.select_option("#view select", "pcad"); pg.wait_for_timeout(200)
    pg.locator(".pkey").nth(1).click(); pg.wait_for_timeout(200)
    pg.screenshot(path=f"{out}/5-buttons-profile.png")
    pg.locator(".seg button").nth(1).click(); pg.wait_for_timeout(200)
    pg.locator(".pkey").nth(4).click(); pg.wait_for_timeout(200)
    pg.screenshot(path=f"{out}/6-buttons-num.png")
    # calibration wizard
    pg.locator("#tabs button").nth(4).click(); pg.wait_for_timeout(200)
    pg.get_by_role("button", name="Начать", exact=True).click(); pg.wait_for_timeout(1300)
    pg.screenshot(path=f"{out}/7-calib-rec.png")
    pg.get_by_role("button", name="Готово", exact=True).click(); pg.wait_for_timeout(300)
    pg.get_by_role("button", name="Начать", exact=True).click(); pg.wait_for_timeout(300)
    pg.get_by_role("button", name="Готово", exact=True).click(); pg.wait_for_timeout(800)
    pg.screenshot(path=f"{out}/8-calib-result.png", full_page=True)
    # phone width
    pg.set_viewport_size({"width": 900, "height": 700})
    pg.locator("#tabs button").nth(0).click(); pg.wait_for_timeout(300)
    pg.screenshot(path=f"{out}/9-narrow.png")
    b.close()
print("errors:", errors)
