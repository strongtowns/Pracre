package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

// Reference values from PROJ (pyproj 3.7) for the systems Pracre implements itself.
func TestProjections(t *testing.T) {
	ca3, err := crsFromString(`PROJCS["NAD_1983_StatePlane_California_III_FIPS_0403_Feet",GEOGCS["GCS_North_American_1983",DATUM["D_North_American_1983",SPHEROID["GRS_1980",6378137.0,298.257222101]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]],PROJECTION["Lambert_Conformal_Conic"],PARAMETER["False_Easting",6561666.667],PARAMETER["False_Northing",1640416.667],PARAMETER["Central_Meridian",-120.5],PARAMETER["Standard_Parallel_1",38.4333333333333],PARAMETER["Standard_Parallel_2",37.0666666666667],PARAMETER["Latitude_Of_Origin",36.5],UNIT["US survey foot",0.304800609601219]]`)
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := crsFromString(`PROJCS["NAD_1983_StatePlane_New_York_Long_Island_FIPS_3104_Feet",GEOGCS["GCS_North_American_1983",DATUM["D_North_American_1983",SPHEROID["GRS_1980",6378137.0,298.257222101]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]],PROJECTION["Lambert_Conformal_Conic"],PARAMETER["False_Easting",984250.0],PARAMETER["False_Northing",0.0],PARAMETER["Central_Meridian",-74.0],PARAMETER["Standard_Parallel_1",41.0333333333333],PARAMETER["Standard_Parallel_2",40.6666666666667],PARAMETER["Latitude_Of_Origin",40.1666666666667],UNIT["US survey foot",0.304800609601219]]`)
	bcw, _ := crsFromString(`PROJCS["NAD_1983_BC_Environment_Albers",GEOGCS["GCS_North_American_1983",DATUM["D_North_American_1983",SPHEROID["GRS_1980",6378137.0,298.257222101]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]],PROJECTION["Albers"],PARAMETER["False_Easting",1000000.0],PARAMETER["False_Northing",0.0],PARAMETER["Central_Meridian",-126.0],PARAMETER["Standard_Parallel_1",50.0],PARAMETER["Standard_Parallel_2",58.5],PARAMETER["Latitude_Of_Origin",45.0],UNIT["Meter",1.0]]`)
	must := func(s string) CRS {
		c, err := crsFromString(s)
		if err != nil {
			t.Fatal(s, err)
		}
		return c
	}
	cases := []struct {
		c              CRS
		x, y, lon, lat float64
	}{
		{must("EPSG:26910"), 525199.0546, 5432031.8498, -122.6551991, 49.0406493},
		{must("urn:ogc:def:crs:EPSG::3005"), 1250000, 450000, -122.5855663, 49.0120277},
		{bcw, 1250000, 450000, -122.5855663, 49.0120277},
		{must("EPSG:3857"), -13652000, 6300000, -122.6380026, 49.1479335},
		{must("EPSG:5070"), -2000000, 2500000, -120.9920570, 43.1144936},
		{ca3, 6000000, 2100000, -122.4427256, 37.7461992},
		{ny, 990000, 200000, -73.9792580, 40.7156286},
		{must("EPSG:32617"), 380000, 4760000, -82.4718291, 42.9831988},
	}
	for _, k := range cases {
		lon, lat := k.c.ToWGS84(k.x, k.y)
		// 1e-6 degrees is about 10 cm.
		if math.Abs(lon-k.lon) > 1e-6 || math.Abs(lat-k.lat) > 1e-6 {
			t.Errorf("%s: got %.7f %.7f want %.7f %.7f", k.c.Name, lon, lat, k.lon, k.lat)
		}
	}
}

