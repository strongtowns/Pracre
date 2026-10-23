package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

type LegendItem struct {
	Color string `json:"color"`
	Label string `json:"label"`
}

// EmbedOpts is what the UI knows about the current view; the data comes from the cached layer.
type EmbedOpts struct {
	Title       string       `json:"title"`
	Subtitle    string       `json:"subtitle"`
	ThreeD      bool         `json:"threeD"`
	Center      [2]float64   `json:"center"`
	Zoom        float64      `json:"zoom"`
	Pitch       float64      `json:"pitch"`
	Bearing     float64      `json:"bearing"`
	Colors      []string     `json:"colors"`
	Zero        string       `json:"zero"`
	NoArea      string       `json:"noArea"`
	Legend      []LegendItem `json:"legend"`
	Height      float64      `json:"height"` // metres of extrusion per unit of the per-area value
	Cap         float64      `json:"cap"`    // per-area values above this are drawn at this height (0: no cap)
	Basemap     string       `json:"basemap"`
	MetricLabel string       `json:"metricLabel"`
	TotalLabel  string       `json:"totalLabel"`
	Currency    string       `json:"currency"`
	Per         string       `json:"per"`
	Opacity     float64      `json:"opacity"`
}

const mapLibreVersion = "5.24.0"

// ExportHTML writes a single self-contained web page (data inline, MapLibre from a CDN) for websites.
func (a *App) ExportHTML(o EmbedOpts) (string, error) {
	m, L, err := a.currentLayer()
	if err != nil {
		return "", err
	}
	def := a.projectBase() + "-2d.html"
	if o.ThreeD {
		def = a.projectBase() + "-3d.html"
	}
	p, err := a.saveDialog("Export web map", def, "*.html", "Web page")
	if err != nil || p == "" {
		return "", err
	}
	page, err := embedPage(m, L, o)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, page, 0o644)
}

// embedPage fills the web map template with the layer and the view settings.
func embedPage(m *Model, L *Layer, o EmbedOpts) ([]byte, error) {
	var data bytes.Buffer
	if err := L.writeGeoJSON(&data, m, false); err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(o)
	r := strings.NewReplacer(
		"{{TITLE}}", htmlEsc(o.Title),
		"{{VERSION}}", mapLibreVersion,
		"/*CONFIG*/null", string(cfg),
		// A label containing "</script>" must not end the script block.
		"/*DATA*/null", strings.ReplaceAll(data.String(), "</", `<\/`),
	)
	return []byte(r.Replace(embedHTML)), nil
}

func htmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

const embedHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{TITLE}}</title>
<link rel="stylesheet" href="https://unpkg.com/maplibre-gl@{{VERSION}}/dist/maplibre-gl.css">
<script src="https://unpkg.com/maplibre-gl@{{VERSION}}/dist/maplibre-gl.js"></script>
<style>
  html, body { margin: 0; height: 100%; font: 13px/1.4 "Segoe UI", system-ui, -apple-system, Roboto, sans-serif; color: #0f2444; background: #eceef2; }
  #map { position: absolute; inset: 0; }
  .card { position: absolute; background: #fff; border: 1px solid #b9c1cd; box-shadow: 2px 2px 0 rgba(15,36,68,.12); }
  #title { top: 12px; left: 12px; max-width: min(420px, calc(100% - 90px)); }
  #title h1 { margin: 0; padding: 7px 12px; font-size: 15px; font-weight: 600; color: #fff; background: #0f2444; border-bottom: 3px solid #f26522; }
  #title p { margin: 0; padding: 7px 12px; color: #3b4a63; }
  #legend { bottom: 28px; left: 12px; padding: 8px 10px; min-width: 170px; }
  #legend b { display: block; margin-bottom: 5px; font-size: 12px; }
  .row { display: flex; align-items: center; gap: 8px; font-size: 12px; line-height: 18px; white-space: nowrap; }
  .sw { width: 22px; height: 12px; border: 1px solid rgba(15,36,68,.35); }
  #toggle { top: 12px; right: 56px; display: flex; }
  #toggle button { font: inherit; font-weight: 600; padding: 6px 12px; border: 0; border-right: 1px solid #b9c1cd; background: #fff; color: #0f2444; cursor: pointer; }
  #toggle button:last-child { border-right: 0; }
  #toggle button.on { background: #0f2444; color: #fff; box-shadow: inset 0 -3px 0 #f26522; }
  #tip { position: absolute; pointer-events: none; display: none; background: #fff; border: 1px solid #0f2444; padding: 6px 9px; font-size: 12px; box-shadow: 2px 2px 0 rgba(15,36,68,.15); }
  #tip strong { display: block; font-size: 14px; }
  #credit { position: absolute; right: 8px; bottom: 28px; font-size: 11px; background: rgba(255,255,255,.85); padding: 2px 6px; }
  #credit a { color: #0f2444; }
  @media (max-width: 520px) { #legend { bottom: 34px; } #title p { display: none; } }
</style>
</head>
<body>
<div id="map"></div>
<div id="title" class="card"><h1>{{TITLE}}</h1><p id="sub"></p></div>
<div id="toggle" class="card"><button id="b2">2D</button><button id="b3">3D</button></div>
<div id="legend" class="card"></div>
<div id="tip"></div>
<div id="credit">Made with <a href="https://github.com/strongtowns/Pracre" target="_blank" rel="noopener">Pracre</a></div>
<script>
const CFG = /*CONFIG*/null;
const DATA = /*DATA*/null;
const fmt = (v) => v == null ? "n/a" : CFG.currency + (Math.abs(v) >= 1e6 ? (v / 1e6).toFixed(2) + "M" : Math.abs(v) >= 1e4 ? (v / 1e3).toFixed(0) + "K" : Math.round(v).toLocaleString("en-US"));
document.getElementById("sub").textContent = CFG.subtitle || "";
if (!CFG.subtitle) document.getElementById("sub").style.display = "none";
const lg = document.getElementById("legend");
lg.innerHTML = "<b></b>";
lg.firstChild.textContent = CFG.metricLabel;
CFG.legend.slice().reverse().forEach(function (it) {
  const r = document.createElement("div"); r.className = "row";
  const s = document.createElement("span"); s.className = "sw"; s.style.background = it.color;
  const t = document.createElement("span"); t.textContent = it.label;
  r.append(s, t); lg.append(r);
});
let style;
if (CFG.basemap === "osm") {
  style = { version: 8, sources: { osm: { type: "raster", tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"], tileSize: 256, maxzoom: 19,
    attribution: "&copy; <a href=\"https://www.openstreetmap.org/copyright\">OpenStreetMap</a> contributors" } },
    layers: [{ id: "osm", type: "raster", source: "osm", paint: { "raster-saturation": -0.6, "raster-opacity": 0.85 } }] };
} else if (!CFG.basemap || CFG.basemap === "none") {
  style = { version: 8, sources: {}, layers: [{ id: "bg", type: "background", paint: { "background-color": "#eceef2" } }] };
} else style = CFG.basemap;
const map = new maplibregl.Map({ container: "map", style: style, center: CFG.center, zoom: CFG.zoom, pitch: CFG.threeD ? CFG.pitch : 0,
  bearing: CFG.bearing, maxPitch: 75, attributionControl: { compact: true } });
map.addControl(new maplibregl.NavigationControl({ visualizePitch: true }), "top-right");
const color = ["match", ["get", "c"], -1, CFG.zero, -2, CFG.noArea].concat(CFG.colors.flatMap(function (c, i) { return [i, c]; }), [CFG.colors[CFG.colors.length - 1]]);
let threeD = CFG.threeD;
function setMode(on) {
  threeD = on;
  document.getElementById("b3").classList.toggle("on", on);
  document.getElementById("b2").classList.toggle("on", !on);
  if (!map.getLayer("vpa-3d")) return;
  map.setLayoutProperty("vpa-3d", "visibility", on ? "visible" : "none");
  map.setLayoutProperty("vpa-2d", "visibility", on ? "none" : "visible");
  map.easeTo({ pitch: on ? (CFG.pitch || 55) : 0, duration: 900 });
}
document.getElementById("b2").onclick = function () { setMode(false); };
document.getElementById("b3").onclick = function () { setMode(true); };
map.on("load", function () {
  map.addSource("vpa", { type: "geojson", data: DATA });
  map.addLayer({ id: "vpa-2d", type: "fill", source: "vpa", paint: { "fill-color": color, "fill-opacity": CFG.opacity || 0.85, "fill-outline-color": "rgba(15,36,68,0.25)" } });
  map.addLayer({ id: "vpa-3d", type: "fill-extrusion", source: "vpa", paint: { "fill-extrusion-color": color, "fill-extrusion-opacity": 0.95,
    "fill-extrusion-height": ["*", ["min", ["max", ["coalesce", ["get", "v"], 0], 0], CFG.cap > 0 ? CFG.cap : 1e15], CFG.height], "fill-extrusion-vertical-gradient": true } });
  map.addLayer({ id: "vpa-pts", type: "circle", source: "vpa", filter: ["==", ["geometry-type"], "Point"], paint: { "circle-color": color, "circle-radius": 4, "circle-stroke-width": 0.5, "circle-stroke-color": "#0f2444" } });
  setMode(threeD);
  const tip = document.getElementById("tip");
  ["vpa-2d", "vpa-3d", "vpa-pts"].forEach(function (id) {
    map.on("mousemove", id, function (e) {
      const p = e.features[0].properties;
      map.getCanvas().style.cursor = "pointer";
      tip.style.display = "block";
      tip.style.left = Math.min(e.point.x + 14, innerWidth - 220) + "px";
      tip.style.top = (e.point.y + 14) + "px";
      tip.innerHTML = "";
      const h = document.createElement("strong"); h.textContent = fmt(p.v === "null" ? null : p.v) + " per " + CFG.per;
      const l = document.createElement("div"); l.textContent = p.l || (p.n + " parcel" + (p.n == 1 ? "" : "s"));
      const t = document.createElement("div"); t.textContent = CFG.totalLabel + ": " + fmt(p.t) + " on " + Number(p.a).toFixed(2) + " " + CFG.per + "s";
      tip.append(h, l, t);
    });
    map.on("mouseleave", id, function () { tip.style.display = "none"; map.getCanvas().style.cursor = ""; });
  });
});
</script>
</body>
</html>
`

// ---- glTF (.glb) model of the current layer

type GLBOpts struct {
	Colors []string `json:"colors"`
	Zero   string   `json:"zero"`
	NoArea string   `json:"noArea"`
	Height float64  `json:"height"`
	Cap    float64  `json:"cap"`
	Base   bool     `json:"base"`
}

func hexLinear(h string) [3]float32 {
	h = strings.TrimPrefix(strings.TrimSpace(h), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil || len(h) != 6 {
		return [3]float32{0.7, 0.7, 0.7}
	}
	lin := func(c uint64) float32 {
		s := float64(c) / 255
		if s <= 0.04045 {
			return float32(s / 12.92)
		}
		return float32(math.Pow((s+0.055)/1.055, 2.4))
	}
	return [3]float32{lin(v >> 16 & 255), lin(v >> 8 & 255), lin(v & 255)}
}

type meshBuf struct {
	pos, nrm, col []float32
	idx           []uint32
	min, max      [3]float32
}

func (b *meshBuf) vert(x, y, z float64, n [3]float32, c [3]float32) uint32 {
	i := uint32(len(b.pos) / 3)
	p := [3]float32{float32(x), float32(y), float32(z)}
	if i == 0 {
		b.min, b.max = p, p
	}
	for k := 0; k < 3; k++ {
		b.min[k] = min(b.min[k], p[k])
		b.max[k] = max(b.max[k], p[k])
	}
	b.pos = append(b.pos, p[:]...)
	b.nrm = append(b.nrm, n[:]...)
	b.col = append(b.col, c[:]...)
	return i
}

// prism adds an extruded polygon (outer ring only) from y0 to y1. ring is in local metres (x east, y north).
func (b *meshBuf) prism(ring []Pt, y0, y1 float64, c [3]float32) {
	if n := len(ring); n > 1 && ring[0] == ring[n-1] {
		ring = ring[:n-1]
	}
	if len(ring) < 3 {
		return
	}
	if ringSigned(ring) < 0 {
		r := make([]Pt, len(ring))
		for i := range ring {
			r[i] = ring[len(ring)-1-i]
		}
		ring = r
	}
	// glTF is Y up with -Z pointing north.
	up := [3]float32{0, 1, 0}
	base := make([]uint32, len(ring))
	for i, p := range ring {
		base[i] = b.vert(p[0], y1, -p[1], up, c)
	}
	for _, t := range earClip(ring) {
		// Counter-clockwise in x/north stays counter-clockwise seen from +Y in x/-z, so it faces up.
		b.idx = append(b.idx, base[t[0]], base[t[1]], base[t[2]])
	}
	for i := range ring {
		p, q := ring[i], ring[(i+1)%len(ring)]
		dx, dy := q[0]-p[0], q[1]-p[1]
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		n := [3]float32{float32(dy / l), 0, float32(dx / l)} // outward normal (dy,-dx) in x/north, z = -north
		a := b.vert(p[0], y0, -p[1], n, c)
		bb := b.vert(q[0], y0, -q[1], n, c)
		cc := b.vert(q[0], y1, -q[1], n, c)
		d := b.vert(p[0], y1, -p[1], n, c)
		b.idx = append(b.idx, a, bb, cc, a, cc, d)
	}
}

// earClip triangulates a simple counter-clockwise polygon. Falls back to a fan if the polygon is degenerate.
func earClip(r []Pt) [][3]int {
	n := len(r)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var out [][3]int
	cross := func(a, b, c Pt) float64 { return (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0]) }
	inside := func(p, a, b, c Pt) bool {
		return cross(a, b, p) >= 0 && cross(b, c, p) >= 0 && cross(c, a, p) >= 0
	}
	guard := 0
	for len(idx) > 3 && guard < n*n {
		guard++
		clipped := false
		for i := range idx {
			ia, ib, ic := idx[(i+len(idx)-1)%len(idx)], idx[i], idx[(i+1)%len(idx)]
			a, b, c := r[ia], r[ib], r[ic]
			if cross(a, b, c) <= 0 {
				continue
			}
			ear := true
			for _, j := range idx {
				if j != ia && j != ib && j != ic && inside(r[j], a, b, c) {
					ear = false
					break
				}
			}
			if ear {
				out = append(out, [3]int{ia, ib, ic})
				idx = append(idx[:i], idx[i+1:]...)
				clipped = true
				break
			}
		}
		if !clipped {
			break
		}
	}
	if len(idx) == 3 {
		out = append(out, [3]int{idx[0], idx[1], idx[2]})
	} else if len(idx) > 3 {
		for i := 1; i+1 < len(idx); i++ {
			out = append(out, [3]int{idx[0], idx[i], idx[i+1]})
		}
	}
	return out
}

// ExportGLB writes the current layer as a coloured 3D model in metres, ready for Blender or a 3D viewer.
func (a *App) ExportGLB(o GLBOpts) (string, error) {
	_, L, err := a.currentLayer()
	if err != nil {
		return "", err
	}
	p, err := a.saveDialog("Export 3D model", a.projectBase()+"-"+L.Req.Mode+".glb", "*.glb", "glTF binary model")
	if err != nil || p == "" {
		return "", err
	}
	b, err := buildGLB(L, o)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o644)
}

func buildGLB(L *Layer, o GLBOpts) ([]byte, error) {
	bb := emptyBBox()
	for _, f := range L.Feats {
		eachPt(f.G, bb.Add)
	}
	if !bb.Valid() {
		return nil, fmt.Errorf("nothing to export")
	}
	loc := newLocal((bb.MinX+bb.MaxX)/2, (bb.MinY+bb.MaxY)/2)
	var mb meshBuf
	cols := make([][3]float32, len(o.Colors))
	for i, c := range o.Colors {
		cols[i] = hexLinear(c)
	}
	zero, noArea := hexLinear(o.Zero), hexLinear(o.NoArea)
	for _, f := range L.Feats {
		if !f.G.IsPoly() {
			continue
		}
		c, h := zero, 1.0
		switch {
		case f.C == -2:
			c = noArea
		case f.C >= 0 && f.C < len(cols):
			v := f.V
			if o.Cap > 0 {
				v = math.Min(v, o.Cap)
			}
			c, h = cols[f.C], math.Max(1, v*o.Height)
		}
		for _, poly := range f.G.Polys {
			ring := make([]Pt, len(poly[0]))
			for i, q := range poly[0] {
				ring[i] = loc.Fwd(q)
			}
			mb.prism(ring, 0, h, c)
		}
	}
	if o.Base {
		lo, hi := loc.Fwd(Pt{bb.MinX, bb.MinY}), loc.Fwd(Pt{bb.MaxX, bb.MaxY})
		pad := math.Max(hi[0]-lo[0], hi[1]-lo[1]) * 0.03
		mb.prism([]Pt{{lo[0] - pad, lo[1] - pad}, {hi[0] + pad, lo[1] - pad}, {hi[0] + pad, hi[1] + pad}, {lo[0] - pad, hi[1] + pad}}, -pad/4, -0.05, hexLinear("#e6e9ee"))
	}
	if len(mb.idx) == 0 {
		return nil, fmt.Errorf("the current layer has no shapes to extrude (points only)")
	}
	return encodeGLB(&mb)
}

func encodeGLB(mb *meshBuf) ([]byte, error) {
	var bin bytes.Buffer
	write := func(v any) (int, int) {
		off := bin.Len()
		binary.Write(&bin, binary.LittleEndian, v)
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		return off, bin.Len() - off
	}
	nv := len(mb.pos) / 3
	po, pl := write(mb.pos)
	no, nl := write(mb.nrm)
	co, cl := write(mb.col)
	io, il := write(mb.idx)
	doc := map[string]any{
		"asset":  map[string]any{"version": "2.0", "generator": "Pracre " + Version},
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes":  []any{map[string]any{"mesh": 0, "name": "Value per acre"}},
		"meshes": []any{map[string]any{"name": "Value per acre", "primitives": []any{map[string]any{
			"attributes": map[string]int{"POSITION": 0, "NORMAL": 1, "COLOR_0": 2}, "indices": 3, "material": 0}}}},
		"materials": []any{map[string]any{"name": "Pracre", "pbrMetallicRoughness": map[string]any{
			"baseColorFactor": []float64{1, 1, 1, 1}, "metallicFactor": 0, "roughnessFactor": 0.75}}},
		"buffers": []any{map[string]any{"byteLength": bin.Len()}},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": po, "byteLength": pl, "target": 34962},
			map[string]any{"buffer": 0, "byteOffset": no, "byteLength": nl, "target": 34962},
			map[string]any{"buffer": 0, "byteOffset": co, "byteLength": cl, "target": 34962},
			map[string]any{"buffer": 0, "byteOffset": io, "byteLength": il, "target": 34963},
		},
		"accessors": []any{
			map[string]any{"bufferView": 0, "componentType": 5126, "count": nv, "type": "VEC3", "min": mb.min, "max": mb.max},
			map[string]any{"bufferView": 1, "componentType": 5126, "count": nv, "type": "VEC3"},
			map[string]any{"bufferView": 2, "componentType": 5126, "count": nv, "type": "VEC3"},
			map[string]any{"bufferView": 3, "componentType": 5125, "count": len(mb.idx), "type": "SCALAR"},
		},
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	var out bytes.Buffer
	total := 12 + 8 + len(js) + 8 + bin.Len()
	binary.Write(&out, binary.LittleEndian, []uint32{0x46546C67, 2, uint32(total), uint32(len(js)), 0x4E4F534A})
	out.Write(js)
	binary.Write(&out, binary.LittleEndian, []uint32{uint32(bin.Len()), 0x004E4942})
	out.Write(bin.Bytes())
	return out.Bytes(), nil
}
