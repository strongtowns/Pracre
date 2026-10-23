package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CRS converts source coordinates to WGS84 lon/lat. The inverse projection gives lon/lat on the
// source datum; a Helmert shift then moves datums such as NAD27, OSGB36 or ED50 onto WGS 84 (to a
// few metres; grid shifts are left out). NAD83, ETRS89 and other modern datums agree with WGS 84 to
// a metre or two, far below parcel size, and get no shift.
type CRS struct {
	ID    string    `json:"id"`   // "EPSG:26910", "ESRI:102719", "WKT"
	Name  string    `json:"name"` // human label
	Kind  string    `json:"kind"` // geo, merc, tm, lcc, aea, laea, stere, pstere, omerc, cass, poly, eqc, krovak, cea, colurban
	Var   int       `json:"var,omitempty"`
	A     float64   `json:"a,omitempty"`
	InvF  float64   `json:"invf,omitempty"`
	Lat0  float64   `json:"lat0,omitempty"` // natural origin, false origin or projection centre
	Lon0  float64   `json:"lon0,omitempty"`
	Lat1  float64   `json:"lat1,omitempty"` // standard parallels (Krovak: pseudo standard parallel)
	Lat2  float64   `json:"lat2,omitempty"`
	K0    float64   `json:"k0,omitempty"`
	FE    float64   `json:"fe,omitempty"`    // metres
	FN    float64   `json:"fn,omitempty"`    // metres
	Alpha float64   `json:"alpha,omitempty"` // Hotine azimuth; Krovak co-latitude of the cone axis
	Gamma float64   `json:"gamma,omitempty"` // Hotine angle from the rectified to the skew grid
	H0    float64   `json:"h0,omitempty"`    // Colombia Urban plane height
	Unit  float64   `json:"unit,omitempty"`  // metres per unit (projected) or degrees per unit (geographic)
	PM    float64   `json:"pm,omitempty"`    // prime meridian, degrees east of Greenwich
	Swap  bool      `json:"swap,omitempty"`  // geographic data stored lat,lon
	Datum string    `json:"datum,omitempty"` // table datum key, for the shift to WGS 84
	ToWGS []float64 `json:"towgs,omitempty"` // Helmert: method, tx ty tz (m), rx ry rz (arc-seconds), ds (ppm), pivot
	WKT   string    `json:"wkt,omitempty"`
	ext   int       // area of use in the table
	proj  func(x, y float64) (float64, float64)
	shift func(lon, lat float64) (float64, float64)
	ready bool
}

// Method variants.
const (
	varSouth    = 1 << iota // Transverse Mercator south-orientated: westing, southing
	varWest                 // Lambert conic west-orientated
	varCentre               // Hotine variant B: false origin at the projection centre
	varNorth                // Krovak east-north
	varSphere               // spherical formulas on the ellipsoid's semi-major axis
	varAuthalic             // spherical formulas on the sphere of the ellipsoid's area
	varSwapXY               // axes stored in another order or direction than east, north
	varFlipX
	varFlipY
)

// axisVar turns a coordinate system's axis directions ("sw": southing then westing) into variant flags.
func axisVar(ax string) int {
	switch ax {
	case "sw":
		return varSwapXY | varFlipX | varFlipY
	case "ws":
		return varFlipX | varFlipY
	case "nw":
		return varFlipX
	case "es":
		return varFlipY
	}
	return 0
}

const (
	grs80A, grs80InvF = 6378137.0, 298.257222101
	wgs84A, wgs84InvF = 6378137.0, 298.257223563
)

var crsWGS84 = CRS{ID: "EPSG:4326", Name: "WGS 84 (longitude/latitude)", Kind: "geo", A: wgs84A, InvF: wgs84InvF, Unit: 1}

// ---- the EPSG / ESRI table (epsg/gen.py builds it from PROJ's database)

//go:embed epsg/epsg.txt.gz
var epsgGz []byte

type epsgExtent struct {
	name       string
	w, s, e, n float64
}

func (x epsgExtent) contains(lon, lat, margin float64) bool {
	if x.w > x.e { // crosses the antimeridian
		return lat >= x.s-margin && lat <= x.n+margin && (lon >= x.w-margin || lon <= x.e+margin)
	}
	return lat >= x.s-margin && lat <= x.n+margin && lon >= x.w-margin && lon <= x.e+margin
}

type datumShift struct {
	p   []float64
	acc float64
	ext int
}

type epsgTable struct {
	proj, geog map[int]string // P and W lines, G lines
	names      map[string]string
	wgs        map[string]bool
	shifts     map[string][]datumShift
	extents    map[int]epsgExtent
}

var (
	epsgOnce sync.Once
	epsgTab  epsgTable
)

func epsg() *epsgTable {
	epsgOnce.Do(func() {
		t := epsgTable{proj: map[int]string{}, geog: map[int]string{}, names: map[string]string{}, wgs: map[string]bool{},
			shifts: map[string][]datumShift{}, extents: map[int]epsgExtent{}}
		zr, err := gzip.NewReader(bytes.NewReader(epsgGz))
		if err != nil {
			panic(err)
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			f := strings.Split(l, "\t")
			n, _ := strconv.Atoi(f[1])
			switch f[0] {
			case "P", "W":
				t.proj[n] = l
			case "G":
				t.geog[n] = l
			case "D":
				t.wgs[f[1]] = f[2] == "1"
				for _, nm := range strings.Split(f[3], "|") {
					if _, dup := t.names[nm]; !dup {
						t.names[nm] = f[1]
					}
				}
			case "H":
				var p []float64
				for _, s := range append([]string{f[2]}, f[3:10]...) {
					v, _ := strconv.ParseFloat(s, 64)
					p = append(p, v)
				}
				for _, s := range f[12:] {
					v, _ := strconv.ParseFloat(s, 64)
					p = append(p, v)
				}
				acc, _ := strconv.ParseFloat(f[10], 64)
				ext, _ := strconv.Atoi(f[11])
				t.shifts[f[1]] = append(t.shifts[f[1]], datumShift{p, acc, ext})
			case "E":
				num := func(i int) float64 { v, _ := strconv.ParseFloat(f[i], 64); return v }
				t.extents[n] = epsgExtent{f[6], num(2), num(3), num(4), num(5)}
			}
		}
		epsgTab = t
	})
	return &epsgTab
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// normDatum makes datum names from .prj files, WKT and EPSG comparable: "D_North_American_1927" -> "northamerican1927".
func normDatum(s string) string {
	return nonAlnum.ReplaceAllString(strings.TrimPrefix(strings.ToLower(s), "d_"), "")
}

func parseKV(s string) map[string]float64 {
	m := map[string]float64{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k], _ = strconv.ParseFloat(v, 64)
		}
	}
	return m
}

