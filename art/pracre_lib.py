"""Shared helpers for the Pracre art scripts: palette, materials, mesh building,
lighting, camera fitting and rendering. Runs inside Blender (bpy)."""

import math
import os

import bmesh
import bpy
from mathutils import Matrix, Vector

# ---------------------------------------------------------------- palette ---

PAL = {
    "navy": "#0f2444",
    "orange": "#f26522",
    "offwhite": "#f4f1ea",
    "slate": "#5b6b82",
    "chrome": "#e6e9ee",
}
RAMP = ["#22355c", "#2e6f95", "#48a9a6", "#f2c14e", "#f26522", "#c0262d", "#6b0f2a"]

FONT_BOLD = "C:/Windows/Fonts/segoeuib.ttf"


def srgb_to_lin(c):
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def hex_lin(h):
    h = h.lstrip("#")
    return tuple(srgb_to_lin(int(h[i:i + 2], 16) / 255.0) for i in (0, 2, 4))


# -------------------------------------------------------------- materials ---

def _principled(mat):
    return next(n for n in mat.node_tree.nodes if n.type == "BSDF_PRINCIPLED")


def _set_input(node, names, value):
    for n in names:
        if n in node.inputs:
            node.inputs[n].default_value = value
            return


def material(name, color, rough=0.42, metal=0.0, spec=0.35, emit=0.0, coat=0.0):
    """Get or create a Principled material. color is a hex string (sRGB)."""
    mat = bpy.data.materials.get(name)
    if mat is None:
        mat = bpy.data.materials.new(name)
        try:
            mat.use_nodes = True
        except Exception:
            pass
    p = _principled(mat)
    lin = hex_lin(color) if isinstance(color, str) else tuple(color)
    p.inputs["Base Color"].default_value = (*lin, 1.0)
    p.inputs["Roughness"].default_value = rough
    p.inputs["Metallic"].default_value = metal
    _set_input(p, ["Specular IOR Level", "Specular"], spec)
    _set_input(p, ["Coat Weight"], coat)
    if emit > 0:
        _set_input(p, ["Emission Color"], (*lin, 1.0))
        _set_input(p, ["Emission Strength"], emit)
    else:
        _set_input(p, ["Emission Strength"], 0.0)
    mat.diffuse_color = (*lin, 1.0)
    return mat


def std_materials():
    """The shared material set, keyed by short name."""
    M = {}
    M["navy"] = material("navy", PAL["navy"], rough=0.38, spec=0.4)
    M["navy2"] = material("navy_light", "#22457a", rough=0.38, spec=0.4)
    M["orange"] = material("orange", PAL["orange"], rough=0.35, spec=0.45)
    M["amber"] = material("amber", "#f2a33a", rough=0.3, spec=0.5)
    M["gold"] = material("gold", RAMP[3], rough=0.3, spec=0.5)
    M["offwhite"] = material("offwhite", PAL["offwhite"], rough=0.55, spec=0.25)
    M["paper"] = material("paper", "#fbfaf7", rough=0.6, spec=0.2)
    M["white"] = material("white", "#ffffff", rough=0.5, spec=0.25)
    M["slate"] = material("slate", PAL["slate"], rough=0.45, spec=0.3)
    M["slate_lt"] = material("slate_light", "#9aa6b8", rough=0.5, spec=0.3)
    M["chrome"] = material("chrome", PAL["chrome"], rough=0.28, metal=0.55, spec=0.5)
    M["steel"] = material("steel", "#b9c1cc", rough=0.3, metal=0.6, spec=0.5)
    M["grid"] = material("gridline", "#c3cbd6", rough=0.6, spec=0.2)
    M["glass"] = material("glass", "#162b52", rough=0.12, spec=0.8, coat=0.6)
    M["glint"] = material("glint", "#ffffff", rough=0.5, emit=1.2)
    M["mapgreen"] = material("map_green", "#bfe0a6", rough=0.6, spec=0.2)
    M["mapblue"] = material("map_blue", "#a8d0ee", rough=0.6, spec=0.2)
    M["mapsand"] = material("map_sand", "#efe6cf", rough=0.6, spec=0.2)
    M["water"] = material("map_water", "#8fc1e3", rough=0.4, spec=0.3)
    M["dark"] = material("ink", "#0a1830", rough=0.5, spec=0.2)
    for i, c in enumerate(RAMP):
        M["r%d" % i] = material("ramp_%d" % i, c, rough=0.38, spec=0.4)
    return M


