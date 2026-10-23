package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// Table is one imported layer: an attribute table plus, for spatial formats, a geometry per row.
type Table struct {
	Name    string   `json:"name"`
	Cols    []string `json:"cols"`
	Rows    [][]string
	Geoms   []Geom `json:"-"` // raw coordinates in FileCRS; nil for plain tables
	FileCRS string `json:"fileCrs"`
	Format  string `json:"format"`
}

func (t *Table) col(i, r int) string {
	if r < len(t.Rows) && i >= 0 && i < len(t.Rows[r]) {
		return t.Rows[r][i]
	}
	return ""
}

// readFile reads any supported file into one or more tables (a zip may hold several).
func readFile(path string) ([]*Table, error) {
	ext := strings.ToLower(filepath.Ext(path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if ext == ".zip" || ext == ".kmz" {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return readZip(&zr.Reader, name)
	}
	if ext == ".shp" || ext == ".dbf" {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		files := map[string][]byte{}
		for _, e := range []string{".shp", ".dbf", ".prj", ".cpg"} {
			for _, cand := range []string{base + e, base + strings.ToUpper(e)} {
				if b, err := os.ReadFile(cand); err == nil {
					files[e] = b
					break
				}
			}
		}
		t, err := readShapefile(files)
		if err != nil {
			return nil, err
		}
		t.Name = name
		return []*Table{t}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t, err := readBytes(b, ext)
	if err != nil {
		return nil, err
	}
	t.Name = name
	return []*Table{t}, nil
}

func readBytes(b []byte, ext string) (*Table, error) {
	switch ext {
	case ".csv", ".tsv", ".txt", ".psv", ".tab":
		return readDelimited(b)
	case ".xlsx", ".xlsm", ".xltx":
		return readXLSX(b)
	case ".json", ".geojson", ".esrijson", ".js":
		return readJSON(b)
	case ".kml":
		return readKML(b)
	case ".xls":
		return nil, fmt.Errorf("old .xls workbooks aren't supported: open it in Excel or LibreOffice and save as .xlsx or .csv")
	case ".gpkg", ".gdb", ".sqlite":
		return nil, fmt.Errorf("%s isn't supported yet: export the layer as GeoJSON or a zipped Shapefile (QGIS: right-click the layer, Export)", ext)
	case ".pdf":
		return nil, fmt.Errorf("PDF tables can't be read: type the rates into the Tax Rates table instead")
	}
	// Unknown extension: sniff.
	trim := bytes.TrimSpace(b)
	if len(trim) > 0 && (trim[0] == '{' || trim[0] == '[') {
		return readJSON(b)
	}
	if bytes.HasPrefix(trim, []byte("<?xml")) || bytes.HasPrefix(trim, []byte("<kml")) {
		return readKML(b)
	}
	return readDelimited(b)
}

func readZip(zr *zip.Reader, name string) ([]*Table, error) {
	shp := map[string]map[string][]byte{}
	var out []*Table
	var errs []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasPrefix(filepath.Base(f.Name), ".") || strings.Contains(f.Name, "__MACOSX") {
			continue
		}
		if low := strings.ToLower(filepath.Base(f.Name)); strings.HasPrefix(low, "readme") || strings.HasPrefix(low, "license") || strings.Contains(low, "metadata") {
			continue // documentation shipped beside the data
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		base := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		switch ext {
		case ".shp", ".dbf", ".prj", ".cpg":
			if shp[base] == nil {
				shp[base] = map[string][]byte{}
			}
			shp[base][ext] = b
		case ".csv", ".tsv", ".txt", ".xlsx", ".json", ".geojson", ".kml":
			t, err := readBytes(b, ext)
			if err != nil {
				errs = append(errs, filepath.Base(f.Name)+": "+err.Error())
				continue
			}
			t.Name = filepath.Base(base)
			if strings.EqualFold(t.Name, "doc") {
				t.Name = name // a KMZ's doc.kml is named after the KMZ
			}
			out = append(out, t)
		}
	}
	bases := make([]string, 0, len(shp))
	for k := range shp {
		bases = append(bases, k)
	}
	sort.Strings(bases)
	for _, base := range bases {
		files := shp[base]
		if files[".shp"] == nil && files[".dbf"] == nil {
			continue
		}
		t, err := readShapefile(files)
		if err != nil {
			errs = append(errs, filepath.Base(base)+": "+err.Error())
			continue
		}
		t.Name = filepath.Base(base)
		out = append(out, t)
	}
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil, fmt.Errorf("no readable layers in %s", name)
	}
	return out, nil
}

// toUTF8 decodes UTF-16 with a byte order mark (Excel's "Unicode Text"), and Windows-1252/Latin-1
// text when the bytes aren't valid UTF-8.
func toUTF8(b []byte) []byte {
	if len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF) {
		var bo binary.ByteOrder = binary.LittleEndian
		if b[0] == 0xFE {
			bo = binary.BigEndian
		}
		u := make([]uint16, (len(b)-2)/2)
		for i := range u {
			u[i] = bo.Uint16(b[2+2*i:])
		}
		return []byte(string(utf16.Decode(u)))
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(b) {
		return b
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return []byte(sb.String())
}

func readDelimited(b []byte) (*Table, error) {
	b = toUTF8(b)
	// Pick the delimiter that splits the first lines most consistently.
	lines := strings.SplitN(string(b), "\n", 12)
	if len(lines) > 1 {
		lines = lines[:len(lines)-1]
	}
	best, bestScore := ',', -1
	for _, d := range []rune{',', '\t', ';', '|'} {
		counts := map[int]int{}
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				counts[strings.Count(l, string(d))]++
			}
		}
		for n, c := range counts {
			if n > 0 && c*100+n > bestScore {
				best, bestScore = d, c*100+n
			}
		}
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.Comma = best
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	var rows [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if _, ok := err.(*csv.ParseError); ok {
				continue
			}
			return nil, err
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		rows = append(rows, rec)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}
	if best == ';' {
		decimalCommas(rows)
	}
	return tableFromGrid(rows, "csv"), nil
}

var (
	reCommaDec = regexp.MustCompile(`^-?\d{1,3}(\.\d{3})*,\d+$|^-?\d+,\d+$`)
	reDotDec   = regexp.MustCompile(`^-?\d*\.\d+$`)
	reDotThou  = regexp.MustCompile(`^-?\d{1,3}(\.\d{3})+$`)
	reInt      = regexp.MustCompile(`^-?\d+$`)
)

// decimalCommas rewrites "1.234,56" as "1234.56" in the columns of a semicolon-separated file
// that are written with decimal commas, as French and European locales export them.
func decimalCommas(rows [][]string) {
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	for c := 0; c < width; c++ {
		comma, text, filled, dot := 0, 0, 0, false
		for _, r := range rows {
			if c >= len(r) || strings.TrimSpace(r[c]) == "" {
				continue
			}
			v := strings.TrimSpace(r[c])
			filled++
			switch {
			case reCommaDec.MatchString(v):
				comma++
			case reDotDec.MatchString(v) && !reDotThou.MatchString(v):
				dot = true // written with decimal points after all
			case !reInt.MatchString(v) && !reDotThou.MatchString(v):
				text++
			}
		}
		if dot || comma == 0 || comma*5 < filled || text*3 > filled {
			continue
		}
		for _, r := range rows {
			if c < len(r) {
				if v := strings.TrimSpace(r[c]); reCommaDec.MatchString(v) || reDotThou.MatchString(v) {
					r[c] = strings.Replace(strings.ReplaceAll(v, ".", ""), ",", ".", 1)
				}
			}
		}
	}
}

// tableFromGrid splits a header row off a grid; a numeric first row gets generated names.
// Title and note rows above the header (one or two cells in a wider table) are dropped.
func tableFromGrid(rows [][]string, format string) *Table {
	filled := func(r []string) int {
		n := 0
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				n++
			}
		}
		return n
	}
	wide := 0
	for _, r := range rows[:min(len(rows), 20)] {
		wide = max(wide, filled(r))
	}
	for len(rows) > 1 && filled(rows[0]) < min(3, wide) {
		rows = rows[1:]
	}
	header := rows[0]
	numeric := 0
	for _, h := range header {
		if _, ok := parseNum(h); ok {
			numeric++
		}
	}
	t := &Table{Format: format}
	if numeric > len(header)/2 {
		for i := range header {
			t.Cols = append(t.Cols, fmt.Sprintf("Column %d", i+1))
		}
		t.Rows = rows
	} else {
		t.Cols = append([]string(nil), header...)
		t.Rows = rows[1:]
	}
	width := len(t.Cols)
	for _, r := range t.Rows {
		width = max(width, len(r))
	}
	for i := len(t.Cols); i < width; i++ {
		t.Cols = append(t.Cols, fmt.Sprintf("Column %d", i+1))
	}
	t.Cols = uniqueNames(t.Cols)
	return t
}

