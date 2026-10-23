package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Dataset is an imported table plus how its columns are read.
type Dataset struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	T       *Table    `json:"table"`
	Geoms   []Geom    `json:"geoms,omitempty"` // file geometry (raw), kept for project files
	Roles   []ColRole `json:"roles"`
	CRSText string    `json:"crsText"` // user's choice (EPSG code or WKT); empty means automatic
	Join    Join      `json:"join"`
	Hint    Pt        `json:"hint"` // lon/lat near the data when imported, for UTM zone guesses

	raw     []Geom
	geo     []Geom
	crs     CRS
	crsSure bool
	guesses []CRSGuess
	located int
	polys   int
	locErr  string
	bbox    BBox
}

// Join says how a dataset's rows find their parcels.
type Join struct {
	Mode   string `json:"mode"`   // auto, key, spatial, none
	Col    string `json:"col"`    // this dataset's ID column
	Target string `json:"target"` // the parcel layer's ID column
}

type Rate struct {
	Code string   `json:"code"`
	Desc string   `json:"desc"`
	Rate *float64 `json:"rate"`
}

type Project struct {
	Datasets []*Dataset      `json:"datasets"`
	Rates    []Rate          `json:"rates"`
	RateUnit string          `json:"rateUnit"` // mill, percent, fraction
	Settings json.RawMessage `json:"settings,omitempty"`
	NextID   int             `json:"nextId"`
}

