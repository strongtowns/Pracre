"""Toolbar icon builders. Each builder takes (scene, materials) and adds geometry
to the scene; the caller adds lights and the shared 3/4 orthographic camera.

Conventions: Z is up. The camera sits at azimuth 45 deg (between -Y and +X) and
elevation 30 deg, so -Y faces read as the lit left face and +X faces as the
shaded right face. Upright objects are modelled facing -Y on the XZ plane and
placed with L.frame(yaw, lean)."""

import math

from mathutils import Matrix, Vector

import pracre_lib as L
from pracre_lib import FACE, MB

AZ, EL = 45.0, 30.0


# ------------------------------------------------------------- helpers -----

def fbox(mb, x0, z0, x1, z1, d0, d1, mat, **kw):
    """Box on the front (XZ) plane; depth d0..d1 measured toward the viewer (-Y)."""
    mb.box(x0, -d1, z0, x1, -d0, z1, mat, **kw)


def fprism(mb, pts, d0, d1, mat, xform=None, **kw):
    """2D polygon in face coordinates (x across, z up) extruded toward the viewer."""
    x = FACE if xform is None else xform @ FACE
    mb.prism(pts, d0, d1, mat, xform=x, **kw)


def glyph(sc, body, root, size, cx, cz, depth, extrude, mat, name=None):
    o = L.text_mesh(name or ("glyph_" + body), body, sc, size=size, extrude=extrude, mat=mat,
                    center=(cx, cz))
    o.parent = root
    o.matrix_basis = Matrix.Translation((0, -depth, 0)) @ FACE
    return o


def arrow(sc, M, origin, angle_deg, length=1.3, shaft=0.44, head_w=1.04, head_len=0.6,
          depth=0.26, lean=16, name="arrow"):
    """Bold orange arrow in a camera-facing plane; origin is the tail centre."""
    pts = L.rot2(L.arrow_pts(length, shaft, head_w, head_len), math.radians(angle_deg))
    mb = MB()
    mb.prism(pts, 0.0, depth, M["orange"], xform=L.cam_plane(AZ, origin, lean))
    return L.no_shadow(mb.obj(name, sc, bevel=0.035))


def arrow_at(sc, M, fx, fy, angle_deg, length=1.2, scale=1.0, **kw):
    """Arrow whose tail sits at fraction (fx, fy) of the current screen bounds,
    in front of everything built so far."""
    x0, x1, y0, y1, front = L.screen_bbox(sc, AZ, EL)
    org = L.screen_to_world(x0 + fx * (x1 - x0), y0 + fy * (y1 - y0), front + 0.4, AZ, EL)
    kw.setdefault("shaft", 0.42 * scale)
    kw.setdefault("head_w", 1.0 * scale)
    kw.setdefault("head_len", 0.58 * scale)
    return arrow(sc, M, origin=tuple(org), angle_deg=angle_deg, length=length * scale, **kw)


def paper_sheet(sc, M, root, W, H, T=0.09, name="sheet", mat=None):
    mb = MB()
    fbox(mb, -W / 2, 0, W / 2, H, -T, 0, mat or M["paper"])
    return mb.obj(name, sc, bevel=0.035, bevel_mat=M["slate_lt"], parent=root)


# Shared colour/height layout for view2d / view3d (rows back to front).
TILE_IDX = [[2, 3, 1],
            [3, 5, 2],
            [1, 4, 2]]
TILE_H = [[0.50, 0.80, 0.32],
          [0.78, 1.40, 0.55],
          [0.30, 0.95, 0.45]]


def map_tile(sc, M, heights=None, S=2.2, T=0.28, flat=0.05):
    mb = MB()
    mb.box(-S / 2, -S / 2, -T, S / 2, S / 2, 0, M["navy"])
    mb.obj("tile", sc, bevel=0.04, bevel_mat=M["slate"])
    n, gap = 3, 0.09
    margin = 0.13
    cell = (S - 2 * margin - (n - 1) * gap) / n
    pitch = cell + gap
    mb = MB()
    for j in range(n):
        for i in range(n):
            cx = (i - 1) * pitch
            cy = (1 - j) * pitch
            h = flat if heights is None else heights[j][i]
            mb.cbox(cx, cy, h / 2, cell, cell, h, M["r%d" % TILE_IDX[j][i]])
    mb.obj("cells", sc, bevel=0.03 if heights is not None else 0.015)