# ---------------------------------------------------------- mesh building ---

class MB:
    """Accumulates prisms/boxes/cylinders into one bmesh with material slots."""

    def __init__(self):
        self.bm = bmesh.new()
        self.mats = []

    def mi(self, mat):
        if mat not in self.mats:
            self.mats.append(mat)
        return self.mats.index(mat)

    def _finish(self, verts, faces, mat, xform, smooth=False, side_faces=None, side_mat=None):
        if xform is not None:
            bmesh.ops.transform(self.bm, matrix=xform, verts=verts)
        idx = self.mi(mat)
        for f in faces:
            f.material_index = idx
            f.smooth = smooth
        if side_faces is not None and side_mat is not None:
            si = self.mi(side_mat)
            for f in side_faces:
                f.material_index = si
        return faces

    def prism(self, pts, z0, z1, mat, xform=None, side_mat=None, top_mat=None):
        """Extrude a CCW 2D polygon (list of (x, y)) from z0 to z1."""
        bm = self.bm
        b = [bm.verts.new((x, y, z0)) for x, y in pts]
        t = [bm.verts.new((x, y, z1)) for x, y in pts]
        faces = [bm.faces.new(list(reversed(b))), bm.faces.new(t)]
        sides = []
        n = len(pts)
        for i in range(n):
            j = (i + 1) % n
            sides.append(bm.faces.new((b[i], b[j], t[j], t[i])))
        faces += sides
        self._finish(b + t, faces, mat, xform, side_faces=sides, side_mat=side_mat)
        if top_mat is not None:
            faces[1].material_index = self.mi(top_mat)
        return faces

    def box(self, x0, y0, z0, x1, y1, z1, mat, xform=None, side_mat=None, top_mat=None):
        pts = [(x0, y0), (x1, y0), (x1, y1), (x0, y1)]
        return self.prism(pts, z0, z1, mat, xform, side_mat=side_mat, top_mat=top_mat)

    def cbox(self, cx, cy, cz, sx, sy, sz, mat, xform=None, **kw):
        """Box from a centre and full size."""
        return self.box(cx - sx / 2, cy - sy / 2, cz - sz / 2,
                        cx + sx / 2, cy + sy / 2, cz + sz / 2, mat, xform, **kw)

    def cyl(self, cx, cy, z0, z1, r, mat, n=64, xform=None, smooth=True, r2=None):
        r2 = r if r2 is None else r2
        bm = self.bm
        b = [bm.verts.new((cx + r * math.cos(2 * math.pi * i / n),
                           cy + r * math.sin(2 * math.pi * i / n), z0)) for i in range(n)]
        t = [bm.verts.new((cx + r2 * math.cos(2 * math.pi * i / n),
                           cy + r2 * math.sin(2 * math.pi * i / n), z1)) for i in range(n)]
        caps = [bm.faces.new(list(reversed(b))), bm.faces.new(t)]
        sides = [bm.faces.new((b[i], b[(i + 1) % n], t[(i + 1) % n], t[i])) for i in range(n)]
        self._finish(b + t, caps, mat, xform, smooth=False)
        self._finish([], sides, mat, None, smooth=smooth)
        # cap rims stay sharp so smooth sides do not bleed into flat caps
        for f in caps:
            for e in f.edges:
                e.smooth = False
        return caps + sides

    def obj(self, name, scene, bevel=0.03, segments=1, angle=30.0, bevel_mat=None,
            parent=None, matrix=None):
        me = bpy.data.meshes.new(name)
        self.bm.normal_update()
        self.bm.to_mesh(me)
        self.bm.free()
        o = bpy.data.objects.new(name, me)
        for m in self.mats:
            me.materials.append(m)
        if bevel_mat is not None:
            if bevel_mat not in self.mats:
                me.materials.append(bevel_mat)
                self.mats.append(bevel_mat)
        scene.collection.objects.link(o)
        if bevel and bevel > 0:
            add_bevel(o, bevel, segments, angle,
                      None if bevel_mat is None else self.mats.index(bevel_mat))
        if parent is not None:
            o.parent = parent
        if matrix is not None:
            o.matrix_basis = matrix
        return o