func (p *Project) dataset(id string) *Dataset {
	for _, d := range p.Datasets {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (d *Dataset) isRates() bool { return indexOfRole(d.Roles, RoleRate) >= 0 }

func (d *Dataset) hasValues() bool {
	for _, r := range d.Roles {
		switch r.Role {
		case RoleTotal, RoleLand, RoleImpr, RoleTax, RoleClassVal:
			return true
		}
	}
	return false
}

// prepare locates rows and resolves the coordinate system. hint is a lon/lat near the data.
func (d *Dataset) prepare(hint Pt) {
	d.raw, d.geo, d.guesses, d.locErr = nil, nil, nil, ""
	d.located, d.polys, d.crsSure = 0, 0, false
	d.bbox = emptyBBox()
	if d.isRates() {
		return
	}
	d.T.Geoms = d.Geoms
	raw, degrees, srid := locate(d.T, d.Roles)
	if raw == nil {
		return
	}
	d.raw = raw
	sample := sampleCoords(raw, 400)
	if len(sample) == 0 {
		d.locErr = "No row has a readable location"
		return
	}
	d.guesses = guessCRS(sample, hint)
	var err error
	switch {
	case d.CRSText != "":
		d.crs, err = crsFromString(d.CRSText)
		d.crsSure = true
	case degrees:
		d.crs, d.crsSure = crsWGS84, true
	case d.T.FileCRS != "":
		d.crs, err = crsFromString(d.T.FileCRS)
		d.crsSure = true
	case srid != 0:
		d.crs, err = crsFromString("EPSG:" + strconv.Itoa(srid))
		d.crsSure = true
	case len(d.guesses) > 0:
		d.crs = d.guesses[0].CRS
		d.crsSure = d.crs.Kind == "geo"
	default:
		err = fmt.Errorf("couldn't recognise the coordinate system, pick one below")
	}
	if err != nil {
		d.locErr = err.Error()
		d.crsSure = false
		if len(d.guesses) == 0 {
			return
		}
		d.crs = d.guesses[0].CRS
	}
	f := d.crs.Fn()
	d.geo = make([]Geom, len(raw))
	bad := 0
	for i, g := range raw {
		if !g.OK {
			continue
		}
		w := mapGeom(g, f)
		if math.IsNaN(w.Pt[0]) || math.Abs(w.Pt[0]) > 180 || math.Abs(w.Pt[1]) > 90 {
			bad++
			continue
		}
		if w.IsPoly() {
			d.polys++
		}
		d.geo[i] = w
		d.located++
		eachPt(w, d.bbox.Add)
	}
	if d.located == 0 && bad > 0 {
		d.locErr = "The coordinates don't land on Earth with " + d.crs.Name + ", pick another coordinate system"
		d.crsSure = false
	}
}

// ---------------------------------------------------------------- model

type Parcel struct {
	G        Geom
	C        Pt
	Area     float64 // m², 0 when unknown
	Value    float64
	Tax      float64
	HasValue bool
	HasTax   bool
	Key      string
	Label    string
	DS, Row  int
	Links    []rowRef
	Merged   bool // a strata unit or duplicate shape folded into the lot it sits on
}

type rowRef struct {
	DS, Row int
	How     string
}

type ClassInfo struct {
	Code  string  `json:"code"`
	Value float64 `json:"value"`
	Rows  int     `json:"rows"`
	Rated bool    `json:"rated"`
}

type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // done, todo, warn, optional
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
	Label  string `json:"label,omitempty"`
}

type Stats struct {
	Parcels       int        `json:"parcels"`
	WithValue     int        `json:"withValue"`
	WithTax       int        `json:"withTax"`
	WithArea      int        `json:"withArea"`
	TotalValue    float64    `json:"totalValue"`
	TotalTax      float64    `json:"totalTax"`
	TotalAcres    float64    `json:"totalAcres"`
	HasValue      bool       `json:"hasValue"`
	HasTax        bool       `json:"hasTax"`
	HasArea       bool       `json:"hasArea"`
	Polygons      bool       `json:"polygons"`
	Merged        int        `json:"merged"`
	HalfLandValue float64    `json:"halfLandValue"` // share of land producing half of all value
	HalfLandTax   float64    `json:"halfLandTax"`
	BBox          [4]float64 `json:"bbox"`
}

type Model struct {
	Parcels []Parcel
	Primary int
	Checks  []Check
	Stats   Stats
	Classes []ClassInfo
	ds      []*Dataset
}

type dsCols struct {
	key, total, land, impr, tax, class, area, unitCol, addr, house, street, year int
	wide                                                                         []int
	wideClass                                                                    []string
	areaUnit                                                                     string
	scores                                                                       map[string]int
	latest                                                                       float64
}

// old reports a row from an earlier roll year than the dataset's latest, which a multi-year export repeats.
func (c dsCols) old(d *Dataset, r int) bool {
	if c.year < 0 {
		return false
	}
	y, ok := parseNum(d.T.col(c.year, r))
	return ok && y < c.latest
}

func colsOf(d *Dataset) dsCols {
	c := dsCols{scores: map[string]int{}}
	idx := func(role string) int {
		i := indexOfRole(d.Roles, role)
		if i >= 0 {
			c.scores[role] = d.Roles[i].Score
		}
		return i
	}
	c.key, c.total, c.land, c.impr, c.tax = idx(RoleKey), idx(RoleTotal), idx(RoleLand), idx(RoleImpr), idx(RoleTax)
	c.class, c.area, c.unitCol = idx(RoleClass), idx(RoleArea), idx(RoleAreaUnit)
	c.addr, c.house, c.street = idx(RoleAddress), idx(RoleHouse), idx(RoleStreet)
	if c.year = idx(RoleYear); c.year >= 0 {
		for r := range d.T.Rows {
			if y, ok := parseNum(d.T.col(c.year, r)); ok && y > c.latest {
				c.latest = y
			}
		}
	}
	for i, r := range d.Roles {
		if r.Role == RoleClassVal {
			c.wide = append(c.wide, i)
			c.wideClass = append(c.wideClass, r.Class)
		}
	}
	if c.area >= 0 {
		c.areaUnit = d.Roles[c.area].Unit
		if c.areaUnit == "" || c.areaUnit == "auto" {
			if c.unitCol < 0 {
				st := statColumn(d.T, c.area)
				c.areaUnit = "sqft"
				if st.median() < 100 {
					c.areaUnit = "acre"
				}
			} else {
				c.areaUnit = "auto"
			}
		}
	}
	return c
}

type rateBook struct {
	m       map[string]float64
	factor  float64
	def     *float64
	anyRate bool
}

func newRateBook(p *Project) rateBook {
	b := rateBook{m: map[string]float64{}, factor: 0.001}
	switch p.RateUnit {
	case "percent":
		b.factor = 0.01
	case "fraction":
		b.factor = 1
	}
	for _, r := range p.Rates {
		if r.Rate == nil {
			continue
		}
		b.anyRate = true
		v := *r.Rate * b.factor
		if r.Code == "*" {
			b.def = &v
			continue
		}
		b.m[normKey(r.Code)] = v
		if r.Desc != "" {
			if _, ok := b.m[normKey(r.Desc)]; !ok {
				b.m[normKey(r.Desc)] = v
			}
		}
	}
	return b
}

func (b rateBook) rate(code string) (float64, bool) {
	if v, ok := b.m[normKey(code)]; ok {
		return v, true
	}
	if b.def != nil {
		return *b.def, true
	}
	return 0, false
}

type rowVal struct {
	val, tax       float64
	vScore, tScore int
	hasVal, hasTax bool
	area           float64
}

type contrib struct {
	ds int
	rowVal
}

func (m *Model) evalRow(d *Dataset, c dsCols, r int, rb rateBook, classes map[string]*ClassInfo) rowVal {
	var out rowVal
	num := func(i int) (float64, bool) {
		if i < 0 {
			return 0, false
		}
		return parseNum(d.T.col(i, r))
	}
	total, hasTotal := num(c.total)
	land, hasLand := num(c.land)
	impr, hasImpr := num(c.impr)
	wideSum, hasWide := 0.0, false
	wideTax, wideTaxOK := 0.0, true
	for k, i := range c.wide {
		if v, ok := num(i); ok {
			hasWide = true
			wideSum += v
			if v != 0 {
				ci := classes[c.wideClass[k]]
				if ci == nil {
					ci = &ClassInfo{Code: c.wideClass[k]}
					classes[c.wideClass[k]] = ci
				}
				ci.Value += v
				ci.Rows++
				if rt, ok := rb.rate(c.wideClass[k]); ok {
					wideTax += v * rt
				} else {
					wideTaxOK = false
				}
			}
		}
	}
	// Taxable land and buildings beat a market or assessed total in the same row.
	split := max(c.scores[RoleLand], c.scores[RoleImpr])
	splitFirst := (hasLand || hasImpr) && split > 8 && split > c.scores[RoleTotal]
	switch {
	case hasTotal && !splitFirst:
		out.val, out.vScore, out.hasVal = total, c.scores[RoleTotal]+1, true
	case hasWide:
		out.val, out.vScore, out.hasVal = wideSum, 9, true
	case hasLand || hasImpr:
		out.val, out.vScore, out.hasVal = land+impr, 6, true
		if splitFirst {
			out.vScore = split + 1
		}
	}
	cls := ""
	if c.class >= 0 {
		cls = strings.TrimSpace(d.T.col(c.class, r))
		if cls != "" && out.hasVal {
			ci := classes[cls]
			if ci == nil {
				ci = &ClassInfo{Code: cls}
				classes[cls] = ci
			}
			ci.Value += out.val
			ci.Rows++
		}
	}
	if t, ok := num(c.tax); ok {
		out.tax, out.tScore, out.hasTax = t, 20, true
	} else if hasWide && rb.anyRate {
		out.tax, out.tScore, out.hasTax = wideTax, 6, true
		if !wideTaxOK {
			out.tScore = 5
		}
	} else if out.hasVal && rb.anyRate {
		if rt, ok := rb.rate(cls); ok {
			out.tax, out.tScore, out.hasTax = out.val*rt, 6, true
		}
	}
	if a, ok := num(c.area); ok && a > 0 {
		unit := c.areaUnit
		if unit == "auto" {
			unit = unitFromWord(d.T.col(c.unitCol, r))
		}
		out.area = a * areaFactor(unit, d.crs.Unit)
	}
	return out
}

// buildModel joins every dataset onto the parcel layer and works out value, tax and area per parcel.
func buildModel(p *Project) *Model {
	m := &Model{Primary: -1, ds: p.Datasets}
	rb := newRateBook(p)
	classes := map[string]*ClassInfo{}

	for i, d := range p.Datasets {
		if d.located == 0 || d.isRates() {
			continue
		}
		if m.Primary < 0 {
			m.Primary = i
			continue
		}
		pd := p.Datasets[m.Primary]
		if d.polys > pd.polys || (d.polys == pd.polys && d.located > pd.located) {
			m.Primary = i
		}
	}
	if m.Primary < 0 {
		m.Checks = m.checklist(p, rb, nil)
		return m
	}
	prim := p.Datasets[m.Primary]
	pc := colsOf(prim)
	rowParcel := make([]int, len(prim.T.Rows))
	for r := range prim.T.Rows {
		rowParcel[r] = -1
		g := prim.geo[r]
		if !g.OK || pc.old(prim, r) {
			continue
		}
		pa := Parcel{G: g, C: labelPoint(g), DS: m.Primary, Row: r}
		if g.IsPoly() {
			pa.Area = geomAreaGeo(g)
		}
		if pc.key >= 0 {
			pa.Key = prim.T.col(pc.key, r)
		}
		pa.Label = labelOf(prim, pc, r)
		rowParcel[r] = len(m.Parcels)
		m.Parcels = append(m.Parcels, pa)
	}
	contribs := make([][]contrib, len(m.Parcels))
	add := func(pi, ds int, v rowVal, share float64) {
		v.val *= share
		v.tax *= share
		for k := range contribs[pi] {
			c := &contribs[pi][k]
			if c.ds == ds {
				c.val += v.val
				c.tax += v.tax
				c.hasVal = c.hasVal || v.hasVal
				c.hasTax = c.hasTax || v.hasTax
				c.vScore = max(c.vScore, v.vScore)
				c.tScore = max(c.tScore, v.tScore)
				c.area += v.area
				return
			}
		}
		contribs[pi] = append(contribs[pi], contrib{ds, v})
	}

	var grid *spatialIndex
	links := map[string]*linkStat{}
	for di, d := range p.Datasets {
		if d.isRates() || !d.hasValues() && di != m.Primary {
			continue
		}
		c := colsOf(d)
		if di == m.Primary {
			for r := range d.T.Rows {
				if pi := rowParcel[r]; pi >= 0 {
					v := m.evalRow(d, c, r, rb, classes)
					add(pi, di, v, 1)
					if v.area > 0 && m.Parcels[pi].Area == 0 {
						m.Parcels[pi].Area = v.area
					}
				}
			}
			continue
		}
		mode := d.Join.Mode
		if mode == "" {
			mode = "auto"
		}
		if mode == "none" {
			continue // "Don't link": nothing to report either
		}
		ls := &linkStat{rows: len(d.T.Rows), year: int(c.latest), hit: map[int]bool{}}
		links[d.ID] = ls
		// Key join.
		var byKey map[string][]int
		ki, kj := -1, -1
		if mode == "auto" || mode == "key" {
			ki, kj = colIndex(prim.T, d.Join.Target), colIndex(d.T, d.Join.Col)
			if ki < 0 || kj < 0 {
				g := bestKeyJoin(prim.T, prim.Roles, d.T, d.Roles)
				if g.Rate >= 0.05 || mode == "key" {
					ki, kj = g.ColA, g.ColB
				}
			}
			if ki >= 0 && kj >= 0 {
				ls.keyA, ls.keyB = prim.T.Cols[ki], d.T.Cols[kj]
				byKey = map[string][]int{}
				for r := range prim.T.Rows {
					if pi := rowParcel[r]; pi >= 0 {
						k := normKey(prim.T.col(ki, r))
						if k != "" {
							byKey[k] = append(byKey[k], pi)
						}
					}
				}
			}
		}
		spatial := (mode == "auto" || mode == "spatial") && prim.polys > 0 && d.located > 0
		if spatial && grid == nil {
			grid = newSpatialIndex(m.Parcels)
		}
		for r := range d.T.Rows {
			if c.old(d, r) {
				ls.empty++ // not counted as a missed link
				ls.old++
				continue
			}
			v := m.evalRow(d, c, r, rb, classes)
			if !v.hasVal && !v.hasTax {
				ls.empty++
				continue
			}
			if byKey != nil {
				if ps := byKey[normKey(d.T.col(kj, r))]; len(ps) > 0 {
					shares := areaShares(m.Parcels, ps)
					for k, pi := range ps {
						add(pi, di, v, shares[k])
						m.Parcels[pi].Links = append(m.Parcels[pi].Links, rowRef{di, r, "key"})
						ls.hit[pi] = true
					}
					ls.byKey++
					continue
				}
			}
			if spatial && d.geo[r].OK {
				if pi := grid.find(labelPoint(d.geo[r])); pi >= 0 {
					add(pi, di, v, 1)
					m.Parcels[pi].Links = append(m.Parcels[pi].Links, rowRef{di, r, "location"})
					ls.hit[pi] = true
					ls.bySpace++
					continue
				}
			}
			ls.missed++
		}
	}

	// Resolve: per parcel, value and tax each come from the best-scoring source that has them.
	for pi := range m.Parcels {
		pa := &m.Parcels[pi]
		bv, bt := -1, -1
		for k, c := range contribs[pi] {
			if c.hasVal && (bv < 0 || c.vScore > contribs[pi][bv].vScore) {
				bv = k
			}
			if c.hasTax && (bt < 0 || c.tScore > contribs[pi][bt].tScore) {
				bt = k
			}
			if pa.Area == 0 && c.area > 0 {
				pa.Area = c.area
			}
		}
		if bv >= 0 {
			pa.Value, pa.HasValue = contribs[pi][bv].val, true
		}
		if bt >= 0 {
			pa.Tax, pa.HasTax = contribs[pi][bt].tax, true
		}
	}
	st := &m.Stats
	st.Polygons = prim.polys > 0
	if st.Polygons {
		if grid == nil {
			grid = newSpatialIndex(m.Parcels)
		}
		st.Merged = m.mergeStacked(grid)
	}
	for pi := range m.Parcels {
		pa := &m.Parcels[pi]
		if pa.Merged {
			continue
		}
		st.Parcels++
		if pa.HasValue {
			st.WithValue++
			st.TotalValue += pa.Value
		}
		if pa.HasTax {
			st.WithTax++
			st.TotalTax += pa.Tax
		}
		if pa.Area > 0 {
			st.WithArea++
			st.TotalAcres += pa.Area / 4046.8564224
		}
	}
	st.HasValue, st.HasTax, st.HasArea = st.WithValue > 0, st.WithTax > 0, st.WithArea > 0
	st.HalfLandValue = halfLand(m.Parcels, func(p *Parcel) (float64, bool) { return p.Value, p.HasValue })
	st.HalfLandTax = halfLand(m.Parcels, func(p *Parcel) (float64, bool) { return p.Tax, p.HasTax })
	b := emptyBBox()
	for _, pa := range m.Parcels {
		b.Add(pa.C)
	}
	if b.Valid() {
		st.BBox = [4]float64{b.MinX, b.MinY, b.MaxX, b.MaxY}
	}
	for _, ci := range classes {
		_, ci.Rated = rb.m[normKey(ci.Code)]
		m.Classes = append(m.Classes, *ci)
	}
	sort.Slice(m.Classes, func(i, j int) bool { return m.Classes[i].Value > m.Classes[j].Value })
	m.Checks = m.checklist(p, rb, links)
	return m
}

type linkStat struct {
	rows, byKey, bySpace, missed, empty, old, year int
	hit                                            map[int]bool // parcels that got a record
	keyA, keyB                                     string
}

func colIndex(t *Table, name string) int {
	if name == "" {
		return -1
	}
	for i, c := range t.Cols {
		if c == name {
			return i
		}
	}
	return -1
}

func labelOf(d *Dataset, c dsCols, r int) string {
	if c.addr >= 0 {
		if s := strings.TrimSpace(d.T.col(c.addr, r)); s != "" {
			return s
		}
	}
	if c.house >= 0 || c.street >= 0 {
		s := strings.TrimSpace(strings.TrimSpace(d.T.col(c.house, r)) + " " + strings.TrimSpace(d.T.col(c.street, r)))
		if s != "" {
			return s
		}
	}
	if c.key >= 0 {
		return d.T.col(c.key, r)
	}
	return ""
}

// areaShares splits a value over several parcels that share an ID, by area.
func areaShares(ps []Parcel, idx []int) []float64 {
	out := make([]float64, len(idx))
	total := 0.0
	for _, i := range idx {
		total += ps[i].Area
	}
	for k, i := range idx {
		if total > 0 {
			out[k] = ps[i].Area / total
		} else {
			out[k] = 1 / float64(len(idx))
		}
	}
	return out
}

// halfLand is the share of land that produces half of the total (parcels ranked by productivity).
func halfLand(ps []Parcel, metric func(*Parcel) (float64, bool)) float64 {
	type row struct{ per, v, a float64 }
	var rows []row
	totV, totA := 0.0, 0.0
	for i := range ps {
		v, ok := metric(&ps[i])
		if !ok || ps[i].Merged || ps[i].Area <= 0 || v < 0 {
			continue
		}
		rows = append(rows, row{v / ps[i].Area, v, ps[i].Area})
		totV += v
		totA += ps[i].Area
	}
	if totV <= 0 || totA <= 0 {
		return 0
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].per > rows[j].per })
	acc, land := 0.0, 0.0
	for _, r := range rows {
		acc += r.v
		land += r.a
		if acc >= totV/2 {
			break
		}
	}
	return land / totA
}