# ------------------------------------------------------------- icons -------

def icon_new(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    W, H = 1.5, 1.95
    paper_sheet(sc, M, root, W, H)
    mb = MB()
    fprism(mb, L.star_pts(W / 2 - 0.1, H - 0.1, 0.58, 0.33, 8), 0.0, 0.16, M["orange"])
    mb.obj("star", sc, bevel=0.03, parent=root)


def icon_open(sc, M):
    root = L.empty("root", sc, L.frame(yaw=16))
    # back panel with tab, leaning back
    back = L.empty("back", sc, Matrix.Translation((0, 0.42, 0)) @ Matrix.Rotation(math.radians(-16), 4, "X"),
                   parent=root)
    mb = MB()
    fbox(mb, -1.1, 0, 1.1, 1.5, -0.13, 0, M["navy"])
    mb.obj("back_panel", sc, bevel=0.035, bevel_mat=M["slate"], parent=back)
    mb = MB()
    fprism(mb, [(-1.1, 1.44), (-0.18, 1.44), (-0.4, 1.78), (-1.1, 1.78)], -0.13, 0.0, M["orange"])
    mb.obj("tab", sc, bevel=0.03, parent=back)
    # paper sticking out on the right, leaving the tab clear
    pap = L.empty("paper", sc, Matrix.Translation((0.12, 0.22, 0.1)) @ Matrix.Rotation(math.radians(-10), 4, "X")
                  @ Matrix.Rotation(math.radians(-4), 4, "Y"), parent=root)
    mb = MB()
    fbox(mb, -0.62, 0, 0.8, 1.62, -0.05, 0, M["paper"])
    mb.obj("paper", sc, bevel=0.025, bevel_mat=M["slate_lt"], parent=pap)
    # front flap, skewed to read as opening
    mb = MB()
    fprism(mb, [(-1.1, 0), (1.1, 0), (1.3, 1.0), (-0.9, 1.0)], 0.0, 0.13, M["navy2"])
    mb.obj("front_flap", sc, bevel=0.035, bevel_mat=M["slate"], parent=root)


def icon_save(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    S, c = 1.9, 0.3
    mb = MB()
    fprism(mb, [(-S / 2, 0), (S / 2, 0), (S / 2, S - c), (S / 2 - c, S), (-S / 2, S)], -0.18, 0.0, M["navy"])
    mb.obj("body", sc, bevel=0.04, bevel_mat=M["slate"], parent=root)
    mb = MB()
    fbox(mb, -0.52, S - 0.70, 0.40, S, 0.0, 0.035, M["chrome"])
    mb.obj("shutter", sc, bevel=0.02, parent=root)
    mb = MB()
    fbox(mb, 0.04, S - 0.58, 0.27, S - 0.12, 0.035, 0.05, M["dark"])
    mb.obj("slot", sc, bevel=0.008, parent=root)
    mb = MB()
    fbox(mb, -0.70, 0.12, 0.70, 0.98, 0.0, 0.04, M["paper"])
    mb.obj("label", sc, bevel=0.02, parent=root)
    mb = MB()
    fbox(mb, -0.70, 0.80, 0.70, 0.98, 0.04, 0.05, M["orange"])
    mb.obj("label_band", sc, bevel=0.008, parent=root)


def icon_import(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    W, H = 1.6, 1.95
    paper_sheet(sc, M, root, W, H)
    mb = MB()
    m = 0.1
    fbox(mb, -W / 2 + m, H - 0.46, W / 2 - m, H - m, 0, 0.035, M["navy"])
    mb.obj("header", sc, bevel=0.012, parent=root)
    mb = MB()
    lw = 0.035
    for x in (-0.24, 0.27):
        fbox(mb, x - lw, m, x + lw, H - 0.46, 0, 0.026, M["slate_lt"])
    for z in (0.47, 0.86, 1.24):
        fbox(mb, -W / 2 + m, z - lw, W / 2 - m, z + lw, 0, 0.02, M["slate_lt"])
    mb.obj("grid", sc, bevel=0.0, parent=root)
    arrow(sc, M, origin=(0.05, -0.55, H + 0.45), angle_deg=-90, length=1.35)


def icon_rates(sc, M):
    mb = MB()
    # coin stack sits back-left on screen, the tag front-right
    cx, cy, r, t = -0.53, -0.22, 0.62, 0.17
    offs = [(0.0, 0.0), (0.05, -0.03), (-0.03, 0.03), (0.04, 0.0), (-0.01, 0.02)]
    for i, (dx, dy) in enumerate(offs):
        mb.cyl(cx + dx, cy + dy, i * t, i * t + t - 0.014, r, M["amber"])
    top = len(offs) * t - 0.014
    dx, dy = offs[-1]
    mb.cyl(cx + dx, cy + dy, top, top + 0.03, r * 0.72, M["gold"])
    mb.obj("coins", sc, bevel=0.03, segments=2)
    # price tag with a navy percent sign
    tag = L.empty("tag", sc, L.frame(yaw=30, lean=14, loc=(0.6, 0.0, 0.0))
                  @ Matrix.Rotation(math.radians(-10), 4, "Y"))
    mb = MB()
    fprism(mb, [(-0.5, 0), (0.5, 0), (0.5, 0.98), (0.0, 1.4), (-0.5, 0.98)], -0.08, 0.0, M["offwhite"])
    mb.obj("tag_body", sc, bevel=0.03, bevel_mat=M["slate_lt"], parent=tag)
    mb = MB()
    mb.cyl(0.0, 1.1, 0.0, 0.015, 0.1, M["slate"], xform=FACE)
    mb.obj("tag_hole", sc, bevel=0.0, parent=tag)
    glyph(sc, "%", tag, 0.92, 0.0, 0.48, 0.0, 0.025, M["navy"])


def icon_basemap(sc, M):
    # zigzag runs along the screen-right axis, folds alternate toward the camera
    a = math.radians(AZ)
    u = Vector((math.cos(a), math.sin(a), 0))
    n = Vector((math.sin(a), -math.cos(a), 0))
    lean = Matrix.Rotation(math.radians(-10), 4, u)
    P = [tuple(u * s + n * o) for s, o in ((-1.05, 0.0), (-0.35, 0.36), (0.35, 0.0), (1.05, 0.36))]
    P = [(p[0], p[1]) for p in P]
    H, T = 1.5, 0.06
    cols = [M["mapgreen"], M["mapblue"], M["mapgreen"]]
    # unfolded road polylines: (s, z) pairs
    s_starts, lens = [], []
    s = 0.0
    for i in range(3):
        (x0, y0), (x1, y1) = P[i], P[i + 1]
        ln = math.hypot(x1 - x0, y1 - y0)
        s_starts.append(s)
        lens.append(ln)
        s += ln
    total = s
    roads = [((0.0, 0.5), (total, 0.62), "gold"), ((0.3, 1.5), (total * 0.66, 0.0), "white")]
    rw = 0.085
    for i in range(3):
        (x0, y0), (x1, y1) = P[i], P[i + 1]
        ang = math.atan2(y1 - y0, x1 - x0)
        mtx = lean @ Matrix.Translation((x0, y0, 0)) @ Matrix.Rotation(ang, 4, "Z")
        pan = L.empty("panel%d" % i, sc, mtx)
        mb = MB()
        fbox(mb, 0, 0, lens[i], H, -T, 0, cols[i])
        mb.obj("panel%d" % i, sc, bevel=0.025, bevel_mat=M["slate_lt"], parent=pan)
        mb = MB()
        sa, sb = s_starts[i], s_starts[i] + lens[i]
        for (p0, p1, rcol) in roads:
            (u0, v0), (u1, v1) = p0, p1
            lo, hi = max(min(u0, u1), sa), min(max(u0, u1), sb)
            if hi <= lo:
                continue
            f = lambda u: v0 + (v1 - v0) * (u - u0) / (u1 - u0)
            za, zb = f(lo), f(hi)
            xa, xb = lo - sa, hi - sa
            # strip perpendicular half-width in z (approximate for slope)
            slope = (v1 - v0) / (u1 - u0)
            hz = rw * math.sqrt(1 + slope * slope)
            pts = [(xa, za - hz), (xb, zb - hz), (xb, zb + hz), (xa, za + hz)]
            pts = [(x, max(0.04, min(H - 0.04, z))) for x, z in pts]
            fprism(mb, pts, 0.0, 0.015, M[rcol])
        mb.obj("roads%d" % i, sc, bevel=0.0, parent=pan)
    # map pin, tip on the middle panel, head above the map
    pin_h, pin_r = 0.98, 0.37
    org = tuple(u * 0.3 + n * 0.75 + Vector((0, 0, 0.72)))
    pl = L.cam_plane(AZ, org, lean=14)
    mb = MB()
    mb.prism(L.pin_pts(pin_h, pin_r), 0.0, 0.26, M["orange"], xform=pl)
    L.no_shadow(mb.obj("pin", sc, bevel=0.035))
    mb = MB()
    mb.prism(L.ngon_pts(0.0, pin_h - pin_r, 0.17, 40), 0.26, 0.28, M["white"], xform=pl)
    L.no_shadow(mb.obj("pin_dot", sc, bevel=0.0))


def icon_view2d(sc, M):
    map_tile(sc, M, heights=None)


def icon_view3d(sc, M):
    map_tile(sc, M, heights=TILE_H)


def icon_parcels(sc, M):
    b0, b1, b2, b3 = (-1.25, -0.95), (-0.45, -1.05), (0.35, -1.0), (1.25, -0.85)
    m0, m1, m2, m3 = (-1.2, 0.05), (-0.3, 0.22), (0.25, -0.05), (1.2, 0.15)
    t0, t1, t3 = (-1.1, 1.0), (-0.1, 1.1), (1.1, 0.95)
    lots = [
        ([b0, b1, m1, m0], 0.24, "r2"),
        ([b1, b2, m2, m1], 0.50, "r5"),
        ([b2, b3, m3, m2], 0.30, "r3"),
        ([m0, m1, t1, t0], 0.38, "r4"),
        ([m1, m2, m3, t3, t1], 0.16, "r1"),
    ]
    mb = MB()
    for pts, h, col in lots:
        mb.prism(L.inset(pts, 0.055), 0.0, h, M[col])
    mb.obj("lots", sc, bevel=0.03)


def icon_hex(sc, M):
    R = 0.56
    r = R * 0.92
    d = math.sqrt(3) * R
    cells = [((0.0, 0.0), 1.45, "r5")]
    spec = {30: (0.70, "r3"), 90: (1.0, "r4"), 150: (0.6, "r3"),
            210: (0.38, "r2"), 270: (0.24, "r1"), 330: (0.46, "r2")}
    for a, (h, col) in spec.items():
        cells.append(((d * math.cos(math.radians(a)), d * math.sin(math.radians(a))), h, col))
    mb = MB()
    for (cx, cy), h, col in cells:
        mb.prism(L.ngon_pts(cx, cy, r, 6, 0.0), 0.0, h, M[col])
    mb.obj("hexes", sc, bevel=0.03)


def icon_square(sc, M):
    H = [[0.80, 1.15, 0.62],
         [1.00, 1.80, 0.72],
         [0.48, 0.88, 0.40]]
    def col(h):
        for lim, c in ((0.55, "r1"), (0.75, "r2"), (0.95, "r3"), (1.3, "r4")):
            if h < lim:
                return c
        return "r5"
    cell, gap = 0.72, 0.08
    p = cell + gap
    mb = MB()
    for j in range(3):
        for i in range(3):
            h = H[j][i]
            mb.cbox((i - 1) * p, (1 - j) * p, h / 2, cell, cell, h, M[col(h)])
    mb.obj("columns", sc, bevel=0.035)


def icon_export_image(sc, M):
    root = L.empty("root", sc, L.frame(yaw=24))
    mb = MB()
    fbox(mb, -1.15, 0, 1.15, 1.32, -0.8, 0, M["navy"])
    fbox(mb, -0.42, 1.32, 0.42, 1.62, -0.62, -0.12, M["navy"])
    mb.obj("body", sc, bevel=0.04, bevel_mat=M["slate"], parent=root)
    mb = MB()
    fbox(mb, -0.98, 1.32, -0.56, 1.46, -0.55, -0.22, M["orange"])
    mb.obj("shutter_btn", sc, bevel=0.025, parent=root)
    mb = MB()
    fbox(mb, 0.62, 0.98, 0.98, 1.17, 0.0, 0.04, M["offwhite"])
    mb.obj("flash", sc, bevel=0.012, parent=root)
    mb = MB()
    mb.cyl(0.0, 0.64, 0.0, 0.24, 0.56, M["slate"], xform=FACE)
    mb.obj("lens_ring", sc, bevel=0.03, segments=2, parent=root)
    mb = MB()
    mb.cyl(0.0, 0.64, 0.24, 0.28, 0.41, M["glass"], xform=FACE)
    mb.obj("lens_glass", sc, bevel=0.015, segments=2, parent=root)
    # glint
    mb = MB()
    gl = [(-0.24, 0.80), (-0.12, 0.92), (-0.04, 0.86), (-0.17, 0.73)]
    fprism(mb, gl, 0.28, 0.285, M["glint"])
    mb.obj("glint", sc, bevel=0.0, parent=root)


def icon_export_3d(sc, M):
    mb = MB()
    mb.box(-0.85, -0.7, 0.0, 0.85, 0.7, 0.14, M["slate"])
    mb.obj("base", sc, bevel=0.035)
    bars = [((-0.38, 0.3), 0.62, "r2"), ((0.38, 0.3), 1.15, "r4"),
            ((-0.38, -0.33), 0.38, "r1"), ((0.38, -0.33), 0.8, "r3")]
    mb = MB()
    for (cx, cy), h, col in bars:
        mb.cbox(cx, cy, 0.14 + h / 2, 0.6, 0.54, h, M[col])
    mb.obj("bars", sc, bevel=0.035)
    arrow_at(sc, M, 0.66, 0.6, 40, length=1.25)


def icon_export_web(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    W, H = 2.1, 1.75
    mb = MB()
    fbox(mb, -W / 2, 0, W / 2, H, -0.12, 0, M["navy"])
    mb.obj("frame", sc, bevel=0.035, bevel_mat=M["slate"], parent=root)
    mb = MB()
    fbox(mb, -W / 2 + 0.1, 0.1, W / 2 - 0.1, H - 0.36, 0, 0.02, M["paper"])
    mb.obj("content", sc, bevel=0.01, parent=root)
    # tiny map: choropleth squares
    mb = MB()
    cw, ch, g = 0.36, 0.3, 0.07
    cols = [["r1", "r2", "r3", "r1"], ["r2", "r5", "r4", "r2"], ["r0", "r3", "r2", "r1"]]
    x0 = -W / 2 + 0.22
    z_top = H - 0.48
    for r_, row in enumerate(cols):
        for c_, cname in enumerate(row):
            xa = x0 + c_ * (cw + g)
            za = z_top - (r_ + 1) * ch - r_ * g
            fbox(mb, xa, za, xa + cw, za + ch, 0.02, 0.05, M[cname])
    mb.obj("minimap", sc, bevel=0.012, parent=root)
    # title bar buttons
    mb = MB()
    for k in range(2):
        xa = W / 2 - 0.26 - k * 0.22
        fbox(mb, xa, H - 0.27, xa + 0.14, H - 0.13, 0.0, 0.02, M["offwhite"])
    mb.obj("tb_buttons", sc, bevel=0.0, parent=root)
    arrow_at(sc, M, 0.72, 0.55, 40, length=1.2)


def icon_export_data(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    W, H = 1.6, 1.95
    paper_sheet(sc, M, root, W, H)
    glyph(sc, "{ }", root, 1.5, -0.08, 0.84, 0.0, 0.03, M["navy"], name="braces")
    arrow_at(sc, M, 0.74, 0.58, 40, length=1.2)


def icon_checklist(sc, M):
    root = L.empty("root", sc, L.frame(yaw=20, lean=20))
    W, H = 1.8, 2.1
    mb = MB()
    fbox(mb, -W / 2, 0, W / 2, H, -0.12, 0, M["navy"])
    mb.obj("board", sc, bevel=0.035, bevel_mat=M["slate"], parent=root)
    mb = MB()
    fbox(mb, -W / 2 + 0.14, 0.14, W / 2 - 0.14, H - 0.3, 0.0, 0.03, M["paper"])
    mb.obj("paper", sc, bevel=0.015, parent=root)
    mb = MB()
    fbox(mb, -0.45, H - 0.42, 0.45, H - 0.08, 0.03, 0.14, M["chrome"])
    fbox(mb, -0.2, H - 0.08, 0.2, H + 0.1, 0.06, 0.11, M["chrome"])
    mb.obj("clip", sc, bevel=0.025, parent=root)
    mb = MB()
    lines = MB()
    for k, z in enumerate((1.33, 0.88, 0.43)):
        pts = [(x * 0.5, y * 0.5) for x, y in L.check_mark(0.24, 0.45, 1.0)]
        pts = L.move2(pts, -0.42, z - 0.2)
        fprism(mb, pts, 0.03, 0.1, M["orange"])
        fbox(lines, -0.1, z - 0.05, 0.6, z + 0.06, 0.03, 0.05, M["slate_lt"])
    mb.obj("checks", sc, bevel=0.015, parent=root)
    lines.obj("lines", sc, bevel=0.0, parent=root)


def icon_help(sc, M):
    root = L.empty("root", sc, L.frame(yaw=22, lean=10))
    S = 1.9
    mb = MB()
    fbox(mb, -S / 2, 0, S / 2, S, -0.32, 0, M["navy"])
    mb.obj("badge", sc, bevel=0.05, bevel_mat=M["slate"], parent=root)
    glyph(sc, "?", root, 2.3, 0.0, S / 2, 0.0, 0.035, M["white"])


def icon_zoom_fit(sc, M):
    S, T = 1.5, 0.18
    mb = MB()
    mb.box(-S / 2, -S / 2, -T, S / 2, S / 2, 0, M["navy"])
    mb.obj("tile", sc, bevel=0.035, bevel_mat=M["slate"])
    cell, gap = 0.56, 0.1
    p = cell + gap
    spec = [[(0.42, "r4"), (0.62, "r5")], [(0.16, "r1"), (0.3, "r2")]]
    mb = MB()
    for j in range(2):
        for i in range(2):
            h, col = spec[j][i]
            mb.cbox((i - 0.5) * p, (0.5 - j) * p, h / 2, cell, cell, h, M[col])
    mb.obj("cells", sc, bevel=0.03)
    # four L brackets in a camera-facing plane around the tile
    x0, x1, y0, y1, front = L.screen_bbox(sc, AZ, EL)
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    half = max(x1 - x0, y1 - y0) / 2 + 0.3
    A, w = 0.62, 0.22
    for sx in (-1, 1):
        for sy in (-1, 1):
            org = L.screen_to_world(cx + sx * half, cy + sy * half, front + 0.3, AZ, EL)
            p2 = [(0, 0), (-sx * A, 0), (-sx * A, -sy * w), (-sx * w, -sy * w), (-sx * w, -sy * A), (0, -sy * A)]
            area = sum(p2[i][0] * p2[(i + 1) % 6][1] - p2[(i + 1) % 6][0] * p2[i][1] for i in range(6))
            if area < 0:
                p2 = p2[::-1]
            mb = MB()
            mb.prism(p2, 0.0, 0.22, M["orange"], xform=L.cam_plane(AZ, tuple(org), lean=18))
            L.no_shadow(mb.obj("bracket", sc, bevel=0.03))


ICONS = {
    "new": icon_new,
    "open": icon_open,
    "save": icon_save,
    "import": icon_import,
    "rates": icon_rates,
    "basemap": icon_basemap,
    "view2d": icon_view2d,
    "view3d": icon_view3d,
    "parcels": icon_parcels,
    "hex": icon_hex,
    "square": icon_square,
    "export-image": icon_export_image,
    "export-3d": icon_export_3d,
    "export-web": icon_export_web,
    "export-data": icon_export_data,
    "checklist": icon_checklist,
    "help": icon_help,
    "zoom-fit": icon_zoom_fit,
}
