"""Turn the Blender master renders in art/renders/ into the app's image files,
the Windows .ico and the review contact sheet.

Run with plain Python (Pillow + numpy) after render_art.py:
  python art/postprocess.py
"""

import os

import numpy as np
from PIL import Image, ImageDraw, ImageFont

ART = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(ART)
RENDERS = os.path.join(ART, "renders")

ICON_NAMES = [
    "new", "open", "save", "import", "rates", "basemap", "view2d", "view3d", "parcels",
    "hex", "square", "export-image", "export-3d", "export-web", "export-data", "checklist",
    "help", "zoom-fit",
]
ICO_SIZES = [16, 24, 32, 48, 64, 128, 256]


def _blur(x, sigma):
    """Separable gaussian blur over the first two axes of a float array."""
    r = max(1, int(round(sigma * 3)))
    k = np.exp(-(np.arange(-r, r + 1) ** 2) / (2 * sigma * sigma))
    k /= k.sum()
    pad = np.pad(x, ((r, r), (r, r), (0, 0)), mode="edge")
    tmp = sum(k[i] * pad[i:i + x.shape[0], :, :] for i in range(2 * r + 1))
    out = sum(k[i] * tmp[:, i:i + x.shape[1], :] for i in range(2 * r + 1))
    return out


def downsample(im, size, sharpen=0.0, sigma=0.6):
    """Lanczos on premultiplied alpha, then an optional light unsharp mask."""
    if isinstance(size, int):
        size = (size, size)
    pm = im.convert("RGBA").convert("RGBa").resize(size, Image.LANCZOS)
    a = np.asarray(pm).astype(np.float32) / 255.0
    if sharpen > 0:
        a = a + sharpen * (a - _blur(a, sigma))
        a = np.clip(a, 0.0, 1.0)
        a[..., :3] = np.minimum(a[..., :3], a[..., 3:4])
    alpha = a[..., 3:4]
    rgb = np.where(alpha > 1e-4, a[..., :3] / np.maximum(alpha, 1e-4), 0.0)
    out = np.concatenate([np.clip(rgb, 0, 1), alpha], axis=2)
    return Image.fromarray((out * 255.0 + 0.5).astype(np.uint8), "RGBA")


def small_sharpen(px):
    """Unsharp amount by output size: small sizes need more help."""
    if px <= 24:
        return 0.55
    if px <= 48:
        return 0.45
    if px <= 64:
        return 0.3
    return 0.15


def out_path(*parts):
    p = os.path.join(REPO, *parts)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    return p


def do_icons():
    made = {}
    for n in ICON_NAMES:
        src = os.path.join(RENDERS, "icons", n + ".png")
        if not os.path.exists(src):
            print("missing", src)
            continue
        master = Image.open(src)
        im64 = downsample(master, 64, small_sharpen(64))
        im64.save(out_path("frontend", "dist", "img", "icons", n + ".png"), optimize=True)
        made[n] = (downsample(master, 32, small_sharpen(32)), im64)
    return made


def do_appicon():
    src = os.path.join(RENDERS, "appicon_2048.png")
    if not os.path.exists(src):
        print("missing", src)
        return None
    master = Image.open(src)
    downsample(master, 1024, 0.0).save(out_path("build", "appicon.png"), optimize=True)
    downsample(master, 64, small_sharpen(64)).save(out_path("frontend", "dist", "img", "logo-64.png"), optimize=True)
    downsample(master, 128, small_sharpen(128)).save(out_path("frontend", "dist", "img", "logo-128.png"), optimize=True)
    sizes = {s: downsample(master, s, small_sharpen(s)) for s in ICO_SIZES}
    ico = out_path("build", "windows", "icon.ico")
    sizes[256].save(ico, format="ICO", sizes=[(s, s) for s in ICO_SIZES],
                    append_images=[sizes[s] for s in ICO_SIZES if s != 256])
    return sizes


def do_hero():
    src = os.path.join(RENDERS, "hero_master.png")
    if not os.path.exists(src):
        print("missing", src)
        return
    master = Image.open(src)
    w, h = master.size
    im = downsample(master, (w // 2, h // 2), 0.0)
    # drop the shadow catcher's faint noise veil (alpha of a few levels) around the scene
    a = np.asarray(im).copy()
    a[a[..., 3] < 5] = 0
    Image.fromarray(a, "RGBA").save(out_path("frontend", "dist", "img", "hero.png"), optimize=True)


def _font(px, bold=False):
    for f in (("segoeuib.ttf" if bold else "segoeui.ttf"), "arial.ttf"):
        p = os.path.join("C:/Windows/Fonts", f)
        if os.path.exists(p):
            return ImageFont.truetype(p, px)
    return ImageFont.load_default()


def contact_sheet(icons, app):
    cols = 6
    cell_w, cell_h = 150, 112
    pad = 24
    rows = (len(ICON_NAMES) + cols - 1) // cols
    panel_w = cols * cell_w + pad * 2
    panel_h = rows * cell_h + 56
    app_h = 256 + 80
    W = panel_w * 2
    H = panel_h + app_h + 70
    sheet = Image.new("RGBA", (W, H), (255, 255, 255, 255))
    d = ImageDraw.Draw(sheet)
    f_title = _font(22, True)
    f_lab = _font(13)
    f_small = _font(12)
    d.rectangle([0, 0, W, 60], fill=(15, 36, 68, 255))
    d.text((pad, 16), "Pracre art: toolbar icons (2x = 64 px, 1x = 32 px) and app icon", font=f_title,
           fill=(244, 241, 234, 255))
    for k, bg in enumerate([(240, 240, 240, 255), (255, 255, 255, 255)]):
        x0, y0 = k * panel_w, 60
        d.rectangle([x0, y0, x0 + panel_w, y0 + panel_h + app_h + 10], fill=bg)
        d.text((x0 + pad, y0 + 14), "on #f0f0f0" if k == 0 else "on #ffffff", font=f_lab, fill=(91, 107, 130, 255))
        for i, n in enumerate(ICON_NAMES):
            if n not in icons:
                continue
            im32, im64 = icons[n]
            cx = x0 + pad + (i % cols) * cell_w
            cy = y0 + 44 + (i // cols) * cell_h
            sheet.alpha_composite(im64, (cx, cy))
            sheet.alpha_composite(im32, (cx + 76, cy + 16))
            d.text((cx, cy + 70), n, font=f_lab, fill=(30, 30, 30, 255))
        if app:
            ay = y0 + panel_h + 10
            d.text((x0 + pad, ay), "app icon 256 / 48 / 32 / 16", font=f_lab, fill=(91, 107, 130, 255))
            ax = x0 + pad
            for s in (256, 48, 32, 16):
                sheet.alpha_composite(app[s], (ax, ay + 24 + (256 - s) // 2))
                d.text((ax, ay + 24 + 256 + 6), "%d px" % s, font=f_small, fill=(60, 60, 60, 255))
                ax += s + 40
    sheet.convert("RGB").save(os.path.join(ART, "contact-sheet.png"), optimize=True)


def main():
    icons = do_icons()
    app = do_appicon()
    do_hero()
    contact_sheet(icons, app)
    print("done")


if __name__ == "__main__":
    main()