// ---------------------------------------------------------------- spatial index

type spatialIndex struct {
	cell  float64
	cells map[[2]int][]int
	ps    []Parcel
}

func newSpatialIndex(ps []Parcel) *spatialIndex {
	s := &spatialIndex{cell: 0.002, cells: map[[2]int][]int{}, ps: ps}
	for i, p := range ps {
		if !p.G.IsPoly() {
			continue
		}
		b := geomBBox(p.G)
		x0, y0 := int(math.Floor(b.MinX/s.cell)), int(math.Floor(b.MinY/s.cell))
		x1, y1 := int(math.Floor(b.MaxX/s.cell)), int(math.Floor(b.MaxY/s.cell))
		if (x1-x0+1)*(y1-y0+1) > 2500 {
			continue // ponytail: parcels over ~20 km² skip the index, never matched spatially
		}
		for x := x0; x <= x1; x++ {
			for y := y0; y <= y1; y++ {
				k := [2]int{x, y}
				s.cells[k] = append(s.cells[k], i)
			}
		}
	}
	return s
}

// find returns the smallest parcel containing p (strata lots sit on top of their common property).
func (s *spatialIndex) find(p Pt) int {
	best, bestA := -1, math.Inf(1)
	for _, i := range s.cells[[2]int{int(math.Floor(p[0] / s.cell)), int(math.Floor(p[1] / s.cell))}] {
		if pointInGeom(p, s.ps[i].G) && s.ps[i].Area < bestA {
			best, bestA = i, s.ps[i].Area
		}
	}
	return best
}

