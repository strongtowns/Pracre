"""Builds epsg.txt.gz, Pracre's coordinate system table, from PROJ's proj.db.

    python gen.py path/to/proj.db

proj.db ships with PROJ (and with the pyproj package, under pyproj/proj_dir/share/proj). The table
holds every EPSG and ESRI projected and geographic system: projection parameters in degrees and
metres, ellipsoids, prime meridians, Helmert shifts to WGS 84 with their areas of use, and datum
names so a .prj without codes can still find its datum. Lines are tab-separated:

  E id west south east north name             area of use
  G code auth a invf pm_deg unit_deg datum name     geographic system (unit_deg: degrees per axis unit)
  D datum wgs84like name|alias|...            datum and its names, normalised
  H datum method tx ty tz rx ry rz ds acc extent [px py pz]   shift to WGS 84 (m, arc-seconds, ppm)
  P code auth geog method unit_m extent key=v,... name   projected system (degrees, metres)
  W code extent wkt                            ESRI system defined only by its WKT
"""
import gzip
import math
import re
import sqlite3
import sys

db = sqlite3.connect(sys.argv[1])
q = lambda s, *a: db.execute(s, a).fetchall()
AUTHS = ("EPSG", "ESRI")  # their codes never collide, so Pracre keys on the number alone

units = {(a, c): (t, f) for a, c, t, f in q("select auth_name, code, type, conv_factor from unit_of_measure")}


def dms(v):
    # EPSG's "sexagesimal DMS" unit: 49.3015 means 49 deg 30 min 15 s
    s = "%.10f" % abs(v)
    d, frac = s.split(".")
    deg = int(d) + int(frac[0:2]) / 60 + float(frac[2:4] + "." + frac[4:]) / 3600
    return -deg if v < 0 else deg


def to_deg(v, ua, uc):
    if uc == 9110:
        return dms(v)
    f = units[(ua, uc)][1]
    if f is None:
        return v  # an axis written as degrees-minutes-seconds text; files hold decimal degrees
    return v * f * 180 / math.pi


def to_si(v, ua, uc):
    if v is None:
        return 0.0
    if ua is None:
        return v
    t, f = units[(ua, uc)]
    if t == "angle":
        return to_deg(v, ua, uc)
    return v * f


# Areas of use: one bounding box per object (the union when it has several).
extents, ext_id = {}, {}


def extent_of(table, auth, code):
    rows = q("""select e.name, e.west_lon, e.south_lat, e.east_lon, e.north_lat from usage u join extent e
        on e.auth_name=u.extent_auth_name and e.code=u.extent_code
        where u.object_table_name=? and u.object_auth_name=? and u.object_code=?""", table, auth, str(code))
    rows = [r for r in rows if None not in r[1:]]
    if not rows:
        return 0
    name = rows[0][0]
    w, s, e, n = rows[0][1:]
    for r in rows[1:]:
        if r[1] <= r[3] and w <= e:  # boxes crossing the antimeridian are kept as they are
            w, e = min(w, r[1]), max(e, r[3])
        s, n = min(s, r[2]), max(n, r[4])
    key = (name, w, s, e, n)
    if key not in ext_id:
        ext_id[key] = len(ext_id) + 1
        extents[ext_id[key]] = key
    return ext_id[key]


def norm(name):
    n = name.lower()
    if n.startswith("d_"):
        n = n[2:]
    return re.sub(r"[^a-z0-9]", "", n)


# Datums. WGS 84, its realisations, ITRF and the datums EPSG shifts to WGS 84 by zero are treated as
# WGS 84: they agree to a metre or two, far below parcel size.
datums = {}
for auth, code, name, ell_a, ell_c, pm_a, pm_c in q("""select auth_name, code, name, ellipsoid_auth_name, ellipsoid_code,
        prime_meridian_auth_name, prime_meridian_code from geodetic_datum where auth_name in ('EPSG','ESRI')"""):
    datums[(auth, code)] = {"name": name, "ell": (ell_a, ell_c), "pm": (pm_a, pm_c), "names": {norm(name)}}