// TestEPSGTable checks systems from the built-in table against PROJ 9.8 (projected coordinate to WGS 84,
// datum shift included), with lookups written the ways files and people write them.
func TestEPSGTable(t *testing.T) {
	refs := []struct {
		crs                 string
		x, y, lon, lat, tol float64
	}{
		{"EPSG:26718", 500000, 5649050.243913, -74.9993587922, 50.9950357877, 0.5},        // NAD27 UTM 18N
		{"EPSG:32054", 2215252.659687, 513118.105875, -89.1900708287, 43.4050033981, 0.5}, // NAD27 Wisconsin South (ftUS)
		{"EPSG:27700", 304969.207371, 610584.993677, -3.5013607419, 55.3800061682, 0.5},   // OSGB36 British National Grid
		{"EPSG:23031", 500000, 6707811.983480, 2.9983323098, 60.5044802431, 0.5},          // ED50 UTM 31N
		{"EPSG:31292", 199775.994383, 5297982.962369, 15.9988781862, 47.7895354518, 2},    // MGI (Ferro) Austria
		{"EPSG:2065", 1144058.330762, 544115.474379, 17.3235654599, 49.3943955559, 2},     // S-JTSK Krovak, southing/westing
		{"EPSG:5514", -544115.474379, -1144058.330762, 17.3235654599, 49.3943955559, 2},   // S-JTSK Krovak East North
		{"EPSG:27572", 549771.539352, 2192985.113948, 1.6792797647, 46.7349454902, 0.5},   // NTF (Paris) Lambert II
		{"EPSG:3078", 478167.878450, 494847.000784, -86.2750000000, 45.0050000000, 0.5},   // Michigan Oblique Mercator
		{"EPSG:28992", 142863.978813, 470673.112492, 5.2095910472, 52.2240039447, 0.5},    // Amersfoort RD New
		{"EPSG:21781", 659938.986167, 184470.966642, 8.2239312474, 46.8087011523, 0.5},    // Swiss LV03
		{"EPSG:2443", -17694.129182, -238365.457322, 129.3150000000, 30.8500000000, 0.5},  // Japan Plane Rectangular I
		{"ESRI:102719", 2105000, 740000, -78.64598258031904, 35.78273665383, 0.5},         // ESRI's code for NC State Plane feet
		{"urn:ogc:def:crs:EPSG:6.3:2264", 2105000, 740000, -78.64598258031904, 35.78273665383, 0.5},
		// A .prj without codes finds NAD27 by its datum name and shifts it.
		{`PROJCS["NAD_1927_StatePlane_Wisconsin_South_FIPS_4803",GEOGCS["GCS_North_American_1927",DATUM["D_North_American_1927",SPHEROID["Clarke_1866",6378206.4,294.978698213898]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]],PROJECTION["Lambert_Conformal_Conic"],PARAMETER["False_Easting",2000000.0],PARAMETER["False_Northing",0.0],PARAMETER["Central_Meridian",-90.0],PARAMETER["Standard_Parallel_1",42.7333333333333],PARAMETER["Standard_Parallel_2",44.0666666666667],PARAMETER["Latitude_Of_Origin",42.0],UNIT["US survey foot",0.304800609601219]]`,
			2215252.659687, 513118.105875, -89.1900708287, 43.4050033981, 0.5},
	}
	for _, r := range refs {
		c, err := crsFromString(r.crs)
		if err != nil {
			t.Errorf("%.40s: %v", r.crs, err)
			continue
		}
		lon, lat := c.ToWGS84(r.x, r.y)
		if d := math.Hypot((lon-r.lon)*111320*math.Cos(rad(r.lat)), (lat-r.lat)*110540); d > r.tol {
			t.Errorf("%s (%s): %.2f m from PROJ", c.ID, c.Name, d)
		}
	}
	if _, err := crsFromString("EPSG:1078"); err == nil {
		t.Errorf("a code that isn't a coordinate system was accepted")
	}
	// Unlabelled State Plane feet: the hint picks the state.
	if g := guessCRS([]Pt{{2105000, 740000}, {2110000, 745000}}, Pt{-78.6, 35.8}); len(g) == 0 || !strings.Contains(g[0].CRS.Name, "North Carolina") {
		t.Errorf("NC State Plane guess: %v", g)
	}
}

