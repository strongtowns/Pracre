package main

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Pt is x/y in source units, or lon/lat in degrees once reprojected.
type Pt [2]float64

// Ring is a closed or open ring of points.
type Ring []Pt

// Poly is an outer ring followed by its holes.
type Poly []Ring

// Geom is either a point (Polys empty) or a multipolygon. OK is false when a row has no usable location.
type Geom struct {
	Pt    Pt     `json:"p,omitempty"`
	Polys []Poly `json:"m,omitempty"`
	OK    bool   `json:"ok,omitempty"`
}

func (g Geom) IsPoly() bool { return len(g.Polys) > 0 }

func pointGeom(x, y float64) Geom {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return Geom{}
	}
	return Geom{Pt: Pt{x, y}, OK: true}
}

func polyGeom(polys []Poly) Geom {
	if len(polys) == 0 {
		return Geom{}
	}
	return Geom{Polys: polys, OK: true, Pt: ringCentroid(polys[0][0])}
}

// mapGeom applies f to every coordinate.
func mapGeom(g Geom, f func(Pt) Pt) Geom {
	if !g.OK {
		return g
	}
	out := Geom{OK: true, Pt: f(g.Pt)}
	if len(g.Polys) == 0 {
		return out
	}
	out.Polys = make([]Poly, len(g.Polys))
	for i, p := range g.Polys {
		np := make(Poly, len(p))
		for j, r := range p {
			nr := make(Ring, len(r))
			for k, q := range r {
				nr[k] = f(q)
			}
			np[j] = nr
		}
		out.Polys[i] = np
	}
	return out
}

// eachPt calls f for a sample of coordinates (all of them for points, ring vertices for polygons).
func eachPt(g Geom, f func(Pt)) {
	if !g.OK {
		return
	}
	if len(g.Polys) == 0 {
		f(g.Pt)
		return
	}
	for _, p := range g.Polys {
		for _, r := range p {
			for _, q := range r {
				f(q)
			}
		}
	}
}

const earthR = 6378137.0

func rad(d float64) float64 { return d * math.Pi / 180 }

// ringAreaGeo is the spherical area of a lon/lat ring in m² (signed; same method as geojson-area).
func ringAreaGeo(r Ring) float64 {
	n := len(r)
	if n < 3 {
		return 0
	}
	a := 0.0
	for i := 0; i < n; i++ {
		p1, p2, p3 := r[i], r[(i+1)%n], r[(i+2)%n]
		a += (rad(p3[0]) - rad(p1[0])) * math.Sin(rad(p2[1]))
	}
	return a * earthR * earthR / 2
}

// geomAreaGeo is the area of a lon/lat multipolygon in m².
func geomAreaGeo(g Geom) float64 {
	total := 0.0
	for _, p := range g.Polys {
		for i, r := range p {
			a := math.Abs(ringAreaGeo(r))
			if i == 0 {
				total += a
			} else {
				total -= a
			}
		}
	}
	return math.Max(total, 0)
}

// ringSigned is the planar signed area (positive when counter-clockwise with y up).
func ringSigned(r Ring) float64 {
	a := 0.0
	n := len(r)
	for i := 0; i < n; i++ {
		p, q := r[i], r[(i+1)%n]
		a += p[0]*q[1] - q[0]*p[1]
	}
	return a / 2
}

func ringCentroid(r Ring) Pt {
	n := len(r)
	if n == 0 {
		return Pt{}
	}
	// Shift to the first vertex so large projected coordinates keep their precision.
	o := r[0]
	var cx, cy, a float64
	for i := 0; i < n; i++ {
		p := Pt{r[i][0] - o[0], r[i][1] - o[1]}
		q := Pt{r[(i+1)%n][0] - o[0], r[(i+1)%n][1] - o[1]}
		c := p[0]*q[1] - q[0]*p[1]
		a += c
		cx += (p[0] + q[0]) * c
		cy += (p[1] + q[1]) * c
	}
	if math.Abs(a) < 1e-18 {
		var sx, sy float64
		for _, p := range r {
			sx += p[0]
			sy += p[1]
		}
		return Pt{sx / float64(n), sy / float64(n)}
	}
	return Pt{o[0] + cx/(3*a), o[1] + cy/(3*a)}
}

