package main

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed sample/langley-sample.zip
var sampleZip []byte

// App is bound to the front end: every exported method is callable as window.go.main.App.<Method>.
type App struct {
	ctx   context.Context
	mu    sync.Mutex
	proj  *Project
	model *Model
	layer *Layer
	path  string
	dirty bool
}

func NewApp() *App { return &App{proj: &Project{RateUnit: "mill"}} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if len(os.Args) > 1 && strings.HasSuffix(strings.ToLower(os.Args[1]), ".pracre") {
		a.openPath(os.Args[1])
	}
}

// openFromSystem handles a .pracre file opened from Finder while the app runs (or launches it).
func (a *App) openFromSystem(path string) {
	if err := a.openPath(path); err == nil && a.ctx != nil {
		wr.EventsEmit(a.ctx, "menu", "reload")
	}
}

// ---- state sent to the UI

type DatasetDTO struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Format   string     `json:"format"`
	Rows     int        `json:"rows"`
	Cols     []string   `json:"cols"`
	Roles    []ColRole  `json:"roles"`
	Kind     string     `json:"kind"`
	Located  int        `json:"located"`
	Polys    int        `json:"polys"`
	CRS      CRS        `json:"crs"`
	CRSSure  bool       `json:"crsSure"`
	CRSText  string     `json:"crsText"`
	FileCRS  string     `json:"fileCrs"`
	Guesses  []CRSGuess `json:"guesses"`
	LocErr   string     `json:"locErr"`
	Join     Join       `json:"join"`
	BBox     []float64  `json:"bbox"`
	Primary  bool       `json:"primary"`
	Preview  [][]string `json:"preview,omitempty"`
	HasLocUI bool       `json:"hasLoc"`
}

type State struct {
	Datasets []DatasetDTO    `json:"datasets"`
	Rates    []Rate          `json:"rates"`
	RateUnit string          `json:"rateUnit"`
	Classes  []ClassInfo     `json:"classes"`
	Checks   []Check         `json:"checks"`
	Stats    Stats           `json:"stats"`
	Settings json.RawMessage `json:"settings"`
	Project  string          `json:"project"`
	Dirty    bool            `json:"dirty"`
	Version  string          `json:"version"`
}

func (a *App) dto(d *Dataset, preview int) DatasetDTO {
	kind := "table"
	switch {
	case d.isRates():
		kind = "rates"
	case d.polys > 0:
		kind = "parcels"
	case d.located > 0:
		kind = "points"
	case d.hasValues():
		kind = "values"
	}
	out := DatasetDTO{ID: d.ID, Name: d.T.Name, Path: d.Path, Format: d.T.Format, Rows: len(d.T.Rows), Cols: d.T.Cols,
		Roles: d.Roles, Kind: kind, Located: d.located, Polys: d.polys, CRS: d.crs, CRSSure: d.crsSure, CRSText: d.CRSText,
		FileCRS: d.T.FileCRS, Guesses: d.guesses, LocErr: d.locErr, Join: d.Join, HasLocUI: d.raw != nil}
	out.CRS.WKT = ""
	if d.bbox.Valid() {
		out.BBox = []float64{d.bbox.MinX, d.bbox.MinY, d.bbox.MaxX, d.bbox.MaxY}
	}
	if a.model != nil && a.model.Primary >= 0 && a.model.Primary < len(a.proj.Datasets) && a.proj.Datasets[a.model.Primary] == d {
		out.Primary = true
	}
	if preview > 0 {
		out.Preview = d.T.Rows[:min(preview, len(d.T.Rows))]
	}
	return out
}

// ensureModel rebuilds the model after any data change. Callers hold a.mu.
func (a *App) ensureModel() *Model {
	if a.model == nil {
		a.model = buildModel(a.proj)
		a.layer = nil
		// Keep every tax class in the rates table so people can fill them in.
		have := map[string]bool{}
		for _, r := range a.proj.Rates {
			have[normKey(r.Code)] = true
			if r.Desc != "" {
				have[normKey(r.Desc)] = true
			}
		}
		for _, c := range a.model.Classes {
			if !have[normKey(c.Code)] {
				a.proj.Rates = append(a.proj.Rates, Rate{Code: c.Code})
				have[normKey(c.Code)] = true
			}
		}
	}
	return a.model
}