// TestSampleData runs the whole pipeline on ValuePerAcre2's Langley and Moldovan files when they sit beside this repo.
func TestSampleData(t *testing.T) {
	dir := "../ValuePerAcre2/"
	load := func(p *Project, names ...string) {
		for _, n := range names {
			tabs, err := readFile(dir + n)
			if err != nil {
				t.Skip("sample data not present:", err)
			}
			for _, tb := range tabs {
				d := &Dataset{ID: n, T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}
				d.prepare(Pt{-122.6, 49.1})
				p.Datasets = append(p.Datasets, d)
				t.Logf("%s: %d rows, crs %q located %d polys %d err %q", n, len(tb.Rows), d.crs.Name, d.located, d.polys, d.locErr)
				for i, r := range d.Roles {
					if r.Role != "" {
						t.Logf("   %-36s %s %s %s", tb.Cols[i], r.Role, r.Class, r.Unit)
					}
				}
			}
		}
	}
	p := &Project{}
	load(p, "2024_parcels.geojson", "2024_assessments.geojson", "2024_tax_rates.json")
	for _, d := range p.Datasets {
		if d.isRates() {
			c, r := indexOfRole(d.Roles, RoleRateCode), indexOfRole(d.Roles, RoleRate)
			for row := range d.T.Rows {
				v, _ := parseNum(d.T.col(r, row))
				p.Rates = append(p.Rates, Rate{Code: d.T.col(c, row), Rate: &v})
			}
		}
	}
	m := buildModel(p)
	for _, c := range m.Checks {
		t.Logf("[%s] %s: %s", c.Status, c.Title, c.Detail)
	}
	t.Logf("stats %+v", m.Stats)
	if m.Stats.WithValue < 25000 || math.Abs(m.Stats.TotalTax-231250988) > 1000 {
		t.Errorf("Langley: expected most parcels valued and >$100M levy, got %d parcels, $%.0f", m.Stats.WithValue, m.Stats.TotalTax)
	}
	L := m.layer(LayerReq{Mode: "hex", Size: 250, Metric: "tax", Method: "jenks"})
	t.Logf("hex layer: %d tiles, breaks %v counts %v", len(L.Feats), L.Breaks, L.Counts)

	q := &Project{}
	load(q, "moldovan_parcels.json", "moldovan_taxparcelinformation.csv")
	m2 := buildModel(q)
	for _, c := range m2.Checks {
		t.Logf("[%s] %s: %s", c.Status, c.Title, c.Detail)
	}
	if m2.Stats.WithValue < 9000 {
		t.Errorf("Moldovan: expected >10k valued parcels, got %d", m2.Stats.WithValue)
	}
}

// TestEncodings feeds the detector the location encodings people actually export.
func TestEncodings(t *testing.T) {
	near := func(p Pt, lon, lat float64) bool { return math.Abs(p[0]-lon) < 1e-3 && math.Abs(p[1]-lat) < 1e-3 }
	cases := []struct {
		name, ext, body string
		lon, lat        float64
	}{
		{"utm without crs", ".csv", "Folio,Easting,Northing,Assessed Value\n1,525199.05,5432031.85,\"$1,250,000\"\n2,525300,5432100,900000\n", -122.6551991, 49.0406493},
		{"lat lon swapped", ".csv", "id,lat,lon,total_value\nA,-122.6552,49.0406,1\nB,-122.6,49.1,2\n", -122.6552, 49.0406},
		{"one column", ".csv", "roll_number;Location;Taxes\n7;\"(49.0406, -122.6552)\";2500\n8;\"(49.1, -122.6)\";10\n", -122.6552, 49.0406},
		{"dms", ".tsv", "PIN\tLatitude\tLongitude\tTotal Assessment\nX1\t49°02'26.3\"N\t122°39'18.7\"W\t500000\nX2\t49°05'00\"N\t122°36'00\"W\t1\n", -122.65519, 49.04064},
		{"wkt", ".csv", "apn,the_geom,taxable\n1,\"POLYGON ((-122.656 49.040, -122.654 49.040, -122.654 49.041, -122.656 49.041, -122.656 49.040))\",100\n", -122.655, 49.0405},
		{"esri json", ".json", `{"spatialReference":{"wkid":102100},"features":[{"attributes":{"PARCELID":"9","TOTVAL":5},"geometry":{"x":-13652000,"y":6300000}}]}`, -122.6380026, 49.1479335},
		{"kml", ".kml", `<kml><Document><Placemark><name>Lot 1</name><ExtendedData><Data name="Value"><value>12</value></Data></ExtendedData><Point><coordinates>-122.6552,49.0406,0</coordinates></Point></Placemark></Document></kml>`, -122.6552, 49.0406},
		// One-column grid references: the Washington Monument as USNG / MGRS and as UTM text,
		// the Plus code and geohash examples from their specifications.
		{"usng", ".csv", "pin,usng,assessed_value\n1,18S UJ 23487 06483,100\n2,18SUJ2348706483,200\n", -77.03524, 38.88947},
		{"utm text", ".csv", "pin,location,assessed_value\n1,18S 323487 4306483,100\n2,18S 323500 4306500,200\n", -77.03524, 38.88947},
		{"plus code", ".csv", "pin,plus_code,assessed_value\nA,8FVC9G8F+6X,1\nB,8FVC9G8F+7X,2\n", 8.524813, 47.365562},
		{"geohash", ".csv", "pin,geohash,assessed_value\n1,u4pruydqqvj,5\n2,u4pruydqqvm,6\n", 10.40744, 57.64911},
	}
	for _, c := range cases {
		tb, err := readBytes([]byte(c.body), c.ext)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		d := &Dataset{T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}
		d.prepare(Pt{-122.6, 49.1})
		if d.located == 0 {
			t.Errorf("%s: nothing located (roles %v, err %q)", c.name, d.Roles, d.locErr)
			continue
		}
		if p := labelPoint(d.geo[0]); !near(p, c.lon, c.lat) {
			t.Errorf("%s: first row at %v, want %.5f %.5f (crs %s)", c.name, p, c.lon, c.lat, d.crs.Name)
		}
		if !d.hasValues() && c.name != "kml" {
			t.Errorf("%s: no value column found in %v", c.name, d.Roles)
		}
	}
	// A two-column code/rate table is recognised as tax rates, with mill rates kept as written.
	tb, _ := readBytes([]byte("Class,Mill Rate\nResidential,3.17967\nBusiness,9.61864\n"), ".csv")
	if d := (&Dataset{T: tb, Roles: detectRoles(tb)}); !d.isRates() {
		t.Errorf("rates table not recognised: %v", d.Roles)
	}
}

