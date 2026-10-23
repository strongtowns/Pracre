"""App icon and hero illustration builders."""

import math
import random

import bpy
from bpy_extras.object_utils import world_to_camera_view
from mathutils import Vector

import pracre_lib as L
from pracre_lib import MB

APPICON_VARIANTS = ["iso", "front", "high"]
APPICON_CHOICE = "high"

# Value-per-acre skyline for the app icon, rows back (+Y) to front (-Y),
# columns left (-X) to right (+X). The camera looks from the front-right corner.
APP_H = [
    [0.10, 0.16, 0.24, 0.18, 0.10],
    [0.16, 0.34, 0.56, 0.74, 0.20],
    [0.22, 0.50, 1.00, 0.48, 0.18],
    [0.14, 0.26, 0.42, 0.28, 0.12],
    [0.08, 0.14, 0.18, 0.12, 0.07],
]


def app_colour(h):
    for lim, c in ((0.135, "r1"), (0.235, "r2"), (0.45, "r3"), (0.9, "r4")):
        if h < lim:
            return c
    return "r5"


VIEWS = {
    # name: (azimuth, elevation, height scale)
    "iso": (45.0, 30.0, 2.7),
    "front": (24.0, 24.0, 2.5),
    "high": (45.0, 52.0, 3.6),
}


def build_appicon(variant, size=1024, samples=160):
    az, el, hs = VIEWS[variant]
    sc = L.reset_scene()
    M = L.std_materials()
    edge = L.material("edge_bright", "#dfe5ee", rough=0.3, spec=0.5)
    L.setup_render(sc, size, size, samples)
    L.world(sc)
    L.sun(sc, az, el)
    S, T = 4.0, 0.36
    L.rim_slab("tile", sc, L.rect_pts(-S / 2, -S / 2, S / 2, S / 2), -T, 0.0, M["navy"], edge,
               rim=0.07, rest=0.04)
    n, gap = 5, 0.1
    margin = 0.3
    cell = (S - 2 * margin - (n - 1) * gap) / n
    p = cell + gap
    mb = MB()
    for j in range(n):
        for i in range(n):
            h = APP_H[j][i]
            cx = (i - (n - 1) / 2) * p
            cy = ((n - 1) / 2 - j) * p
            mb.cbox(cx, cy, h * hs / 2, cell, cell, h * hs, M[app_colour(h)])
    mb.obj("columns", sc, bevel=0.045)
    L.ortho_camera(sc, az, el, margin=0.045)
    return sc


# ------------------------------------------------------------------ hero ---

HERO_RINGS = 8
HERO_SHARES = [0.27, 0.22, 0.17, 0.13, 0.10, 0.07, 0.04]
HERO_RES = (3200, 1800)


def _hash01(q, r, seed=0):
    rnd = random.Random(q * 73856093 ^ r * 19349663 ^ seed * 83492791)
    return rnd.random()


def hero_value(x, y, q, r):
    """Synthetic value-per-acre surface: a sharp, jagged downtown peak, two
    secondary centres, a main-street ridge and gentle noise."""
    def g(cx, cy, a, s):
        return a * math.exp(-((x - cx) ** 2 + (y - cy) ** 2) / (2 * s * s))
    jag = 0.78 + 0.44 * _hash01(q, r, 11)
    if q == 0 and r == 0:
        jag = 1.3  # the single tallest downtown tower
    v = g(0.6, 0.4, 1.0, 1.75) * jag     # downtown
    v += g(-7.6, 6.2, 0.40, 1.7) * (0.8 + 0.4 * _hash01(q, r, 3))   # older town centre, back left
    v += g(8.4, -3.6, 0.32, 1.45) * (0.8 + 0.4 * _hash01(q, r, 5))  # suburban node, front right
    # main street ridge along a line through downtown
    d = abs((x - 0.6) * 0.42 - (y - 0.4) * 0.91)
    along = (x - 0.6) * 0.91 + (y - 0.4) * 0.42
    v += 0.12 * math.exp(-d * d / 1.2) * math.exp(-along * along / 160.0)
    # broad falloff from downtown keeps the periphery calm, light noise breaks it up
    v += 0.09 * math.exp(-((x - 0.6) ** 2 + (y - 0.4) ** 2) / (2 * 7.5 ** 2))
    v += 0.022 * _hash01(q, r) + 0.03 * math.sin(x * 0.45 + 1.0) * math.cos(y * 0.38 - 0.5)
    return max(0.0, v)