def rim_slab(name, scene, pts, z0, z1, mat, rim_mat, rim=0.07, rest=0.03, side_mat=None):
    """Polygon slab whose top perimeter gets a chamfer in `rim_mat` (a bright edge);
    every other edge gets a plain chamfer."""
    bm = bmesh.new()
    b = [bm.verts.new((x, y, z0)) for x, y in pts]
    t = [bm.verts.new((x, y, z1)) for x, y in pts]
    bm.faces.new(list(reversed(b)))
    top = bm.faces.new(t)
    n = len(pts)
    sides = [bm.faces.new((b[i], b[(i + 1) % n], t[(i + 1) % n], t[i])) for i in range(n)]
    lay = bm.edges.layers.float.get("bevel_weight_edge") or bm.edges.layers.float.new("bevel_weight_edge")
    for e in top.edges:
        e[lay] = 1.0
    mats = [mat, rim_mat] + ([side_mat] if side_mat else [])
    if side_mat:
        for f in sides:
            f.material_index = 2
    me = bpy.data.meshes.new(name)
    bm.normal_update()
    bm.to_mesh(me)
    bm.free()
    for m in mats:
        me.materials.append(m)
    o = bpy.data.objects.new(name, me)
    scene.collection.objects.link(o)
    m1 = o.modifiers.new("RimBevel", "BEVEL")
    m1.limit_method = "WEIGHT"
    m1.width = rim
    m1.segments = 1
    m1.material = 1
    m1.use_clamp_overlap = True
    m2 = o.modifiers.new("Bevel", "BEVEL")
    m2.limit_method = "ANGLE"
    m2.angle_limit = math.radians(60)
    m2.width = rest
    m2.segments = 1
    m2.use_clamp_overlap = True
    return o


def no_shadow(o):
    """Overlay elements (arrows, pins, brackets) stay out of the shadow pass."""
    o.visible_shadow = False
    return o


def add_bevel(o, width, segments=1, angle=30.0, mat_index=None):
    m = o.modifiers.new("Bevel", "BEVEL")
    m.width = width
    m.segments = segments
    m.limit_method = "ANGLE"
    m.angle_limit = math.radians(angle)
    m.use_clamp_overlap = True
    m.offset_type = "OFFSET"
    if mat_index is not None:
        m.material = mat_index
    return m


def empty(name, scene, matrix=None, parent=None):
    e = bpy.data.objects.new(name, None)
    scene.collection.objects.link(e)
    if parent is not None:
        e.parent = parent
    if matrix is not None:
        e.matrix_basis = matrix
    return e


def frame(yaw=0.0, lean=0.0, loc=(0, 0, 0)):
    """Matrix for an upright object that faces -Y: lean back (deg) then yaw toward camera (deg)."""
    return (Matrix.Translation(Vector(loc)) @ Matrix.Rotation(math.radians(yaw), 4, "Z")
            @ Matrix.Rotation(math.radians(-lean), 4, "X"))


# Rotates a prism built in XY (extruded along +Z) so it stands on the XZ plane,
# extruded toward -Y (toward the viewer's left/front face).
FACE = Matrix.Rotation(math.radians(90), 4, "X")