// labelPoint is the centroid of the largest outer ring, or the point itself.
func labelPoint(g Geom) Pt {
	if len(g.Polys) == 0 {
		return g.Pt
	}
	best, bestA := 0, -1.0
	for i, p := range g.Polys {
		if a := math.Abs(ringSigned(p[0])); a > bestA {
			best, bestA = i, a
		}
	}
	c := ringCentroid(g.Polys[best][0])
	if !pointInPoly(c, g.Polys[best]) {
		// Concave lot: fall back to the middle of the widest horizontal slice through the centroid.
		if p, ok := sliceMid(g.Polys[best][0], c[1]); ok {
			return p
		}
	}
	return c
}

func sliceMid(r Ring, y float64) (Pt, bool) {
	var xs []float64
	n := len(r)
	for i := 0; i < n; i++ {
		a, b := r[i], r[(i+1)%n]
		if (a[1] > y) != (b[1] > y) {
			xs = append(xs, a[0]+(y-a[1])/(b[1]-a[1])*(b[0]-a[0]))
		}
	}
	if len(xs) < 2 {
		return Pt{}, false
	}
	sort.Float64s(xs)
	bi, bw := 0, -1.0
	for i := 0; i+1 < len(xs); i += 2 {
		if w := xs[i+1] - xs[i]; w > bw {
			bi, bw = i, w
		}
	}
	return Pt{(xs[bi] + xs[bi+1]) / 2, y}, true
}

func pointInRing(p Pt, r Ring) bool {
	in := false
	n := len(r)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

func pointInPoly(p Pt, poly Poly) bool {
	if len(poly) == 0 || !pointInRing(p, poly[0]) {
		return false
	}
	for _, h := range poly[1:] {
		if pointInRing(p, h) {
			return false
		}
	}
	return true
}

func pointInGeom(p Pt, g Geom) bool {
	for _, poly := range g.Polys {
		if pointInPoly(p, poly) {
			return true
		}
	}
	return false
}

type BBox struct{ MinX, MinY, MaxX, MaxY float64 }

func emptyBBox() BBox { return BBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} }

func (b *BBox) Add(p Pt) {
	b.MinX = math.Min(b.MinX, p[0])
	b.MinY = math.Min(b.MinY, p[1])
	b.MaxX = math.Max(b.MaxX, p[0])
	b.MaxY = math.Max(b.MaxY, p[1])
}

func (b BBox) Valid() bool { return b.MinX <= b.MaxX && b.MinY <= b.MaxY }

func geomBBox(g Geom) BBox {
	b := emptyBBox()
	eachPt(g, b.Add)
	return b
}

// assembleRings groups rings into polygons the shapefile / Esri way: clockwise rings are outers,
// counter-clockwise rings are holes of the outer that contains them.
func assembleRings(rings []Ring) []Poly {
	var polys []Poly
	var holes []Ring
	for _, r := range rings {
		if len(r) < 3 {
			continue
		}
		if ringSigned(r) <= 0 {
			polys = append(polys, Poly{r})
		} else {
			holes = append(holes, r)
		}
	}
	if len(polys) == 0 {
		// Wrong winding everywhere: treat every ring as its own polygon.
		for _, r := range holes {
			polys = append(polys, Poly{r})
		}
		return polys
	}
	for _, h := range holes {
		placed := false
		for i := range polys {
			if pointInRing(h[0], polys[i][0]) {
				polys[i] = append(polys[i], h)
				placed = true
				break
			}
		}
		if !placed {
			polys = append(polys, Poly{h})
		}
	}
	return polys
}

// ---- GeoJSON geometry ----

type gjGeom struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
	Geometries  []gjGeom        `json:"geometries"`
}

func parseGeoJSONGeom(raw []byte) Geom {
	if len(raw) == 0 || string(raw) == "null" {
		return Geom{}
	}
	var g gjGeom
	if json.Unmarshal(raw, &g) != nil {
		return Geom{}
	}
	return g.toGeom()
}

func (g gjGeom) toGeom() Geom {
	switch strings.ToLower(g.Type) {
	case "point":
		var c []float64
		if json.Unmarshal(g.Coordinates, &c) == nil && len(c) >= 2 {
			return pointGeom(c[0], c[1])
		}
	case "multipoint":
		var c [][]float64
		if json.Unmarshal(g.Coordinates, &c) == nil && len(c) > 0 && len(c[0]) >= 2 {
			return pointGeom(c[0][0], c[0][1])
		}
	case "polygon":
		var c [][][]float64
		if json.Unmarshal(g.Coordinates, &c) == nil {
			return polyGeom([]Poly{toPoly(c)})
		}
	case "multipolygon":
		var c [][][][]float64
		if json.Unmarshal(g.Coordinates, &c) == nil {
			var ps []Poly
			for _, p := range c {
				if pp := toPoly(p); len(pp) > 0 {
					ps = append(ps, pp)
				}
			}
			return polyGeom(ps)
		}
	case "geometrycollection":
		var ps []Poly
		var pt *Geom
		for _, s := range g.Geometries {
			sg := s.toGeom()
			if sg.IsPoly() {
				ps = append(ps, sg.Polys...)
			} else if sg.OK && pt == nil {
				pt = &sg
			}
		}
		if len(ps) > 0 {
			return polyGeom(ps)
		}
		if pt != nil {
			return *pt
		}
	}
	return Geom{}
}

