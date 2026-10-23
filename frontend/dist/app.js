"use strict";
/* Pracre front end. Go does the data work (window.go.main.App and /api/*); this file draws it. */

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const api = () => window.go.main.App;
const rt = () => window.runtime;

// ---------------------------------------------------------------- constants

const RAMPS = {
  heat: { name: "Strong Towns heat", stops: ["#22355c", "#2e6f95", "#48a9a6", "#f2c14e", "#f26522", "#c0262d", "#6b0f2a"] },
  brand: { name: "Navy to orange", stops: ["#d7dee9", "#8fa3c2", "#3f5f8f", "#0f2444", "#8a4a2a", "#f26522", "#ffb27a"] },
  viridis: { name: "Viridis", stops: ["#440154", "#46327e", "#365c8d", "#277f8e", "#1fa187", "#4ac16d", "#a0da39", "#fde725"] },
  magma: { name: "Magma", stops: ["#1b0c41", "#4a0c6b", "#781c6d", "#a52c60", "#cf4446", "#ed6925", "#fb9b06", "#f7d13d"] },
  bluered: { name: "Blue to red", stops: ["#2166ac", "#67a9cf", "#d1e5f0", "#fddbc7", "#ef8a62", "#b2182b"] },
  greens: { name: "Greens", stops: ["#edf8e9", "#c7e9c0", "#a1d99b", "#74c476", "#41ab5d", "#238b45", "#005a32"] },
};
const ZERO = "#b8bfca"; // exempt or zero value
const NOAREA = "#e3e6eb"; // no lot area
const BASEMAPS = {
  light: { name: "Light (Positron)", url: "https://tiles.openfreemap.org/styles/positron" },
  streets: { name: "Streets (Liberty)", url: "https://tiles.openfreemap.org/styles/liberty" },
  bright: { name: "Bright", url: "https://tiles.openfreemap.org/styles/bright" },
  dark: { name: "Dark", url: "https://tiles.openfreemap.org/styles/dark" },
  osm: { name: "OpenStreetMap standard", url: "osm" },
  none: { name: "None (offline)", url: "none" },
};
const DEFAULTS = {
  metric: "auto", per: "acre", mode: "hex", size: 200, basis: "net", gap: 0.08, classes: 7, method: "quantile",
  ramp: "heat", opacity: 0.88, height: 100, cap: true, basemap: "light", title: "", view: "3d", currency: "$",
};
const ROLE_GROUPS = [
  ["", [["", "Not used"]]],
  ["Location", [["lat", "Latitude"], ["lon", "Longitude"], ["x", "X / Easting"], ["y", "Y / Northing"], ["latlon", "Lat, Lon in one column"], ["gridref", "Grid reference (MGRS, USNG, UTM, Plus code)"], ["geom", "Shape (WKT, WKB or GeoJSON)"]]],
  ["Linking", [["key", "Parcel ID"]]],
  ["Values", [["total", "Total value"], ["land", "Land value"], ["impr", "Improvement value"], ["classvalue", "Value for one tax class"], ["tax", "Tax amount"], ["class", "Tax class or code"], ["year", "Roll year (latest kept)"]]],
  ["Lot", [["area", "Lot area"], ["areaunit", "Lot area unit"]]],
  ["Label", [["address", "Address"], ["house", "House number"], ["street", "Street name"]]],
  ["Tax rates table", [["ratecode", "Rate code"], ["rate", "Rate"], ["ratedesc", "Rate description"]]],
];
const ROLE_KIND = { lat: "loc", lon: "loc", x: "loc", y: "loc", latlon: "loc", gridref: "loc", geom: "loc", key: "id", total: "val", land: "val", impr: "val",
  classvalue: "val", tax: "val", class: "cls", year: "cls", area: "lot", areaunit: "lot", address: "addr", house: "addr", street: "addr", ratecode: "rate", rate: "rate", ratedesc: "rate" };
const SINGULAR = new Set(Object.keys(ROLE_KIND).filter((r) => r !== "classvalue"));

// ---------------------------------------------------------------- state

let set = { ...DEFAULTS };
let S = null; // State from Go
let fc = null; // current layer FeatureCollection
let meta = null; // its pracre metadata
let sel = null; // selected feature id
let map = null;
let tableSort = "v";
let layerSeq = 0;
let hadData = false;

// ---------------------------------------------------------------- formatting

function money(v) {
  if (v == null || Number.isNaN(v)) return "–";
  const c = set.currency || "$";
  const a = Math.abs(v);
  if (a >= 1e9) return c + (v / 1e9).toFixed(2) + "B";
  if (a >= 1e6) return c + (v / 1e6).toFixed(a >= 1e8 ? 0 : a >= 1e7 ? 1 : 2) + "M";
  if (a >= 1e4) return c + Math.round(v / 1e3).toLocaleString("en-US") + "K";
  if (a >= 100) return c + Math.round(v).toLocaleString("en-US");
  return c + v.toFixed(2);
}
const int = (v) => Math.round(v).toLocaleString("en-US");
const areaFmt = (v) => (v >= 100 ? int(v) : v >= 10 ? v.toFixed(1) : v.toFixed(2));
const pct = (v) => (v * 100 < 10 ? (v * 100).toFixed(1) : Math.round(v * 100)) + "%";
const perWord = () => (set.per === "hectare" ? "hectare" : "acre");
const perShort = () => (set.per === "hectare" ? "ha" : "ac");
function metric() {
  if (!S) return "value";
  const st = S.stats;
  if (set.metric === "tax" && st.hasTax) return "tax";
  if (set.metric === "value" && st.hasValue) return "value";
  return st.hasTax ? "tax" : "value";
}
const metricLabel = () => (metric() === "tax" ? "Tax revenue per " : "Value per ") + perWord();
const totalLabel = () => (metric() === "tax" ? "Tax" : "Value");
function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
}
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "html") el.innerHTML = v;
    else if (v !== false && v != null) el.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k.nodeType ? k : document.createTextNode(String(k)));
  return el;
}