// TestTilesAndGLB checks hex binning keeps every dollar and that the 3D model is valid glTF.
func TestTilesAndGLB(t *testing.T) {
	var ps []Parcel
	for i := 0; i < 400; i++ {
		x, y := -122.6+float64(i%20)*0.001, 49.1+float64(i/20)*0.001
		r := Ring{{x, y}, {x + 0.0008, y}, {x + 0.0008, y + 0.0008}, {x, y + 0.0008}, {x, y}}
		g := polyGeom([]Poly{{r}})
		ps = append(ps, Parcel{G: g, C: labelPoint(g), Area: geomAreaGeo(g), Value: float64(1000 * (i + 1)), HasValue: true})
	}
	m := &Model{Parcels: ps}
	m.Stats = Stats{HasValue: true, BBox: [4]float64{-122.6, 49.1, -122.58, 49.12}}
	for _, mode := range []string{"hex", "square"} {
		L := m.layer(LayerReq{Mode: mode, Size: 150})
		sum, n := 0.0, 0
		for _, f := range L.Feats {
			sum += f.T
			n += f.N
		}
		if n != 400 || math.Abs(sum-80200000) > 1 {
			t.Errorf("%s: %d parcels, $%.0f in tiles; want 400 and $80,200,000", mode, n, sum)
		}
		b, err := buildGLB(L, GLBOpts{Colors: []string{"#22355c", "#f26522"}, Height: 0.001, Base: true})
		if err != nil || len(b) < 1000 || string(b[:4]) != "glTF" || int(binary.LittleEndian.Uint32(b[8:12])) != len(b) {
			t.Errorf("%s: bad glb (%d bytes, %v)", mode, len(b), err)
		}
	}
}

// TestEmbedPage writes the Langley web map to $PRACRE_OUT for a visual check in a browser.
func TestEmbedPage(t *testing.T) {
	out := os.Getenv("PRACRE_OUT")
	if out == "" {
		t.Skip("set PRACRE_OUT to write the page")
	}
	tabs, err := readFile("sample/langley-sample.zip")
	if err != nil {
		t.Fatal(err)
	}
	p := &Project{RateUnit: "mill"}
	for _, tb := range tabs {
		d := &Dataset{T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}
		if d.isRates() {
			c, r := indexOfRole(d.Roles, RoleRateCode), indexOfRole(d.Roles, RoleRate)
			for row := range tb.Rows {
				v, _ := parseNum(tb.col(r, row))
				p.Rates = append(p.Rates, Rate{Code: tb.col(c, row), Rate: &v})
			}
			continue
		}
		d.prepare(Pt{})
		p.Datasets = append(p.Datasets, d)
	}
	m := buildModel(p)
	L := m.layer(LayerReq{Mode: "hex", Size: 200, Metric: "tax"})
	cols := []string{"#22355c", "#2e6f95", "#48a9a6", "#f2c14e", "#f26522", "#c0262d", "#6b0f2a"}
	var legend []LegendItem
	for i := 0; i+1 < len(L.Breaks); i++ {
		legend = append(legend, LegendItem{cols[i], fmt.Sprintf("$%.0f+", L.Breaks[i])})
	}
	b := m.Stats.BBox
	page, err := embedPage(m, L, EmbedOpts{Title: "Township of Langley", Subtitle: "Tax revenue per acre", ThreeD: true,
		Center: [2]float64{(b[0] + b[2]) / 2, (b[1] + b[3]) / 2}, Zoom: 12.6, Pitch: 55, Bearing: -17, Colors: cols, Zero: "#b8bfca", NoArea: "#e3e6eb",
		Legend: legend, Height: 0.004, Cap: L.P99 * 1.15, Basemap: "https://tiles.openfreemap.org/styles/positron", MetricLabel: "Tax revenue per acre",
		TotalLabel: "Tax", Currency: "$", Per: "acre", Opacity: 0.88})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(out, page, 0o644)
}