func (a *App) invalidate() {
	a.model, a.layer = nil, nil
	a.dirty = true
}

// State returns everything the UI shows outside the map.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state()
}

func (a *App) state() State {
	m := a.ensureModel()
	// Empty lists must reach the UI as [] rather than null.
	s := State{Datasets: []DatasetDTO{}, Rates: append([]Rate{}, a.proj.Rates...), RateUnit: a.proj.RateUnit,
		Classes: append([]ClassInfo{}, m.Classes...), Checks: append([]Check{}, m.Checks...), Stats: m.Stats,
		Settings: a.proj.Settings, Project: a.path, Dirty: a.dirty, Version: Version}
	for _, d := range a.proj.Datasets {
		s.Datasets = append(s.Datasets, a.dto(d, 0))
	}
	return s
}

// Dataset returns one dataset with a preview of its first rows for the import dialog.
func (a *App) Dataset(id string) (DatasetDTO, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.proj.dataset(id)
	if d == nil {
		return DatasetDTO{}, fmt.Errorf("no dataset %s", id)
	}
	a.ensureModel()
	return a.dto(d, 60), nil
}

func (a *App) CommonCRS() []CRS { return commonCRS() }

// ---- import

type ImportResult struct {
	Added  []string `json:"added"`
	Rates  int      `json:"rates"`
	Errors []string `json:"errors"`
}