var unsupportedMethods = map[int]string{9811: "New Zealand Map Grid", 9813: "Laborde Oblique Mercator", 9816: "Tunisia Mining Grid",
	9817: "Lambert Conic Near-Conformal", 9824: "Transverse Mercator Zoned Grid", 9828: "Bonne (South Orientated)", 9830: "Polar Stereographic (variant C)",
	9831: "Guam", 9832: "Modified Azimuthal Equidistant", 9833: "Hyperbolic Cassini-Soldner", 9842: "Equidistant Cylindrical (variant)",
	1042: "Krovak Modified", 1043: "Krovak Modified (North Orientated)", 1078: "Equal Earth", 1125: "Azimuthal Equidistant", 1130: "Local Orthographic"}

// crsFromCode looks a code up in the table: any EPSG or ESRI projected or geographic system.
func crsFromCode(code int) (CRS, error) { return crsLookup(code, "EPSG") }

// crsLookup prefers the given authority where an EPSG and an ESRI code coincide (only 4812 does).
func crsLookup(code int, prefer string) (CRS, error) {
	t := epsg()
	g, gok := t.geog[code]
	l, ok := t.proj[code]
	if gok && ok {
		pauth := "ESRI"
		if strings.HasPrefix(l, "P") {
			pauth = strings.Split(l, "\t")[2]
		}
		if pauth == strings.ToUpper(prefer) {
			gok = false
		}
	}
	if gok {
		return geogLine(g), nil
	}
	if !ok {
		return CRS{}, fmt.Errorf("EPSG:%d isn't a known coordinate system; paste its WKT from epsg.io or import the layer's .prj file", code)
	}
	f := strings.Split(l, "\t")
	if f[0] == "W" {
		c, err := crsFromWKT(wktAuthRe.ReplaceAllString(f[3], "]")) // without its own code, which would lead back here
		c.ID = "ESRI:" + f[1]
		c.ext, _ = strconv.Atoi(f[2])
		return c, err
	}
	gc, _ := strconv.Atoi(f[3])
	method, _ := strconv.Atoi(f[4])
	if t.geog[gc] == "" {
		return CRS{}, fmt.Errorf("%s:%d has no datum in the table", f[2], code)
	}
	c := geogLine(t.geog[gc])
	c.ID, c.Name, c.WKT = f[2]+":"+f[1], f[8], ""
	c.Unit, _ = strconv.ParseFloat(f[5], 64)
	c.ext, _ = strconv.Atoi(f[6])
	p := parseKV(f[7])
	ax := ""
	if i := strings.Index(f[7], "ax="); i >= 0 {
		ax = f[7][i+3 : i+5]
	}
	c.Lat0, c.Lon0, c.K0, c.FE, c.FN = p["lat0"], p["lon0"], p["k0"], p["fe"], p["fn"]
	switch method {
	case 9807, 1111:
		c.Kind = "tm"
	case 9808:
		c.Kind, c.Var = "tm", varSouth
	case 9801, 9826:
		c.Kind, c.Lat1, c.Lat2 = "lcc", p["lat0"], p["lat0"]
		if method == 9826 {
			c.Var = varWest
		}
	case 1102:
		c.Kind, c.Lat1, c.Lat2, c.Lat0, c.Lon0, c.FE, c.FN = "lcc", p["lat0"], p["lat0"], p["latf"], p["lonf"], p["ef"], p["nf"]
	case 9802, 9803, 1051: // PROJ reads 9803 (Belgium) without its 29.2985" rotation, and so does Pracre
		c.Kind, c.Lat0, c.Lon0, c.Lat1, c.Lat2, c.FE, c.FN = "lcc", p["latf"], p["lonf"], p["lat1"], p["lat2"], p["ef"], p["nf"]
		if method == 1051 {
			c.A *= p["es"] // Michigan: the ellipsoid is scaled to the average terrain height
		}
	case 9822:
		c.Kind, c.Lat0, c.Lon0, c.Lat1, c.Lat2, c.FE, c.FN = "aea", p["latf"], p["lonf"], p["lat1"], p["lat2"], p["ef"], p["nf"]
	case 9820:
		c.Kind = "laea"
	case 1027:
		c.Kind, c.Var = "laea", varAuthalic
	case 9804, 9841:
		c.Kind = "merc"
		if method == 9841 {
			c.Var = varSphere
		}
	case 9805:
		c.Kind, c.Lat1 = "merc", p["lat1"]
	case 1024, 1026:
		c.Kind, c.Var = "merc", varSphere
	case 9809:
		c.Kind = "stere"
	case 9810:
		c.Kind = "pstere"
	case 9829:
		c.Kind, c.Lat1, c.Lon0, c.Lat0 = "pstere", p["lats"], p["lono"], math.Copysign(90, p["lats"])
	case 9812, 9815:
		c.Kind, c.Lat0, c.Lon0, c.Alpha, c.Gamma, c.K0 = "omerc", p["latc"], p["lonc"], p["alpha"], p["gamma"], p["kc"]
		if method == 9815 {
			c.Var, c.FE, c.FN = varCentre, p["ec"], p["nc"]
		}
	case 9806:
		c.Kind = "cass"
	case 9818:
		c.Kind = "poly"
	case 1028, 1029, 9823: // PROJ applies the ellipsoidal formulas to the spherical variants too
		c.Kind, c.Lat1 = "eqc", p["lat1"]
	case 9819, 1041:
		c.Kind, c.Lat0, c.Lon0, c.Alpha, c.Lat1, c.K0, c.Var = "krovak", p["latc"], p["lono"], p["colat"], p["latp"], p["kp"], varNorth
	case 9834, 9835:
		c.Kind, c.Lat1 = "cea", p["lat1"]
		if method == 9834 {
			c.Var = varAuthalic
		}
	case 1052:
		c.Kind, c.H0 = "colurban", p["h0"]
	default:
		name := unsupportedMethods[method]
		if name == "" {
			name = "EPSG method " + strconv.Itoa(method)
		}
		return CRS{}, fmt.Errorf("%s (%s) uses the %s projection, which Pracre can't read yet; reproject the layer to WGS 84 or UTM", c.ID, c.Name, name)
	}
	if method != 9808 { // south-orientated Transverse Mercator builds its directions into the formulas
		c.Var |= axisVar(ax)
	}
	return c, nil
}

func geogLine(l string) CRS {
	f := strings.Split(l, "\t")
	num := func(i int) float64 { v, _ := strconv.ParseFloat(f[i], 64); return v }
	return CRS{ID: f[2] + ":" + f[1], Name: f[8], Kind: "geo", A: num(3), InvF: num(4), PM: num(5), Unit: num(6), Datum: f[7]}
}

