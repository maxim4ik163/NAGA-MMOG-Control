# Removes the white background of the mouse photo: remove_bg.py <in.png> <out.png>
# web/mouse.png = out.png cropped to (110, 60, 460, 480).
#
# The background is the white region touching the image border. Only the
# outer ring of the mouse (up to 2 px from the background) gets partial
# transparency: its observed colour is a mix of the mouse colour F (taken from
# the nearest interior pixels) and white, C = a*F + (1-a)*255, so
# a = (255 - C) / (255 - F) and the pixel keeps the colour F. Background
# pixels are fully transparent, so no white fringe is left.
import sys
import cv2
import numpy as np

img = cv2.imread(sys.argv[1])
is_white = np.all(img >= 252, axis=-1).astype(np.uint8)
_, labels = cv2.connectedComponents(is_white)
border = np.concatenate([labels[0], labels[-1], labels[:, 0], labels[:, -1]])
border = border[border > 0]
bg_label = np.bincount(border).argmax()
bg = (labels == bg_label).astype(np.uint8)
fg = 1 - bg

dist_fg = cv2.distanceTransform(fg, cv2.DIST_L2, 5)  # distance from the background
band = (fg == 1) & (dist_fg <= 2.0)
interior = (fg == 1) & (dist_fg > 2.0)

# mouse colour near the edge: spread interior colours outwards
f = img.astype(np.float32)
known = interior.astype(np.float32)
acc = f * known[..., None]
for _ in range(6):
    k = np.ones((3, 3), np.float32)
    s = cv2.filter2D(acc, -1, k, borderType=cv2.BORDER_REPLICATE)
    n = cv2.filter2D(known, -1, k, borderType=cv2.BORDER_REPLICATE)
    grow = (known == 0) & (n > 0)
    acc[grow] = s[grow] / n[grow][:, None]
    known[grow] = 1
F = acc

alpha = np.zeros(img.shape[:2], np.float32)
alpha[interior] = 1.0
den = np.maximum(255.0 - F[band], 1.0)
a = ((255.0 - f[band]) / den).mean(axis=1)
alpha[band] = np.clip(a, 0.0, 1.0)

out = f.copy()
out[band] = F[band]
out[bg == 1] = 0
rgba = np.dstack([np.clip(out, 0, 255).astype(np.uint8), (alpha * 255 + 0.5).astype(np.uint8)])
cv2.imwrite(sys.argv[2], rgba)