// mergeStacked folds parcels that sit inside a bigger parcel (strata units drawn over their common
// property, air-space parcels) or exactly on top of another into that lot, so a 40-unit townhouse
// complex reads as one lot with 40 units of value instead of 40 slivers with absurd per-acre values.
func (m *Model) mergeStacked(grid *spatialIndex) int {
	parent := make([]int, len(m.Parcels))
	for i := range parent {
		parent[i] = -1
		if m.Parcels[i].G.IsPoly() && m.Parcels[i].Area > 0 {
			parent[i] = grid.container(i)
		}
	}
	root := func(i int) int {
		for steps := 0; parent[i] >= 0 && steps < 32; steps++ {
			i = parent[i]
		}
		return i
	}
	n := 0
	for i := range m.Parcels {
		if parent[i] < 0 {
			continue
		}
		r := root(i)
		if r == i {
			continue
		}
		c, p := &m.Parcels[r], &m.Parcels[i]
		if p.HasValue {
			c.Value += p.Value
			c.HasValue = true
		}
		if p.HasTax {
			c.Tax += p.Tax
			c.HasTax = true
		}
		c.Links = append(c.Links, rowRef{p.DS, p.Row, "unit on this lot"})
		c.Links = append(c.Links, p.Links...)
		p.Merged = true
		n++
	}
	return n + m.mergeSameAddress()
}

// mergeSameAddress combines neighbouring parcels that share a civic address, the way strata
// complexes drawn as one shape per unit appear in many parcel layers.
func (m *Model) mergeSameAddress() int {
	groups := map[string][]int{}
	for i := range m.Parcels {
		p := &m.Parcels[i]
		lbl := strings.ToUpper(strings.Join(strings.Fields(p.Label), " "))
		if p.Merged || !p.G.IsPoly() || lbl == "" || lbl[0] < '1' || lbl[0] > '9' || lbl == p.Key || !strings.ContainsAny(lbl, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			continue // needs a real civic number and a street
		}
		groups[lbl] = append(groups[lbl], i)
	}
	n := 0
	for _, idx := range groups {
		if len(idx) < 2 {
			continue
		}
		root := idx[0]
		for _, i := range idx {
			if m.Parcels[i].Area > m.Parcels[root].Area {
				root = i
			}
		}
		r := &m.Parcels[root]
		loc := newLocal(r.C[0], r.C[1])
		for _, i := range idx {
			p := &m.Parcels[i]
			if i == root {
				continue
			}
			if q := loc.Fwd(p.C); math.Hypot(q[0], q[1]) > 300 {
				continue // same address in another part of town
			}
			r.G.Polys = append(r.G.Polys, p.G.Polys...)
			r.Area += p.Area
			if p.HasValue {
				r.Value += p.Value
				r.HasValue = true
			}
			if p.HasTax {
				r.Tax += p.Tax
				r.HasTax = true
			}
			r.Links = append(r.Links, rowRef{p.DS, p.Row, "unit at this address"})
			r.Links = append(r.Links, p.Links...)
			p.Merged = true
			n++
		}
	}
	return n
}

