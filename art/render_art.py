"""Build and render every Pracre art asset in Blender (Cycles).

Headless, from the repo root:
  "C:/Program Files/Blender Foundation/Blender 5.2/blender.exe" -b --factory-startup -P art/render_art.py -- [options]

Options (after the bare --):
  --only a,b,c     render only these assets (icon names, appicon, appicon-variants, hero)
  --samples N      override Cycles samples (default: per asset)
  --out DIR        master render folder (default: art/renders)
  --save-blend     save art/pracre_art.blend with one scene per asset

The masters land in art/renders/. Run art/postprocess.py (plain Python + Pillow)
afterwards to write the downsampled app files, the .ico and the contact sheet.

Inside a live Blender session the same functions can be imported and called:
  import sys; sys.path.insert(0, ART_DIR); import render_art; render_art.run(["hex"])
"""

import importlib
import os
import sys

import bpy

sys.dont_write_bytecode = True
ART = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(ART)
if ART not in sys.path:
    sys.path.insert(0, ART)

import pracre_lib as L  # noqa: E402
import pracre_icons as I  # noqa: E402
import pracre_scenes as S  # noqa: E402

ICON_SIZE = 512
ICON_SAMPLES = 160


def reload():
    for m in (L, I, S):
        importlib.reload(m)


def build_icon(name, size=ICON_SIZE, samples=ICON_SAMPLES):
    sc = L.reset_scene()
    M = L.std_materials()
    L.setup_render(sc, size, size, samples)
    L.world(sc)
    L.sun(sc, I.AZ, I.EL)
    I.ICONS[name](sc, M)
    L.ortho_camera(sc, I.AZ, I.EL, margin=0.035)
    return sc


def render_icon(name, out_dir, size=ICON_SIZE, samples=ICON_SAMPLES):
    sc = build_icon(name, size, samples)
    return L.render(sc, os.path.join(out_dir, "icons", name + ".png"))


def all_names():
    return list(I.ICONS) + ["appicon", "appicon-variants", "hero"]


def run(names=None, samples=None, out_dir=None, save_blend=False):
    out_dir = out_dir or os.path.join(ART, "renders")
    names = names or all_names()
    for name in names:
        print("=== rendering", name, flush=True)
        if name in I.ICONS:
            render_icon(name, out_dir, samples=samples or ICON_SAMPLES)
            if save_blend:
                L.stash(bpy.context.scene, "icon_" + name)
        elif name == "appicon":
            sc = S.build_appicon(S.APPICON_CHOICE, size=2048, samples=samples or 256)
            L.render(sc, os.path.join(out_dir, "appicon_2048.png"))
            if save_blend:
                L.stash(sc, "appicon")
        elif name == "appicon-variants":
            for v in S.APPICON_VARIANTS:
                sc = S.build_appicon(v, size=1024, samples=samples or 160)
                L.render(sc, os.path.join(ART, "variants", "appicon_%s.png" % v))
        elif name == "hero":
            sc = S.build_hero(samples=samples or 256)
            L.render(sc, os.path.join(out_dir, "hero_master.png"))
            if save_blend:
                L.stash(sc, "hero")
        else:
            print("unknown asset", name)
    if save_blend:
        sc = bpy.context.scene
        if not sc.objects and len(bpy.data.scenes) > 1:
            pass
        bpy.ops.wm.save_as_mainfile(filepath=os.path.join(ART, "pracre_art.blend"), check_existing=False)


def _parse(argv):
    argv = argv[argv.index("--") + 1:] if "--" in argv else []
    opts = {"names": None, "samples": None, "out_dir": None, "save_blend": False}
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--only":
            opts["names"] = [s.strip() for s in argv[i + 1].split(",") if s.strip()]
            i += 1
        elif a == "--samples":
            opts["samples"] = int(argv[i + 1])
            i += 1
        elif a == "--out":
            opts["out_dir"] = argv[i + 1]
            i += 1
        elif a == "--save-blend":
            opts["save_blend"] = True
        i += 1
    return opts


if __name__ == "__main__" and bpy.app.background:
    bpy.ops.wm.read_homefile(use_empty=True)
    run(**_parse(sys.argv))