// commonCRS is the picker list offered when nothing could be detected.
func commonCRS() []CRS {
	var out []CRS
	for _, code := range []int{4326, 3857, 3005, 5070, 3347, 2193, 27700} {
		if c, err := crsFromCode(code); err == nil {
			out = append(out, c)
		}
	}
	for z := 1; z <= 23; z++ {
		if c, err := crsFromCode(26900 + z); err == nil {
			out = append(out, c)
		}
	}
	return out
}

var epsgRe = regexp.MustCompile(`(?i)(EPSG|ESRI)\W*(?:\d+(?:\.\d+)+\W+)?(\d{4,6})\b`)

// crsFromString accepts "EPSG:26910", "ESRI:102719", "urn:ogc:def:crs:EPSG::3005", a bare code, "CRS84", or WKT1/WKT2.
func crsFromString(s string) (CRS, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return CRS{}, fmt.Errorf("empty projection")
	}
	if base, ok := strings.CutSuffix(s, "#swap"); ok {
		// Degrees stored latitude first, picked from the import window's suggestions.
		c, err := crsFromString(base)
		if err == nil && c.Kind == "geo" {
			c.Swap, c.Name = true, "Latitude/longitude (columns swapped)"
		}
		return c, err
	}
	up := strings.ToUpper(s)
	if strings.Contains(up, "CRS84") {
		return crsWGS84, nil
	}
	for _, p := range []string{"PROJCS", "GEOGCS", "PROJCRS", "PROJECTEDCRS", "GEOGCRS", "GEODCRS", "BOUNDCRS"} {
		if strings.HasPrefix(up, p+"[") {
			return crsFromWKT(s)
		}
	}
	if m := epsgRe.FindStringSubmatch(s); m != nil {
		code, _ := strconv.Atoi(m[2])
		return crsLookup(code, m[1])
	}
	if n, err := strconv.Atoi(s); err == nil {
		return crsFromCode(n)
	}
	return CRS{}, fmt.Errorf("unrecognised projection text")
}

// ---- WKT

var (
	wktParamRe = regexp.MustCompile(`(?i)PARAMETER\["([^"]+)",\s*([-0-9.eE+]+)(?:\s*,\s*(ANGLEUNIT|LENGTHUNIT|SCALEUNIT)\["[^"]*",\s*([-0-9.eE+]+))?`)
	wktSpherRe = regexp.MustCompile(`(?i)(?:SPHEROID|ELLIPSOID)\["[^"]*",\s*([-0-9.eE+]+),\s*([-0-9.eE+]+)`)
	wktProjRe  = regexp.MustCompile(`(?i)(?:PROJECTION|METHOD)\["([^"]+)"`)
	wktUnitRe  = regexp.MustCompile(`(?i)UNIT\["([^"]+)",\s*([-0-9.eE+]+)`)
	wktNameRe  = regexp.MustCompile(`^\s*\w+\["([^"]+)"`)
	wktAuthRe  = regexp.MustCompile(`(?i)(?:AUTHORITY|ID)\["(?:EPSG|ESRI)",\s*"?(\d+)"?\]\s*\]\s*$`)
	wktDatumRe = regexp.MustCompile(`(?i)DATUM\["([^"]+)"`)
	wktPMRe    = regexp.MustCompile(`(?i)PRIMEM\["[^"]*",\s*([-0-9.eE+]+)`)
	wktToWGSRe = regexp.MustCompile(`(?i)TOWGS84\[([-0-9.eE+,\s]+)\]`)
	wktGeogRe  = regexp.MustCompile(`(?i)(?:GEOGCS|BASEGEOGCRS|BASEGEODCRS)\[`)
)