// container is the smallest parcel that holds parcel i's label point and is clearly larger
// (or the same shape listed earlier), or -1.
func (s *spatialIndex) container(i int) int {
	p := &s.ps[i]
	pt := p.C
	best, bestA := -1, math.Inf(1)
	for _, j := range s.cells[[2]int{int(math.Floor(pt[0] / s.cell)), int(math.Floor(pt[1] / s.cell))}] {
		if j == i {
			continue
		}
		q := &s.ps[j]
		bigger := q.Area > p.Area*1.2
		same := j < i && math.Abs(q.Area-p.Area) <= p.Area*0.01 && math.Abs(q.C[0]-pt[0]) < 1e-5 && math.Abs(q.C[1]-pt[1]) < 1e-5
		if (bigger || same) && q.Area < bestA && pointInGeom(pt, q.G) {
			best, bestA = j, q.Area
		}
	}
	return best
}

// ---------------------------------------------------------------- checklist

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commas(n) + " " + many
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (m *Model) checklist(p *Project, rb rateBook, links map[string]*linkStat) []Check {
	var out []Check
	// 1. Locations
	loc := Check{ID: "locations", Title: "Parcel locations", Status: "todo", Action: "import", Label: "Import",
		Detail: "Import parcel shapes (Shapefile, GeoJSON, KML, Esri JSON) or a table with latitude/longitude, X/Y or WKT columns."}
	for _, d := range p.Datasets {
		if d.raw != nil && (d.locErr != "" || !d.crsSure) {
			msg := d.locErr
			if msg == "" {
				msg = "Pracre guessed " + d.crs.Name + " for " + d.T.Name + ". Check the parcels land in the right place."
			}
			loc = Check{ID: "locations", Title: "Parcel locations", Status: "warn", Detail: msg, Action: "dataset:" + d.ID, Label: "Check"}
		}
	}
	if m.Primary >= 0 && loc.Status == "todo" {
		d := p.Datasets[m.Primary]
		if d.polys > 0 {
			detail := plural(d.polys, "parcel shape", "parcel shapes") + " from " + d.T.Name + " (" + d.crs.Name + ")."
			if m.Stats.Merged > 0 {
				detail += " " + plural(m.Stats.Merged, "strata or condo unit was", "strata or condo units and stacked shapes were") + " folded into the lots they sit on."
			}
			loc = Check{ID: "locations", Title: "Parcel locations", Status: "done", Detail: detail, Action: "dataset:" + d.ID, Label: "Review"}
		} else {
			loc = Check{ID: "locations", Title: "Parcel locations", Status: "done",
				Detail: plural(d.located, "point", "points") + " from " + d.T.Name + ". Parcel shapes would give exact lot areas (optional).", Action: "import", Label: "Add shapes"}
		}
	}
	out = append(out, loc)

	// 2. Values
	st := m.Stats
	val := Check{ID: "values", Title: "Property values", Status: "todo", Action: "import", Label: "Import",
		Detail: "Import assessed values or tax amounts per property (CSV, Excel, or columns in the parcel file)."}
	anyValues, valuesID := false, ""
	for _, d := range p.Datasets {
		if d.hasValues() {
			anyValues = true
			if valuesID == "" {
				valuesID = d.ID
			}
		}
	}
	switch {
	case st.HasValue || st.HasTax:
		parts := []string{}
		if st.HasValue {
			parts = append(parts, "values for "+plural(st.WithValue, "parcel", "parcels"))
		}
		if st.HasTax {
			parts = append(parts, "tax for "+plural(st.WithTax, "parcel", "parcels"))
		}
		val = Check{ID: "values", Title: "Property values", Status: "done", Detail: upperFirst(strings.Join(parts, ", ")) + "."}
	case anyValues && m.Primary < 0:
		val = Check{ID: "values", Title: "Property values", Status: "done", Detail: "Values are loaded and waiting for parcel locations."}
	case anyValues:
		val = Check{ID: "values", Title: "Property values", Status: "todo", Action: "dataset:" + valuesID, Label: "Link",
			Detail: "Values are loaded but none reached a parcel. Check how the values file links to the parcels."}
	}
	out = append(out, val)

	// 3. Links between files
	for _, d := range p.Datasets {
		ls := links[d.ID]
		if ls == nil {
			continue
		}
		matched := ls.byKey + ls.bySpace
		total := ls.rows - ls.empty
		if total <= 0 {
			continue
		}
		rate := float64(matched) / float64(total)
		how := []string{}
		if ls.byKey > 0 {
			how = append(how, commas(ls.byKey)+" by "+ls.keyB+" = "+ls.keyA)
		}
		if ls.bySpace > 0 {
			how = append(how, commas(ls.bySpace)+" by location")
		}
		c := Check{ID: "link:" + d.ID, Title: "Linked " + d.T.Name, Action: "dataset:" + d.ID, Label: "Link"}
		c.Detail = fmt.Sprintf("%s of %s records matched a parcel (%.0f%%)", commas(matched), commas(total), rate*100)
		if len(how) > 0 {
			c.Detail += ": " + strings.Join(how, ", ")
		}
		c.Detail += "."
		if ls.old > 0 {
			c.Detail += fmt.Sprintf(" Only %d values are used; %s from earlier years are left out.", ls.year, plural(ls.old, "row", "rows"))
		}
		cover := float64(len(ls.hit)) / float64(max(len(m.Parcels), 1))
		switch {
		case rate >= 0.9:
			c.Status = "done"
		case cover >= 0.9:
			// A county-wide roll against a city's parcels: the link is fine, the file is just bigger.
			c.Status = "done"
			c.Detail += fmt.Sprintf(" The other records are outside the parcel layer, which has %.0f%% of its parcels linked.", cover*100)
		case rate >= 0.5:
			c.Status = "warn"
			c.Detail += " Check the ID columns if this seems low."
		default:
			c.Status = "todo"
			c.Detail += " Pick the ID column both files share."
		}
		out = append(out, c)
	}

	// 4. Tax rates
	unrated := 0
	for _, c := range m.Classes {
		if _, ok := rb.rate(c.Code); !ok && c.Value > 0 {
			unrated++
		}
	}
	directTax := false
	for _, d := range p.Datasets {
		if indexOfRole(d.Roles, RoleTax) >= 0 {
			directTax = true
		}
	}
	rates := Check{ID: "rates", Title: "Tax rates", Action: "rates", Label: "Edit rates"}
	switch {
	case directTax:
		rates.Status, rates.Detail = "done", "Tax amounts come straight from the data."
	case len(m.Classes) > 0 && unrated == 0 && rb.anyRate:
		rates.Status, rates.Detail = "done", plural(len(m.Classes), "tax class has", "tax classes have")+" rates."
	case len(m.Classes) > 0 && rb.anyRate:
		rates.Status, rates.Detail = "warn", plural(unrated, "tax class still needs", "tax classes still need")+" a rate."
	case len(m.Classes) > 0:
		rates.Status, rates.Detail = "optional", "Add rates for the "+plural(len(m.Classes), "tax class", "tax classes")+" to map tax revenue per acre."
	case rb.def != nil:
		rates.Status, rates.Detail = "done", "One rate applies to every property."
	case rb.anyRate && st.HasValue:
		rates.Status, rates.Detail = "warn", "No column gives each property a tax class to match these rates. Pick the tax class column in the import window, or add a default rate."
	case rb.anyRate:
		rates.Status, rates.Detail = "done", "Rates are ready for the tax classes in your values."
	default:
		rates.Status, rates.Detail = "optional", "Add your mill rate (or one per tax class) to map tax revenue per acre."
	}
	out = append(out, rates)

	// 5. Lot areas
	area := Check{ID: "area", Title: "Lot areas"}
	switch {
	case m.Primary < 0:
		area.Status, area.Detail = "todo", "Measured from parcel shapes, or read from a lot size column."
	case st.Polygons:
		area.Status, area.Detail = "done", "Measured from the parcel shapes."
	case st.HasArea:
		area.Status, area.Detail = "done", fmt.Sprintf("Read from a lot size column for %s.", plural(st.WithArea, "parcel", "parcels"))
	default:
		area.Status, area.Detail = "warn", "No lot areas, so tiles divide by their own area. Add parcel shapes or a lot size column for exact acres."
		area.Action, area.Label = "import", "Import"
	}
	out = append(out, area)
	out = append(out, Check{ID: "basemap", Title: "Base map", Status: "done", Detail: "OpenStreetMap, loaded online.", Action: "basemap", Label: "Change"})
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---------------------------------------------------------------- layers

type LayerReq struct {
	Mode    string  `json:"mode"`   // parcels, hex, square
	Size    float64 `json:"size"`   // tile width in metres
	Metric  string  `json:"metric"` // value, tax
	Per     string  `json:"per"`    // acre, hectare
	Basis   string  `json:"basis"`  // net, gross
	Classes int     `json:"classes"`
	Method  string  `json:"method"` // quantile, jenks, equal, log
	Gap     float64 `json:"gap"`
}

type Feat struct {
	G     Geom
	V     float64 // per area, NaN unknown
	T     float64 // total metric
	A     float64 // area in acres or hectares
	N     int
	C     int
	P     int // parcel index (parcels mode) or -1
	Label string
}

type Layer struct {
	Req     LayerReq
	Feats   []Feat
	Breaks  []float64
	Counts  []int
	Members [][]int // tile mode: parcels in each feature
	P99     float64
	Max     float64
}

func (r *LayerReq) norm() {
	if r.Mode == "" {
		r.Mode = "hex"
	}
	if r.Size <= 0 {
		r.Size = 200
	}
	r.Size = math.Max(10, math.Min(r.Size, 20000))
	if r.Classes < 2 || r.Classes > 12 {
		r.Classes = 7
	}
	if r.Per != "hectare" {
		r.Per = "acre"
	}
	if r.Gap < 0 || r.Gap > 0.5 {
		r.Gap = 0.08
	}
}

func (r LayerReq) perM2() float64 {
	if r.Per == "hectare" {
		return 10000
	}
	return 4046.8564224
}

func (m *Model) metric(p *Parcel, which string) (float64, bool) {
	if which == "tax" {
		return p.Tax, p.HasTax
	}
	return p.Value, p.HasValue
}

func (m *Model) layer(req LayerReq) *Layer {
	req.norm()
	if req.Metric == "tax" && !m.Stats.HasTax {
		req.Metric = "value"
	}
	if req.Metric != "tax" && !m.Stats.HasValue && m.Stats.HasTax {
		req.Metric = "tax"
	}
	L := &Layer{Req: req}
	unit := req.perM2()
	if req.Mode == "parcels" {
		for i := range m.Parcels {
			p := &m.Parcels[i]
			v, ok := m.metric(p, req.Metric)
			if !ok || p.Merged {
				continue
			}
			f := Feat{G: p.G, T: v, P: i, N: 1, Label: p.Label, V: math.NaN()}
			if p.Area > 0 {
				f.A = p.Area / unit
				f.V = v / f.A
			}
			L.Feats = append(L.Feats, f)
		}
	} else {
		m.tiles(L, unit)
	}
	L.classify()
	return L
}

func (m *Model) tiles(L *Layer, unit float64) {
	req := L.Req
	b := m.Stats.BBox
	loc := newLocal((b[0]+b[2])/2, (b[1]+b[3])/2)
	w := req.Size
	type bin struct {
		cx, cy    float64
		sum, sumA float64 // metric total, and metric total of parcels with area
		area      float64
		n         int
		members   []int
	}
	bins := map[[2]int]*bin{}
	var order [][2]int
	s := w / math.Sqrt(3) // hex circumradius for a flat-to-flat width w
	for i := range m.Parcels {
		p := &m.Parcels[i]
		v, ok := m.metric(p, req.Metric)
		if !ok || p.Merged {
			continue
		}
		q := loc.Fwd(p.C)
		var key [2]int
		var cx, cy float64
		if req.Mode == "square" {
			key = [2]int{int(math.Floor(q[0] / w)), int(math.Floor(q[1] / w))}
			cx, cy = (float64(key[0])+0.5)*w, (float64(key[1])+0.5)*w
		} else {
			fq := (math.Sqrt(3)/3*q[0] - q[1]/3) / s
			fr := (2.0 / 3 * q[1]) / s
			rq, rr := hexRound(fq, fr)
			key = [2]int{rq, rr}
			cx, cy = s*(math.Sqrt(3)*float64(rq)+math.Sqrt(3)/2*float64(rr)), s*1.5*float64(rr)
		}
		bn := bins[key]
		if bn == nil {
			bn = &bin{cx: cx, cy: cy}
			bins[key] = bn
			order = append(order, key)
		}
		bn.sum += v
		bn.n++
		if p.Area > 0 {
			bn.sumA += v
			bn.area += p.Area
		}
		bn.members = append(bn.members, i)
	}
	tileArea := w * w
	if req.Mode != "square" {
		tileArea = 3 * math.Sqrt(3) / 2 * s * s
	}
	k := 1 - req.Gap
	for _, key := range order {
		bn := bins[key]
		var ring Ring
		if req.Mode == "square" {
			h := w / 2 * k
			for _, d := range [][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}, {-1, -1}} {
				ring = append(ring, loc.Inv(Pt{bn.cx + d[0]*h, bn.cy + d[1]*h}))
			}
		} else {
			for j := 0; j <= 6; j++ {
				a := rad(30 + 60*float64(j%6))
				ring = append(ring, loc.Inv(Pt{bn.cx + s*k*math.Cos(a), bn.cy + s*k*math.Sin(a)}))
			}
		}
		f := Feat{G: Geom{OK: true, Polys: []Poly{{ring}}}, T: bn.sum, N: bn.n, P: -1, Label: m.tileLabel(bn.members, req.Metric)}
		if req.Basis != "gross" && bn.area > 0 {
			f.A = bn.area / unit
			f.V = bn.sumA / f.A
		} else {
			f.A = tileArea / unit
			f.V = bn.sum / f.A
		}
		L.Feats = append(L.Feats, f)
		L.Members = append(L.Members, bn.members)
	}
}