func uniqueNames(cols []string) []string {
	seen := map[string]int{}
	out := make([]string, len(cols))
	for i, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			c = fmt.Sprintf("Column %d", i+1)
		}
		if n := seen[strings.ToLower(c)]; n > 0 {
			seen[strings.ToLower(c)]++
			c = fmt.Sprintf("%s (%d)", c, n+1)
		} else {
			seen[strings.ToLower(c)] = 1
		}
		out[i] = c
	}
	return out
}

func readXLSX(b []byte) (*Table, error) {
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var best [][]string
	for _, sh := range f.GetSheetList() {
		rows, err := f.GetRows(sh)
		if err == nil && len(rows) > len(best) {
			best = rows
		}
	}
	if len(best) == 0 {
		return nil, fmt.Errorf("the workbook has no table")
	}
	return tableFromGrid(best, "xlsx"), nil
}

// ---- JSON: GeoJSON, Esri JSON, arrays of records, {code: rate} maps ----

func rawToString(r json.RawMessage) string {
	if len(r) == 0 {
		return ""
	}
	switch r[0] {
	case '"':
		var s string
		if json.Unmarshal(r, &s) == nil {
			return s
		}
	case 'n':
		return ""
	}
	return string(r)
}

// keyOrder returns an object's keys in file order.
func keyOrder(raw json.RawMessage) []string {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	for d.More() {
		t, err := d.Token()
		if err != nil {
			break
		}
		k, _ := t.(string)
		keys = append(keys, k)
		var skip json.RawMessage
		if d.Decode(&skip) != nil {
			break
		}
	}
	return keys
}