// TestRealWorldQuirks covers column names and file shapes seen in Vancouver, Calgary, Philadelphia,
// NYC, MassGIS, Opendatasoft and ArcGIS exports.
func TestRealWorldQuirks(t *testing.T) {
	roleOf := func(tb *Table, roles []ColRole, col string) ColRole {
		for i, c := range tb.Cols {
			if c == col {
				return roles[i]
			}
		}
		t.Fatalf("no column %s", col)
		return ColRole{}
	}
	read := func(ext, body string) *Table {
		tb, err := readBytes([]byte(body), ext)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		return tb
	}

	// Vancouver: "current" holds "rent", land_coordinate is an ID, report_year beats the assessment year.
	van := read(".csv", "pid;land_coordinate;current_land_value;current_improvement_value;tax_assessment_year;report_year;tax_levy\n"+
		"015-121-852;07264836;2121000;244000;2024;2025;8433,29\n016-121-852;07264837;1121000;144000;;2025;4433,10\n")
	vr := detectRoles(van)
	for col, want := range map[string]string{"land_coordinate": "", "current_land_value": RoleLand, "current_improvement_value": RoleImpr, "report_year": RoleYear, "tax_levy": RoleTax} {
		if got := roleOf(van, vr, col).Role; got != want {
			t.Errorf("vancouver %s: role %q, want %q", col, got, want)
		}
	}
	if van.Rows[0][6] != "8433.29" {
		t.Errorf("decimal comma read as %q", van.Rows[0][6])
	}

	// Philadelphia: taxable land and buildings beat market value; exempt columns are not tax classes; hex EWKB points.
	phl := read(".csv", "parcel_number,market_value,taxable_land,taxable_building,exempt_land,exempt_building,the_geom\n"+
		"1,300000,50000,200000,10000,40000,0101000020E6100000918813EF06CA52C01A35D50723F74340\n"+
		"2,100000,20000,80000,0,0,0101000020E6100000918813EF06CA52C01A35D50723F74340\n")
	pr := detectRoles(phl)
	for col, want := range map[string]string{"taxable_land": RoleLand, "taxable_building": RoleImpr, "market_value": RoleTotal, "exempt_land": "", "the_geom": RoleGeom} {
		if got := roleOf(phl, pr, col).Role; got != want {
			t.Errorf("philadelphia %s: role %q, want %q", col, got, want)
		}
	}
	pd := &Dataset{T: phl, Roles: pr}
	pd.prepare(Pt{})
	if pd.located != 2 || math.Abs(labelPoint(pd.geo[0])[0]+75.1567) > 1e-3 || math.Abs(labelPoint(pd.geo[0])[1]-39.9308) > 1e-3 {
		t.Errorf("EWKB point: located %d at %v", pd.located, labelPoint(pd.geo[0]))
	}
	if m := buildModel(&Project{Datasets: []*Dataset{pd}}); m.Stats.TotalValue != 350000 {
		t.Errorf("philadelphia value %v, want taxable 350000", m.Stats.TotalValue)
	}

	// Calgary: a legal description is not "lat, lon"; land size units come from the suffix; tax class beats use type.
	cal := read(".csv", "roll_number,short_legal,land_size_sm,land_size_sf,land_size_ac,assessed_value,property_type,assessment_class\n"+
		"068197003,7910655;17,394.5,4246,0.1,2130000,LI,RE\n068197004,7910655;18,400,4305,0.1,2000000,LI,NR\n")
	cr := detectRoles(cal)
	if r := roleOf(cal, cr, "short_legal"); r.Role != "" {
		t.Errorf("short_legal read as %q", r.Role)
	}
	if r := roleOf(cal, cr, "land_size_sm"); r.Role != RoleArea || r.Unit != "m2" {
		t.Errorf("land_size_sm: %+v", r)
	}
	if r := roleOf(cal, cr, "assessment_class"); r.Role != RoleClass {
		t.Errorf("assessment_class: %+v", r)
	}
	for n, want := range map[string]string{"numbldgs": "", "county_assessed_value": RoleTotal, "prev_land_value": ""} {
		if got, _ := valueRole(n); got != want {
			t.Errorf("valueRole(%s) = %q, want %q", n, got, want)
		}
	}
	var built strings.Builder
	built.WriteString("pid,year,total_value\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&built, "%d,%d,100000\n", i, 1950+i)
	}
	if yb := read(".csv", built.String()); detectRoles(yb)[1].Role == RoleYear {
		t.Errorf("a year-built column was taken for the roll year")
	}
	if !reHouse.MatchString("civic_numb") {
		t.Errorf("shapefile-truncated civic_numb not a house number")
	}

	// Title rows above a CSV header, UTF-16 text, Opendatasoft's Feature-wrapped geometry.
	if tb := read(".csv", "District of Example - 2025 Roll\n\nRoll,Address,Assessed Value\n1,12 Main,100\n"); tb.Cols[2] != "Assessed Value" || len(tb.Rows) != 1 {
		t.Errorf("title rows: cols %v rows %d", tb.Cols, len(tb.Rows))
	}
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "Roll\tValue\r\n1\t350000\r\n" {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	if tb, err := readBytes(u16, ".txt"); err != nil || tb.Cols[1] != "Value" || tb.Rows[0][1] != "350000" {
		t.Errorf("UTF-16: %v %v", tb, err)
	}
	ods := read(".json", `[{"site_id":"1","geom":{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[-123.1,49.2],[-123.09,49.2],[-123.09,49.21],[-123.1,49.2]]]},"properties":{}}}]`)
	od := &Dataset{T: ods, Roles: detectRoles(ods)}
	if od.prepare(Pt{}); od.polys != 1 {
		t.Errorf("Feature-wrapped geometry not read: %+v", od.Roles)
	}
	for _, body := range []string{`[]`, `{"type":"FeatureCollection","features":[]}`, `{"error":{"code":400,"message":"Unable to complete operation.","details":[]}}`} {
		if _, err := readBytes([]byte(body), ".json"); err == nil {
			t.Errorf("%s: imported without an error", body)
		}
	}

	// WKT2 pasted from epsg.io reads the same as the layer's ESRI .prj; "#swap" flips latitude-first degrees.
	w1, _ := crsFromString(`PROJCS["NAD_1983_StatePlane_North_Carolina_FIPS_3200_Feet",GEOGCS["GCS_North_American_1983",DATUM["D_North_American_1983",SPHEROID["GRS_1980",6378137.0,298.257222101]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]],PROJECTION["Lambert_Conformal_Conic"],PARAMETER["False_Easting",2000000.0],PARAMETER["False_Northing",0.0],PARAMETER["Central_Meridian",-79.0],PARAMETER["Standard_Parallel_1",36.1666666666667],PARAMETER["Standard_Parallel_2",34.3333333333333],PARAMETER["Latitude_Of_Origin",33.75],UNIT["US survey foot",0.304800609601219]]`)
	w2, err := crsFromString(`PROJCRS["NAD83 / North Carolina (ftUS)",BASEGEOGCRS["NAD83",DATUM["North American Datum 1983",ELLIPSOID["GRS 1980",6378137,298.257222101,LENGTHUNIT["metre",1]]],PRIMEM["Greenwich",0,ANGLEUNIT["degree",0.0174532925199433]],ID["EPSG",4269]],CONVERSION["SPCS83 North Carolina zone (US survey foot)",METHOD["Lambert Conic Conformal (2SP)",ID["EPSG",9802]],PARAMETER["Latitude of false origin",33.75,ANGLEUNIT["degree",0.0174532925199433],ID["EPSG",8821]],PARAMETER["Longitude of false origin",-79,ANGLEUNIT["degree",0.0174532925199433],ID["EPSG",8822]],PARAMETER["Latitude of 1st standard parallel",36.1666666666667,ANGLEUNIT["degree",0.0174532925199433],ID["EPSG",8823]],PARAMETER["Latitude of 2nd standard parallel",34.3333333333333,ANGLEUNIT["degree",0.0174532925199433],ID["EPSG",8824]],PARAMETER["Easting at false origin",2000000,LENGTHUNIT["US survey foot",0.304800609601219],ID["EPSG",8826]],PARAMETER["Northing at false origin",0,LENGTHUNIT["US survey foot",0.304800609601219],ID["EPSG",8827]]],CS[Cartesian,2],AXIS["easting (X)",east,ORDER[1],LENGTHUNIT["US survey foot",0.304800609601219]],AXIS["northing (Y)",north,ORDER[2],LENGTHUNIT["US survey foot",0.304800609601219]],ID["EPSG",2264]]`)
	x1, y1 := w1.ToWGS84(2105000, 740000)
	x2, y2 := w2.ToWGS84(2105000, 740000)
	if err != nil || math.Abs(x1-x2) > 1e-9 || math.Abs(y1-y2) > 1e-9 || math.Abs(x1+78.65) > 0.05 {
		t.Errorf("WKT2 %v: %.6f %.6f vs WKT1 %.6f %.6f", err, x2, y2, x1, y1)
	}
	if c, _ := crsFromString("EPSG:4326#swap"); !c.Swap {
		t.Errorf("#swap ignored")
	}
}

// TestMultiYearStrata joins a values table that repeats each lot's ID for every strata unit and every year.
func TestMultiYearStrata(t *testing.T) {
	var gj, csv strings.Builder
	gj.WriteString(`{"type":"FeatureCollection","features":[`)
	csv.WriteString("pid,land_coordinate,current_land_value,current_improvement_value,report_year\n")
	for i := 0; i < 60; i++ {
		x := -123.1 + float64(i)*0.001
		if i > 0 {
			gj.WriteString(",")
		}
		fmt.Fprintf(&gj, `{"type":"Feature","properties":{"tax_coord":"%08d"},"geometry":{"type":"Polygon","coordinates":[[[%f,49.2],[%f,49.2],[%f,49.2008],[%f,49.2008],[%f,49.2]]]}}`, 7000000+i, x, x+0.0008, x+0.0008, x, x)
		for _, year := range []int{2024, 2025} {
			for unit := 0; unit < 2; unit++ {
				fmt.Fprintf(&csv, "%03d-%03d-%03d,%08d,%d,1000,%d\n", i, unit, year%100, 7000000+i, year*100, year)
			}
		}
	}
	gj.WriteString("]}")
	p := &Project{}
	for _, f := range []struct{ ext, body string }{{".geojson", gj.String()}, {".csv", csv.String()}} {
		tb, err := readBytes([]byte(f.body), f.ext)
		if err != nil {
			t.Fatal(err)
		}
		d := &Dataset{ID: f.ext, T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}
		d.prepare(Pt{})
		p.Datasets = append(p.Datasets, d)
	}
	m := buildModel(p)
	// 60 lots x 2 units x (202500 land + 1000 buildings), 2024 rows left out.
	if want := 60 * 2 * 203500.0; m.Stats.TotalValue != want || m.Stats.WithValue != 60 {
		t.Errorf("value %.0f on %d parcels, want %.0f on 60", m.Stats.TotalValue, m.Stats.WithValue, want)
	}
	for _, c := range m.Checks {
		if strings.HasPrefix(c.ID, "link:") && (c.Status != "done" || !strings.Contains(c.Detail, "land_coordinate = tax_coord") || !strings.Contains(c.Detail, "Only 2025")) {
			t.Errorf("link check: %+v", c)
		}
	}
}

// TestBundledSamples loads each sample the File menu offers and expects a finished checklist.
func TestBundledSamples(t *testing.T) {
	for name, at := range samplePlaces {
		tabs, err := readFile("sample/" + name + "-sample.zip")
		if err != nil {
			t.Fatal(err)
		}
		p := &Project{}
		for _, tb := range tabs {
			if d := (&Dataset{T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}); !d.isRates() {
				d.prepare(at)
				p.Datasets = append(p.Datasets, d)
			}
		}
		m := buildModel(p)
		if m.Stats.WithValue < 7000 {
			t.Errorf("%s: values on %d parcels", name, m.Stats.WithValue)
		}
		for _, c := range m.Checks {
			if c.Status == "todo" || c.Status == "warn" {
				t.Errorf("%s: [%s] %s: %s", name, c.Status, c.Title, c.Detail)
			}
		}
	}
}

// TestCommonPropertyHoles merges strata units drawn in holes of their common property, and leaves
// lots in holes of a road or an assessed parcel alone.
func TestCommonPropertyHoles(t *testing.T) {
	// The sample's 20391 96 AVE townhouses sit in holes of the 20381 96 AVE common property.
	tabs, err := readFile("sample/langley-sample.zip")
	if err != nil {
		t.Fatal(err)
	}
	p := &Project{}
	for _, tb := range tabs {
		if d := (&Dataset{T: tb, Geoms: tb.Geoms, Roles: detectRoles(tb)}); !d.isRates() {
			d.prepare(Pt{})
			p.Datasets = append(p.Datasets, d)
		}
	}
	m := buildModel(p)
	for _, pa := range m.Parcels {
		if !pa.Merged && pa.Label == "20391 96 AVE" {
			t.Errorf("20391 96 AVE left on its own: %.0f m², $%.0f", pa.Area, pa.Value)
		}
		if !pa.Merged && pa.Label == "20381 96 AVE" && (pa.Area < 69000 || pa.Value < 160e6) {
			t.Errorf("20381 96 AVE: %.0f m², $%.0f; want the 20391 units' land and value added", pa.Area, pa.Value)
		}
	}

	// A block with a hole holding two lots, as a common property, a road and an assessed parcel.
	sq := func(x0, y0, x1, y1 float64) Ring { return Ring{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}} }
	for _, c := range []struct {
		label string
		value float64
		merge bool
	}{{"1 MAIN ST", 0, true}, {"", 0, false}, {"1 MAIN ST", 5e5, false}} {
		block := Parcel{G: polyGeom([]Poly{{sq(0, 0, 0.001, 0.001), sq(0.0002, 0.0002, 0.0008, 0.0008)}}), C: Pt{0.0001, 0.0005}, Area: 5000, Label: c.label, Value: c.value, HasValue: c.value > 0}
		lots := []Parcel{block}
		for _, x := range []float64{0.0002, 0.0005} {
			lots = append(lots, Parcel{G: polyGeom([]Poly{{sq(x, 0.0002, x+0.0003, 0.0008)}}), C: Pt{x + 0.00015, 0.0005}, Area: 2000, Label: "3 MAIN ST", Value: 1e6, HasValue: true})
		}
		m := &Model{Parcels: lots}
		m.mergeStacked(newSpatialIndex(m.Parcels))
		if got := m.Parcels[1].Merged && m.Parcels[2].Merged && m.Parcels[0].Area == 9000; got != c.merge {
			t.Errorf("block %q worth %.0f: merged %v, want %v", c.label, c.value, got, c.merge)
		}
	}
}

