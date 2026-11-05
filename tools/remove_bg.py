# Removes the white background of the mouse photo: remove_bg.py <in.png> <out.png>
# web/mouse.png = out.png cropped to (110, 60, 460, 480).
import sys
import cv2
import numpy as np

# 1. Загрузка исходного изображения (RGB)
img = cv2.imread(sys.argv[1])
h, w = img.shape[:2]

# 2. Выделение внешнего белого фона через связные компоненты
is_white = np.all(img >= 252, axis=-1).astype(np.uint8)
num_labels, labels, stats, _ = cv2.connectedComponentsWithStats(is_white)

# Фон — крупнейшая компонента, соприкасающаяся с краями
bg_label = 1
bg_mask = (labels == bg_label).astype(np.uint8)

# 3. Расчёт альфа-маски и карты расстояний до границы
fg_binary = (1 - bg_mask).astype(np.uint8)
dist_bg = cv2.distanceTransform(bg_mask, cv2.DIST_L2, 5)
dist_fg = cv2.distanceTransform(fg_binary, cv2.DIST_L2, 5)

# Формируем плавный альфа-канал на стыке (антиалиасинг шириной ~1.5 px)
alpha = np.clip((dist_bg - 0.5) / 1.5, 0.0, 1.0)
alpha[dist_fg > 1.5] = 1.0
alpha[dist_bg > 2.0] = 0.0

# 4. De-matting: убираем белый ореол с пограничных пикселей
# C_fg = (C_obs - (1 - alpha) * 255) / alpha
img_float = img.astype(np.float32)
alpha_3d = np.repeat(alpha[:, :, np.newaxis], 3, axis=2)

# Корректируем цвет там, где есть частичная прозрачность
mask_edge = (alpha > 0.01) & (alpha < 0.99)
img_corrected = img_float.copy()
img_corrected[mask_edge] = np.clip(
    (img_float[mask_edge] - (1.0 - alpha_3d[mask_edge]) * 255.0) / np.maximum(alpha_3d[mask_edge], 1e-3),
    0, 255
)

# 5. Сборка финального RGBA PNG
rgba = np.dstack([img_corrected.astype(np.uint8), (alpha * 255).astype(np.uint8)])
cv2.imwrite(sys.argv[2], rgba)