func crsFromWKT(w string) (CRS, error) {
	name := ""
	if m := wktNameRe.FindStringSubmatch(w); m != nil {
		name = strings.ReplaceAll(m[1], "_", " ")
	}
	if m := wktAuthRe.FindStringSubmatch(w); m != nil {
		code, _ := strconv.Atoi(m[1])
		if c, err := crsFromCode(code); err == nil {
			return c, nil
		}
	}
	c := CRS{ID: "WKT", Name: name, WKT: w, A: grs80A, InvF: grs80InvF, Unit: 1}
	if m := wktSpherRe.FindStringSubmatch(w); m != nil {
		c.A, _ = strconv.ParseFloat(m[1], 64)
		c.InvF, _ = strconv.ParseFloat(m[2], 64)
	}
	// The datum: an explicit TOWGS84, or its name looked up in the table.
	if m := wktToWGSRe.FindStringSubmatch(w); m != nil {
		c.ToWGS = []float64{9606}
		for _, s := range strings.Split(m[1], ",") {
			v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
			c.ToWGS = append(c.ToWGS, v)
		}
		for len(c.ToWGS) < 8 {
			c.ToWGS = append(c.ToWGS, 0)
		}
	} else if m := wktDatumRe.FindStringSubmatch(w); m != nil {
		c.Datum = epsg().names[normDatum(m[1])]
	}
	// Geographic angle unit (grads in some French data) and prime meridian.
	geogUnit := 1.0
	if g := wktGeogRe.FindStringIndex(w); g != nil {
		if u := wktUnitRe.FindStringSubmatch(w[g[1]:]); u != nil {
			if f, _ := strconv.ParseFloat(u[2], 64); f > 0 && f < 0.1 {
				geogUnit = f * 180 / math.Pi
			}
		}
	}
	if m := wktPMRe.FindStringSubmatch(w); m != nil {
		c.PM, _ = strconv.ParseFloat(m[1], 64)
		if geogUnit != 1 && math.Abs(c.PM-2.5969213) < 1e-6 {
			c.PM = 2.33722917 // Paris written in grads
		}
	}
	up := strings.ToUpper(strings.TrimSpace(w))
	if strings.HasPrefix(up, "GEOGCS") || strings.HasPrefix(up, "GEOGCRS") || strings.HasPrefix(up, "GEODCRS") {
		c.Kind, c.Unit = "geo", geogUnit
		return c, nil
	}
	if u := wktUnitRe.FindAllStringSubmatch(w, -1); len(u) > 0 {
		c.Unit, _ = strconv.ParseFloat(u[len(u)-1][2], 64)
	}
	// Parameters, in degrees and metres. WKT1 writes lengths in the projected unit; WKT2 gives each its unit.
	p := map[string]float64{}
	for _, m := range wktParamRe.FindAllStringSubmatch(w, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		key := strings.ReplaceAll(strings.ToLower(m[1]), " ", "_")
		f, _ := strconv.ParseFloat(m[4], 64)
		switch strings.ToUpper(m[3]) {
		case "ANGLEUNIT":
			v *= f * 180 / math.Pi
		case "LENGTHUNIT":
			v *= f
		case "":
			if strings.Contains(key, "easting") || strings.Contains(key, "northing") || strings.Contains(key, "height") {
				v *= c.Unit
			}
		}
		p[key] = v
	}
	has := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := p[k]; ok {
				return true
			}
		}
		return false
	}
	get := func(def float64, keys ...string) float64 {
		for _, k := range keys {
			if v, ok := p[k]; ok {
				return v
			}
		}
		return def
	}
	c.FE = get(0, "false_easting", "easting_at_false_origin", "easting_at_projection_centre")
	c.FN = get(0, "false_northing", "northing_at_false_origin", "northing_at_projection_centre")
	c.Lon0 = get(0, "central_meridian", "longitude_of_center", "longitude_of_origin", "longitude_of_natural_origin", "longitude_of_false_origin", "longitude_of_projection_centre")
	c.Lat0 = get(0, "latitude_of_origin", "latitude_of_center", "latitude_of_natural_origin", "latitude_of_false_origin", "latitude_of_projection_centre", "central_parallel")
	c.Lat1 = get(c.Lat0, "standard_parallel_1", "latitude_of_1st_standard_parallel", "latitude_of_standard_parallel", "pseudo_standard_parallel_1", "latitude_of_pseudo_standard_parallel")
	c.Lat2 = get(c.Lat1, "standard_parallel_2", "latitude_of_2nd_standard_parallel")
	c.K0 = get(0, "scale_factor", "scale_factor_at_natural_origin", "scale_factor_on_initial_line", "scale_factor_at_projection_centre", "scale_factor_on_pseudo_standard_parallel")
	c.Alpha = get(0, "azimuth", "azimuth_of_initial_line", "azimuth_at_projection_centre", "co-latitude_of_cone_axis")
	c.Gamma = get(c.Alpha, "rectified_grid_angle", "xy_plane_rotation", "angle_from_rectified_to_skew_grid")
	c.H0 = get(0, "projection_plane_origin_height")
	if es := get(1, "ellipsoid_scaling_factor"); es != 1 {
		c.A *= es
	}
	proj := ""
	if m := wktProjRe.FindStringSubmatch(w); m != nil {
		proj = strings.ReplaceAll(strings.ToLower(m[1]), " ", "_")
	}
	in := func(words ...string) bool {
		for _, s := range words {
			if strings.Contains(proj, s) {
				return true
			}
		}
		return false
	}
	switch {
	case in("laborde", "gall", "two_point"):
		return CRS{}, fmt.Errorf("projection %q is not supported yet; reproject the layer to WGS 84 or UTM", proj)
	case in("transverse_mercator", "gauss_kruger", "gauss-kruger"):
		c.Kind = "tm"
		if in("south") {
			c.Var = varSouth
		}
	case in("lambert_conformal_conic", "lambert_conic_conformal"):
		c.Kind = "lcc"
		if !has("standard_parallel_1", "latitude_of_1st_standard_parallel") {
			c.Lat1, c.Lat2 = c.Lat0, c.Lat0
		}
		if in("west") {
			c.Var = varWest
		}
	case in("albers"):
		c.Kind = "aea"
	case in("lambert_azimuthal_equal_area"):
		c.Kind = "laea"
	case in("mercator_auxiliary_sphere", "pseudo_mercator", "popular_visualisation"):
		c.Kind, c.Var = "merc", varSphere
	case in("oblique_mercator", "rectified_skew", "hotine"):
		c.Kind = "omerc"
		if in("center", "centre", "variant_b") {
			c.Var = varCentre
		}
	case in("mercator"):
		c.Kind = "merc"
	case in("oblique_stereographic", "double_stereographic"):
		c.Kind = "stere"
	case in("polar_stereographic", "stereographic_north_pole", "stereographic_south_pole"):
		c.Kind = "pstere"
		if in("north_pole") {
			c.Lat0 = 90
		} else if in("south_pole") {
			c.Lat0 = -90
		} else if math.Abs(c.Lat0) != 90 {
			c.Lat0 = math.Copysign(90, c.Lat1)
		}
	case in("stereographic"):
		c.Kind = "stere"
		if math.Abs(c.Lat0) == 90 {
			c.Kind = "pstere"
		}
	case in("cassini"):
		c.Kind = "cass"
	case in("polyconic"):
		c.Kind = "poly"
	case in("equidistant_cylindrical", "equirectangular", "plate_carree"):
		c.Kind = "eqc"
	case in("krovak"):
		c.Kind, c.Var = "krovak", varNorth
		if !in("north") && get(1, "x_scale") != -1 {
			c.Var |= axisVar("sw") // plain Krovak: southing, westing
		}
	case in("cylindrical_equal_area"):
		c.Kind = "cea"
	case in("colombia_urban"):
		c.Kind = "colurban"
	default:
		return CRS{}, fmt.Errorf("projection %q is not supported yet; reproject the layer to WGS 84 or UTM", proj)
	}
	return c, nil
}

// ---- conversion

// ToWGS84 maps a source coordinate to lon/lat.
func (c *CRS) ToWGS84(x, y float64) (float64, float64) {
	if !c.ready {
		c.init()
	}
	lon, lat := c.proj(x, y)
	if c.shift == nil {
		if math.IsNaN(lon) || math.IsNaN(lat) {
			return lon, lat
		}
		c.pickShift(lon, lat)
	}
	return c.shift(lon, lat)
}

func (c *CRS) Fn() func(Pt) Pt {
	return func(p Pt) Pt {
		lon, lat := c.ToWGS84(p[0], p[1])
		return Pt{lon, lat}
	}
}

// pickShift chooses the datum shift for data around lon/lat: the most accurate one whose area of use
// holds it (NAD27 has different shifts for the east and west of the US, Canada, Mexico...).
func (c *CRS) pickShift(lon, lat float64) {
	p := c.ToWGS
	if p == nil && c.Datum != "" {
		t := epsg()
		if !t.wgs[c.Datum] {
			best, bestScore := -1, math.Inf(1)
			for i, s := range t.shifts[c.Datum] {
				x := t.extents[s.ext]
				score := s.acc*1e6 + math.Abs((x.e-x.w)*(x.n-x.s)) // most accurate, then most local
				if s.ext != 0 && !x.contains(lon, lat, 0.01) {
					// Outside its area: only when nothing covers the data, and then the nearest.
					dx := math.Max(0, math.Max(x.w-lon, lon-x.e))
					dy := math.Max(0, math.Max(x.s-lat, lat-x.n))
					score = 1e12 + math.Hypot(dx, dy)*1e6 + s.acc
				}
				if score < bestScore {
					best, bestScore = i, score
				}
			}
			if best >= 0 {
				p = t.shifts[c.Datum][best].p
			}
		}
	}
	c.shift = helmert(p, c.A, c.InvF)
}