function hexToRgb(x) {
  const n = parseInt(x.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
function rampColors(key, n) {
  const st = (RAMPS[key] || RAMPS.heat).stops.map(hexToRgb);
  const out = [];
  for (let i = 0; i < n; i++) {
    const t = n === 1 ? 1 : i / (n - 1);
    const p = t * (st.length - 1);
    const j = Math.min(Math.floor(p), st.length - 2);
    const f = p - j;
    const c = st[j].map((v, k) => Math.round(v + (st[j + 1][k] - v) * f));
    out.push("#" + c.map((v) => v.toString(16).padStart(2, "0")).join(""));
  }
  return out;
}
const classCount = () => (meta && meta.breaks ? Math.max(meta.breaks.length - 1, 1) : set.classes);
const colors = () => rampColors(set.ramp, classCount());

// ---------------------------------------------------------------- status

let busyCount = 0;
let busyMsg = "";
function busy(on, msg) {
  busyCount = Math.max(0, busyCount + (on ? 1 : -1));
  $("#stBusy").classList.toggle("hidden", busyCount === 0);
  document.body.style.cursor = busyCount ? "progress" : "";
  if (msg) status((busyMsg = msg));
  else if (!busyCount && $("#stMsg").textContent === busyMsg) status("Ready"); // keep a result such as "Opened …" set meanwhile
}
function status(msg) {
  $("#stMsg").textContent = msg;
}
async function guard(fn, msg) {
  try {
    if (msg) busy(true, msg);
    return await fn();
  } catch (e) {
    alertBox("Something went wrong", String(e && e.message ? e.message : e));
  } finally {
    if (msg) busy(false);
  }
}

// ---------------------------------------------------------------- map

function styleFor(key) {
  const b = BASEMAPS[key] || BASEMAPS.light;
  if (b.url === "osm") {
    return {
      version: 8,
      sources: { osm: { type: "raster", tiles: [location.origin + "/osm/{z}/{x}/{y}.png"], tileSize: 256, maxzoom: 19, attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors' } },
      layers: [{ id: "osm", type: "raster", source: "osm", paint: { "raster-saturation": -0.55, "raster-opacity": 0.9 } }],
    };
  }
  if (b.url === "none") return { version: 8, sources: {}, layers: [{ id: "bg", type: "background", paint: { "background-color": "#e9ecf0" } }] };
  return b.url;
}

function initMap() {
  map = new maplibregl.Map({
    container: "map", style: styleFor(set.basemap), center: [-98, 44], zoom: 3, maxPitch: 78,
    attributionControl: { compact: true }, canvasContextAttributes: { preserveDrawingBuffer: true }, fadeDuration: 150,
  });
  map.addControl(new maplibregl.NavigationControl({ visualizePitch: true }), "top-right");
  map.addControl(new maplibregl.ScaleControl({ unit: "metric" }), "bottom-right");
  map.on("style.load", onStyleLoad);
  map.on("mousemove", (e) => {
    $("#stCursor").textContent = e.lngLat.lat.toFixed(5) + ", " + e.lngLat.lng.toFixed(5);
  });
  map.on("zoom", () => ($("#stZoom").textContent = "Zoom " + map.getZoom().toFixed(1)));
  map.on("error", (e) => console.warn("map", e && e.error && e.error.message));
  const layers = ["vpa-3d", "vpa-fill", "vpa-pts"];
  for (const id of layers) {
    map.on("mousemove", id, (e) => hover(e));
    map.on("mouseleave", id, () => {
      $("#tip").classList.add("hidden");
      map.getCanvas().style.cursor = "";
    });
  }
  map.on("click", (e) => {
    const f = map.queryRenderedFeatures(e.point, { layers: layers.filter((l) => map.getLayer(l)) })[0];
    if (f) select(f.id, false);
    else select(null);
  });
}

function onStyleLoad() {
  // The base map's own 3D buildings would compete with the value columns.
  for (const l of map.getStyle().layers) if (l.type === "fill-extrusion" && !l.id.startsWith("vpa")) map.removeLayer(l.id);
  const firstSymbol = map.getStyle().layers.find((l) => l.type === "symbol");
  const before = firstSymbol ? firstSymbol.id : undefined;
  map.addSource("vpa", { type: "geojson", data: fc || { type: "FeatureCollection", features: [] } });
  map.addLayer({ id: "vpa-fill", type: "fill", source: "vpa", filter: ["==", ["geometry-type"], "Polygon"], paint: { "fill-color": ZERO, "fill-opacity": 0.85 } }, before);
  map.addLayer({ id: "vpa-line", type: "line", source: "vpa", filter: ["==", ["geometry-type"], "Polygon"], paint: { "line-color": "rgba(15,36,68,0.28)", "line-width": ["interpolate", ["linear"], ["zoom"], 11, 0.2, 16, 0.8] } }, before);
  map.addLayer({ id: "vpa-3d", type: "fill-extrusion", source: "vpa", filter: ["==", ["geometry-type"], "Polygon"], paint: { "fill-extrusion-color": ZERO, "fill-extrusion-opacity": 0.95, "fill-extrusion-vertical-gradient": true } });
  map.addLayer({ id: "vpa-pts", type: "circle", source: "vpa", filter: ["==", ["geometry-type"], "Point"], paint: { "circle-radius": ["interpolate", ["linear"], ["zoom"], 10, 2, 16, 6], "circle-stroke-width": 0.5, "circle-stroke-color": "#0f2444" } });
  map.addLayer({ id: "vpa-sel-3d", type: "fill-extrusion", source: "vpa", filter: ["==", ["id"], -999], paint: { "fill-extrusion-color": "#ffd166", "fill-extrusion-opacity": 1 } });
  map.addLayer({ id: "vpa-sel", type: "line", source: "vpa", filter: ["==", ["id"], -999], paint: { "line-color": "#f26522", "line-width": 3 } });
  applyPaint();
  applyView(false);
  if (sel != null) select(sel, false, true);
}

function heightScale() {
  if (!meta || !S || !S.stats.bbox) return { H: 0, cap: 0 };
  const b = S.stats.bbox;
  const midLat = ((b[1] + b[3]) / 2) * (Math.PI / 180);
  const w = (b[2] - b[0]) * 111320 * Math.cos(midLat);
  const hgt = (b[3] - b[1]) * 110540;
  const diag = Math.max(Math.hypot(w, hgt), 500);
  const ref = set.cap ? meta.p99 : meta.max;
  const target = Math.max(150, diag * 0.07) * (set.height / 100);
  return { H: ref > 0 ? target / ref : 0, cap: set.cap && meta.p99 ? meta.p99 * 1.15 : 0 };
}

function applyPaint() {
  if (!map || !map.getLayer("vpa-fill")) return;
  const cols = colors();
  const color = ["match", ["get", "c"], -1, ZERO, -2, NOAREA, ...cols.flatMap((c, i) => [i, c]), cols[cols.length - 1]];
  const { H, cap } = heightScale();
  let v = ["max", ["coalesce", ["get", "v"], 0], 0];
  if (cap) v = ["min", v, cap];
  const height = ["*", v, H];
  map.setPaintProperty("vpa-fill", "fill-color", color);
  map.setPaintProperty("vpa-fill", "fill-opacity", set.opacity);
  map.setPaintProperty("vpa-3d", "fill-extrusion-color", color);
  map.setPaintProperty("vpa-3d", "fill-extrusion-height", height);
  map.setPaintProperty("vpa-3d", "fill-extrusion-opacity", Math.min(1, set.opacity + 0.08));
  map.setPaintProperty("vpa-sel-3d", "fill-extrusion-height", ["+", height, 0.5]);
  map.setPaintProperty("vpa-pts", "circle-color", color);
  map.setPaintProperty("vpa-pts", "circle-opacity", set.opacity);
}

function applyView(animate = true) {
  if (!map || !map.getLayer("vpa-fill")) return;
  const three = set.view === "3d";
  const vis = (id, on) => map.setLayoutProperty(id, "visibility", on ? "visible" : "none");
  vis("vpa-fill", !three);
  vis("vpa-line", !three);
  vis("vpa-3d", three);
  vis("vpa-sel-3d", three);
  syncToolbar();
  if (map.isMoving()) {
    // Tilting now would cut short a zoom to the data that is still flying; tilt once it lands.
    map.once("moveend", () => applyView(animate));
    return;
  }
  const want = three ? Math.max(map.getPitch(), 55) : 0;
  if (Math.abs(map.getPitch() - want) > 1) map.easeTo({ pitch: want, bearing: three && Math.abs(map.getBearing()) < 1 ? -17 : three ? map.getBearing() : 0, duration: animate ? 900 : 0 });
}

function hover(e) {
  const f = e.features && e.features[0];
  if (!f) return;
  map.getCanvas().style.cursor = "pointer";
  const p = f.properties;
  const tip = $("#tip");
  const v = p.v == null || p.v === "null" ? null : Number(p.v);
  const what = p.l ? esc(p.l) : p.n > 1 || set.mode !== "parcels" ? int(p.n) + " parcel" + (p.n == 1 ? "" : "s") : "Parcel";
  tip.innerHTML = "<strong>" + (v == null ? "No lot area" : money(v) + " per " + perWord()) + "</strong>" + what + "<br>" + totalLabel() + " " + money(Number(p.t)) + " on " + areaFmt(Number(p.a)) + " " + perShort();
  tip.classList.remove("hidden");
  const r = $("#center").getBoundingClientRect();
  tip.style.left = Math.min(e.point.x + r.left + 16, innerWidth - 240) + "px";
  tip.style.top = Math.min(e.point.y + r.top + 16, innerHeight - 70) + "px";
}

async function loadLayer() {
  const seq = ++layerSeq;
  if (!S || !S.stats.parcels || (!S.stats.hasValue && !S.stats.hasTax)) {
    fc = { type: "FeatureCollection", features: [] };
    meta = null;
    if (map && map.getSource("vpa")) map.getSource("vpa").setData(fc);
    renderLegend();
    renderTable([], 0);
    return;
  }
  busy(true, "Building the map…");
  try {
    const q = new URLSearchParams({ mode: set.mode, size: set.size, metric: metric(), per: set.per, basis: set.basis, classes: set.classes, method: set.method, gap: set.gap });
    const res = await fetch("/api/layer?" + q);
    const data = await res.json();
    if (seq !== layerSeq) return;
    fc = data;
    meta = data.pracre;
    if (map.getSource("vpa")) map.getSource("vpa").setData(fc);
    applyPaint();
    renderLegend();
    loadTable();
    if (sel != null) select(sel < fc.features.length ? sel : null, false, true);
    status(int(fc.features.length) + (set.mode === "parcels" ? " parcels" : " tiles") + " on the map");
  } finally {
    busy(false);
  }
}

function fit(animate = true) {
  if (!S || !S.stats.parcels) return;
  const b = S.stats.bbox;
  map.fitBounds([[b[0], b[1]], [b[2], b[3]]], { padding: { top: 60, bottom: 60, left: 60, right: 60 }, duration: animate ? 1000 : 0, pitch: set.view === "3d" ? 55 : 0, bearing: set.view === "3d" ? -17 : 0 });
}

// ---------------------------------------------------------------- selection

async function select(id, fly, quiet) {
  sel = id;
  const filt = ["==", ["id"], id == null ? -999 : id];
  if (map.getLayer("vpa-sel")) {
    map.setFilter("vpa-sel", filt);
    map.setFilter("vpa-sel-3d", ["all", ["==", ["geometry-type"], "Polygon"], filt]);
  }
  $$("#parcelGrid tbody tr").forEach((tr) => tr.classList.toggle("on", Number(tr.dataset.id) === id));
  const box = $("#selection");
  if (id == null) {
    box.innerHTML = '<p class="hint">Click a parcel or tile on the map, or a row in the table.</p>';
    return;
  }
  const d = await (await fetch("/api/detail?id=" + id)).json();
  if (!d || sel !== id) return;
  if (fly) flyTo(id);
  box.innerHTML = "";
  const v = d.v;
  const title = d.kind === "parcel" ? d.label || d.key || "Parcel" : "Tile with " + int(d.n) + " parcel" + (d.n === 1 ? "" : "s");
  box.append(h("div", { class: "muted" }, d.kind === "parcel" ? "Parcel" + (d.key && d.label !== d.key ? " " + d.key : "") : "Tile"));
  box.append(h("div", { style: "font-weight:600;font-size:13px;margin:1px 0 6px" }, title));
  box.append(h("div", { class: "big" }, v == null ? "No lot area" : money(v), v == null ? "" : h("small", {}, " per " + perWord())));
  if (d.rank != null) {
    box.append(h("div", { class: "bar" }, h("i", { style: "width:" + Math.round(d.rank * 100) + "%" })));
    const pctl = Math.min(99, Math.floor(d.rank * 100));
    box.append(h("div", { class: "muted" }, (pctl >= 99 ? "In the top 1% of " : "More productive than " + pctl + "% of ") + (set.mode === "parcels" ? "parcels" : "tiles")));
  }
  const kv = h("dl", { class: "kv" });
  const add = (k, val) => kv.append(h("dt", {}, k), h("dd", {}, val));
  if (d.kind === "parcel") {
    if (d.value != null) add("Value", money(d.value));
    if (d.tax != null) add("Tax", money(d.tax));
    add("Area", areaFmt(d.acres) + " acres");
  } else {
    add(totalLabel(), money(d.t));
    add("Area", areaFmt(d.a) + " " + perWord() + "s");
  }
  box.append(kv);
  if (d.top && d.top.length) {
    const t = h("table", { class: "grid" }, h("thead", {}, h("tr", {}, h("th", {}, "Top parcels"), h("th", { class: "num" }, totalLabel()))));
    const tb = h("tbody");
    for (const r of d.top) tb.append(h("tr", {}, h("td", { title: r.label }, r.label || "–"), h("td", { class: "num" }, money(r.t))));
    t.append(tb);
    box.append(t);
  }
  for (const g of d.groups || []) {
    const det = h("details", { class: "attrs" }, h("summary", {}, g.name + (g.how === "parcel" ? "" : " (linked by " + g.how + ")")));
    const t = h("table");
    for (const [k, val] of g.rows) t.append(h("tr", {}, h("td", {}, k), h("td", {}, val)));
    det.append(t);
    box.append(det);
  }
  if (!quiet) status(title);
}

function flyTo(id) {
  const f = fc && fc.features[id];
  if (!f) return;
  let x0 = 180, y0 = 90, x1 = -180, y1 = -90;
  const walk = (c) => {
    if (typeof c[0] === "number") {
      x0 = Math.min(x0, c[0]); x1 = Math.max(x1, c[0]); y0 = Math.min(y0, c[1]); y1 = Math.max(y1, c[1]);
    } else c.forEach(walk);
  };
  walk(f.geometry.coordinates);
  map.fitBounds([[x0, y0], [x1, y1]], { padding: 160, maxZoom: 17, duration: 900, pitch: map.getPitch(), bearing: map.getBearing() });
}

// ---------------------------------------------------------------- panels

async function refresh(fitAfter) {
  S = await api().State();
  renderChecklist();
  renderDatasets();
  renderRates();
  renderStats();
  syncControls();
  const has = S.stats.parcels > 0;
  $("#empty").classList.toggle("hidden", S.datasets.length > 0);
  $("#app").classList.toggle("nobottom", S.datasets.length === 0);
  if (map) map.resize();
  await loadLayer();
  if (has && (fitAfter || !hadData || offscreen())) fit(hadData);
  hadData = has;
}

// offscreen is true when the data's centre is outside the view, as after picking another coordinate system.
function offscreen() {
  const b = S.stats.bbox;
  const v = map.getBounds && map.getBounds();
  return !!v && !v.contains([(b[0] + b[2]) / 2, (b[1] + b[3]) / 2]);
}

function renderChecklist() {
  const ol = $("#checklist");
  ol.innerHTML = "";
  let done = 0;
  S.checks.forEach((c, i) => {
    if (c.status === "done") done++;
    const mark = { done: "✓", todo: String(i + 1), warn: "!", optional: "–" }[c.status] || "";
    const li = h("li", { class: c.status }, h("span", { class: "ck " + c.status }, mark), h("b", {}, c.title), h("p", {}, c.detail));
    if (c.action && !(c.status === "done" && c.action === "import")) li.append(h("button", { class: "btn", onclick: () => run(c.action) }, c.label || "Open"));
    ol.append(li);
  });
  $("#checkCount").textContent = done + " / " + S.checks.length;
}

function renderDatasets() {
  const ul = $("#datasets");
  ul.innerHTML = "";
  const label = { parcels: "LOTS", points: "PTS", values: "VAL", table: "TAB" };
  for (const d of S.datasets) {
    const what = d.kind === "parcels" ? int(d.polys) + " shapes" : d.kind === "points" ? int(d.located) + " points" : int(d.rows) + " rows";
    ul.append(h("li", { title: d.path, onclick: () => openDataset(d.id) },
      h("span", { class: "kind " + d.kind }, label[d.kind] || "TAB"),
      h("div", {}, h("b", {}, d.name), h("small", {}, what + (d.primary ? " · map layer" : "") + (d.locErr ? " · check location" : ""))),
      h("button", { class: "x", title: "Remove", onclick: (e) => { e.stopPropagation(); removeDataset(d); } }, "×")));
  }
}

async function removeDataset(d) {
  const ok = await confirmBox("Remove data", "Remove " + d.name + " from the project? The file on disk is not touched.", "Remove");
  if (!ok) return;
  await api().Remove(d.id);
  await refresh();
}

function renderStats() {
  const st = S.stats;
  const ins = $("#insight");
  const m = metric();
  const share = m === "tax" ? st.halfLandTax : st.halfLandValue;
  if (!st.parcels || (!st.hasValue && !st.hasTax)) {
    ins.classList.add("hidden");
  } else {
    ins.classList.remove("hidden");
    ins.innerHTML = "";
    ins.append(h("div", { class: "ihead" }, set.title || metricLabel()));
    const body = h("div", { class: "ibody" });
    if (share > 0) body.append(h("div", { class: "ibig", html: "Half of all " + (m === "tax" ? "tax revenue" : "property value") + " comes from <b>" + pct(share) + "</b> of the land." }));
    const total = m === "tax" ? st.totalTax : st.totalValue;
    body.append(h("div", { class: "imeta" }, (m === "tax" ? "Tax " : "Value ") + money(total) + " · " + (st.totalAcres ? int(st.totalAcres) + " acres · " : "") + int(st.parcels) + " parcels"));
    ins.append(body);
  }
  $("#stParcels").textContent = st.parcels ? int(st.parcels) + " parcels" : "";
  $("#stTotal").textContent = st.hasTax ? "Tax " + money(st.totalTax) : st.hasValue ? "Value " + money(st.totalValue) : "";
  $("#stAcres").textContent = st.totalAcres ? int(st.totalAcres) + " acres" : "";
}

function renderLegend() {
  const lg = $("#legend");
  if (!meta || !fc.features.length) {
    lg.classList.add("hidden");
    return;
  }
  lg.classList.remove("hidden");
  const cols = colors();
  const b = meta.breaks || []; // points without lot areas have no classes, only the grey rows below
  lg.innerHTML = "";
  lg.append(h("b", {}, metricLabel()));
  for (let i = b.length - 2; i >= 0; i--) {
    const label = i === b.length - 2 ? money(b[i]) + " and up" : money(b[i]) + " – " + money(b[i + 1]);
    lg.append(h("div", { class: "lrow" }, h("span", { class: "sw", style: "background:" + cols[i] }), h("span", {}, label), h("span", { class: "cnt" }, int(meta.counts[i] || 0))));
  }
  let zero = 0, noArea = 0;
  for (const f of fc.features) {
    if (f.properties.c === -1) zero++;
    else if (f.properties.c === -2) noArea++;
  }
  if (zero) lg.append(h("div", { class: "lrow" }, h("span", { class: "sw", style: "background:" + ZERO }), h("span", {}, "Exempt or $0"), h("span", { class: "cnt" }, int(zero))));
  if (noArea) lg.append(h("div", { class: "lrow" }, h("span", { class: "sw", style: "background:" + NOAREA }), h("span", {}, "No lot area"), h("span", { class: "cnt" }, int(noArea))));
  const note = set.mode === "parcels" ? "Each parcel" : (set.mode === "hex" ? "Hexagons " : "Squares ") + sizeText();
  lg.append(h("div", { class: "lnote" }, note + (set.view === "3d" ? " · height shows value" : "")));
}

function legendItems() {
  const cols = colors();
  const b = (meta && meta.breaks) || []; // none when no feature has a lot area
  const out = [];
  for (let i = 0; i < b.length - 1; i++) out.push({ color: cols[i], label: i === b.length - 2 ? money(b[i]) + " and up" : money(b[i]) + " – " + money(b[i + 1]) });
  return out;
}

// rates grid
let ratesTimer = 0;
function renderRates() {
  const tb = $("#ratesGrid tbody");
  if (typingRates()) return; // don't fight the typist
  tb.innerHTML = "";
  const classes = new Map((S.classes || []).map((c) => [normKey(c.code), c]));
  S.rates.forEach((r, i) => {
    const c = classes.get(normKey(r.code));
    const unrated = r.rate == null && c && c.value > 0;
    const tr = h("tr", {},
      h("td", {}, h("input", { value: r.code === "*" ? "(default)" : r.code, disabled: r.code === "*", "data-i": i, "data-k": "code" })),
      h("td", {}, h("input", { value: r.desc || "", "data-i": i, "data-k": "desc", placeholder: "" })),
      h("td", { class: unrated ? "unrated" : "" }, h("input", { class: "num", value: r.rate == null ? "" : r.rate, "data-i": i, "data-k": "rate", placeholder: unrated ? "needed" : "" })),
      h("td", { class: "num muted" }, c ? money(c.value) : ""),
      h("td", {}, h("button", { class: "del", title: "Delete row", onclick: () => { S.rates.splice(i, 1); pushRates(true); } }, "×")));
    tb.append(tr);
  });
  if (!S.rates.length) tb.append(h("tr", {}, h("td", { colspan: 5, class: "muted" }, "No tax classes yet. Import values with a tax class column, or add a default rate.")));
  $("#selRateUnit").value = S.rateUnit || "mill";
  $("#levy").textContent = S.stats.hasTax ? "Total tax: " + money(S.stats.totalTax) : "";
}
// typingRates is true while a rates cell has the cursor (a clicked delete button doesn't count).
function typingRates() {
  const a = document.activeElement;
  return !!a && a.tagName === "INPUT" && !!a.closest("#ratesGrid");
}
function normKey(s) {
  return String(s || "").toUpperCase().replace(/[^A-Z0-9]/g, "").replace(/^0+(?=.)/, "");
}
function pushRates(now) {
  clearTimeout(ratesTimer);
  ratesTimer = setTimeout(async () => {
    const local = S.rates;
    const sent = JSON.stringify(local);
    const res = await api().SetRates(local, $("#selRateUnit").value);
    S = res;
    if (JSON.stringify(local) !== sent) {
      // Edited while the save was on its way: keep those edits and save them too.
      S.rates = local;
      pushRates(false);
    }
    renderChecklist();
    renderStats();
    syncControls();
    if (!typingRates()) renderRates();
    else $("#levy").textContent = S.stats.hasTax ? "Total tax: " + money(S.stats.totalTax) : "";
    await loadLayer();
  }, now ? 0 : 500);
}

// parcels table
let tableTimer = 0;
async function loadTable() {
  clearTimeout(tableTimer);
  tableTimer = setTimeout(async () => {
    const q = new URLSearchParams({ q: $("#tableSearch").value, sort: tableSort, limit: 400 });
    const d = await (await fetch("/api/table?" + q)).json();
    renderTable(d.rows, d.total);
  }, 120);
}
function renderTable(rows, total) {
  const tile = set.mode !== "parcels";
  $("#tableTitle").textContent = tile ? "Tiles" : "Parcels";
  $("#parcelGrid th[data-sort=label]").textContent = tile ? "Tile" : "Parcel";
  $("#thTotal").textContent = totalLabel();
  $("#thPer").textContent = "Per " + perWord();
  $("#parcelGrid th[data-sort=a]").textContent = set.per === "hectare" ? "Hectares" : "Acres";
  $$("#parcelGrid th[data-sort]").forEach((th) => th.classList.toggle("sorted", th.dataset.sort === tableSort));
  const tb = $("#parcelGrid tbody");
  tb.innerHTML = "";
  const cols = colors();
  for (const r of rows) {
    const f = fc && fc.features[r.id];
    const c = f ? f.properties.c : -1;
    const sw = h("span", { class: "swatch", style: "background:" + (c >= 0 ? cols[Math.min(c, cols.length - 1)] : c === -1 ? ZERO : NOAREA) });
    const tr = h("tr", { "data-id": r.id, class: r.id === sel ? "on" : "", onclick: () => select(r.id, true) },
      h("td", { title: r.label + (r.key && r.key !== r.label ? " · " + r.key : "") + (tile ? " · " + int(f ? f.properties.n : 0) + " parcels" : "") }, sw, r.label || r.key || "–"),
      h("td", { class: "num" }, areaFmt(r.acres)),
      h("td", { class: "num" }, money(r.t)),
      h("td", { class: "num" }, r.v == null ? "–" : money(r.v)));
    tb.append(tr);
  }
  $("#tableNote").textContent = total ? (rows.length < total ? "Showing " + int(rows.length) + " of " + int(total) + ". Search to find others." : int(total) + " rows") : "";
}

// ---------------------------------------------------------------- controls

function sizeToSlider(s) {
  return Math.round((Math.log(s / 25) / Math.log(80)) * 1000);
}
function sliderToSize(t) {
  const s = 25 * Math.pow(80, t / 1000);
  return s < 100 ? Math.round(s / 5) * 5 : s < 1000 ? Math.round(s / 10) * 10 : Math.round(s / 50) * 50;
}
function sizeText() {
  const w = set.size;
  const m2 = set.mode === "square" ? w * w : (Math.sqrt(3) / 2) * w * w;
  const a = m2 / (set.per === "hectare" ? 10000 : 4046.8564224);
  return (w >= 1000 ? (w / 1000).toFixed(w % 1000 ? 2 : 0) + " km" : w + " m") + " (" + areaFmt(a) + " " + perShort() + ")";
}

function syncControls() {
  const st = S ? S.stats : {};
  $$("#segMetric button").forEach((b) => {
    b.classList.toggle("on", b.dataset.v === metric());
    b.disabled = b.dataset.v === "tax" ? !st.hasTax : !st.hasValue;
    if (b.dataset.v === "tax") b.title = st.hasTax ? "" : "Add tax rates or a tax amount column to map tax revenue";
  });
  $$("#segMode button").forEach((b) => b.classList.toggle("on", b.dataset.v === set.mode));
  $("#selPer").value = set.per;
  $("#rngSize").value = sizeToSlider(set.size);
  $("#outSize").textContent = sizeText();
  $$(".tileonly").forEach((el) => (el.style.display = set.mode === "parcels" ? "none" : ""));
  $("#selBasis").value = set.basis;
  $("#rngGap").value = Math.round(set.gap * 100);
  $("#outGap").textContent = Math.round(set.gap * 100) + "%";
  $("#selClasses").value = set.classes;
  $("#selMethod").value = set.method;
  $("#rngOpacity").value = Math.round(set.opacity * 100);
  $("#outOpacity").textContent = Math.round(set.opacity * 100) + "%";
  $("#rngHeight").value = set.height;
  $("#outHeight").textContent = set.height + "%";
  $("#chkCap").checked = set.cap;
  $("#selBase").value = set.basemap;
  if (document.activeElement !== $("#txtTitle")) $("#txtTitle").value = set.title;
  $$("#ramps .ramp").forEach((r) => r.classList.toggle("on", r.dataset.k === set.ramp));
  syncToolbar();
}

function syncToolbar() {
  $$(".tb[data-toggle=view]").forEach((b) => b.classList.toggle("on", b.dataset.act === "view" + set.view));
  $$(".tb[data-toggle=mode]").forEach((b) => b.classList.toggle("on", b.dataset.act === set.mode));
}

let settingsTimer = 0;
function saveSettings() {
  clearTimeout(settingsTimer);
  settingsTimer = setTimeout(() => api().SaveSettings(JSON.stringify(currentSettings())), 400);
}
function currentSettings() {
  const c = map.getCenter();
  return { ...set, camera: { center: [c.lng, c.lat], zoom: map.getZoom(), pitch: map.getPitch(), bearing: map.getBearing() } };
}

// change(key, value, needsLayer): update a setting and redraw what depends on it
function change(patch, data) {
  const baseChanged = patch.basemap && patch.basemap !== set.basemap;
  Object.assign(set, patch);
  syncControls();
  saveSettings();
  if (baseChanged) map.setStyle(styleFor(set.basemap));
  if (data) {
    loadLayer();
    renderStats(); // the insight card names the measure and its total
  } else {
    applyPaint();
    renderLegend();
    renderStats();
    if (patch.view) applyView();
    if ("ramp" in patch) loadTable();
  }
}

function initControls() {
  // ramps
  const rp = $("#ramps");
  for (const [k, r] of Object.entries(RAMPS)) {
    const el = h("div", { class: "ramp", "data-k": k, title: r.name, onclick: () => change({ ramp: k }) });
    for (const c of rampColors(k, 9)) el.append(h("i", { style: "background:" + c }));
    rp.append(el);
  }
  const cl = $("#selClasses");
  for (let i = 3; i <= 12; i++) cl.append(h("option", { value: i }, i));
  const bs = $("#selBase");
  for (const [k, b] of Object.entries(BASEMAPS)) bs.append(h("option", { value: k }, b.name));

  $$("#segMetric button").forEach((b) => b.addEventListener("click", () => change({ metric: b.dataset.v }, true)));
  $$("#segMode button").forEach((b) => b.addEventListener("click", () => change({ mode: b.dataset.v }, true)));
  $("#selPer").onchange = (e) => change({ per: e.target.value }, true);
  $("#rngSize").oninput = (e) => {
    set.size = sliderToSize(Number(e.target.value));
    $("#outSize").textContent = sizeText();
  };
  $("#rngSize").onchange = () => change({ size: set.size }, true);
  $("#selBasis").onchange = (e) => change({ basis: e.target.value }, true);
  $("#rngGap").oninput = (e) => ($("#outGap").textContent = e.target.value + "%");
  $("#rngGap").onchange = (e) => change({ gap: Number(e.target.value) / 100 }, true);
  $("#selClasses").onchange = (e) => change({ classes: Number(e.target.value) }, true);
  $("#selMethod").onchange = (e) => change({ method: e.target.value }, true);
  $("#rngOpacity").oninput = (e) => change({ opacity: Number(e.target.value) / 100 });
  $("#rngHeight").oninput = (e) => change({ height: Number(e.target.value) });
  $("#chkCap").onchange = (e) => change({ cap: e.target.checked });
  $("#selBase").onchange = (e) => change({ basemap: e.target.value });
  $("#txtTitle").oninput = (e) => change({ title: e.target.value });
  $("#selClear").onclick = () => select(null);

  $("#selRateUnit").onchange = () => {
    S.rateUnit = $("#selRateUnit").value; // a redraw before the save returns must not put back the old unit
    pushRates(true);
  };
  $("#ratesGrid").addEventListener("input", (e) => {
    const el = e.target;
    const i = Number(el.dataset.i);
    if (!el.dataset.k || !S.rates[i]) return;
    if (el.dataset.k === "rate") {
      const raw = el.value.includes(".") ? el.value : el.value.replace(",", "."); // "2,05" is a decimal comma
      const v = parseFloat(raw.replace(/[^0-9.\-]/g, ""));
      S.rates[i].rate = Number.isFinite(v) ? v : null;
    } else S.rates[i][el.dataset.k] = el.value;
    pushRates(false);
  });
  $("#ratesGrid").addEventListener("focusout", () => setTimeout(() => {
    if (!typingRates()) renderRates();
  }, 600));
  $("#btnAddRate").onclick = () => {
    S.rates.push({ code: "New class", desc: "", rate: null });
    renderRates();
    const inputs = $$("#ratesGrid input[data-k=code]");
    inputs[inputs.length - 1].select();
    pushRates(false);
  };
  $("#btnDefaultRate").onclick = () => {
    if (!S.rates.some((r) => r.code === "*")) S.rates.push({ code: "*", desc: "All other properties", rate: null });
    renderRates();
    pushRates(true);
  };
  $("#btnImportRates").onclick = () => guard(async () => {
    const n = await api().ImportRates();
    if (n) status("Loaded " + n + " tax rates");
    await refresh();
  });
  $("#tableSearch").oninput = () => loadTable();
  $$("#parcelGrid th[data-sort]").forEach((th) => th.addEventListener("click", () => {
    tableSort = th.dataset.sort;
    loadTable();
  }));

  // toolbar and any [data-act]
  document.addEventListener("click", (e) => {
    const el = e.target.closest("[data-act]");
    if (el && !el.closest(".popup")) run(el.dataset.act, el);
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      closePopup();
      const x = $("#modal .close");
      if (x) x.click();
    }
  });
}

// ---------------------------------------------------------------- actions

const ACTIONS = {
  new: async () => {
    if (!(await api().ConfirmDiscard())) return;
    await resetProject();
    map.jumpTo({ center: [-98, 44], zoom: 3, pitch: 0, bearing: 0 });
    await refresh();
    status("New project");
  },
  open: () => guard(async () => {
    if (!(await api().ConfirmDiscard())) return;
    busy(true, "Opening project…");
    try {
      await api().OpenProject("");
      await afterOpen();
    } finally {
      busy(false);
    }
  }),
  save: () => saveProject(false),
  saveas: () => saveProject(true),
  import: () => guard(async () => {
    const paths = await api().ChooseFiles();
    if (paths && paths.length) await importPaths(paths);
  }),
  sample: () => loadSample("langley", "Township of Langley"),
  "sample-porthuron": () => loadSample("port-huron", "Port Huron"),
  rates: () => {
    $("#app").classList.remove("nobottom");
    const first = $("#ratesGrid td.unrated input") || $("#ratesGrid input");
    if (first) first.focus();
    $("#ratesPanel").animate([{ outline: "3px solid #f26522" }, { outline: "3px solid transparent" }], 900);
  },
  checklist: () => $("#checkPanel").animate([{ outline: "3px solid #f26522" }, { outline: "3px solid transparent" }], 900),
  basemap: (el) => popup(el || $("[data-act=basemap]"), Object.entries(BASEMAPS).map(([k, b]) => ({ label: b.name, mark: k === set.basemap ? "●" : "", act: () => change({ basemap: k }) }))),
  view2d: () => change({ view: "2d" }),
  view3d: () => change({ view: "3d" }),
  parcels: () => change({ mode: "parcels" }, true),
  hex: () => change({ mode: "hex" }, true),
  square: () => change({ mode: "square" }, true),
  fit: () => fit(),
  "export-image": exportImage,
  export3d: (el) => popup(el, [
    { label: "3D web map (HTML)", sub: "A page to upload or embed in your site", act: () => exportWeb(true), tall: true },
    { label: "3D model (.glb)", sub: "For Blender, PowerPoint or 3D viewers", act: exportGLB, tall: true },
    { label: "3D image (PNG)", sub: "The current view as a picture", act: () => { change({ view: "3d" }); setTimeout(exportImage, 1000); }, tall: true },
  ]),
  export2d: (el) => popup(el, [
    { label: "2D web map (HTML)", sub: "A page to upload or embed in your site", act: () => exportWeb(false), tall: true },
    { label: "GeoJSON", sub: "For QGIS, ArcGIS or web maps", act: () => saveVia(api().ExportGeoJSON), tall: true },
    { label: "CSV table", sub: "For Excel or Google Sheets", act: () => saveVia(api().ExportCSV), tall: true },
    { label: "2D image (PNG)", sub: "The current view as a picture", act: () => { change({ view: "2d" }); setTimeout(exportImage, 1000); }, tall: true },
  ]),
  "export-web3d": () => exportWeb(true),
  "export-web2d": () => exportWeb(false),
  "export-glb": exportGLB,
  "export-geojson": () => saveVia(api().ExportGeoJSON),
  "export-csv": () => saveVia(api().ExportCSV),
  reload: afterOpen,
  help: showHelp,
  about: showAbout,
};

function run(act, el) {
  closePopup();
  if (act.startsWith("dataset:")) return openDataset(act.slice(8));
  const f = ACTIONS[act];
  if (f) f(el);
}

async function afterOpen() {
  S = await api().State();
  let s = {};
  try {
    // Go sends the saved settings as a JSON object; older builds may have sent the text.
    s = typeof S.settings === "string" ? JSON.parse(S.settings) : { ...(S.settings || {}) };
  } catch (e) {}
  const cam = s.camera;
  delete s.camera;
  const oldBase = set.basemap;
  set = { ...DEFAULTS, ...s };
  sel = null;
  if (set.basemap !== oldBase) map.setStyle(styleFor(set.basemap));
  hadData = true;
  await refresh(!cam);
  if (cam) map.jumpTo(cam);
  applyView(false);
  status("Opened " + (S.project || "project"));
}

async function resetProject() {
  await api().NewProject();
  const oldBase = set.basemap;
  set = { ...DEFAULTS };
  sel = null;
  hadData = false;
  if (set.basemap !== oldBase) map.setStyle(styleFor(set.basemap));
}

// A sample opens like a project, in place of whatever is loaded.
async function loadSample(name, place) {
  if (!(await api().ConfirmDiscard())) return;
  return guard(async () => {
    await resetProject();
    const res = await api().LoadSample(name);
    if (res.errors && res.errors.length) alertBox("Sample data", res.errors.join("\n"));
    if (!set.title) set.title = place + ", value per acre";
    await refresh(true);
  }, "Loading sample data…");
}

async function saveProject(as) {
  await guard(async () => {
    await api().SaveSettings(JSON.stringify(currentSettings()));
    const p = await api().SaveProject(as);
    if (p) status("Saved " + p);
  });
}

async function saveVia(fn) {
  await guard(async () => {
    const p = await fn();
    if (p) status("Saved " + p);
  });
}

async function importPaths(paths) {
  const proj = paths.find((p) => p.toLowerCase().endsWith(".pracre"));
  if (proj) {
    if (!(await api().ConfirmDiscard())) return;
    await guard(async () => {
      await api().OpenProject(proj);
      await afterOpen();
    }, "Opening project…");
    return;
  }
  // The map centre hints where unlabelled coordinates belong, once someone has zoomed to their town.
  const c = map.getZoom() >= 5 ? map.getCenter() : { lng: 0, lat: 0 };
  busy(true, "Reading " + paths.map((p) => p.split(/[\\/]/).pop()).join(", ") + "…");
  let res;
  try {
    res = await api().Import(paths, c.lng, c.lat);
  } finally {
    busy(false);
  }
  if (res.errors && res.errors.length) await alertBox("Some files couldn't be read", res.errors.join("\n\n"));
  if (res.rates) status("Loaded " + res.rates + " tax rates");
  await refresh();
  for (const id of res.added || []) await openDataset(id, true);
}

// ---------------------------------------------------------------- popups and dialogs

function popup(anchor, items) {
  const p = $("#menuPop");
  p.innerHTML = "";
  for (const it of items) {
    if (it === "hr") {
      p.append(h("hr"));
      continue;
    }
    p.append(h("button", { class: it.tall ? "tall" : "", onclick: () => { closePopup(); it.act(); } }, h("span", { class: "mk" }, it.mark || ""), h("span", {}, it.label), it.sub ? h("small", {}, it.sub) : null));
  }
  const r = anchor.getBoundingClientRect();
  p.style.left = Math.min(r.left, innerWidth - 280) + "px";
  p.style.top = r.bottom + 1 + "px";
  p.classList.remove("hidden");
  setTimeout(() => document.addEventListener("mousedown", outside), 0);
}
function outside(e) {
  if (!e.target.closest("#menuPop")) closePopup();
}
function closePopup() {
  $("#menuPop").classList.add("hidden");
  document.removeEventListener("mousedown", outside);
}

// dialog() shows a modal window and resolves with whatever close(value) is given.
function dialog({ title, icon, cls = "", body, foot }) {
  const modal = $("#modal");
  let resolve;
  const done = new Promise((r) => (resolve = r));
  const close = (v) => {
    modal.classList.add("hidden");
    modal.innerHTML = "";
    resolve(v);
  };
  const dlg = h("div", { class: "dlg " + cls },
    h("h3", {}, icon ? h("img", { src: "img/icons/" + icon + ".png", alt: "" }) : null, h("span", {}, title), h("button", { class: "close", title: "Close", onclick: () => close(undefined) }, "✕")),
    h("div", { class: "body" }, body), foot ? h("div", { class: "foot" }, foot) : null);
  modal.innerHTML = "";
  modal.append(dlg);
  modal.classList.remove("hidden");
  return { dlg, close, done };
}

function alertBox(title, msg) {
  const ok = h("button", { class: "btn primary" }, "OK");
  const d = dialog({ title, cls: "small-dlg", body: h("p", { style: "white-space:pre-wrap;user-select:text" }, msg), foot: [h("span", { class: "spacer" }), ok] });
  ok.onclick = () => d.close();
  ok.focus();
  return d.done;
}

function confirmBox(title, msg, yes = "OK") {
  const ok = h("button", { class: "btn primary" }, yes);
  const no = h("button", { class: "btn" }, "Cancel");
  const d = dialog({ title, cls: "small-dlg", body: h("p", {}, msg), foot: [h("span", { class: "spacer" }), ok, no] });
  ok.onclick = () => d.close(true);
  no.onclick = () => d.close(false);
  ok.focus();
  return d.done;
}

// ---------------------------------------------------------------- import dialog

async function openDataset(id, fresh) {
  let d = await api().Dataset(id);
  const name = d.name;
  const crsList = await api().CommonCRS();
  const orig = { roles: JSON.parse(JSON.stringify(d.roles)), crs: d.crsText, join: { ...d.join } };
  let roles = JSON.parse(JSON.stringify(d.roles));
  let mini = null;
  const miniBox = h("div", { class: "mini" }); // kept across redraws so the small map isn't rebuilt each time

  const summary = h("div", { class: "summary" });
  const gridWrap = h("div", { class: "gridwrap" });
  const side = h("div", { class: "side" });
  const body = h("div", { style: "display:contents" }, summary, h("div", { class: "cols" }, gridWrap), side);
  const done = h("button", { class: "btn primary" }, "Done");
  const cancel = h("button", { class: "btn" }, fresh ? "Don't Import" : "Cancel");
  const auto = h("button", { class: "btn", title: "Run detection again" }, "Auto-detect");
  const remove = fresh ? null : h("button", { class: "btn danger" }, "Remove");
  const D = dialog({ title: (fresh ? "Import · " : "") + d.name, icon: "import", cls: "imp", body, foot: [auto, remove, h("span", { class: "spacer" }), h("span", { class: "muted" }, "Changes apply as you make them."), done, cancel] });

  async function apply() {
    busy(true, "Applying…");
    try {
      d = await api().Configure(id, roles, d.crsText || "", d.join);
      S = await api().State();
    } catch (e) {
      alertBox("Couldn't apply that", String(e));
    } finally {
      busy(false);
    }
    if (d.kind === "rates") {
      // Marked as a rates table: its rows are now in the Tax Rates table and the dataset is gone.
      D.close("rates");
      status("Read " + name + " as a tax rates table");
      return;
    }
    draw();
  }

  function roleSelect(i) {
    const s = h("select", {});
    for (const [g, opts] of ROLE_GROUPS) {
      const parent = g ? h("optgroup", { label: g }) : s;
      for (const [v, l] of opts) parent.append(h("option", { value: v }, l));
      if (g) s.append(parent);
    }
    s.value = roles[i].role || "";
    s.onchange = () => {
      const r = s.value;
      if (SINGULAR.has(r)) roles.forEach((x, j) => { if (j !== i && x.role === r) roles[j] = { role: "", score: 0 }; });
      roles[i] = { role: r, score: 20, unit: r === "area" ? "auto" : "", class: r === "classvalue" ? d.cols[i].replace(/[_ ](land|buildings?|improvements?|impr|bldg|imp)$/i, "") : "" };
      apply();
    };
    return s;
  }

  function draw() {
    // summary chips
    const kinds = new Set(roles.map((r) => ROLE_KIND[r.role]).filter(Boolean));
    const valueCols = roles.filter((r) => ["total", "land", "impr", "tax", "classvalue"].includes(r.role));
    const chip = (state, text) => h("span", { class: "chip " + state }, h("i"), text);
    summary.innerHTML = "";
    summary.append(h("b", {}, int(d.rows) + " rows"), h("span", { class: "muted" }, "· " + (d.format || "table").toUpperCase() + " ·"));
    summary.append(d.located ? chip(d.locErr || !d.crsSure ? "warn" : "ok", (d.polys ? int(d.polys) + " parcel shapes" : int(d.located) + " points") + " located")
      : d.hasLoc ? chip("warn", "Locations need checking") : chip("no", "No locations"));
    const cvc = valueCols.filter((r) => r.role === "classvalue").length;
    const valText = valueCols.length ? "Values: " + [...new Set(valueCols.map((r) => (r.role === "classvalue" ? cvc + " class columns" : { total: "total", land: "land", impr: "improvements", tax: "tax amount" }[r.role])))].join(", ") : "No value columns";
    summary.append(chip(valueCols.length ? "ok" : "no", valText));
    const key = roles.findIndex((r) => r.role === "key");
    summary.append(chip(key >= 0 ? "ok" : "no", key >= 0 ? "ID: " + d.cols[key] : "No ID column"));
    const cls = roles.findIndex((r) => r.role === "class");
    if (cls >= 0) summary.append(chip("ok", "Tax class: " + d.cols[cls]));
    if (kinds.has("rate")) summary.append(chip("ok", "Tax rates table"));

    // grid
    const t = h("table", { class: "grid" });
    const hr = h("tr");
    d.cols.forEach((c, i) => {
      const r = roles[i];
      const th = h("th", { class: r.role ? "r-" + ROLE_KIND[r.role] : "" }, h("span", { class: "cname", title: c }, c), roleSelect(i));
      if (r.role === "classvalue") {
        const inp = h("input", { value: r.class || "", title: "Tax class this column holds" });
        inp.onchange = () => { roles[i].class = inp.value; apply(); };
        th.append(inp);
      }
      if (r.role === "area") {
        const u = h("select", { title: "Unit" });
        for (const [v, l] of [["auto", "Unit: automatic"], ["acre", "Acres"], ["ha", "Hectares"], ["sqft", "Square feet"], ["m2", "Square metres"], ["crs", "Map units squared"]]) u.append(h("option", { value: v }, l));
        u.value = r.unit || "auto";
        u.onchange = () => { roles[i].unit = u.value; apply(); };
        th.append(u);
      }
      hr.append(th);
    });
    const tb = h("tbody");
    for (const row of d.preview || []) tb.append(h("tr", {}, d.cols.map((_, i) => h("td", { title: row[i] || "" }, row[i] || ""))));
    t.append(h("thead", {}, hr), tb);
    const sx = gridWrap.scrollLeft;
    gridWrap.innerHTML = "";
    gridWrap.append(t);
    gridWrap.scrollLeft = sx;

    // side: location
    side.innerHTML = "";
    if (d.hasLoc) {
      const fs = h("fieldset", {}, h("legend", {}, "Where is it?"));
      fs.append(miniBox);
      const sel = h("select", {});
      sel.append(h("option", { value: "" }, "Automatic: " + (d.crs.name || "unknown")));
      const g1 = h("optgroup", { label: "Suggested" });
      for (const g of d.guesses || []) g1.append(h("option", { value: g.crs.id + (g.crs.swap ? "#swap" : "") }, g.crs.name + " (" + g.reason + ")"));
      if ((d.guesses || []).length) sel.append(g1);
      const g2 = h("optgroup", { label: "Common systems" });
      for (const c of crsList) g2.append(h("option", { value: c.id }, c.name + "  [" + c.id + "]"));
      sel.append(g2);
      sel.append(h("option", { value: "custom" }, "Other: EPSG code or .prj text…"));
      const custom = h("textarea", { rows: 3, placeholder: "EPSG:2227  or paste PROJCS[...] from the layer's .prj / epsg.io" });
      const isCustom = d.crsText && ![...sel.options].some((o) => o.value === d.crsText);
      sel.value = isCustom ? "custom" : d.crsText || "";
      custom.value = isCustom ? d.crsText : "";
      custom.style.display = isCustom ? "" : "none";
      const applyCustom = h("button", { class: "btn", style: isCustom ? "margin-top:4px" : "display:none" }, "Use This");
      sel.onchange = () => {
        if (sel.value === "custom") {
          custom.style.display = "";
          applyCustom.style.display = "";
          applyCustom.style.marginTop = "4px";
          custom.focus();
          return;
        }
        d.crsText = sel.value;
        apply();
      };
      applyCustom.onclick = () => { d.crsText = custom.value.trim(); apply(); };
      fs.append(h("label", { class: "row", style: "display:block" }, "Coordinate system"), sel, custom, applyCustom);
      const near = h("button", { class: "btn", style: "margin-top:4px", title: "Move the small map to where the data belongs, then rank the systems that fit there" }, "Guess for This Area");
      near.onclick = async () => {
        if (!mini) return;
        const c = mini.getCenter();
        busy(true, "Finding systems that fit here…");
        try {
          d = await api().GuessNear(id, c.lng, c.lat);
          S = await api().State();
        } finally {
          busy(false);
        }
        draw();
      };
      let msg;
      if (d.locErr) msg = h("div", { class: "msg err" }, d.locErr);
      else if (!d.crsSure) msg = h("div", { class: "msg warn" }, "Pracre guessed this. Check that the orange dots land in the right town; if not, move the small map to your town and click Guess for This Area, or pick another system.");
      else msg = h("div", { class: "msg ok" }, "✓ " + int(d.located) + " of " + int(d.rows) + " rows located" + (d.fileCrs && !d.crsText ? " (system read from the file)" : ""));
      if (!d.crsSure && !d.fileCrs) fs.append(near);
      fs.append(msg);
      side.append(fs);
      requestAnimationFrame(drawMini);
    }
    // side: linking
    const primary = S.datasets.find((x) => x.primary);
    const hasVals = roles.some((r) => ["total", "land", "impr", "tax", "classvalue"].includes(r.role));
    if (primary && primary.id !== id && hasVals) {
      const fs = h("fieldset", {}, h("legend", {}, "Link to parcels in " + primary.name));
      const mode = h("select", {});
      for (const [v, l] of [["auto", "Automatic (ID, then location)"], ["key", "By ID column only"], ["spatial", "By location only"], ["none", "Don't link"]]) mode.append(h("option", { value: v }, l));
      mode.value = d.join.mode || "auto";
      const mine = h("select", {}, h("option", { value: "" }, "Automatic"), d.cols.map((c) => h("option", { value: c }, c)));
      mine.value = d.join.col || "";
      const theirs = h("select", {}, h("option", { value: "" }, "Automatic"), primary.cols.map((c) => h("option", { value: c }, c)));
      theirs.value = d.join.target || "";
      const upd = () => { d.join = { mode: mode.value, col: mine.value, target: theirs.value }; apply(); };
      mode.onchange = mine.onchange = theirs.onchange = upd;
      fs.append(h("label", { class: "row", style: "display:block" }, "Method"), mode,
        h("label", { class: "row", style: "display:block" }, "This file's ID column"), mine,
        h("label", { class: "row", style: "display:block" }, "matches the parcel column"), theirs);
      const ck = S.checks.find((c) => c.id === "link:" + id);
      if (ck) fs.append(h("div", { class: "msg " + (ck.status === "done" ? "ok" : ck.status === "warn" ? "warn" : "err") }, ck.detail));
      side.append(fs);
    }
    const key2 = h("fieldset", {}, h("legend", {}, "Column colours"));
    key2.append(h("div", { class: "legendkey", html:
      '<span><i style="background:#2e6f95"></i>Location</span><span><i style="background:#7b4fa0"></i>Parcel ID</span><span><i style="background:#2e8b57"></i>Values</span><span><i style="background:#c98a00"></i>Tax class</span><span><i style="background:#8a6d3b"></i>Lot area</span><span><i style="background:#5b6b82"></i>Address</span><span><i style="background:#f26522"></i>Rates</span>' }));
    key2.append(h("p", { class: "muted", style: "margin:6px 0 0" }, "Pracre guessed each column's meaning from its name and contents. Change any heading's role if it guessed wrong; columns marked Not used are ignored."));
    side.append(key2);
  }

  function drawMini() {
    if (!miniBox.isConnected) return;
    const url = () => "/api/locations?id=" + id + "&t=" + Date.now();
    const fit = () => d.bbox && mini.fitBounds([[d.bbox[0], d.bbox[1]], [d.bbox[2], d.bbox[3]]], { padding: 20, duration: 0, maxZoom: 15 });
    if (mini) {
      mini.resize();
      if (mini.getSource("pts")) {
        mini.getSource("pts").setData(url());
        fit();
      }
      return;
    }
    mini = new maplibregl.Map({ container: miniBox, style: styleFor(set.basemap === "none" ? "light" : set.basemap), attributionControl: false, center: [-98, 44], zoom: 2 });
    mini.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right"); // zoom out to find the right town
    mini.on("load", () => {
      mini.addSource("pts", { type: "geojson", data: url() });
      mini.addLayer({ id: "pts", type: "circle", source: "pts", paint: { "circle-radius": 2.5, "circle-color": "#f26522", "circle-stroke-color": "#fff", "circle-stroke-width": 0.5 } });
      fit();
    });
  }

  auto.onclick = async () => { roles = await api().DetectRoles(id); apply(); };
  if (remove) remove.onclick = async () => {
    D.close("removed");
    await api().Remove(id);
    await refresh();
  };
  done.onclick = () => D.close("done");
  cancel.onclick = () => D.close("cancel");
  draw();
  const how = await D.done;
  if (mini) mini.remove();
  if (how === "cancel" || (how === undefined && fresh)) {
    if (fresh) await api().Remove(id);
    else await api().Configure(id, orig.roles, orig.crs, orig.join);
  }
  if (how !== "removed") await refresh();
}

// ---------------------------------------------------------------- exports

function composeImage() {
  const src = map.getCanvas();
  const dpr = src.width / src.clientWidth;
  const c = document.createElement("canvas");
  c.width = src.width;
  c.height = src.height;
  const g = c.getContext("2d");
  g.drawImage(src, 0, 0);
  g.scale(dpr, dpr);
  const W = src.clientWidth, H = src.clientHeight;
  const font = (w, s) => (g.font = w + " " + s + "px " + getComputedStyle(document.body).fontFamily);
  // title card
  const title = set.title || metricLabel();
  const st = S.stats;
  const share = metric() === "tax" ? st.halfLandTax : st.halfLandValue;
  const sub = share > 0 ? "Half of all " + (metric() === "tax" ? "tax revenue" : "property value") + " comes from " + pct(share) + " of the land." : "";
  font("600", 18);
  const tw = Math.max(g.measureText(title).width, (font("400", 13), g.measureText(sub).width)) + 28;
  g.fillStyle = "#0f2444";
  g.fillRect(14, 14, tw, 34);
  g.fillStyle = "#f26522";
  g.fillRect(14, 48, tw, 3);
  font("600", 18);
  g.fillStyle = "#fff";
  g.fillText(title, 28, 37);
  if (sub) {
    g.fillStyle = "rgba(255,255,255,0.95)";
    g.fillRect(14, 51, tw, 26);
    font("400", 13);
    g.fillStyle = "#13213a";
    g.fillText(sub, 28, 69);
  }
  // legend
  if (meta && meta.breaks) {
    const items = legendItems().reverse();
    font("400", 12);
    const lw = Math.max(170, ...items.map((i) => g.measureText(i.label).width + 50));
    const lh = 30 + items.length * 18;
    const x = 14, y = H - lh - 30;
    g.fillStyle = "rgba(255,255,255,0.96)";
    g.fillRect(x, y, lw, lh);
    g.strokeStyle = "#8b95a6";
    g.strokeRect(x + 0.5, y + 0.5, lw, lh);
    font("600", 12);
    g.fillStyle = "#0f2444";
    g.fillText(metricLabel(), x + 10, y + 18);
    font("400", 12);
    items.forEach((it, i) => {
      g.fillStyle = it.color;
      g.fillRect(x + 10, y + 27 + i * 18, 22, 12);
      g.fillStyle = "#13213a";
      g.fillText(it.label, x + 40, y + 37 + i * 18);
    });
  }
  // credit
  font("400", 11);
  const credit = "Map data © OpenStreetMap contributors" + (BASEMAPS[set.basemap].url.includes("openfreemap") ? ", OpenFreeMap" : "") + " · Made with Pracre";
  const cw = g.measureText(credit).width + 12;
  g.fillStyle = "rgba(255,255,255,0.85)";
  g.fillRect(W - cw - 6, H - 22, cw, 18);
  g.fillStyle = "#3b4a63";
  g.fillText(credit, W - cw, H - 9);
  return c.toDataURL("image/png");
}

async function exportImage() {
  if (!fc || !fc.features.length) return alertBox("Nothing to export", "Import data first, then export the map.");
  await new Promise((r) => {
    map.once("render", r);
    map.triggerRepaint();
  });
  await guard(async () => {
    const p = await api().SaveImage(composeImage());
    if (p) status("Saved " + p);
  });
}

function exportWeb(threeD) {
  if (!fc || !fc.features.length) return alertBox("Nothing to export", "Import data first, then export the map.");
  const st = S.stats;
  const share = metric() === "tax" ? st.halfLandTax : st.halfLandValue;
  const title = h("input", { type: "text", value: set.title || metricLabel() });
  const sub = h("input", { type: "text", value: share > 0 ? "Half of all " + (metric() === "tax" ? "tax revenue" : "property value") + " comes from " + pct(share) + " of the land." : "" });
  const three = h("input", { type: "checkbox", checked: threeD });
  const big = fc.features.length > 30000 ? h("div", { class: "imp" }, h("div", { class: "msg warn" }, "This layer has " + int(fc.features.length) + " shapes, so the page will be large. Hex or square tiles make a much lighter web page.")) : null;
  const go = h("button", { class: "btn primary" }, "Export…");
  const no = h("button", { class: "btn" }, "Cancel");
  const D = dialog({ title: "Export web map", icon: "export-web", cls: "small-dlg", body: [
    h("p", { class: "lead" }, "Saves one HTML file with the data inside. Upload it to your website and embed it with an iframe."),
    h("label", { class: "row" }, "Title", title), h("label", { class: "row" }, "Subtitle", sub),
    h("label", { class: "check" }, three, "Open in 3D (visitors can switch)"), big,
  ], foot: [h("span", { class: "spacer" }), go, no] });
  no.onclick = () => D.close();
  go.onclick = async () => {
    D.close();
    const c = map.getCenter();
    const { H, cap } = heightScale();
    const base = BASEMAPS[set.basemap].url;
    const o = {
      title: title.value, subtitle: sub.value, threeD: three.checked, center: [c.lng, c.lat], zoom: map.getZoom(),
      pitch: three.checked ? Math.max(map.getPitch(), 50) : 0, bearing: map.getBearing(), colors: colors(), zero: ZERO, noArea: NOAREA,
      legend: legendItems(), height: H, cap: cap, basemap: base, metricLabel: metricLabel(), totalLabel: totalLabel(), currency: set.currency, per: perWord(), opacity: set.opacity,
    };
    await guard(async () => {
      const p = await api().ExportHTML(o);
      if (!p) return;
      status("Saved " + p);
      const name = p.split(/[\\/]/).pop();
      const snip = '<iframe src="' + name + '" title="' + esc(title.value) + '" width="100%" height="600" style="border:0" loading="lazy"></iframe>';
      const ta = h("textarea", { class: "snippet", readonly: true }, snip);
      const copy = h("button", { class: "btn primary" }, "Copy Embed Code");
      const ok = h("button", { class: "btn" }, "Close");
      const D2 = dialog({ title: "Web map saved", icon: "export-web", cls: "small-dlg", body: [
        h("p", {}, "Saved " + p + "."), h("p", {}, "Upload the file next to your page, then paste this where the map should appear:"), ta,
      ], foot: [h("button", { class: "btn", onclick: () => api().OpenURL("file:///" + p.replace(/\\/g, "/")) }, "Open in Browser"), h("span", { class: "spacer" }), copy, ok] });
      copy.onclick = async () => {
        await rt().ClipboardSetText(snip);
        copy.textContent = "Copied ✓";
      };
      ok.onclick = () => D2.close();
    });
  };
}

function exportGLB() {
  if (!fc || !fc.features.length) return alertBox("Nothing to export", "Import data first, then export the map.");
  const base = h("input", { type: "checkbox", checked: true });
  const go = h("button", { class: "btn primary" }, "Export…");
  const no = h("button", { class: "btn" }, "Cancel");
  const D = dialog({ title: "Export 3D model", icon: "export-3d", cls: "small-dlg", body: [
    h("p", { class: "lead" }, "Saves the current " + (set.mode === "parcels" ? "parcels" : "tiles") + " as a coloured glTF model (.glb) in metres, with the same heights as the map. It opens in Blender, PowerPoint, Windows 3D Viewer and most 3D tools."),
    h("label", { class: "check" }, base, "Add a base plate under the model"),
    set.mode === "parcels" && fc.features.length > 20000 ? h("div", { class: "imp" }, h("div", { class: "msg warn" }, "Many parcels make a heavy model; tiles export faster.")) : null,
  ], foot: [h("span", { class: "spacer" }), go, no] });
  no.onclick = () => D.close();
  go.onclick = async () => {
    D.close();
    const { H, cap } = heightScale();
    await guard(async () => {
      const p = await api().ExportGLB({ colors: colors(), zero: ZERO, noArea: NOAREA, height: H, cap: cap, base: base.checked });
      if (p) status("Saved " + p);
    }, "Building the 3D model…");
  };
}

// ---------------------------------------------------------------- help and about

function showHelp() {
  const D = dialog({ title: "Getting started", icon: "help", cls: "help", body: h("div", { html: `
    <p class="lead">Pracre maps how much value or tax each acre of land produces. Work down the checklist on the left; it says what is still missing.</p>
    <h4>1. Parcel locations</h4>
    <ul><li>Parcel shapes: GeoJSON, zipped Shapefile, KML/KMZ or Esri JSON (ArcGIS downloads).</li>
    <li>Or a table (CSV, Excel) with latitude/longitude, X/Y, "lat, lon" in one column, degrees-minutes-seconds or WKT shapes.</li>
    <li>Coordinate systems are read from the file (.prj, GeoJSON crs) or guessed; the import window shows where the points land so you can check.</li></ul>
    <h4>2. Values</h4>
    <ul><li>Assessed or taxable value (total, or land plus improvements), or the tax amount itself.</li>
    <li>Values can sit in the parcel file or a separate table. Pracre links them by the ID both files share (folio, PID, APN, roll number…), and falls back to location.</li>
    <li>Value columns per tax class (Residential_Land, Business_Buildings…) are recognised too.</li></ul>
    <h4>3. Tax rates (optional)</h4>
    <ul><li>Fill in the Tax Rates table at the bottom, or import a table of codes and rates. Rates can be mill rates (per $1,000), percent or fractions.</li>
    <li>A default rate covers properties without a class.</li></ul>
    <h4>Reading the map</h4>
    <ul><li>Colour and height both show value per acre: tall red columns produce the most per acre.</li>
    <li>Hex and square tiles smooth out small lots; parcels show every lot. Drag with the right mouse button (or Ctrl) to tilt and turn the 3D view.</li>
    <li>Export images, 3D models and web pages with the Export buttons. Save the project to keep everything in one .pracre file.</li></ul>` }),
    foot: [h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => D.close() }, "OK")] });
}

function showAbout() {
  const D = dialog({ title: "About Pracre", cls: "about", body: [
    h("img", { class: "hero", src: "img/hero.png", alt: "" }),
    h("div", { style: "padding:12px 4px 0" },
      h("h1", {}, "Pracre ", h("small", { class: "muted", style: "font-size:13px" }, S && S.version)),
      h("p", { style: "margin:4px 0 10px" }, "Value per acre maps for Strong Towns advocates."),
      h("p", { class: "muted" }, "Free software under the GNU General Public License v3. Base maps © OpenStreetMap contributors, served by OpenFreeMap and the OpenStreetMap Foundation. Map rendering by MapLibre GL JS. Coordinate systems from the EPSG Geodetic Parameter Dataset (© IOGP) and Esri, via PROJ. Sample data: Township of Langley open data (Open Government Licence – Township of Langley) and City of Port Huron, Michigan parcel data."),
      h("p", {}, h("a", { href: "#", onclick: (e) => { e.preventDefault(); api().OpenURL("https://github.com/strongtowns/Pracre"); } }, "github.com/strongtowns/Pracre"), " · ",
        h("a", { href: "#", onclick: (e) => { e.preventDefault(); api().OpenURL("https://strongtowns.dev"); } }, "strongtowns.dev"))),
  ], foot: [h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => D.close() }, "OK")] });
}