def hero_class(v, breaks):
    for k, b in enumerate(breaks):
        if v < b:
            return k
    return len(breaks)


def grid_material(name, base="#f1efe9", line="#d6dbe2", cell=2.0, width=0.035):
    mat = bpy.data.materials.get(name) or bpy.data.materials.new(name)
    try:
        mat.use_nodes = True
    except Exception:
        pass
    nt = mat.node_tree
    for nd in list(nt.nodes):
        if nd.type not in {"BSDF_PRINCIPLED", "OUTPUT_MATERIAL"}:
            nt.nodes.remove(nd)
    p = next(nd for nd in nt.nodes if nd.type == "BSDF_PRINCIPLED")
    p.inputs["Roughness"].default_value = 0.7
    tc = nt.nodes.new("ShaderNodeTexCoord")
    sep = nt.nodes.new("ShaderNodeSeparateXYZ")
    nt.links.new(tc.outputs["Object"], sep.inputs[0])

    def axis_line(out):
        m = nt.nodes.new("ShaderNodeMath"); m.operation = "MULTIPLY"; m.inputs[1].default_value = 1.0 / cell
        nt.links.new(out, m.inputs[0])
        f = nt.nodes.new("ShaderNodeMath"); f.operation = "FRACT"
        nt.links.new(m.outputs[0], f.inputs[0])
        s = nt.nodes.new("ShaderNodeMath"); s.operation = "SUBTRACT"; s.inputs[1].default_value = 0.5
        nt.links.new(f.outputs[0], s.inputs[0])
        a = nt.nodes.new("ShaderNodeMath"); a.operation = "ABSOLUTE"
        nt.links.new(s.outputs[0], a.inputs[0])
        # 1 on the line, 0 elsewhere, with a soft edge
        ss = nt.nodes.new("ShaderNodeMapRange")
        ss.interpolation_type = "SMOOTHSTEP"
        ss.inputs["From Min"].default_value = 0.5 - width / cell * 1.6
        ss.inputs["From Max"].default_value = 0.5 - width / cell * 0.4
        nt.links.new(a.outputs[0], ss.inputs["Value"])
        return ss.outputs["Result"]

    lx = axis_line(sep.outputs["X"])
    ly = axis_line(sep.outputs["Y"])
    mx = nt.nodes.new("ShaderNodeMath"); mx.operation = "MAXIMUM"
    nt.links.new(lx, mx.inputs[0]); nt.links.new(ly, mx.inputs[1])
    mix = nt.nodes.new("ShaderNodeMix")
    mix.data_type = "RGBA"
    nt.links.new(mx.outputs[0], mix.inputs["Factor"])
    mix.inputs["A"].default_value = (*L.hex_lin(base), 1)
    mix.inputs["B"].default_value = (*L.hex_lin(line), 1)
    nt.links.new(mix.outputs["Result"], p.inputs["Base Color"])
    return mat


def fit_perspective(sc, cam, target, dir_vec, pts, margin=0.04, lo=5.0, hi=400.0):
    """Move the camera along dir_vec from target until pts fit inside the frame."""
    for _ in range(40):
        mid = (lo + hi) / 2
        cam.location = target + dir_vec * mid
        bpy.context.view_layer.update()
        ok = True
        for p in pts:
            c = world_to_camera_view(sc, cam, p)
            if not (margin <= c.x <= 1 - margin and margin <= c.y <= 1 - margin):
                ok = False
                break
        if ok:
            hi = mid
        else:
            lo = mid
    cam.location = target + dir_vec * hi
    return hi