for auth, code, alt in q("select auth_name, code, alt_name from alias_name where table_name='geodetic_datum'"):
    if (auth, code) in datums:
        datums[(auth, code)]["names"].add(norm(alt))

geog = {}  # (auth, code) -> datum key
for auth, code, dauth, dcode in q("select auth_name, code, datum_auth_name, datum_code from geodetic_crs where auth_name in ('EPSG','ESRI')"):
    geog[(auth, code)] = (dauth, dcode)

wgs = {("EPSG", 6326)}
for k, d in datums.items():
    if "worldgeodeticsystem1984" in norm(d["name"]) or "internationalterrestrialreferenceframe" in norm(d["name"]) or norm(d["name"]).startswith("wgs84"):
        wgs.add(k)
helm = q("""select auth_name, code, method_code, source_crs_auth_name, source_crs_code, target_crs_auth_name, target_crs_code,
        accuracy, tx, ty, tz, translation_uom_auth_name, translation_uom_code, rx, ry, rz, rotation_uom_auth_name, rotation_uom_code,
        scale_difference, scale_difference_uom_auth_name, scale_difference_uom_code, px, py, pz, pivot_uom_auth_name, pivot_uom_code
        from helmert_transformation_table where deprecated=0 and method_code in (9603, 9606, 9607, 9636, 1063)""")
for h in helm:
    src, dst = geog.get((h[3], h[4])), geog.get((h[5], h[6]))
    if dst in wgs and src and not any(h[8:11]) and not any(x or 0 for x in h[13:16]) and not h[18]:
        wgs.add(src)

def box_of(table, auth, code):
    i = extent_of(table, auth, code)
    return extents[i] if i else None


def box_id(box):
    if box not in ext_id:
        ext_id[box] = len(ext_id) + 1
        extents[ext_id[box]] = box
    return ext_id[box]


def meet(a, b):
    if a is None or b is None:
        return a or b
    w, s, e, n = max(a[1], b[1]), max(a[2], b[2]), min(a[3], b[3]), min(a[4], b[4])
    return (a[0], w, s, e, n) if w < e and s < n else False


# Every operation between two datums that Pracre can apply: Helmert shifts (rewritten as position
# vector so chains can be added up), Molodensky-Badekas shifts and latitude/longitude offsets.
ops = []  # (src datum, dst datum, kind h|m|o, params, accuracy, area)
for h in helm:
    src, dst = geog.get((h[3], h[4])), geog.get((h[5], h[6]))
    if not src or not dst:
        continue
    t = [to_si(v, h[11], h[12]) for v in h[8:11]]
    r = [to_si(v, h[16], h[17]) * 3600 if v is not None else 0.0 for v in h[13:16]]  # degrees to arc-seconds
    if h[2] in (9607, 9636):
        r = [-v for v in r]  # coordinate frame to position vector
    ds = (h[18] or 0) * (units[(h[19], h[20])][1] if h[19] else 1) / 1e-6
    piv = [to_si(v, h[24], h[25]) for v in h[21:24]] if h[2] in (9636, 1063) else []
    ops.append((src, dst, "m" if piv else "h", t + r + [ds] + piv, h[7] if h[7] is not None else 999, box_of("helmert_transformation", h[0], h[1])))
twins = {}  # datum with another prime meridian -> its Greenwich twin
twin_pm = {}  # that datum -> the meridian offset of its twin transformation (Austria's Ferro is 17°39'46.02" W, not 17°40')
for row in q("""select auth_name, code, method_code, source_crs_auth_name, source_crs_code, target_crs_auth_name, target_crs_code,
        accuracy, param1_value, param1_uom_auth_name, param1_uom_code, param2_value, param2_uom_auth_name, param2_uom_code
        from other_transformation where deprecated=0 and method_code in (9601, 9618, 9619) order by code"""):
    src, dst = geog.get((row[3], row[4])), geog.get((row[5], row[6]))
    if not src or not dst:
        continue
    if row[2] == 9601:  # the first (lowest code) gives the meridian; every twin lends its shifts
        twins.setdefault(src, []).append(dst)
        twin_pm.setdefault(src, to_deg(row[8], row[9], row[10]))
        continue
    off = [to_deg(row[8], row[9], row[10]) * 3600, to_deg(row[11], row[12], row[13]) * 3600]  # arc-seconds
    ops.append((src, dst, "o", off, row[7] if row[7] is not None else 999, box_of("other_transformation", row[0], row[1])))