// TestImportFolder drops every part of a shapefile plus a rates table, as people select a folder's files.
func TestImportFolder(t *testing.T) {
	dir := t.TempDir()
	le, be := binary.LittleEndian, binary.BigEndian
	shp := make([]byte, 128)
	be.PutUint32(shp[0:], 9994)
	be.PutUint32(shp[24:], 64)
	le.PutUint32(shp[28:], 1000)
	le.PutUint32(shp[32:], 1)
	be.PutUint32(shp[100:], 1)
	be.PutUint32(shp[104:], 10)
	le.PutUint32(shp[108:], 1)
	le.PutUint64(shp[112:], math.Float64bits(-122.6))
	le.PutUint64(shp[120:], math.Float64bits(49.1))
	dbf := make([]byte, 65+11)
	dbf[0] = 3
	le.PutUint32(dbf[4:], 1)
	le.PutUint16(dbf[8:], 65)
	le.PutUint16(dbf[10:], 11)
	copy(dbf[32:], "TOTAL_VAL")
	dbf[43], dbf[48], dbf[64] = 'N', 10, 0x0D
	copy(dbf[65:], "     500000")
	files := map[string][]byte{"lots.shp": shp, "lots.dbf": dbf, "lots.prj": []byte(`GEOGCS["GCS_WGS_1984",DATUM["D_WGS_1984",SPHEROID["WGS_1984",6378137.0,298.257223563]],PRIMEM["Greenwich",0.0],UNIT["Degree",0.0174532925199433]]`),
		"lots.sbn": {0, 0, 39, 10, 255, 1}, "lots.shp.xml": []byte(`<?xml version="1.0"?><metadata/>`), "rates.csv": []byte("Class,Tax Rate (%)\nResidential,1.05\nBusiness,2.3\n")}
	var paths []string
	for n, b := range files {
		os.WriteFile(dir+"/"+n, b, 0o644)
		paths = append(paths, dir+"/"+n)
	}
	a := NewApp()
	res := a.Import(paths, 0, 0)
	if len(res.Added) != 1 || res.Rates != 2 || len(res.Errors) > 0 || a.proj.RateUnit != "percent" {
		t.Errorf("added %v rates %d errors %v unit %s; want one layer, 2 percent rates", res.Added, res.Rates, res.Errors, a.proj.RateUnit)
	}
	if s := a.State(); s.Stats.TotalValue != 500000 {
		t.Errorf("value %v", s.Stats.TotalValue)
	}
}