def text_mesh(name, body, scene, size=1.0, extrude=0.05, mat=None, font_path=FONT_BOLD,
              center=(0.0, 0.0), bevel=0.0):
    """Create a glyph as a real mesh object lying in XY, centred on `center`, extruded +Z."""
    cu = bpy.data.curves.new(name + "_crv", "FONT")
    cu.body = body
    if font_path and os.path.exists(font_path):
        cu.font = bpy.data.fonts.load(font_path, check_existing=True)
    cu.size = size
    cu.extrude = extrude
    cu.bevel_depth = bevel
    cu.align_x = "CENTER"
    cu.align_y = "CENTER"
    tmp = bpy.data.objects.new(name + "_tmp", cu)
    scene.collection.objects.link(tmp)
    dg = bpy.context.evaluated_depsgraph_get()
    dg.update()
    ev = tmp.evaluated_get(dg)
    me = bpy.data.meshes.new_from_object(ev)
    scene.collection.objects.unlink(tmp)
    bpy.data.objects.remove(tmp)
    bpy.data.curves.remove(cu)
    # centre the glyph's xy bounds, put its back at z=0
    xs = [v.co.x for v in me.vertices]
    ys = [v.co.y for v in me.vertices]
    zs = [v.co.z for v in me.vertices]
    off = Vector((center[0] - (min(xs) + max(xs)) / 2, center[1] - (min(ys) + max(ys)) / 2, -min(zs)))
    me.transform(Matrix.Translation(off))
    for p in me.polygons:
        p.use_smooth = False
    o = bpy.data.objects.new(name, me)
    if mat is not None:
        me.materials.append(mat)
    scene.collection.objects.link(o)
    return o


# ------------------------------------------------------------ 2D shapes ----

def rect_pts(x0, y0, x1, y1):
    return [(x0, y0), (x1, y0), (x1, y1), (x0, y1)]


def ngon_pts(cx, cy, r, n, rot=0.0):
    return [(cx + r * math.cos(rot + 2 * math.pi * i / n), cy + r * math.sin(rot + 2 * math.pi * i / n))
            for i in range(n)]


def star_pts(cx, cy, r_out, r_in, n, rot=math.pi / 2):
    pts = []
    for i in range(2 * n):
        r = r_out if i % 2 == 0 else r_in
        a = rot + math.pi * i / n
        pts.append((cx + r * math.cos(a), cy + r * math.sin(a)))
    return pts


def arrow_pts(length=1.0, shaft=0.36, head_w=0.9, head_len=0.5):
    """Arrow pointing +X with its tail at x=0, centred on y=0 (CCW)."""
    s, hw, L, hl = shaft / 2, head_w / 2, length, head_len
    return [(0, -s), (L - hl, -s), (L - hl, -hw), (L, 0), (L - hl, hw), (L - hl, s), (0, s)]


def rot2(pts, ang, about=(0, 0)):
    c, s = math.cos(ang), math.sin(ang)
    ax, ay = about
    return [(ax + (x - ax) * c - (y - ay) * s, ay + (x - ax) * s + (y - ay) * c) for x, y in pts]


def move2(pts, dx, dy):
    return [(x + dx, y + dy) for x, y in pts]


def stroke_pts(points, width):
    """Outline (CCW) of an open polyline stroked with mitred joins and square ends."""
    hw = width / 2
    n = len(points)
    P = [Vector((x, y)) for x, y in points]
    left, right = [], []
    for i in range(n):
        if i == 0:
            d = (P[1] - P[0]).normalized()
            nrm = Vector((-d.y, d.x))
            off = nrm * hw
        elif i == n - 1:
            d = (P[i] - P[i - 1]).normalized()
            nrm = Vector((-d.y, d.x))
            off = nrm * hw
        else:
            d0 = (P[i] - P[i - 1]).normalized()
            d1 = (P[i + 1] - P[i]).normalized()
            n0 = Vector((-d0.y, d0.x))
            n1 = Vector((-d1.y, d1.x))
            m = (n0 + n1).normalized()
            off = m * (hw / max(0.2, m.dot(n0)))
        left.append(P[i] + off)
        right.append(P[i] - off)
    poly = [tuple(v) for v in right] + [tuple(v) for v in reversed(left)]
    area = sum(poly[i][0] * poly[(i + 1) % len(poly)][1] - poly[(i + 1) % len(poly)][0] * poly[i][1]
               for i in range(len(poly)))
    return poly if area > 0 else poly[::-1]