def inverse(op):
    src, dst, kind, p, acc, box = op
    if kind == "m":  # about the shifted pivot
        p = [-v for v in p[:7]] + [p[7] + p[0], p[8] + p[1], p[9] + p[2]]
    else:
        p = [-v for v in p]
    return (dst, src, kind, p, acc, box)


steps = ops + [inverse(o) for o in ops]
direct = {}
for o in steps:
    if o[1] in wgs and o[0] not in wgs:
        direct.setdefault(o[0], []).append(o)
for d, ts in twins.items():  # Paris, Ferro, Jakarta... meridians share their twins' shifts
    for twin in ts:
        if twin in direct:
            direct[d] = direct.get(d, []) + [o for o in direct[twin] if o not in direct.get(d, [])]
shifts = {d: list(v) for d, v in direct.items()}
# Two steps through another datum (Tokyo 1892 to Korean 1985 to WGS 84), for datums with no direct shift.
for o in steps:
    if o[0] in wgs or o[0] in direct or o[1] in wgs or o[1] not in direct or o[2] == "m":
        continue
    for o2 in direct[o[1]]:
        box = meet(o[5], o2[5])
        if o2[2] == o[2] and box is not False:
            p = [a + b for a, b in zip(o[3], o2[3])]
            shifts.setdefault(o[0], []).append((o[0], None, o[2], p, o[4] + o2[4], box))
shifts = [(d,) + o[2:] for d, v in shifts.items() for o in v]

out = []
used_datums = set()


def geog_line(auth, code, name):
    d = datums[geog[(auth, code)]]
    ea, ec = d["ell"]
    a, ua, uc, invf, b = q("select semi_major_axis, uom_auth_name, uom_code, inv_flattening, semi_minor_axis from ellipsoid where auth_name=? and code=?", ea, ec)[0]
    a *= units[(ua, uc)][1]
    if not invf:
        b = (b or 0) * units[(ua, uc)][1]
        invf = a / (a - b) if b and a != b else 0
    pm_lon, pa, pc = q("select longitude, uom_auth_name, uom_code from prime_meridian where auth_name=? and code=?", *d["pm"])[0]
    cs = q("select coordinate_system_auth_name, coordinate_system_code from geodetic_crs where auth_name=? and code=?", auth, code)[0]
    ax = q("select uom_auth_name, uom_code from axis where coordinate_system_auth_name=? and coordinate_system_code=? order by coordinate_system_order", *cs)
    unit_deg = to_deg(1, *ax[0]) if ax and ax[0][0] else 1
    used_datums.add(geog[(auth, code)])
    dk = "%s:%s" % geog[(auth, code)]
    pm = twin_pm.get(geog[(auth, code)], to_deg(pm_lon, pa, pc))
    return "G\t%d\t%s\t%.4f\t%.9f\t%.10g\t%.10g\t%s\t%s" % (code, auth, a, invf, pm, unit_deg, dk, name)


geog_rows = q("""select auth_name, code, name from geodetic_crs where auth_name in ('EPSG','ESRI') and type in ('geographic 2D','geographic 3D')""")
seen_geog = set()
for auth, code, name in geog_rows:
    if code in seen_geog:
        continue
    seen_geog.add(code)
    out.append(geog_line(auth, code, name))

