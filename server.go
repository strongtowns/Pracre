package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ServeHTTP answers the requests the embedded assets don't: map layers, details and the OSM tile cache.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case strings.HasPrefix(r.URL.Path, "/osm/"):
		serveOSM(w, r)
	case r.URL.Path == "/api/layer":
		num := func(k string, def float64) float64 {
			if v, err := strconv.ParseFloat(q.Get(k), 64); err == nil {
				return v
			}
			return def
		}
		req := LayerReq{Mode: q.Get("mode"), Size: num("size", 200), Metric: q.Get("metric"), Per: q.Get("per"),
			Basis: q.Get("basis"), Classes: int(num("classes", 7)), Method: q.Get("method"), Gap: num("gap", 0.08)}
		req.norm()
		a.mu.Lock()
		m := a.ensureModel()
		if a.layer == nil || a.layer.Req != req {
			a.layer = m.layer(req)
		}
		L := a.layer
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		L.writeGeoJSON(w, m, false)
	case r.URL.Path == "/api/detail":
		id, _ := strconv.Atoi(q.Get("id"))
		a.mu.Lock()
		var out map[string]any
		if a.model != nil {
			out = a.model.detail(a.layer, id)
		}
		a.mu.Unlock()
		writeJSON(w, out)
	case r.URL.Path == "/api/table":
		limit, _ := strconv.Atoi(q.Get("limit"))
		a.mu.Lock()
		var rows []TableRow
		total := 0
		if a.model != nil {
			rows, total = a.model.table(a.layer, q.Get("q"), q.Get("sort"), limit)
		}
		a.mu.Unlock()
		if rows == nil {
			rows = []TableRow{}
		}
		writeJSON(w, map[string]any{"rows": rows, "total": total})
	case r.URL.Path == "/api/locations":
		a.mu.Lock()
		d := a.proj.dataset(q.Get("id"))
		var pts []Pt
		if d != nil {
			for i := 0; i < len(d.geo); i += max(1, len(d.geo)/4000) {
				if d.geo[i].OK {
					pts = append(pts, labelPoint(d.geo[i]))
				}
			}
		}
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var sb strings.Builder
		sb.WriteString(`{"type":"FeatureCollection","features":[`)
		for i, p := range pts {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"type":"Feature","properties":{},"geometry":{"type":"Point","coordinates":[%.6f,%.6f]}}`, p[0], p[1])
		}
		sb.WriteString("]}")
		io.WriteString(w, sb.String())
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// The OpenStreetMap tile policy asks apps to identify themselves and cache tiles, which a web view can't do.
var (
	osmClient = &http.Client{Timeout: 20 * time.Second}
	osmSlots  = make(chan struct{}, 2)
)

func serveOSM(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/osm/"), ".png"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	var zxy [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			http.NotFound(w, r)
			return
		}
		zxy[i] = v
	}
	if zxy[0] > 19 || zxy[1] >= 1<<zxy[0] || zxy[2] >= 1<<zxy[0] {
		http.NotFound(w, r)
		return
	}
	dir, _ := os.UserCacheDir()
	file := filepath.Join(dir, "Pracre", "osm", strconv.Itoa(zxy[0]), strconv.Itoa(zxy[1]), strconv.Itoa(zxy[2])+".png")
	if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		serveTile(w, file)
		return
	}
	osmSlots <- struct{}{}
	defer func() { <-osmSlots }()
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", zxy[0], zxy[1], zxy[2]), nil)
	req.Header.Set("User-Agent", "Pracre/"+Version+" (+https://github.com/strongtowns/Pracre)")
	resp, err := osmClient.Do(req)
	if err == nil && resp.StatusCode == 200 {
		b, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr == nil {
			os.MkdirAll(filepath.Dir(file), 0o755)
			writeAtomic(file, b)
			w.Header().Set("Content-Type", "image/png")
			w.Write(b)
			return
		}
	} else if resp != nil {
		resp.Body.Close()
	}
	if _, err := os.Stat(file); err == nil {
		serveTile(w, file) // offline: stale beats blank
		return
	}
	http.Error(w, "tile unavailable", http.StatusBadGateway)
}

func serveTile(w http.ResponseWriter, file string) {
	b, err := os.ReadFile(file)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Write(b)
}