func toPoly(c [][][]float64) Poly {
	var p Poly
	for _, r := range c {
		var ring Ring
		for _, q := range r {
			if len(q) >= 2 {
				ring = append(ring, Pt{q[0], q[1]})
			}
		}
		if len(ring) >= 3 {
			p = append(p, ring)
		}
	}
	return p
}

// ---- WKT / EWKT ----

// parseWKT reads POINT, MULTIPOINT, POLYGON and MULTIPOLYGON (with optional Z/M and SRID=n; prefix).
func parseWKT(s string) (Geom, int) {
	s = strings.TrimSpace(s)
	srid := 0
	if strings.HasPrefix(strings.ToUpper(s), "SRID=") {
		if i := strings.IndexByte(s, ';'); i > 0 {
			srid, _ = strconv.Atoi(s[5:i])
			s = s[i+1:]
		}
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return Geom{}, srid
	}
	kind := strings.ToUpper(strings.Fields(s[:open] + " x")[0])
	nums, ok := parseNested(s[open:])
	if !ok {
		return Geom{}, srid
	}
	switch kind {
	case "POINT":
		if pts := leafPoints(nums); len(pts) > 0 {
			return pointGeom(pts[0][0], pts[0][1]), srid
		}
	case "MULTIPOINT":
		if pts := leafPoints(nums); len(pts) > 0 {
			return pointGeom(pts[0][0], pts[0][1]), srid
		}
	case "POLYGON":
		return polyGeom([]Poly{nestedPoly(nums)}), srid
	case "MULTIPOLYGON":
		var ps []Poly
		for _, p := range nums.kids {
			if pp := nestedPoly(p); len(pp) > 0 {
				ps = append(ps, pp)
			}
		}
		return polyGeom(ps), srid
	}
	return Geom{}, srid
}

type nest struct {
	coords []float64
	kids   []*nest
}

// parseNested turns "((1 2, 3 4), (5 6))" into a tree whose leaves are coordinate tuples.
func parseNested(s string) (*nest, bool) {
	var stack []*nest
	var root *nest
	cur := ""
	flush := func() {
		f := strings.Fields(cur)
		cur = ""
		if len(f) == 0 || len(stack) == 0 {
			return
		}
		leaf := &nest{}
		for _, t := range f {
			v, err := strconv.ParseFloat(t, 64)
			if err != nil {
				return
			}
			leaf.coords = append(leaf.coords, v)
		}
		top := stack[len(stack)-1]
		top.kids = append(top.kids, leaf)
	}
	for _, ch := range s {
		switch ch {
		case '(':
			n := &nest{}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.kids = append(top.kids, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case ')':
			flush()
			if len(stack) == 0 {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		case ',':
			flush()
		default:
			cur += string(ch)
		}
	}
	return root, root != nil
}

func leafPoints(n *nest) []Pt {
	if n == nil {
		return nil
	}
	if len(n.coords) >= 2 {
		return []Pt{{n.coords[0], n.coords[1]}}
	}
	var out []Pt
	for _, k := range n.kids {
		out = append(out, leafPoints(k)...)
	}
	return out
}

func nestedPoly(n *nest) Poly {
	var p Poly
	for _, r := range n.kids {
		if ring := Ring(leafPoints(r)); len(ring) >= 3 {
			p = append(p, ring)
		}
	}
	return p
}

// ---- local planar frame for tiling, 3D export ----

// Local is an equirectangular frame around a centre: metres east/north. Good to well under 1% at city scale.
type Local struct{ Lon0, Lat0, kx, ky float64 }

func newLocal(lon0, lat0 float64) Local {
	return Local{lon0, lat0, earthR * math.Pi / 180 * math.Cos(rad(lat0)), earthR * math.Pi / 180}
}

func (l Local) Fwd(p Pt) Pt { return Pt{(p[0] - l.Lon0) * l.kx, (p[1] - l.Lat0) * l.ky} }
func (l Local) Inv(p Pt) Pt { return Pt{l.Lon0 + p[0]/l.kx, l.Lat0 + p[1]/l.ky} }