KEYS = {8801: "lat0", 8802: "lon0", 8805: "k0", 8806: "fe", 8807: "fn", 8821: "latf", 8822: "lonf", 8823: "lat1", 8824: "lat2",
        8826: "ef", 8827: "nf", 8811: "latc", 8812: "lonc", 8813: "alpha", 8814: "gamma", 8815: "kc", 8816: "ec", 8817: "nc",
        8818: "latp", 8819: "kp", 8832: "lats", 8833: "lono", 1036: "colat", 1038: "es", 1039: "h0", 8830: "lat0b", 8831: "lon0b"}
missing = set()
for (auth, code, name, csa, csc, ga, gc, ca, cc, wkt, dep) in q("""select auth_name, code, name, coordinate_system_auth_name, coordinate_system_code,
        geodetic_crs_auth_name, geodetic_crs_code, conversion_auth_name, conversion_code, text_definition, deprecated
        from projected_crs where auth_name in ('EPSG','ESRI')"""):
    ext = extent_of("projected_crs", auth, code)
    if cc is None:
        if wkt and "PROJCS" in wkt:
            out.append("W\t%d\t%d\t%s" % (code, ext, wkt.replace("\t", " ").replace("\n", " ")))
        continue
    if (ga, gc) not in geog:
        continue
    if gc not in seen_geog:
        seen_geog.add(gc)
        out.append(geog_line(ga, gc, q("select name from geodetic_crs where auth_name=? and code=?", ga, gc)[0][0]))
    cv = q("select * from conversion_table where auth_name=? and code=?", ca, cc)[0]
    method = cv[5]
    params = []
    for i in range(7):
        pa, pc, pv, ua, uc = cv[6 + 5 * i: 11 + 5 * i]
        if pc is None:
            continue
        if pc not in KEYS:
            missing.add((method, pc))
            continue
        v = to_si(pv, ua, uc) if ua else pv
        if pc in (8805, 8815, 8819) and uc == 9202 and pv > 0.1:
            v = pv  # EPSG lists a few scale factors such as 1.000007 under "parts per million"; they are plain ratios
        params.append("%s=%.12g" % (KEYS[pc], v))
    ax = q("select uom_auth_name, uom_code, orientation from axis where coordinate_system_auth_name=? and coordinate_system_code=? order by coordinate_system_order", csa, csc)
    unit = units[(ax[0][0], ax[0][1])][1] if ax else 1
    # Axis directions other than east, north (files hold the values in that order, as PROJ reads them).
    o = "".join(a[2][0] for a in ax[:2] if a[2] in ("east", "west", "north", "south"))
    if len(o) == 2 and o not in ("en", "ne"):
        params.append("ax=" + o)
    out.append("P\t%d\t%s\t%d\t%d\t%.15g\t%d\t%s\t%s" % (code, auth, gc, method, unit, ext, ",".join(params), name))

for dk in sorted(used_datums | {s[0] for s in shifts}, key=str):
    d = datums[dk]
    out.append("D\t%s:%s\t%d\t%s" % (dk[0], dk[1], 1 if dk in wgs else 0, "|".join(sorted(d["names"]))))
for src, kind, p, acc, box in shifts:
    method = {"h": 9606, "m": 1063, "o": 9619}[kind]
    seven = (p + [0] * 7)[:7]  # offsets: latitude and longitude in arc-seconds, then zeros
    out.append("H\t%s:%s\t%d\t%s\t%.10g\t%d%s" % (src[0], src[1], method, "\t".join("%.10g" % v for v in seven),
               acc, box_id(box) if box else 0, "".join("\t%.10g" % v for v in p[7:10])))
for i, (name, w, s, e, n) in extents.items():
    out.append("E\t%d\t%.4f\t%.4f\t%.4f\t%.4f\t%s" % (i, w, s, e, n, name))

if missing:
    print("parameters left out:", sorted(missing), file=sys.stderr)
with gzip.open("epsg.txt.gz", "wt", encoding="utf-8", compresslevel=9) as f:
    f.write("\n".join(sorted(out, key=lambda l: "EGDHPW".index(l[0]))) + "\n")
print(len(out), "lines", file=sys.stderr)
