package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
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