// Stands in for the map when WebGL is missing: every call is a harmless no-op.
const NOMAP = new Proxy({}, {
  get: (_, k) => (k === "getCenter" ? () => ({ lng: -98, lat: 44 }) : k === "getCanvas" ? () => document.createElement("canvas") : ["getZoom", "getPitch", "getBearing"].includes(k) ? () => 0 : () => undefined),
});

// ---------------------------------------------------------------- start

async function start() {
  for (let i = 0; i < 100 && !(window.go && window.go.main); i++) await new Promise((r) => setTimeout(r, 20));
  window.addEventListener("error", (e) => status("Error: " + e.message));
  window.addEventListener("unhandledrejection", (e) => status("Error: " + (e.reason && e.reason.message ? e.reason.message : e.reason)));
  initControls();
  try {
    initMap();
  } catch (e) {
    // Without WebGL there is no map, but import, rates, tables and data exports still work.
    map = NOMAP;
    $("#map").append(h("div", { class: "nomap" }, h("b", {}, "The map can't start on this computer."), "Pracre draws maps with WebGL, which this computer's graphics driver or virtual machine doesn't provide. Importing, the checklist, the tables and data exports still work.", h("small", {}, String(e && e.message ? e.message : e))));
  }
  rt().EventsOn("menu", (a) => run(a));
  rt().OnFileDrop((x, y, paths) => {
    $("#dropcover").classList.add("hidden");
    importPaths(paths);
  }, false);
  let dragDepth = 0;
  document.addEventListener("dragenter", (e) => { e.preventDefault(); dragDepth++; $("#dropcover").classList.remove("hidden"); });
  document.addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; $("#dropcover").classList.add("hidden"); } });
  document.addEventListener("dragover", (e) => e.preventDefault());
  document.addEventListener("drop", () => { dragDepth = 0; $("#dropcover").classList.add("hidden"); });
  S = await api().State();
  if (S.datasets.length) await afterOpen();
  else {
    syncControls();
    await refresh();
  }
}
start();