type recordSet struct {
	cols  []string
	index map[string]int
	rows  [][]string
}

func (rs *recordSet) add(raw json.RawMessage) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		rs.rows = append(rs.rows, make([]string, len(rs.cols)))
		return
	}
	if rs.index == nil {
		rs.index = map[string]int{}
	}
	if len(rs.cols) == 0 || len(m) > len(rs.cols) {
		for _, k := range keyOrder(raw) {
			if _, ok := rs.index[k]; !ok {
				rs.index[k] = len(rs.cols)
				rs.cols = append(rs.cols, k)
			}
		}
	}
	row := make([]string, len(rs.cols))
	for k, v := range m {
		i, ok := rs.index[k]
		if !ok {
			i = len(rs.cols)
			rs.index[k] = i
			rs.cols = append(rs.cols, k)
			row = append(row, "")
		}
		row[i] = rawToString(v)
	}
	rs.rows = append(rs.rows, row)
}

func (rs *recordSet) table(format string) *Table {
	for i, r := range rs.rows {
		if len(r) < len(rs.cols) {
			rs.rows[i] = append(r, make([]string, len(rs.cols)-len(r))...)
		}
	}
	return &Table{Cols: uniqueNames(rs.cols), Rows: rs.rows, Format: format}
}

func readJSON(b []byte) (*Table, error) {
	b = toUTF8(b)
	trim := bytes.TrimSpace(b)
	if len(trim) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}
	if trim[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trim, &arr); err != nil {
			return nil, err
		}
		if len(arr) == 0 {
			return nil, fmt.Errorf("the file has no records")
		}
		rs := &recordSet{}
		for _, r := range arr {
			rs.add(r)
		}
		return rs.table("json"), nil
	}
	var doc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
		CRS      *struct {
			Properties struct {
				Name string `json:"name"`
			} `json:"properties"`
		} `json:"crs"`
		SpatialReference *struct {
			Wkid       int    `json:"wkid"`
			LatestWkid int    `json:"latestWkid"`
			Wkt        string `json:"wkt"`
		} `json:"spatialReference"`
		Geometry json.RawMessage `json:"geometry"`
	}
	if err := json.Unmarshal(trim, &doc); err != nil {
		return nil, err
	}
	if doc.Features == nil && strings.EqualFold(doc.Type, "Feature") {
		doc.Features = []json.RawMessage{trim}
	}
	if doc.Features != nil && len(doc.Features) == 0 {
		return nil, fmt.Errorf("the layer has no features")
	}
	if doc.Features != nil {
		rs := &recordSet{}
		var geoms []Geom
		esri := false
		for _, fr := range doc.Features {
			var f struct {
				ID         json.RawMessage `json:"id"`
				Geometry   json.RawMessage `json:"geometry"`
				Properties json.RawMessage `json:"properties"`
				Attributes json.RawMessage `json:"attributes"`
			}
			json.Unmarshal(fr, &f)
			if f.Attributes != nil {
				esri = true
				rs.add(f.Attributes)
				geoms = append(geoms, parseEsriGeom(f.Geometry))
			} else {
				rs.add(f.Properties)
				geoms = append(geoms, parseGeoJSONGeom(f.Geometry))
			}
		}
		t := rs.table("geojson")
		if esri {
			t.Format = "esrijson"
		}
		t.Geoms = geoms
		switch {
		case doc.CRS != nil && doc.CRS.Properties.Name != "":
			t.FileCRS = doc.CRS.Properties.Name
		case doc.SpatialReference != nil && doc.SpatialReference.LatestWkid != 0:
			t.FileCRS = "EPSG:" + strconv.Itoa(doc.SpatialReference.LatestWkid)
		case doc.SpatialReference != nil && doc.SpatialReference.Wkid != 0:
			t.FileCRS = "EPSG:" + strconv.Itoa(doc.SpatialReference.Wkid)
		case doc.SpatialReference != nil && doc.SpatialReference.Wkt != "":
			t.FileCRS = doc.SpatialReference.Wkt
		case !esri:
			t.FileCRS = "EPSG:4326" // RFC 7946
		}
		return t, nil
	}
	// A plain object: {code: rate} becomes a two-column table, a wrapper holding one array is unwrapped.
	var m map[string]json.RawMessage
	json.Unmarshal(trim, &m)
	var srvErr struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(m["error"], &srvErr) == nil && srvErr.Message != "" {
		return nil, fmt.Errorf("the file holds an error from the server it came from: %s", srvErr.Message) // ArcGIS REST
	}
	for _, k := range keyOrder(trim) {
		if v := bytes.TrimSpace(m[k]); len(v) > 0 && v[0] == '[' {
			return readJSON(v)
		}
	}
	t := &Table{Cols: []string{"Code", "Rate"}, Format: "json"}
	for _, k := range keyOrder(trim) {
		t.Rows = append(t.Rows, []string{k, rawToString(m[k])})
	}
	return t, nil
}