// helmert returns the datum shift to WGS 84 (methods 9603 translations, 9606 position vector,
// 9607 coordinate frame, 9636 / 1063 Molodensky-Badekas, 9619 latitude/longitude offsets), or the identity.
func helmert(p []float64, a, invf float64) func(lon, lat float64) (float64, float64) {
	zero := true
	for _, v := range p[min(1, len(p)):] {
		if v != 0 {
			zero = false
		}
	}
	if len(p) < 8 || zero {
		return func(lon, lat float64) (float64, float64) { return lon, lat }
	}
	m := int(p[0])
	if m == 9619 { // latitude and longitude offsets in arc-seconds
		return func(lon, lat float64) (float64, float64) { return lon + p[2]/3600, lat + p[1]/3600 }
	}
	sec := math.Pi / 180 / 3600
	tx, ty, tz, rx, ry, rz, ds := p[1], p[2], p[3], p[4]*sec, p[5]*sec, p[6]*sec, p[7]*1e-6
	if m == 9607 || m == 9636 { // coordinate frame: the rotations turn the other way
		rx, ry, rz = -rx, -ry, -rz
	}
	var px, py, pz float64
	if len(p) >= 11 {
		px, py, pz = p[8], p[9], p[10]
	}
	f := 0.0
	if invf != 0 {
		f = 1 / invf
	}
	e2 := f * (2 - f)
	we2 := 1 / wgs84InvF * (2 - 1/wgs84InvF)
	return func(lon, lat float64) (float64, float64) {
		phi, lam := rad(lat), rad(lon)
		s := math.Sin(phi)
		n := a / math.Sqrt(1-e2*s*s)
		X, Y, Z := n*math.Cos(phi)*math.Cos(lam)-px, n*math.Cos(phi)*math.Sin(lam)-py, n*(1-e2)*s-pz
		X, Y, Z = (1+ds)*(X-rz*Y+ry*Z)+tx+px, (1+ds)*(rz*X+Y-rx*Z)+ty+py, (1+ds)*(-ry*X+rx*Y+Z)+tz+pz
		q := math.Hypot(X, Y)
		phi = math.Atan2(Z, q*(1-we2))
		for i := 0; i < 6; i++ {
			s := math.Sin(phi)
			n := wgs84A / math.Sqrt(1-we2*s*s)
			h := q/math.Cos(phi) - n
			phi = math.Atan2(Z, q*(1-we2*n/(n+h)))
		}
		return math.Atan2(Y, X) * 180 / math.Pi, phi * 180 / math.Pi
	}
}