def check_mark(width=0.2, short=0.42, long_=1.0, a_short=140.0, a_long=52.0):
    """Check mark outline with its elbow at the origin."""
    s = (short * math.cos(math.radians(a_short)), short * math.sin(math.radians(a_short)))
    t = (long_ * math.cos(math.radians(a_long)), long_ * math.sin(math.radians(a_long)))
    return stroke_pts([s, (0.0, 0.0), t], width)


def check_pts(w=1.0, t=0.26):
    """A bold check mark polygon (CCW), roughly w wide, origin at the elbow bottom."""
    # elbow at (0,0); short arm up-left, long arm up-right
    a = math.radians(45)
    k = t / math.sqrt(2)
    short = 0.5 * w
    long_ = 0.95 * w
    p = [
        (0.0, -k * 2.0),
        (long_ * math.cos(a) + k, long_ * math.sin(a) - k),
        (long_ * math.cos(a) - k, long_ * math.sin(a) + k),
        (0.0, k * 2.0),
        (-short * math.cos(a) + k, short * math.sin(a) + k),
        (-short * math.cos(a) - k, short * math.sin(a) - k),
    ]
    return p


# ------------------------------------------------------- scene / render ----

def reset_scene():
    """Clear the working (context) scene. Every asset is built and rendered in it,
    since evaluated geometry and rendering both follow the context scene."""
    sc = bpy.context.scene
    for o in list(sc.collection.objects):
        bpy.data.objects.remove(o, do_unlink=True)
    for c in list(sc.collection.children):
        sc.collection.children.unlink(c)
    purge_orphans()
    return sc


def stash(sc, name):
    """Move the working scene's objects into a new scene named `name` (kept for the .blend)."""
    dst = bpy.data.scenes.get(name)
    if dst is not None:
        bpy.data.scenes.remove(dst)
    dst = bpy.data.scenes.new(name)
    for o in list(sc.collection.objects):
        dst.collection.objects.link(o)
        sc.collection.objects.unlink(o)
    dst.camera = sc.camera
    dst.world = sc.world
    setup_render(dst, sc.render.resolution_x, sc.render.resolution_y, sc.cycles.samples,
                 sc.render.film_transparent)
    return dst


def purge_orphans():
    for coll in (bpy.data.meshes, bpy.data.curves, bpy.data.lights, bpy.data.cameras):
        for d in list(coll):
            if d.users == 0:
                coll.remove(d)


_DEV_REPORTED = False


def setup_render(sc, res_x, res_y, samples=128, transparent=True):
    try:
        sc.render.engine = "CYCLES"
    except TypeError:
        pass
    try:
        prefs = bpy.context.preferences.addons["cycles"].preferences
        prefs.compute_device_type = "OPTIX"
        prefs.get_devices()
        for d in prefs.devices:
            d.use = d.type == "OPTIX"
        sc.cycles.device = "GPU"
        global _DEV_REPORTED
        if not _DEV_REPORTED:
            print("Cycles devices:", [d.name for d in prefs.devices if d.use], flush=True)
            _DEV_REPORTED = True
    except Exception as e:  # CPU fallback
        print("GPU setup failed:", e)
    sc.cycles.samples = samples
    sc.cycles.use_adaptive_sampling = True
    sc.cycles.adaptive_threshold = 0.005
    sc.cycles.use_denoising = True
    try:
        sc.cycles.denoiser = "OPENIMAGEDENOISE"
    except TypeError:
        pass
    sc.cycles.max_bounces = 8
    sc.cycles.pixel_filter_type = "BLACKMAN_HARRIS"
    sc.cycles.filter_width = 1.5
    sc.render.film_transparent = transparent
    sc.render.resolution_x = res_x
    sc.render.resolution_y = res_y
    sc.render.resolution_percentage = 100
    sc.render.image_settings.file_format = "PNG"
    sc.render.image_settings.color_mode = "RGBA"
    sc.render.image_settings.color_depth = "8"
    sc.view_settings.view_transform = "Standard"
    try:
        sc.view_settings.look = "None"
    except TypeError:
        pass
    sc.view_settings.exposure = 0.0
    sc.view_settings.gamma = 1.0