// tileLabel names a tile after the parcel that contributes most to it.
func (m *Model) tileLabel(members []int, which string) string {
	best, bestV := "", math.Inf(-1)
	for _, i := range members {
		p := &m.Parcels[i]
		v, _ := m.metric(p, which)
		lbl := p.Label
		if lbl == "" {
			lbl = p.Key
		}
		if lbl != "" && v > bestV {
			best, bestV = lbl, v
		}
	}
	if best == "" {
		return ""
	}
	if len(members) == 1 {
		return best
	}
	return "Around " + best
}

func hexRound(q, r float64) (int, int) {
	x, z := q, r
	y := -x - z
	rx, ry, rz := math.Round(x), math.Round(y), math.Round(z)
	dx, dy, dz := math.Abs(rx-x), math.Abs(ry-y), math.Abs(rz-z)
	if dx > dy && dx > dz {
		rx = -ry - rz
	} else if dy <= dz {
		rz = -rx - ry
	}
	return int(rx), int(rz)
}

// classify computes class breaks over the positive values and assigns each feature a class.
// Zero and negative (exempt) features get class -1, features without an area -2.
func (L *Layer) classify() {
	var vals []float64
	for _, f := range L.Feats {
		if !math.IsNaN(f.V) && f.V > 0 {
			vals = append(vals, f.V)
		}
	}
	sort.Float64s(vals)
	k := L.Req.Classes
	if len(vals) > 0 {
		L.Max = vals[len(vals)-1]
		L.P99 = vals[int(float64(len(vals)-1)*0.99)]
		L.Breaks = breaks(vals, k, L.Req.Method)
	}
	L.Counts = make([]int, max(len(L.Breaks)-1, 0))
	for i := range L.Feats {
		f := &L.Feats[i]
		switch {
		case math.IsNaN(f.V):
			f.C = -2
		case f.V <= 0:
			f.C = -1
		default:
			c := sort.SearchFloat64s(L.Breaks[1:len(L.Breaks)-1], f.V)
			// SearchFloat64s finds the first break >= v; values equal to a break go to the upper class.
			if c < len(L.Breaks)-2 && f.V >= L.Breaks[c+1] {
				c++
			}
			f.C = min(c, len(L.Counts)-1)
			L.Counts[f.C]++
		}
	}
}

