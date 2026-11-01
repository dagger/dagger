import sys
import numpy as np
from PIL import Image

src, cols = sys.argv[1], int(sys.argv[2])
mode = sys.argv[3] if len(sys.argv) > 3 else "ansi"

im = np.asarray(Image.open(src).convert("RGB")).astype(int)
cx, cy, r = 300, 296, 296
im = im[cy - r:cy + r, cx - r:cx + r]
n = im.shape[0]

palette = {
    1: (0x15, 0x14, 0x28),
    2: (255, 255, 255),
    3: (0xBA, 0x1E, 0x45),
    4: (0xF0, 0x7D, 0x1A),
    5: (0x40, 0xBA, 0xBF),
    6: (0xFC, 0xC0, 0x09),
}
keys = list(palette)
pal = np.array([palette[k] for k in keys])
d = ((im[:, :, None, :] - pal[None, None]) ** 2).sum(-1)
labels = np.array(keys)[d.argmin(-1)]
yy, xx = np.mgrid[0:n, 0:n]
labels[(xx - r) ** 2 + (yy - r) ** 2 > (r - 1) ** 2] = 0

W = cols * 2
H = W
rows = -(-H // 4)
step = n / W
grid = np.zeros((rows * 4, W), int)
for j in range(H):
    for i in range(W):
        block = labels[int(j * step):int((j + 1) * step), int(i * step):int((i + 1) * step)]
        vals, counts = np.unique(block, return_counts=True)
        grid[j, i] = vals[counts.argmax()]

edge = np.zeros_like(grid)
for j in range(grid.shape[0]):
    for i in range(W):
        g = grid[j, i]
        if g == 0:
            continue
        nb = [grid[jj, ii] for jj, ii in ((j-1,i),(j+1,i),(j,i-1),(j,i+1),(j-1,i-1),(j-1,i+1),(j+1,i-1),(j+1,i+1)) if 0 <= jj < grid.shape[0] and 0 <= ii < W]
        if g == 1:
            if any(v == 0 for v in nb) or len(nb) < 8:
                edge[j, i] = 7
        elif g == 2 or any(v != g for v in nb) or len(nb) < 8:
            edge[j, i] = g

colours = dict(palette)
colours[2] = (0xE8, 0xE8, 0xF0)
colours[7] = (0x9A, 0x98, 0xB8)
bits = [(0, 0, 0x01), (1, 0, 0x02), (2, 0, 0x04), (0, 1, 0x08), (1, 1, 0x10), (2, 1, 0x20), (3, 0, 0x40), (3, 1, 0x80)]
for row in range(rows):
    line = []
    for c in range(cols):
        code, seen = 0, []
        for dy, dx, b in bits:
            v = edge[row * 4 + dy, c * 2 + dx]
            if v:
                code |= b
                seen.append(v)
        ch = chr(0x2800 + code)
        if mode == "classes":
            ch = str(max(set(seen), key=seen.count)) if seen else "."
        elif mode == "ansi" and seen:
            rgb = colours[max(set(seen), key=seen.count)]
            ch = "\x1b[38;2;%d;%d;%dm%s\x1b[0m" % (*rgb, ch)
        line.append(ch)
    print("".join(line))