// ChooseFiles shows the open dialog for data files.
func (a *App) ChooseFiles() ([]string, error) {
	return wr.OpenMultipleFilesDialog(a.ctx, wr.OpenDialogOptions{
		Title: "Import parcel, value or tax rate data",
		Filters: []wr.FileFilter{
			{DisplayName: "All supported data", Pattern: "*.csv;*.tsv;*.txt;*.xlsx;*.json;*.geojson;*.shp;*.zip;*.kml;*.kmz"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}

// Import reads files and adds them to the project. hint is the map centre, used to guess UTM zones.
func (a *App) Import(paths []string, hintLon, hintLat float64) ImportResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	hint := Pt{hintLon, hintLat}
	if m := a.model; m != nil && m.Stats.Parcels > 0 {
		b := m.Stats.BBox
		hint = Pt{(b[0] + b[2]) / 2, (b[1] + b[3]) / 2}
	}
	var res ImportResult
	stem := func(p string) string { return strings.ToLower(strings.TrimSuffix(p, filepath.Ext(p))) }
	shps := map[string]bool{}
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".shp") {
			shps[stem(p)] = true
		}
	}
	for _, p := range paths {
		// A shapefile's other parts are read with its .shp (a .dbf alone is a table), and its
		// index and metadata files (.sbn, .shp.xml) aren't data.
		ext := strings.ToLower(filepath.Ext(p))
		if sidecars[ext] || ext == ".dbf" && shps[stem(p)] || ext == ".xml" && filepath.Ext(stem(p)) != "" {
			continue
		}
		tabs, err := readFile(p)
		if err != nil {
			res.Errors = append(res.Errors, filepath.Base(p)+": "+err.Error())
			continue
		}
		for _, t := range tabs {
			id, n := a.addTable(t, p, hint)
			if id != "" {
				res.Added = append(res.Added, id)
			}
			res.Rates += n
		}
	}
	a.invalidate()
	return res
}

var sidecars = map[string]bool{".prj": true, ".cpg": true, ".shx": true, ".sbn": true, ".sbx": true, ".qix": true, ".fix": true, ".qmd": true, ".atx": true, ".aih": true, ".ain": true, ".lyr": true, ".lyrx": true}

// addTable adds one table; a tax rate table goes straight into the rates list instead.
func (a *App) addTable(t *Table, path string, hint Pt) (string, int) {
	roles := detectRoles(t)
	d := &Dataset{T: t, Path: path, Geoms: t.Geoms, Roles: roles, Hint: hint}
	if d.isRates() {
		return "", a.mergeRates(d)
	}
	a.proj.NextID++
	d.ID = "d" + strconv.Itoa(a.proj.NextID)
	d.prepare(hint)
	a.proj.Datasets = append(a.proj.Datasets, d)
	return d.ID, 0
}

func (a *App) mergeRates(d *Dataset) int {
	ci, ri, di := indexOfRole(d.Roles, RoleRateCode), indexOfRole(d.Roles, RoleRate), indexOfRole(d.Roles, RoleRateDesc)
	n := 0
	small := true
	for r := range d.T.Rows {
		code := strings.TrimSpace(d.T.col(ci, r))
		v, ok := parseNum(d.T.col(ri, r))
		if code == "" || !ok {
			continue
		}
		if v > 0.1 {
			small = false
		}
		vv := v
		rate := Rate{Code: code, Rate: &vv}
		if di >= 0 {
			rate.Desc = d.T.col(di, r)
		}
		replaced := false
		for i := range a.proj.Rates {
			if normKey(a.proj.Rates[i].Code) == normKey(code) {
				if rate.Desc == "" {
					rate.Desc = a.proj.Rates[i].Desc
				}
				a.proj.Rates[i] = rate
				replaced = true
			}
		}
		if !replaced {
			a.proj.Rates = append(a.proj.Rates, rate)
		}
		n++
	}
	if n > 0 {
		// The rate column's heading says the unit when it can; otherwise rates under 0.1 are fractions.
		switch h := strings.ToLower(d.T.Cols[ri]); {
		case strings.Contains(h, "%") || strings.Contains(h, "percent") || strings.Contains(h, "pct"):
			a.proj.RateUnit = "percent"
		case strings.Contains(h, "mill") || strings.Contains(h, "1000") || strings.Contains(h, "1,000"):
			a.proj.RateUnit = "mill"
		case small:
			a.proj.RateUnit = "fraction"
		}
	}
	return n
}

// Configure applies the column roles, coordinate system and link settings chosen in the import dialog.
func (a *App) Configure(id string, roles []ColRole, crsText string, join Join) (DatasetDTO, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.proj.dataset(id)
	if d == nil {
		return DatasetDTO{}, fmt.Errorf("no dataset %s", id)
	}
	if len(roles) == len(d.T.Cols) {
		d.Roles = roles
	}
	if crsText != "" {
		if _, err := crsFromString(crsText); err != nil {
			return DatasetDTO{}, err
		}
	}
	d.CRSText, d.Join = crsText, join
	if d.isRates() {
		a.proj.Datasets = removeDS(a.proj.Datasets, id)
		a.mergeRates(d)
		a.invalidate()
		return DatasetDTO{ID: id, Kind: "rates"}, nil
	}
	d.prepare(d.Hint)
	a.invalidate()
	a.ensureModel()
	return a.dto(d, 60), nil
}

// GuessNear ranks the coordinate system guesses again for data near lon/lat, the import window's
// small map view: unlabelled State Plane feet fit dozens of states until Pracre knows the area.
func (a *App) GuessNear(id string, lon, lat float64) (DatasetDTO, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.proj.dataset(id)
	if d == nil {
		return DatasetDTO{}, fmt.Errorf("no dataset %s", id)
	}
	d.Hint, d.CRSText = Pt{lon, lat}, ""
	d.prepare(d.Hint)
	a.invalidate()
	a.ensureModel()
	return a.dto(d, 60), nil
}

// DetectRoles re-runs detection for one dataset (the dialog's "Auto-detect" button).
func (a *App) DetectRoles(id string) []ColRole {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.proj.dataset(id); d != nil {
		return detectRoles(d.T)
	}
	return nil
}

func removeDS(ds []*Dataset, id string) []*Dataset {
	out := ds[:0]
	for _, d := range ds {
		if d.ID != id {
			out = append(out, d)
		}
	}
	return out
}

func (a *App) Remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.proj.Datasets = removeDS(a.proj.Datasets, id)
	a.invalidate()
}

// ---- rates and settings