func parseEsriGeom(raw json.RawMessage) Geom {
	if len(raw) == 0 || string(raw) == "null" {
		return Geom{}
	}
	var g struct {
		X      *float64      `json:"x"`
		Y      *float64      `json:"y"`
		Rings  [][][]float64 `json:"rings"`
		Points [][]float64   `json:"points"`
	}
	if json.Unmarshal(raw, &g) != nil {
		return Geom{}
	}
	switch {
	case g.X != nil && g.Y != nil:
		return pointGeom(*g.X, *g.Y)
	case len(g.Rings) > 0:
		var rings []Ring
		for _, r := range g.Rings {
			var ring Ring
			for _, q := range r {
				if len(q) >= 2 {
					ring = append(ring, Pt{q[0], q[1]})
				}
			}
			rings = append(rings, ring)
		}
		return polyGeom(assembleRings(rings))
	case len(g.Points) > 0 && len(g.Points[0]) >= 2:
		return pointGeom(g.Points[0][0], g.Points[0][1])
	}
	// Some exports wrap GeoJSON geometry in Esri features.
	return parseGeoJSONGeom(raw)
}

// ---- Shapefile ----

func readShapefile(files map[string][]byte) (*Table, error) {
	t := &Table{Format: "shapefile"}
	if prj := files[".prj"]; prj != nil {
		t.FileCRS = strings.TrimSpace(string(prj))
	}
	utf := false
	if cpg := strings.ToUpper(strings.TrimSpace(string(files[".cpg"]))); strings.Contains(cpg, "UTF") || cpg == "65001" {
		utf = true
	}
	if dbf := files[".dbf"]; dbf != nil {
		if err := readDBF(dbf, utf, t); err != nil {
			return nil, err
		}
	}
	if shp := files[".shp"]; shp != nil {
		geoms, err := readSHP(shp)
		if err != nil {
			return nil, err
		}
		t.Geoms = geoms
		if t.Rows == nil {
			t.Cols = []string{"FID"}
			for i := range geoms {
				t.Rows = append(t.Rows, []string{strconv.Itoa(i)})
			}
		}
		for len(t.Geoms) < len(t.Rows) {
			t.Geoms = append(t.Geoms, Geom{})
		}
		t.Geoms = t.Geoms[:len(t.Rows)]
	}
	if t.Cols == nil {
		return nil, fmt.Errorf("shapefile is missing its .shp and .dbf parts")
	}
	return t, nil
}