// init builds the inverse projection. Formulas follow IOGP Guidance Note 7-2; Transverse Mercator uses
// Krüger's 6th-order series, good to millimetres thousands of kilometres from the central meridian.
func (c *CRS) init() {
	c.ready = true
	unit := c.Unit
	if unit == 0 {
		unit = 1
	}
	deg := 180 / math.Pi
	a := c.A
	if a == 0 {
		a = grs80A
	}
	f := 0.0
	if c.InvF != 0 && c.Var&varSphere == 0 {
		f = 1 / c.InvF
	}
	if c.Var&varAuthalic != 0 && f > 0 {
		e := math.Sqrt(f * (2 - f))
		a *= math.Sqrt((1 - e*e) * (1/(1-e*e) - 1/(2*e)*math.Log((1-e)/(1+e))) / 2)
		f = 0
	}
	e2 := f * (2 - f)
	e := math.Sqrt(e2)
	lat0, lon0, lat1, lat2 := rad(c.Lat0), rad(c.Lon0), rad(c.Lat1), rad(c.Lat2)
	k0 := c.K0
	if k0 == 0 {
		k0 = 1
	}
	fe, fn, pm := c.FE, c.FN, c.PM
	out := func(lam, phi float64) (float64, float64) {
		lon := lam*deg + pm
		if lon > 180 || lon < -180 { // systems centred near the antimeridian
			lon = math.Remainder(lon, 360)
		}
		return lon, phi * deg
	}
	// in converts input units to metres and undoes south- and west-orientated axes.
	in := func(x, y float64) (float64, float64) {
		x, y = x*unit, y*unit
		if c.Var&varSwapXY != 0 {
			x, y = y, x
		}
		if c.Var&varFlipX != 0 {
			x = -x
		}
		if c.Var&varFlipY != 0 {
			y = -y
		}
		if c.Var&(varSouth|varWest) != 0 {
			x = 2*fe - x
		}
		if c.Var&varSouth != 0 {
			y = 2*fn - y
		}
		return x - fe, y - fn
	}
	// Ellipsoid helpers.
	sin2 := func(p float64) float64 { s := math.Sin(p); return s * s }
	tOf := func(p float64) float64 { // isometric t
		s := math.Sin(p)
		return math.Tan(math.Pi/4-p/2) / math.Pow((1-e*s)/(1+e*s), e/2)
	}
	phiFromT := func(t float64) float64 {
		phi := math.Pi/2 - 2*math.Atan(t)
		for i := 0; i < 30; i++ {
			s := math.Sin(phi)
			n := math.Pi/2 - 2*math.Atan(t*math.Pow((1-e*s)/(1+e*s), e/2))
			if math.Abs(n-phi) < 1e-14 {
				return n
			}
			phi = n
		}
		return phi
	}
	mOf := func(p float64) float64 { return math.Cos(p) / math.Sqrt(1-e2*sin2(p)) }
	qOf := func(p float64) float64 {
		s := math.Sin(p)
		if e == 0 {
			return 2 * s
		}
		return (1 - e2) * (s/(1-e2*s*s) - 1/(2*e)*math.Log((1-e*s)/(1+e*s)))
	}
	qp := qOf(math.Pi / 2)
	phiFromQ := func(q float64) float64 { // authalic inverse
		if math.Abs(q) >= qp-1e-14 {
			return math.Copysign(math.Pi/2, q)
		}
		phi := math.Asin(math.Max(-1, math.Min(1, q/2)))
		for i := 0; i < 30 && e > 0; i++ {
			s, co := math.Sin(phi), math.Cos(phi)
			d := (1 - e2*s*s) * (1 - e2*s*s) / (2 * co) * (q/(1-e2) - s/(1-e2*s*s) + 1/(2*e)*math.Log((1-e*s)/(1+e*s)))
			phi += d
			if math.Abs(d) < 1e-14 {
				break
			}
		}
		return phi
	}
	e4, e6 := e2*e2, e2*e2*e2
	m1 := 1 - e2/4 - 3*e4/64 - 5*e6/256
	arc := func(p float64) float64 { // meridian arc
		return a * (m1*p - (3*e2/8+3*e4/32+45*e6/1024)*math.Sin(2*p) + (15*e4/256+45*e6/1024)*math.Sin(4*p) - (35*e6/3072)*math.Sin(6*p))
	}
	ee := (1 - math.Sqrt(1-e2)) / (1 + math.Sqrt(1-e2))
	foot := func(m float64) float64 { // latitude whose meridian arc is m
		mu := m / (a * m1)
		return mu + (3*ee/2-27*math.Pow(ee, 3)/32)*math.Sin(2*mu) + (21*ee*ee/16-55*math.Pow(ee, 4)/32)*math.Sin(4*mu) +
			(151*math.Pow(ee, 3)/96)*math.Sin(6*mu) + (1097*math.Pow(ee, 4)/512)*math.Sin(8*mu)
	}

	switch c.Kind {
	case "geo":
		c.proj = func(x, y float64) (float64, float64) {
			if c.Swap {
				x, y = y, x
			}
			return x*unit + pm, y * unit
		}
	case "tm":
		n := f / (2 - f)
		n2, n3, n4, n5, n6 := n*n, n*n*n, math.Pow(n, 4), math.Pow(n, 5), math.Pow(n, 6)
		A := a / (1 + n) * (1 + n2/4 + n4/64 + n6/256)
		beta := [6]float64{
			n/2 - 2*n2/3 + 37*n3/96 - n4/360 - 81*n5/512 + 96199*n6/604800,
			n2/48 + n3/15 - 437*n4/1440 + 46*n5/105 - 1118711*n6/3870720,
			17*n3/480 - 37*n4/840 - 209*n5/4480 + 5569*n6/90720,
			4397*n4/161280 - 11*n5/504 - 830251*n6/7257600,
			4583*n5/161280 - 108847*n6/3991680,
			20648693 * n6 / 638668800,
		}
		xi0 := arc(lat0) / A
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			xi, eta := y/(k0*A)+xi0, x/(k0*A)
			xp, ep := xi, eta
			for j := 1; j <= 6; j++ {
				xp -= beta[j-1] * math.Sin(2*float64(j)*xi) * math.Cosh(2*float64(j)*eta)
				ep -= beta[j-1] * math.Cos(2*float64(j)*xi) * math.Sinh(2*float64(j)*eta)
			}
			chi := math.Asin(math.Sin(xp) / math.Cosh(ep))
			// conformal to geodetic latitude
			phi := chi
			for i := 0; i < 30 && e > 0; i++ {
				s := math.Sin(phi)
				n := 2*math.Atan(math.Tan(math.Pi/4+chi/2)*math.Pow((1+e*s)/(1-e*s), e/2)) - math.Pi/2
				if math.Abs(n-phi) < 1e-14 {
					phi = n
					break
				}
				phi = n
			}
			return out(lon0+math.Atan2(math.Sinh(ep), math.Cos(xp)), phi)
		}
	case "lcc":
		m1, m2 := mOf(lat1), mOf(lat2)
		t0, t1, t2 := tOf(lat0), tOf(lat1), tOf(lat2)
		n := math.Sin(lat1)
		if math.Abs(lat1-lat2) > 1e-10 {
			n = (math.Log(m1) - math.Log(m2)) / (math.Log(t1) - math.Log(t2))
		}
		F := m1 / (n * math.Pow(t1, n))
		rho0 := a * k0 * F * math.Pow(t0, n)
		sn := 1.0
		if n < 0 {
			sn = -1
		}
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			y = rho0 - y
			rho := sn * math.Hypot(x, y)
			theta := math.Atan2(sn*x, sn*y)
			return out(theta/n+lon0, phiFromT(math.Pow(rho/(a*k0*F), 1/n)))
		}
	case "aea":
		m1, m2 := mOf(lat1), mOf(lat2)
		q0, q1, q2 := qOf(lat0), qOf(lat1), qOf(lat2)
		n := math.Sin(lat1)
		if math.Abs(lat1-lat2) > 1e-10 {
			n = (m1*m1 - m2*m2) / (q2 - q1)
		}
		C := m1*m1 + n*q1
		rho0 := a * math.Sqrt(C-n*q0) / n
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			rho := math.Hypot(x, rho0-y)
			theta := math.Atan2(x, rho0-y)
			if n < 0 {
				rho, theta = -rho, math.Atan2(-x, -(rho0-y))
			}
			return out(lon0+theta/n, phiFromQ((C-rho*rho*n*n/(a*a))/n))
		}
	case "laea":
		polar := math.Abs(math.Abs(lat0)-math.Pi/2) < 1e-10
		b0 := math.Asin(qOf(lat0) / qp)
		rq := a * math.Sqrt(qp/2)
		D := a * mOf(lat0) / (rq * math.Cos(b0))
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			if polar {
				rho := math.Hypot(x, y)
				if lat0 > 0 {
					return out(lon0+math.Atan2(x, -y), phiFromQ(qp-rho*rho/(a*a)))
				}
				return out(lon0+math.Atan2(x, y), phiFromQ(rho*rho/(a*a)-qp))
			}
			rho := math.Hypot(x/D, D*y)
			if rho == 0 {
				return out(lon0, lat0)
			}
			C := 2 * math.Asin(math.Min(1, rho/(2*rq)))
			b := math.Asin(math.Cos(C)*math.Sin(b0) + D*y*math.Sin(C)*math.Cos(b0)/rho)
			lam := lon0 + math.Atan2(x*math.Sin(C), D*rho*math.Cos(b0)*math.Cos(C)-D*D*y*math.Sin(b0)*math.Sin(C))
			return out(lam, phiFromQ(qp*math.Sin(b)))
		}
	case "merc":
		k := k0
		if c.K0 == 0 {
			k = mOf(lat1) // 2SP: scale set by the standard parallel
		}
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			return out(x/(a*k)+lon0, phiFromT(math.Exp(-y/(a*k))))
		}
	case "stere":
		rho0 := a * (1 - e2) / math.Pow(1-e2*sin2(lat0), 1.5)
		nu0 := a / math.Sqrt(1-e2*sin2(lat0))
		R := math.Sqrt(rho0 * nu0)
		n := math.Sqrt(1 + e2*math.Pow(math.Cos(lat0), 4)/(1-e2))
		s0 := math.Sin(lat0)
		S1, S2 := (1+s0)/(1-s0), (1-e*s0)/(1+e*s0)
		w1 := math.Pow(S1*math.Pow(S2, e), n)
		sc := (w1 - 1) / (w1 + 1)
		cc := (n + s0) * (1 - sc) / ((n - s0) * (1 + sc))
		w2 := cc * w1
		chi0 := math.Asin((w2 - 1) / (w2 + 1))
		g := 2 * R * k0 * math.Tan(math.Pi/4-chi0/2)
		h := 4*R*k0*math.Tan(chi0) + g
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			i := math.Atan(x / (h + y))
			j := math.Atan(x/(g-y)) - i
			chi := chi0 + 2*math.Atan((y-x*math.Tan(j/2))/(2*R*k0))
			lam := (j+2*i)/n + lon0
			psi := 0.5 * math.Log((1+math.Sin(chi))/(cc*(1-math.Sin(chi)))) / n
			phi := 2*math.Atan(math.Exp(psi)) - math.Pi/2
			for k := 0; k < 30; k++ {
				s := math.Sin(phi)
				psii := math.Log(math.Tan(phi/2+math.Pi/4) * math.Pow((1-e*s)/(1+e*s), e/2))
				d := (psii - psi) * math.Cos(phi) * (1 - e2*s*s) / (1 - e2)
				phi -= d
				if math.Abs(d) < 1e-14 {
					break
				}
			}
			return out(lam, phi)
		}
	case "pstere":
		north := lat0 > 0
		k := k0
		ef := math.Sqrt(math.Pow(1+e, 1+e) * math.Pow(1-e, 1-e))
		if c.K0 == 0 && math.Abs(math.Abs(lat1)-math.Pi/2) > 1e-10 { // variant B: scale from the standard parallel
			p := math.Abs(lat1)
			k = mOf(p) * ef / (2 * tOf(p))
		}
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			t := math.Hypot(x, y) * ef / (2 * a * k)
			if north {
				return out(lon0+math.Atan2(x, -y), phiFromT(t))
			}
			return out(lon0+math.Atan2(x, y), -phiFromT(t))
		}
	case "omerc":
		alpha, gammaC := rad(c.Alpha), rad(c.Gamma)
		B := math.Sqrt(1 + e2*math.Pow(math.Cos(lat0), 4)/(1-e2))
		A := a * B * k0 * math.Sqrt(1-e2) / (1 - e2*sin2(lat0))
		t0 := tOf(lat0)
		D := B * math.Sqrt(1-e2) / (math.Cos(lat0) * math.Sqrt(1-e2*sin2(lat0)))
		if D < 1 {
			D = 1
		}
		sign := 1.0
		if lat0 < 0 {
			sign = -1
		}
		F := D + math.Sqrt(D*D-1)*sign
		H := F * math.Pow(t0, B)
		G := (F - 1/F) / 2
		g0 := math.Asin(math.Sin(alpha) / D)
		lamO := lon0 - math.Asin(G*math.Tan(g0))/B
		uc := 0.0
		if c.Var&varCentre != 0 {
			if math.Abs(math.Abs(c.Alpha)-90) < 1e-9 {
				uc = A * (lon0 - lamO)
			} else {
				uc = A / B * math.Atan(math.Sqrt(D*D-1)/math.Cos(alpha)) * sign
			}
		}
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			v := x*math.Cos(gammaC) - y*math.Sin(gammaC)
			u := y*math.Cos(gammaC) + x*math.Sin(gammaC) + uc
			Q := math.Exp(-B * v / A)
			S, T, V := (Q-1/Q)/2, (Q+1/Q)/2, math.Sin(B*u/A)
			U := (V*math.Cos(g0) + S*math.Sin(g0)) / T
			t := math.Pow(H/math.Sqrt((1+U)/(1-U)), 1/B)
			return out(lamO-math.Atan2(S*math.Cos(g0)-V*math.Sin(g0), math.Cos(B*u/A))/B, phiFromT(t))
		}
	case "cass":
		m0 := arc(lat0)
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			p1 := foot(m0 + y)
			t1 := math.Tan(p1) * math.Tan(p1)
			nu1 := a / math.Sqrt(1-e2*sin2(p1))
			rho1 := a * (1 - e2) / math.Pow(1-e2*sin2(p1), 1.5)
			d := x / nu1
			phi := p1 - nu1*math.Tan(p1)/rho1*(d*d/2-(1+3*t1)*math.Pow(d, 4)/24)
			return out(lon0+(d-t1*math.Pow(d, 3)/3+(1+3*t1)*t1*math.Pow(d, 5)/15)/math.Cos(p1), phi)
		}
	case "poly":
		m0 := arc(lat0)
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			if math.Abs(y+m0) < 1e-9 {
				return out(x/a+lon0, 0)
			}
			A, B := (m0+y)/a, (m0+y)*(m0+y)/(a*a)+x*x/(a*a)
			phi := A
			for i := 0; i < 30; i++ {
				s2 := math.Sin(2 * phi)
				C := math.Sqrt(1-e2*sin2(phi)) * math.Tan(phi)
				Ma := arc(phi) / a
				Mn := m1 - 2*(3*e2/8+3*e4/32+45*e6/1024)*math.Cos(2*phi) + 4*(15*e4/256+45*e6/1024)*math.Cos(4*phi) - 6*(35*e6/3072)*math.Cos(6*phi)
				d := (A*(C*Ma+1) - Ma - 0.5*(Ma*Ma+B)*C) / (e2*s2*(Ma*Ma+B-2*A*Ma)/(4*C) + (A-Ma)*(C*Mn-2/s2) - Mn)
				phi -= d
				if math.Abs(d) < 1e-14 {
					break
				}
			}
			C := math.Sqrt(1-e2*sin2(phi)) * math.Tan(phi)
			return out(math.Asin(math.Max(-1, math.Min(1, x*C/a)))/math.Sin(phi)+lon0, phi)
		}
	case "eqc":
		nu1 := a / math.Sqrt(1-e2*sin2(lat1))
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			return out(lon0+x/(nu1*math.Cos(lat1)), foot(y))
		}
	case "krovak":
		latp := lat1
		A := a * math.Sqrt(1-e2) / (1 - e2*sin2(lat0))
		B := math.Sqrt(1 + e2*math.Pow(math.Cos(lat0), 4)/(1-e2))
		g0 := math.Asin(math.Sin(lat0) / B)
		s0 := math.Sin(lat0)
		t0 := math.Tan(math.Pi/4+g0/2) * math.Pow((1+e*s0)/(1-e*s0), e*B/2) / math.Pow(math.Tan(math.Pi/4+lat0/2), B)
		n := math.Sin(latp)
		r0 := k0 * A / math.Tan(latp)
		ac := rad(c.Alpha)
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			xp, yp := -y, -x // east, north -> southing, westing
			r := math.Hypot(xp, yp)
			theta := math.Atan2(yp, xp)
			d := theta / n
			t := 2 * (math.Atan(math.Pow(r0/r, 1/n)*math.Tan(math.Pi/4+latp/2)) - math.Pi/4)
			u := math.Asin(math.Cos(ac)*math.Sin(t) - math.Sin(ac)*math.Cos(t)*math.Cos(d))
			v := math.Asin(math.Cos(t) * math.Sin(d) / math.Cos(u))
			phi := u
			for i := 0; i < 30; i++ {
				s := math.Sin(phi)
				p := 2 * (math.Atan(math.Pow(t0, -1/B)*math.Pow(math.Tan(u/2+math.Pi/4), 1/B)*math.Pow((1+e*s)/(1-e*s), e/2)) - math.Pi/4)
				if math.Abs(p-phi) < 1e-14 {
					phi = p
					break
				}
				phi = p
			}
			return out(lon0-v/B, phi)
		}
	case "cea":
		k := mOf(lat1)
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			return out(lon0+x/(a*k), phiFromQ(qp*math.Max(-1, math.Min(1, 2*y*k/(a*qp)))))
		}
	case "colurban":
		nu0 := a / math.Sqrt(1-e2*sin2(lat0))
		rho0 := a * (1 - e2) / math.Pow(1-e2*sin2(lat0), 1.5)
		B := math.Tan(lat0) / (2 * rho0 * nu0)
		C := 1 + c.H0/a
		D := rho0 * (1 + c.H0/(a*(1-e2)))
		c.proj = func(x, y float64) (float64, float64) {
			x, y = in(x, y)
			phi := lat0 + y/D - B*(x/C)*(x/C)
			nu := a / math.Sqrt(1-e2*sin2(phi))
			return out(lon0+x/(C*nu*math.Cos(phi)), phi)
		}
	default:
		c.proj = func(x, y float64) (float64, float64) { return math.NaN(), math.NaN() }
	}
}