func (a *App) SetRates(rates []Rate, unit string) State {
	a.mu.Lock()
	defer a.mu.Unlock()
	var keep []Rate
	for _, r := range rates {
		r.Code = strings.TrimSpace(r.Code)
		if r.Code != "" {
			keep = append(keep, r)
		}
	}
	a.proj.Rates, a.proj.RateUnit = keep, unit
	a.invalidate()
	return a.state()
}

// ImportRates reads a rates table from a file picked in a dialog.
func (a *App) ImportRates() (int, error) {
	p, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Import tax rates",
		Filters: []wr.FileFilter{{DisplayName: "Tables", Pattern: "*.csv;*.tsv;*.txt;*.xlsx;*.json"}}})
	if err != nil || p == "" {
		return 0, err
	}
	tabs, err := readFile(p)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, t := range tabs {
		d := &Dataset{T: t, Roles: detectRoles(t)}
		if !d.isRates() {
			// Fall back to the first text and first numeric column.
			d.Roles = make([]ColRole, len(t.Cols))
			for i := range t.Cols {
				st := statColumn(t, i)
				if st.numeric() && indexOfRole(d.Roles, RoleRate) < 0 {
					d.Roles[i] = ColRole{Role: RoleRate}
				} else if !st.numeric() && indexOfRole(d.Roles, RoleRateCode) < 0 {
					d.Roles[i] = ColRole{Role: RoleRateCode}
				}
			}
			if !d.isRates() || indexOfRole(d.Roles, RoleRateCode) < 0 {
				continue
			}
		}
		n += a.mergeRates(d)
	}
	if n == 0 {
		return 0, fmt.Errorf("no code and rate columns found in %s", filepath.Base(p))
	}
	a.invalidate()
	return n, nil
}

// SaveSettings stores the UI's display settings in the project.
func (a *App) SaveSettings(raw string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if string(a.proj.Settings) != raw {
		a.proj.Settings = json.RawMessage(raw)
		a.dirty = true
	}
}

// ---- projects

func (a *App) NewProject() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.proj, a.path = &Project{RateUnit: "mill"}, ""
	a.invalidate()
	a.dirty = false
	return a.state()
}

// ConfirmDiscard asks before throwing away unsaved work.
func (a *App) ConfirmDiscard() bool {
	a.mu.Lock()
	dirty := a.dirty && len(a.proj.Datasets) > 0
	a.mu.Unlock()
	if !dirty {
		return true
	}
	res, err := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.QuestionDialog, Title: "Pracre",
		Message: "Discard unsaved changes to this project?", Buttons: []string{"Discard", "Cancel"}, DefaultButton: "Cancel", CancelButton: "Cancel"})
	return err == nil && (res == "Discard" || res == "Yes")
}

func (a *App) OpenProject(path string) error {
	if path == "" {
		p, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Open Pracre project",
			Filters: []wr.FileFilter{{DisplayName: "Pracre projects (*.pracre)", Pattern: "*.pracre"}}})
		if err != nil || p == "" {
			return err
		}
		path = p
	}
	return a.openPath(path)
}

func (a *App) openPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a Pracre project: %w", err)
	}
	var p Project
	if err := json.NewDecoder(zr).Decode(&p); err != nil {
		return fmt.Errorf("couldn't read the project: %w", err)
	}
	for _, d := range p.Datasets {
		d.prepare(d.Hint)
	}
	a.mu.Lock()
	a.proj, a.path = &p, path
	a.invalidate()
	a.dirty = false
	a.mu.Unlock()
	return nil
}

// SaveProject writes a .pracre file (gzipped JSON with all imported data, so it travels on its own).
func (a *App) SaveProject(saveAs bool) (string, error) {
	a.mu.Lock()
	path := a.path
	a.mu.Unlock()
	if path == "" || saveAs {
		def := "Value per acre.pracre"
		if path != "" {
			def = filepath.Base(path)
		}
		p, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{Title: "Save Pracre project", DefaultFilename: def,
			Filters: []wr.FileFilter{{DisplayName: "Pracre projects (*.pracre)", Pattern: "*.pracre"}}})
		if err != nil || p == "" {
			return "", err
		}
		if !strings.HasSuffix(strings.ToLower(p), ".pracre") {
			p += ".pracre"
		}
		path = p
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(a.proj); err != nil {
		return "", err
	}
	zw.Close()
	if err := writeAtomic(path, buf.Bytes()); err != nil {
		return "", err
	}
	a.path, a.dirty = path, false
	return path, nil
}