func readDBF(b []byte, utf bool, t *Table) error {
	if len(b) < 32 {
		return fmt.Errorf("the .dbf file is truncated")
	}
	n := int(binary.LittleEndian.Uint32(b[4:8]))
	hlen := int(binary.LittleEndian.Uint16(b[8:10]))
	rlen := int(binary.LittleEndian.Uint16(b[10:12]))
	type field struct{ off, size int }
	var fields []field
	off := 1
	for p := 32; p+32 <= len(b) && b[p] != 0x0D; p += 32 {
		name := string(bytes.TrimRight(b[p:p+11], "\x00 "))
		size := int(b[p+16])
		fields = append(fields, field{off, size})
		t.Cols = append(t.Cols, decodeText(name, utf))
		off += size
	}
	t.Cols = uniqueNames(t.Cols)
	for i := 0; i < n; i++ {
		start := hlen + i*rlen
		if start+rlen > len(b) {
			break
		}
		rec := b[start : start+rlen]
		row := make([]string, len(fields))
		for j, f := range fields {
			if f.off+f.size <= len(rec) {
				row[j] = decodeText(strings.TrimSpace(string(rec[f.off:f.off+f.size])), utf)
			}
		}
		t.Rows = append(t.Rows, row) // deleted records keep their slot so rows stay aligned with shapes
	}
	return nil
}

func decodeText(s string, utf bool) string {
	if utf || utf8.ValidString(s) {
		return s
	}
	return string(toUTF8([]byte(s)))
}