def world(sc, color="#e9edf3", strength=0.3, name="pracre_world_icons"):
    w = bpy.data.worlds.get(name) or bpy.data.worlds.new(name)
    try:
        w.use_nodes = True
    except Exception:
        pass
    bg = next(n for n in w.node_tree.nodes if n.type == "BACKGROUND")
    bg.inputs["Color"].default_value = (*hex_lin(color), 1.0)
    bg.inputs["Strength"].default_value = strength
    sc.world = w
    return w


def view_dir(az, el):
    """Unit vector from target toward the camera. az measured from -Y toward +X."""
    a, e = math.radians(az), math.radians(el)
    return Vector((math.cos(e) * math.sin(a), -math.cos(e) * math.cos(a), math.sin(e)))


def cam_basis(az, el):
    d = view_dir(az, el)
    q = d.to_track_quat("Z", "Y")
    m = q.to_matrix()
    right = m @ Vector((1, 0, 0))
    up = m @ Vector((0, 1, 0))
    return d, right, up, q


def sun(sc, az=45.0, el=30.0, strength=3.0, angle=4.0, left=0.55, up=1.0, toward=0.3,
        name="Key"):
    """Key sun from the viewer's upper left, expressed relative to the camera."""
    d, right, upv, _ = cam_basis(az, el)
    horiz = Vector((d.x, d.y, 0)).normalized()
    left_h = Vector((-right.x, -right.y, 0)).normalized()
    L = (left * left_h + up * Vector((0, 0, 1)) + toward * horiz).normalized()
    ld = bpy.data.lights.new(name, "SUN")
    ld.energy = strength
    ld.angle = math.radians(angle)
    o = bpy.data.objects.new(name, ld)
    o.rotation_euler = L.to_track_quat("Z", "Y").to_euler()
    sc.collection.objects.link(o)
    return o


def area_light(sc, loc, target, size=4.0, energy=300.0, name="Area", color=(1, 1, 1)):
    ld = bpy.data.lights.new(name, "AREA")
    ld.energy = energy
    ld.size = size
    ld.color = color
    o = bpy.data.objects.new(name, ld)
    o.location = Vector(loc)
    o.rotation_euler = (Vector(loc) - Vector(target)).to_track_quat("Z", "Y").to_euler()
    sc.collection.objects.link(o)
    return o


def _eval_points(sc, objs=None):
    dg = bpy.context.evaluated_depsgraph_get()
    dg.update()
    pts = []
    for o in (objs or sc.objects):
        if o.type not in {"MESH", "CURVE", "FONT"}:
            continue
        if o.hide_render:
            continue
        if getattr(o, "is_shadow_catcher", False):
            continue
        ev = o.evaluated_get(dg)
        try:
            me = ev.to_mesh()
        except RuntimeError:
            continue
        mw = ev.matrix_world
        pts += [mw @ v.co for v in me.vertices]
        ev.to_mesh_clear()
    return pts


def screen_bbox(sc, az=45.0, el=30.0, objs=None):
    """(xmin, xmax, ymin, ymax, front_depth) of the scene in camera-plane units."""
    d, right, up, _ = cam_basis(az, el)
    pts = _eval_points(sc, objs)
    xs = [p.dot(right) for p in pts]
    ys = [p.dot(up) for p in pts]
    zs = [p.dot(d) for p in pts]
    return min(xs), max(xs), min(ys), max(ys), max(zs)


def screen_to_world(sx, sy, depth, az=45.0, el=30.0):
    d, right, up, _ = cam_basis(az, el)
    return right * sx + up * sy + d * depth