def build_hero(samples=256, res=HERO_RES):
    sc = L.reset_scene()
    M = L.std_materials()
    L.setup_render(sc, res[0], res[1], samples)
    L.world(sc, color="#eef1f5", strength=0.34, name="pracre_world_hero")
    sc.cycles.max_bounces = 6

    R = 1.0
    rr = R * 0.93
    cells = []
    N = HERO_RINGS
    for q in range(-N, N + 1):
        for r in range(max(-N, -q - N), min(N, -q + N) + 1):
            x = math.sqrt(3) * R * (q + r / 2.0)
            y = 1.5 * R * r
            cells.append((q, r, x, y, hero_value(x, y, q, r)))
    vals = sorted(c[4] for c in cells)
    vmax = vals[-1]
    # quantile classes (share of cells per legend colour, low to high)
    breaks, acc = [], 0.0
    for share in HERO_SHARES[:-1]:
        acc += share
        breaks.append(vals[min(len(vals) - 1, int(acc * len(vals)))])
    hmax = 9.0
    mb = MB()
    for q, r, x, y, v in cells:
        h = 0.22 + hmax * (v / vmax) ** 1.05
        k = hero_class(v, breaks)
        mb.prism(L.ngon_pts(x, y, rr, 6, math.pi / 2), 0.0, h, M["r%d" % k])
    city = mb.obj("city", sc, bevel=0.05, segments=2)

    # base slab with faint grid
    half = (N + 1.6) * math.sqrt(3) * R
    rb = N * math.sqrt(3) * R + 1.0 * R + 1.1
    gm = grid_material("hero_grid")
    side = L.material("hero_base_side", "#c9d0da", rough=0.5)
    rim = L.material("hero_base_rim", "#ffffff", rough=0.35, spec=0.5)
    base = L.rim_slab("base", sc, L.ngon_pts(0, 0, rb, 6, 0.0), -0.5, -0.02, gm, rim,
                      rim=0.1, rest=0.05, side_mat=side)

    # shadow catcher
    mb = MB()
    mb.box(-half * 4, -half * 4, -0.48, half * 4, half * 4, -0.45, M["white"])
    catcher = mb.obj("shadow_catcher", sc, bevel=0)
    catcher.is_shadow_catcher = True

    # camera
    az, el = 36.0, 37.0
    d = L.view_dir(az, el)
    target = Vector((0.0, 0.0, 1.2))
    cd = bpy.data.cameras.new("HeroCam")
    cd.lens = 70
    cd.clip_start = 0.5
    cd.clip_end = 2000
    cam = bpy.data.objects.new("HeroCam", cd)
    sc.collection.objects.link(cam)
    cam.rotation_euler = (d).to_track_quat("Z", "Y").to_euler()
    sc.camera = cam
    pts = L._eval_points(sc, [city, base])
    fit_perspective(sc, cam, target, d, pts, margin=0.035)

    # soft key from the viewer's upper left, gentle fill from the right
    _, right, up, _ = L.cam_basis(az, el)
    horiz = Vector((d.x, d.y, 0)).normalized()
    left_h = Vector((-right.x, -right.y, 0)).normalized()
    key_pos = left_h * 38 + Vector((0, 0, 46)) + horiz * 14
    L.area_light(sc, key_pos, (0, 0, 0), size=26, energy=50000, name="Key", color=(1.0, 0.97, 0.92))
    fill_pos = -left_h * 40 + Vector((0, 0, 18)) + horiz * 20
    L.area_light(sc, fill_pos, (0, 0, 0), size=30, energy=4000, name="Fill", color=(0.9, 0.95, 1.0))
    return sc
