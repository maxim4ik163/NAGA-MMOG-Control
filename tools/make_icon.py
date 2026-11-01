#!/usr/bin/env python3
"""Draws res/app.ico: the Naga side keypad (3x4 keys) in Razer green on a dark tile."""
from PIL import Image, ImageDraw
import sys
out = sys.argv[1] if len(sys.argv) > 1 else "res/app.ico"
S = 256
img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)
d.rounded_rectangle([8, 8, S - 8, S - 8], radius=52, fill=(26, 28, 32, 255))
green = (68, 214, 44, 255)
kw, kh, gx, gy = 52, 38, 14, 12
x0 = (S - (3 * kw + 2 * gx)) // 2
y0 = (S - (4 * kh + 3 * gy)) // 2
for r in range(4):
    for c in range(3):
        x, y = x0 + c * (kw + gx), y0 + r * (kh + gy)
        d.rounded_rectangle([x, y, x + kw, y + kh], radius=10, fill=green)
img.save(out, sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