def ortho_camera(sc, az=45.0, el=30.0, margin=0.06, objs=None, aspect=1.0, shift=(0.0, 0.0)):
    """Orthographic camera fitted to the scene's evaluated geometry."""
    d, right, up, q = cam_basis(az, el)
    pts = _eval_points(sc, objs)
    xs = [p.dot(right) for p in pts]
    ys = [p.dot(up) for p in pts]
    zs = [p.dot(d) for p in pts]
    cx, cy = (min(xs) + max(xs)) / 2, (min(ys) + max(ys)) / 2
    w, h = max(xs) - min(xs), max(ys) - min(ys)
    span = max(w / aspect, h) if aspect >= 1 else max(w, h * aspect)
    cd = bpy.data.cameras.new("Cam")
    cd.type = "ORTHO"
    if aspect >= 1:
        cd.ortho_scale = max(w, h * aspect) / (1 - 2 * margin)
    else:
        cd.ortho_scale = max(w / aspect, h) / (1 - 2 * margin)
    cd.clip_start = 0.01
    cd.clip_end = 1000
    cam = bpy.data.objects.new("Cam", cd)
    centre = right * (cx + shift[0] * cd.ortho_scale) + up * (cy + shift[1] * cd.ortho_scale)
    cam.location = centre + d * (max(zs) + 20.0)
    cam.rotation_euler = q.to_euler()
    sc.collection.objects.link(cam)
    sc.camera = cam
    return cam


def render(sc, path, res_x=None, res_y=None, samples=None):
    if res_x:
        sc.render.resolution_x = res_x
    if res_y:
        sc.render.resolution_y = res_y
    if samples:
        sc.cycles.samples = samples
    os.makedirs(os.path.dirname(path), exist_ok=True)
    sc.render.filepath = path
    bpy.ops.render.render(write_still=True)
    return path


def cam_plane(az=45.0, origin=(0, 0, 0), lean=0.0):
    """Matrix whose XY plane faces the camera horizontally (X = screen right, Y = up),
    optionally leaned back by `lean` degrees; +Z points toward the camera."""
    a = math.radians(az)
    n = Vector((math.sin(a), -math.cos(a), 0.0))
    u = Vector((math.cos(a), math.sin(a), 0.0))
    v = Vector((0.0, 0.0, 1.0))
    if lean:
        r = Matrix.Rotation(math.radians(-lean), 3, u)
        v, n = r @ v, r @ n
    m = Matrix((u, v, n)).transposed().to_4x4()
    m.translation = Vector(origin)
    return m


def inset(pts, g):
    """Inset a CCW polygon by g (straight-skeleton-free: offsets edges and intersects neighbours)."""
    n = len(pts)
    lines = []
    for i in range(n):
        x0, y0 = pts[i]
        x1, y1 = pts[(i + 1) % n]
        dx, dy = x1 - x0, y1 - y0
        ln = math.hypot(dx, dy)
        nx, ny = -dy / ln, dx / ln  # inward normal for CCW
        lines.append(((x0 + nx * g, y0 + ny * g), (dx, dy)))
    out = []
    for i in range(n):
        (p, d) = lines[i - 1]
        (q, e) = lines[i]
        den = d[0] * e[1] - d[1] * e[0]
        if abs(den) < 1e-9:
            out.append(q)
            continue
        t = ((q[0] - p[0]) * e[1] - (q[1] - p[1]) * e[0]) / den
        out.append((p[0] + d[0] * t, p[1] + d[1] * t))
    return out


def pin_pts(h=1.0, r=0.4, n=40):
    """Map-pin outline (tip at the origin, head circle on top), CCW."""
    c = h - r
    phi = math.acos(r / c)
    a0 = -math.pi / 2 + phi
    a1 = 3 * math.pi / 2 - phi
    pts = [(0.0, 0.0)]
    for i in range(n + 1):
        a = a0 + (a1 - a0) * i / n
        pts.append((r * math.cos(a), c + r * math.sin(a)))
    return pts