// breaks returns k+1 boundaries (min ... max) for sorted positive values.
func breaks(sorted []float64, k int, method string) []float64 {
	n := len(sorted)
	lo, hi := sorted[0], sorted[n-1]
	if lo == hi {
		return []float64{lo, hi}
	}
	b := make([]float64, k+1)
	b[0], b[k] = lo, hi
	switch method {
	case "equal":
		for i := 1; i < k; i++ {
			b[i] = lo + (hi-lo)*float64(i)/float64(k)
		}
	case "log":
		l0, l1 := math.Log(lo), math.Log(hi)
		for i := 1; i < k; i++ {
			b[i] = math.Exp(l0 + (l1-l0)*float64(i)/float64(k))
		}
	case "jenks":
		sample := sorted
		if n > 1500 {
			sample = make([]float64, 1500)
			for i := range sample {
				sample[i] = sorted[i*(n-1)/1499]
			}
		}
		b = jenks(sample, k)
	default: // quantile
		for i := 1; i < k; i++ {
			b[i] = sorted[i*n/k]
		}
	}
	// Round inner breaks to two significant figures so legends read cleanly, then drop duplicates.
	out := []float64{b[0]}
	for i := 1; i < len(b)-1; i++ {
		v := roundSig(b[i], 2)
		if v > out[len(out)-1] && v < hi {
			out = append(out, v)
		}
	}
	return append(out, hi)
}

func roundSig(v float64, sig int) float64 {
	if v == 0 {
		return 0
	}
	p := math.Pow(10, float64(sig)-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*p) / p
}

// jenks is Fisher-Jenks natural breaks on sorted data.
func jenks(data []float64, k int) []float64 {
	n := len(data)
	if k >= n {
		return append([]float64(nil), data...)
	}
	lower := make([][]int, n+1)
	variance := make([][]float64, n+1)
	for i := range lower {
		lower[i] = make([]int, k+1)
		variance[i] = make([]float64, k+1)
	}
	for j := 1; j <= k; j++ {
		lower[1][j] = 1
		for i := 2; i <= n; i++ {
			variance[i][j] = math.Inf(1)
		}
	}
	for l := 2; l <= n; l++ {
		s1, s2, w, v := 0.0, 0.0, 0.0, 0.0
		for m := 1; m <= l; m++ {
			i3 := l - m + 1
			val := data[i3-1]
			s2 += val * val
			s1 += val
			w++
			v = s2 - s1*s1/w
			if i4 := i3 - 1; i4 != 0 {
				for j := 2; j <= k; j++ {
					if variance[l][j] >= v+variance[i4][j-1] {
						lower[l][j] = i3
						variance[l][j] = v + variance[i4][j-1]
					}
				}
			}
		}
		lower[l][1] = 1
		variance[l][1] = v
	}
	out := make([]float64, k+1)
	out[k] = data[n-1]
	idx := n
	for c := k; c >= 2; c-- {
		id := lower[idx][c] - 2
		out[c-1] = data[max(id, 0)]
		idx = lower[idx][c] - 1
	}
	out[0] = data[0]
	return out
}