// writeAtomic avoids leaving a half-written file if the disk fills up mid-save.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// LoadSample adds the bundled Township of Langley extract (Fort Langley and surrounding farmland).
func (a *App) LoadSample() ImportResult {
	dir := filepath.Join(os.TempDir(), "pracre-sample")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "langley-sample.zip")
	if err := os.WriteFile(p, sampleZip, 0o644); err != nil {
		return ImportResult{Errors: []string{err.Error()}}
	}
	return a.Import([]string{p}, -122.6, 49.15)
}

// ---- exports that only need a file name

func (a *App) saveDialog(title, def, pattern, label string) (string, error) {
	p, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{Title: title, DefaultFilename: def,
		Filters: []wr.FileFilter{{DisplayName: label, Pattern: pattern}}})
	if err != nil || p == "" {
		return "", err
	}
	ext := strings.TrimPrefix(pattern, "*")
	if !strings.HasSuffix(strings.ToLower(p), ext) {
		p += ext
	}
	return p, nil
}

func (a *App) projectBase() string {
	if a.path != "" {
		return baseName(a.path)
	}
	return "value-per-acre"
}

// SaveImage writes a PNG produced by the map canvas.
func (a *App) SaveImage(dataURL string) (string, error) {
	i := strings.Index(dataURL, ",")
	if i < 0 {
		return "", fmt.Errorf("no image data")
	}
	b, err := base64.StdEncoding.DecodeString(dataURL[i+1:])
	if err != nil {
		return "", err
	}
	p, err := a.saveDialog("Export image", a.projectBase()+".png", "*.png", "PNG image")
	if err != nil || p == "" {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o644)
}

func (a *App) currentLayer() (*Model, *Layer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.ensureModel()
	if a.layer == nil || len(a.layer.Feats) == 0 {
		return nil, nil, fmt.Errorf("there is nothing on the map to export yet")
	}
	return m, a.layer, nil
}

func (a *App) ExportGeoJSON() (string, error) {
	m, L, err := a.currentLayer()
	if err != nil {
		return "", err
	}
	p, err := a.saveDialog("Export GeoJSON", a.projectBase()+"-"+L.Req.Mode+".geojson", "*.geojson", "GeoJSON")
	if err != nil || p == "" {
		return "", err
	}
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return p, L.writeGeoJSON(f, m, true)
}

func (a *App) ExportCSV() (string, error) {
	m, L, err := a.currentLayer()
	if err != nil {
		return "", err
	}
	p, err := a.saveDialog("Export table", a.projectBase()+"-"+L.Req.Mode+".csv", "*.csv", "CSV table")
	if err != nil || p == "" {
		return "", err
	}
	per := L.Req.Per
	metric := L.Req.Metric
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("id,key,label,longitude,latitude,%ss,parcels,%s,%s_per_%s,class\n", per, metric, metric, per))
	for i, f := range L.Feats {
		key, label := "", f.Label
		if f.P >= 0 {
			key = m.Parcels[f.P].Key
		}
		c := labelPoint(f.G)
		v := ""
		if !math.IsNaN(f.V) {
			v = strconv.FormatFloat(f.V, 'f', 2, 64)
		}
		sb.WriteString(fmt.Sprintf("%d,%s,%s,%.6f,%.6f,%.4f,%d,%.2f,%s,%d\n", i, csvQ(key), csvQ(label), c[0], c[1], f.A, f.N, f.T, v, f.C+1))
	}
	return p, os.WriteFile(p, []byte(sb.String()), 0o644)
}

func csvQ(s string) string {
	if s != "" && strings.ContainsRune("=+@", rune(s[0])) {
		s = "'" + s // imported text must not run as a spreadsheet formula
	}
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// OpenFile reveals an exported file with the system's default app.
func (a *App) OpenURL(url string) { wr.BrowserOpenURL(a.ctx, url) }
