"""Generate the TodoList Agent app icon (ICO + PNG).

Run: python scripts/gen_icon.py
"""
from __future__ import annotations

import math
import os
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

ROOT = Path(__file__).resolve().parent.parent
ASSETS = ROOT / "assets"
ASSETS.mkdir(exist_ok=True)

SIZE = 1024
RADIUS = 228


def lerp(a: float, b: float, t: float) -> float:
    return a + (b - a) * t


def gradient(size: int) -> Image.Image:
    """Diagonal teal -> indigo gradient."""
    img = Image.new("RGBA", (size, size))
    px = img.load()
    c1 = (232, 177, 182)  # pale blush
    c2 = (185, 124, 136)  # dusty rose
    c3 = (125, 155, 134)  # sage
    for y in range(size):
        for x in range(size):
            t = (x / size) * 0.55 + (y / size) * 0.45
            if t < 0.55:
                u = t / 0.55
                r = lerp(c1[0], c2[0], u)
                g = lerp(c1[1], c2[1], u)
                b = lerp(c1[2], c2[2], u)
            else:
                u = (t - 0.55) / 0.45
                r = lerp(c2[0], c3[0], u)
                g = lerp(c2[1], c3[1], u)
                b = lerp(c2[2], c3[2], u)
            px[x, y] = (int(r), int(g), int(b), 255)
    return img


def main() -> None:
    base = gradient(SIZE)

    # Rounded-square mask.
    mask = Image.new("L", (SIZE, SIZE), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (0, 0, SIZE - 1, SIZE - 1), radius=RADIUS, fill=255
    )
    icon = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    icon.paste(base, (0, 0), mask)

    # Soft top-left sheen.
    sheen = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    sd = ImageDraw.Draw(sheen)
    sd.ellipse((-SIZE * 0.35, -SIZE * 0.55, SIZE * 0.9, SIZE * 0.55),
               fill=(255, 255, 255, 54))
    sheen = sheen.filter(ImageFilter.GaussianBlur(90))
    icon = Image.alpha_composite(icon, sheen)

    # Frosted inner ring.
    ring = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    rd = ImageDraw.Draw(ring)
    rd.rounded_rectangle(
        (38, 38, SIZE - 39, SIZE - 39), radius=RADIUS - 34,
        outline=(255, 255, 255, 46), width=7,
    )
    icon = Image.alpha_composite(icon, ring)

    # Check mark with soft shadow.
    shadow = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    sdraw = ImageDraw.Draw(shadow)
    pts = [
        (SIZE * 0.285, SIZE * 0.525),
        (SIZE * 0.445, SIZE * 0.675),
        (SIZE * 0.735, SIZE * 0.355),
    ]
    sdraw.line(pts, fill=(70, 45, 50, 110), width=int(SIZE * 0.105), joint="curve")
    shadow = shadow.filter(ImageFilter.GaussianBlur(24))
    icon = Image.alpha_composite(icon, shadow)

    draw = ImageDraw.Draw(icon)
    draw.line(pts, fill=(255, 255, 255, 255), width=int(SIZE * 0.088), joint="curve")
    # Round the stroke ends.
    for x, y in (pts[0], pts[-1]):
        r = SIZE * 0.044
        draw.ellipse((x - r, y - r, x + r, y + r), fill=(255, 255, 255, 255))

    # Accent spark (top right) to signal the agent link.
    sx, sy = SIZE * 0.755, SIZE * 0.235
    sr = SIZE * 0.048
    draw.ellipse((sx - sr, sy - sr, sx + sr, sy + sr), fill=(255, 255, 255, 240))
    draw.ellipse(
        (sx - sr * 2.2, sy - sr * 2.2, sx + sr * 2.2, sy + sr * 2.2),
        outline=(255, 255, 255, 80), width=int(SIZE * 0.008),
    )

    icon.save(ASSETS / "icon.png")

    sizes = [16, 20, 24, 32, 40, 48, 64, 96, 128, 256]
    # Pillow derives each ICO frame from the base image.
    icon.save(ASSETS / "icon.ico", format="ICO", sizes=[(s, s) for s in sizes])

    print("wrote", ASSETS / "icon.png")
    print("wrote", ASSETS / "icon.ico")


if __name__ == "__main__":
    main()