// ---- guessing unlabelled coordinates

// CRSGuess is a candidate interpretation of unlabelled coordinates.
type CRSGuess struct {
	CRS    CRS    `json:"crs"`
	Reason string `json:"reason"`
}

var (
	allProjOnce sync.Once
	allProj     []*CRS
)

// projected returns every usable projected system in the table, ready to convert.
func projected() []*CRS {
	allProjOnce.Do(func() {
		t := epsg()
		codes := make([]int, 0, len(t.proj))
		for code := range t.proj {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		for _, code := range codes {
			c, err := crsFromCode(code)
			if err != nil || c.ext == 0 {
				continue
			}
			c.init()
			allProj = append(allProj, &c)
		}
	})
	return allProj
}

// guessCRS ranks likely systems for a sample of raw coordinates. Degrees are recognised directly;
// anything else is tried in every projected system, keeping those that put the points inside the
// system's own area of use. hint is a lon/lat near the data (another layer, or the map view).
func guessCRS(sample []Pt, hint Pt) []CRSGuess {
	if len(sample) == 0 {
		return nil
	}
	var geo, geoSwap int
	for _, p := range sample {
		x, y := p[0], p[1]
		switch {
		case math.Abs(x) <= 180 && math.Abs(y) <= 90:
			geo++
		case math.Abs(x) <= 90 && math.Abs(y) <= 180:
			geoSwap++
		}
	}
	n := len(sample)
	most := func(k int) bool { return k*10 >= n*9 }
	if most(geo) {
		return []CRSGuess{{crsWGS84, "Values look like longitude and latitude in degrees"}}
	}
	if most(geoSwap) {
		c := crsWGS84
		c.Swap = true
		c.Name = "Latitude/longitude (columns swapped)"
		return []CRSGuess{{c, "Values look like degrees with latitude first"}}
	}
	sub := sample
	if len(sub) > 25 {
		sub = make([]Pt, 25)
		for i := range sub {
			sub[i] = sample[i*(len(sample)-1)/24]
		}
	}
	t := epsg()
	type cand struct {
		c          *CRS
		dist, size float64
		wgs, epsg  bool
	}
	var cands []cand
	// No hint (the map still shows its overview): lean towards North America, Pracre's home, and
	// towards statewide and national systems over county and city grids.
	weak := hint == (Pt{})
	if weak {
		hint = Pt{-98, 44}
	}
	cosH := math.Cos(rad(hint[1]))
	for _, c := range projected() {
		x := t.extents[c.ext]
		inside := 0
		var sx, sy float64
		for _, p := range sub {
			lon, lat := c.proj(p[0], p[1])
			if x.contains(lon, lat, 0.05) {
				inside++
				sx += lon
				sy += lat
			}
		}
		if inside*10 < len(sub)*9 {
			continue
		}
		lon, lat := sx/float64(inside), sy/float64(inside)
		d := math.Hypot((lon-hint[0])*cosH, lat-hint[1]) * 111 // km
		cands = append(cands, cand{c, d, math.Abs((x.e - x.w) * (x.n - x.s)), c.Datum == "" || t.wgs[c.Datum], strings.HasPrefix(c.ID, "EPSG:")})
	}
	// With a hint: nearest it first (within 25 km counts as equal), EPSG before ESRI, modern datums,
	// then the most specific area. Without: EPSG systems covering at least a square degree first,
	// then distance in 1000 km steps, then the widest area.
	bucket := 25.0
	if weak {
		bucket = 1000
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if weak {
			if ba, bb := a.epsg && a.size >= 1, b.epsg && b.size >= 1; ba != bb {
				return ba
			}
		}
		if da, db := math.Floor(a.dist/bucket), math.Floor(b.dist/bucket); da != db {
			return da < db
		}
		if a.epsg != b.epsg {
			return a.epsg
		}
		if a.wgs != b.wgs {
			return a.wgs
		}
		if weak {
			return a.size > b.size
		}
		return a.size < b.size
	})
	var out []CRSGuess
	seen := map[string]bool{}
	for _, k := range cands {
		if len(out) == 8 {
			break
		}
		// One entry per distinct grid: NAD83, HARN and 2011 versions of a zone put data in the same place.
		key := fmt.Sprintf("%s %.6f %.6f %.6f %.6f %.9f %.1f %.1f %.6f %.6f", k.c.Kind, k.c.Lat0, k.c.Lon0, k.c.Lat1, k.c.Lat2, k.c.K0, k.c.FE, k.c.FN, k.c.Unit, k.c.Alpha)
		if seen[key] {
			continue
		}
		seen[key] = true
		reason := "Coordinates fit " + t.extents[k.c.ext].name
		if !weak && k.dist < 50 {
			reason += ", near the map view or your other data"
		}
		c := *k.c
		c.ready, c.proj, c.shift = false, nil, nil
		out = append(out, CRSGuess{c, reason})
	}
	return out
}