func readSHP(b []byte) ([]Geom, error) {
	if len(b) < 100 || binary.BigEndian.Uint32(b[0:4]) != 9994 {
		return nil, fmt.Errorf("not a shapefile (.shp)")
	}
	var out []Geom
	le := binary.LittleEndian
	f64 := func(p int) float64 { return math.Float64frombits(le.Uint64(b[p:])) }
	for p := 100; p+12 <= len(b); {
		clen := int(binary.BigEndian.Uint32(b[p+4:])) * 2
		c := p + 8
		end := c + clen
		if end > len(b) {
			break
		}
		typ := int(le.Uint32(b[c:]))
		g := Geom{}
		switch typ {
		case 1, 11, 21:
			g = pointGeom(f64(c+4), f64(c+12))
		case 8, 18, 28:
			if int(le.Uint32(b[c+36:])) > 0 {
				g = pointGeom(f64(c+40), f64(c+48))
			}
		case 5, 15, 25:
			nparts := int(le.Uint32(b[c+36:]))
			npts := int(le.Uint32(b[c+40:]))
			pp := c + 44
			ptBase := pp + 4*nparts
			if ptBase+16*npts > end {
				break
			}
			var rings []Ring
			for i := 0; i < nparts; i++ {
				s := int(le.Uint32(b[pp+4*i:]))
				e := npts
				if i+1 < nparts {
					e = int(le.Uint32(b[pp+4*(i+1):]))
				}
				ring := make(Ring, 0, max(e-s, 0))
				for k := s; k < e && k < npts; k++ {
					q := ptBase + 16*k
					ring = append(ring, Pt{f64(q), f64(q + 8)})
				}
				rings = append(rings, ring)
			}
			g = polyGeom(assembleRings(rings))
		}
		out = append(out, g)
		p = end
	}
	return out, nil
}

// ---- KML ----

func readKML(b []byte) (*Table, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	rs := &recordSet{}
	var geoms []Geom
	var stack []string
	var cur map[string]string
	var keys []string
	var polys []Poly
	var pt *Pt
	var dataName string
	text := ""
	in := func(name string) bool {
		for _, s := range stack {
			if s == name {
				return true
			}
		}
		return false
	}
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			stack = append(stack, e.Name.Local)
			text = ""
			switch e.Name.Local {
			case "Placemark":
				cur, keys, polys, pt = map[string]string{}, nil, nil, nil
			case "Polygon":
				if cur != nil {
					polys = append(polys, Poly{})
				}
			case "Data", "SimpleData":
				for _, a := range e.Attr {
					if a.Name.Local == "name" {
						dataName = a.Value
					}
				}
			}
		case xml.CharData:
			text += string(e)
		case xml.EndElement:
			name := e.Name.Local
			if cur != nil {
				set := func(k, v string) {
					if _, ok := cur[k]; !ok {
						keys = append(keys, k)
					}
					cur[k] = strings.TrimSpace(v)
				}
				switch {
				case name == "name" && len(stack) >= 2 && stack[len(stack)-2] == "Placemark":
					set("name", text)
				case name == "value" && in("Data"):
					set(dataName, text)
				case name == "SimpleData":
					set(dataName, text)
				case name == "coordinates":
					var ring Ring
					for _, tup := range strings.Fields(text) {
						f := strings.Split(tup, ",")
						if len(f) >= 2 {
							x, e1 := strconv.ParseFloat(f[0], 64)
							y, e2 := strconv.ParseFloat(f[1], 64)
							if e1 == nil && e2 == nil {
								ring = append(ring, Pt{x, y})
							}
						}
					}
					switch {
					case in("Polygon") && len(polys) > 0 && len(ring) >= 3:
						polys[len(polys)-1] = append(polys[len(polys)-1], ring)
					case in("Point") && len(ring) > 0 && pt == nil:
						pt = &ring[0]
					}
				case name == "Placemark":
					obj := map[string]string{}
					for _, k := range keys {
						obj[k] = cur[k]
					}
					raw, _ := json.Marshal(obj)
					// json.Marshal sorts keys; restore file order for the first record.
					if len(rs.cols) == 0 {
						for _, k := range keys {
							if rs.index == nil {
								rs.index = map[string]int{}
							}
							rs.index[k] = len(rs.cols)
							rs.cols = append(rs.cols, k)
						}
					}
					rs.add(raw)
					var g Geom
					var ps []Poly
					for _, p := range polys {
						if len(p) > 0 {
							ps = append(ps, p)
						}
					}
					if len(ps) > 0 {
						g = polyGeom(ps)
					} else if pt != nil {
						g = pointGeom(pt[0], pt[1])
					}
					geoms = append(geoms, g)
					cur = nil
				}
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text = ""
		}
	}
	if len(rs.rows) == 0 {
		return nil, fmt.Errorf("no placemarks found in the KML")
	}
	t := rs.table("kml")
	t.Geoms = geoms
	t.FileCRS = "EPSG:4326"
	return t, nil
}