// writeGeoJSON streams the layer; coordinates are rounded to 1e-6 degrees (about 10 cm).
func (L *Layer) writeGeoJSON(w io.Writer, m *Model, rich bool) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	buf := make([]byte, 0, 256)
	num := func(v float64, prec int) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			buf = append(buf, "null"...)
			return
		}
		buf = strconv.AppendFloat(buf, v, 'f', prec, 64)
	}
	pt := func(p Pt) {
		buf = append(buf, '[')
		num(p[0], 6)
		buf = append(buf, ',')
		num(p[1], 6)
		buf = append(buf, ']')
	}
	meta, _ := json.Marshal(map[string]any{
		"breaks": L.Breaks, "counts": L.Counts, "max": L.Max, "p99": L.P99, "req": L.Req, "n": len(L.Feats),
	})
	bw.WriteString(`{"type":"FeatureCollection","pracre":`)
	bw.Write(meta)
	bw.WriteString(`,"features":[`)
	for i, f := range L.Feats {
		buf = buf[:0]
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, `{"type":"Feature","id":`...)
		buf = strconv.AppendInt(buf, int64(i), 10)
		buf = append(buf, `,"geometry":`...)
		if f.G.IsPoly() {
			buf = append(buf, `{"type":"MultiPolygon","coordinates":[`...)
			for pi, poly := range f.G.Polys {
				if pi > 0 {
					buf = append(buf, ',')
				}
				buf = append(buf, '[')
				for ri, r := range poly {
					if ri > 0 {
						buf = append(buf, ',')
					}
					buf = append(buf, '[')
					for qi, q := range r {
						if qi > 0 {
							buf = append(buf, ',')
						}
						pt(q)
					}
					buf = append(buf, ']')
				}
				buf = append(buf, ']')
			}
			buf = append(buf, "]}"...)
		} else {
			buf = append(buf, `{"type":"Point","coordinates":`...)
			pt(f.G.Pt)
			buf = append(buf, '}')
		}
		buf = append(buf, `,"properties":{"v":`...)
		num(f.V, 2)
		buf = append(buf, `,"t":`...)
		num(f.T, 2)
		buf = append(buf, `,"a":`...)
		num(f.A, 4)
		buf = append(buf, `,"n":`...)
		buf = strconv.AppendInt(buf, int64(f.N), 10)
		buf = append(buf, `,"c":`...)
		buf = strconv.AppendInt(buf, int64(f.C), 10)
		if f.Label != "" {
			buf = append(buf, `,"l":`...)
			buf = strconv.AppendQuote(buf, f.Label)
		}
		if rich && f.P >= 0 {
			p := &m.Parcels[f.P]
			buf = append(buf, `,"key":`...)
			buf = strconv.AppendQuote(buf, p.Key)
			if p.HasValue {
				buf = append(buf, `,"value":`...)
				num(p.Value, 2)
			}
			if p.HasTax {
				buf = append(buf, `,"tax":`...)
				num(p.Tax, 2)
			}
		}
		buf = append(buf, "}}"...)
		if _, err := bw.Write(buf); err != nil {
			return err
		}
	}
	bw.WriteString("]}")
	return bw.Flush()
}

// detail describes one feature of the current layer for the side panel.
func (m *Model) detail(L *Layer, id int) map[string]any {
	if L == nil || id < 0 || id >= len(L.Feats) {
		return nil
	}
	f := L.Feats[id]
	out := map[string]any{"v": nanNil(f.V), "t": f.T, "a": f.A, "n": f.N, "c": f.C}
	if f.P >= 0 {
		p := &m.Parcels[f.P]
		out["kind"] = "parcel"
		out["key"], out["label"] = p.Key, p.Label
		out["value"], out["tax"], out["acres"] = optNum(p.Value, p.HasValue), optNum(p.Tax, p.HasTax), p.Area/4046.8564224
		out["rank"] = m.rank(L, f.V)
		var groups []map[string]any
		refs := append([]rowRef{{p.DS, p.Row, "parcel"}}, p.Links...)
		for i, r := range refs {
			if i >= 12 {
				break
			}
			d := m.ds[r.DS]
			var rows [][2]string
			for ci, c := range d.T.Cols {
				if v := d.T.col(ci, r.Row); v != "" {
					rows = append(rows, [2]string{c, v})
				}
			}
			groups = append(groups, map[string]any{"name": d.T.Name, "how": r.How, "rows": rows})
		}
		out["groups"] = groups
		return out
	}
	out["kind"] = "tile"
	members := L.Members[id]
	type top struct {
		Label string  `json:"label"`
		Per   any     `json:"per"`
		T     float64 `json:"t"`
	}
	var tops []top
	unit := L.Req.perM2()
	for _, i := range members {
		p := &m.Parcels[i]
		v, _ := m.metric(p, L.Req.Metric)
		per := any(nil)
		if p.Area > 0 {
			per = v / (p.Area / unit)
		}
		lbl := p.Label
		if lbl == "" {
			lbl = p.Key
		}
		tops = append(tops, top{lbl, per, v})
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].T > tops[j].T })
	if len(tops) > 8 {
		tops = tops[:8]
	}
	out["top"] = tops
	out["rank"] = m.rank(L, f.V)
	return out
}

func nanNil(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return v
}

func optNum(v float64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

// rank is the share of features with a lower per-area value.
func (m *Model) rank(L *Layer, v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	below, n := 0, 0
	for _, f := range L.Feats {
		if math.IsNaN(f.V) {
			continue
		}
		n++
		if f.V < v {
			below++
		}
	}
	if n == 0 {
		return nil
	}
	return float64(below) / float64(n)
}

// TableRow is one line of the parcels grid.
type TableRow struct {
	ID    int     `json:"id"` // feature id in the current layer
	Label string  `json:"label"`
	Key   string  `json:"key"`
	Acres float64 `json:"acres"`
	Total float64 `json:"t"`
	Per   any     `json:"v"`
}

func (m *Model) table(L *Layer, q string, sortBy string, limit int) ([]TableRow, int) {
	if L == nil {
		return nil, 0
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var rows []TableRow
	for i, f := range L.Feats {
		r := TableRow{ID: i, Label: f.Label, Acres: f.A, Total: f.T, Per: nanNil(f.V)}
		if f.P >= 0 {
			r.Key = m.Parcels[f.P].Key
		} else if r.Label == "" {
			r.Label = fmt.Sprintf("Tile %d", i+1)
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Label+" "+r.Key), q) {
			continue
		}
		rows = append(rows, r)
	}
	val := func(r TableRow) float64 {
		switch sortBy {
		case "t":
			return r.Total
		case "a":
			return r.Acres
		}
		if v, ok := r.Per.(float64); ok {
			return v
		}
		return math.Inf(-1)
	}
	if sortBy == "label" {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Label < rows[j].Label })
	} else {
		sort.SliceStable(rows, func(i, j int) bool { return val(rows[i]) > val(rows[j]) })
	}
	n := len(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, n
}

func baseName(p string) string { return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) }
