package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// CRS converts source coordinates to WGS84 lon/lat. Datum shifts are ignored: NAD83 and WGS84
// agree to about a metre, which is far below parcel size. NAD27 and OSGB36 data will land ~100 m off.
type CRS struct {
	ID    string  `json:"id"`   // "EPSG:26910", "WKT" or "" for geographic
	Name  string  `json:"name"` // human label
	Kind  string  `json:"kind"` // geo, merc, tm, lcc, aea
	A     float64 `json:"a,omitempty"`
	InvF  float64 `json:"invf,omitempty"`
	Lat0  float64 `json:"lat0,omitempty"`
	Lon0  float64 `json:"lon0,omitempty"`
	Lat1  float64 `json:"lat1,omitempty"`
	Lat2  float64 `json:"lat2,omitempty"`
	K0    float64 `json:"k0,omitempty"`
	FE    float64 `json:"fe,omitempty"` // metres
	FN    float64 `json:"fn,omitempty"` // metres
	Unit  float64 `json:"unit,omitempty"`
	Swap  bool    `json:"swap,omitempty"` // geographic data stored lat,lon
	WKT   string  `json:"wkt,omitempty"`
	proj  func(x, y float64) (float64, float64)
	ready bool
}

const (
	grs80A, grs80InvF = 6378137.0, 298.257222101
	wgs84A, wgs84InvF = 6378137.0, 298.257223563
)

var crsWGS84 = CRS{ID: "EPSG:4326", Name: "WGS 84 (longitude/latitude)", Kind: "geo"}

func utmCRS(zone int, south bool, nad83 bool) CRS {
	c := CRS{Kind: "tm", K0: 0.9996, FE: 500000, Lon0: float64(zone*6 - 183), A: wgs84A, InvF: wgs84InvF, Unit: 1}
	datum := "WGS 84"
	code := 32600 + zone
	if nad83 {
		c.A, c.InvF = grs80A, grs80InvF
		datum = "NAD83"
		code = 26900 + zone
	}
	hemi := "N"
	if south {
		c.FN = 10000000
		hemi = "S"
		code += 100
	}
	c.ID = "EPSG:" + strconv.Itoa(code)
	c.Name = fmt.Sprintf("%s / UTM zone %d%s", datum, zone, hemi)
	return c
}

func albersCRS(id, name string, lat0, lon0, lat1, lat2, fe, fn float64) CRS {
	return CRS{ID: id, Name: name, Kind: "aea", A: grs80A, InvF: grs80InvF, Lat0: lat0, Lon0: lon0, Lat1: lat1, Lat2: lat2, FE: fe, FN: fn, Unit: 1}
}

// crsFromEPSG covers the systems parcel data in North America usually arrives in.
// Anything else (State Plane, etc.) comes in through its .prj / WKT instead.
func crsFromEPSG(code int) (CRS, bool) {
	csrs := map[int]int{3154: 7, 3155: 8, 3156: 9, 3157: 10, 2955: 11, 2956: 12, 2957: 13, 3158: 14, 3159: 15, 3160: 16, 2958: 17, 2959: 18, 2960: 19, 2961: 20, 2962: 21, 22207: 7, 22208: 8, 22209: 9, 22210: 10, 22211: 11, 22212: 12, 22213: 13, 22214: 14, 22215: 15, 22216: 16, 22217: 17, 22218: 18, 22219: 19, 22220: 20, 22221: 21, 22222: 22}
	switch {
	case code == 4326 || code == 4269 || code == 4617 || code == 4258 || code == 4283 || code == 4167 || code == 4152 || code == 6318 || code == 4954 || code == 4955:
		c := crsWGS84
		c.ID = "EPSG:" + strconv.Itoa(code)
		if code != 4326 {
			c.Name = "Geographic longitude/latitude (EPSG:" + strconv.Itoa(code) + ")"
		}
		return c, true
	case code == 3857 || code == 3785 || code == 900913 || code == 102100 || code == 102113:
		return CRS{ID: "EPSG:3857", Name: "Web Mercator", Kind: "merc", Unit: 1}, true
	case code >= 32601 && code <= 32660:
		return utmCRS(code-32600, false, false), true
	case code >= 32701 && code <= 32760:
		return utmCRS(code-32700, true, false), true
	case code >= 26901 && code <= 26923:
		return utmCRS(code-26900, false, true), true
	case code >= 6330 && code <= 6348: // NAD83(2011) UTM zones 1N..19N
		c := utmCRS(code-6329, false, true)
		c.ID, c.Name = "EPSG:"+strconv.Itoa(code), strings.Replace(c.Name, "NAD83", "NAD83(2011)", 1)
		return c, true
	case csrs[code] != 0:
		c := utmCRS(csrs[code], false, true)
		c.ID, c.Name = "EPSG:"+strconv.Itoa(code), strings.Replace(c.Name, "NAD83", "NAD83(CSRS)", 1)
		return c, true
	case code == 3005 || code == 3153:
		return albersCRS("EPSG:"+strconv.Itoa(code), "BC Albers", 45, -126, 50, 58.5, 1000000, 0), true
	case code == 5070 || code == 102003 || code == 6350:
		return albersCRS("EPSG:"+strconv.Itoa(code), "Conus Albers (USA contiguous)", 23, -96, 29.5, 45.5, 0, 0), true
	case code == 3347:
		return CRS{ID: "EPSG:3347", Name: "Statistics Canada Lambert", Kind: "lcc", A: grs80A, InvF: grs80InvF, Lat0: 63.390675, Lon0: -91.866666666667, Lat1: 49, Lat2: 77, FE: 6200000, FN: 3000000, Unit: 1}, true
	case code == 2193:
		return CRS{ID: "EPSG:2193", Name: "NZGD2000 / NZTM", Kind: "tm", A: grs80A, InvF: grs80InvF, Lon0: 173, K0: 0.9996, FE: 1600000, FN: 10000000, Unit: 1}, true
	}
	return CRS{}, false
}

var epsgRe = regexp.MustCompile(`(?i)(?:EPSG|ESRI)\D{0,4}(\d{4,6})\b`)

// crsFromString accepts "EPSG:26910", "urn:ogc:def:crs:EPSG::3005", "CRS84", or OGC/ESRI WKT1.
func crsFromString(s string) (CRS, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return CRS{}, fmt.Errorf("empty projection")
	}
	up := strings.ToUpper(s)
	if strings.Contains(up, "CRS84") {
		return crsWGS84, nil
	}
	if strings.HasPrefix(up, "PROJCS") || strings.HasPrefix(up, "GEOGCS") {
		return crsFromWKT(s)
	}
	if m := epsgRe.FindStringSubmatch(s); m != nil {
		code, _ := strconv.Atoi(m[1])
		if c, ok := crsFromEPSG(code); ok {
			return c, nil
		}
		return CRS{}, fmt.Errorf("EPSG:%d is not built in; import the layer's .prj file or paste its WKT from epsg.io", code)
	}
	if n, err := strconv.Atoi(s); err == nil {
		if c, ok := crsFromEPSG(n); ok {
			return c, nil
		}
		return CRS{}, fmt.Errorf("EPSG:%d is not built in; paste its WKT from epsg.io", n)
	}
	return CRS{}, fmt.Errorf("unrecognised projection text")
}

var (
	wktParamRe = regexp.MustCompile(`(?i)PARAMETER\["([^"]+)",\s*([-0-9.eE+]+)\]`)
	wktSpherRe = regexp.MustCompile(`(?i)(?:SPHEROID|ELLIPSOID)\["[^"]*",\s*([-0-9.eE+]+),\s*([-0-9.eE+]+)`)
	wktProjRe  = regexp.MustCompile(`(?i)PROJECTION\["([^"]+)"`)
	wktUnitRe  = regexp.MustCompile(`(?i)UNIT\["([^"]+)",\s*([-0-9.eE+]+)`)
	wktNameRe  = regexp.MustCompile(`^\s*\w+\["([^"]+)"`)
	wktAuthRe  = regexp.MustCompile(`(?i)AUTHORITY\["EPSG",\s*"?(\d+)"?\]\s*\]\s*$`)
)

func crsFromWKT(w string) (CRS, error) {
	name := ""
	if m := wktNameRe.FindStringSubmatch(w); m != nil {
		name = strings.ReplaceAll(m[1], "_", " ")
	}
	if m := wktAuthRe.FindStringSubmatch(w); m != nil {
		code, _ := strconv.Atoi(m[1])
		if c, ok := crsFromEPSG(code); ok {
			return c, nil
		}
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(w)), "GEOGCS") {
		c := crsWGS84
		c.ID, c.Name, c.WKT = "WKT", name, w
		return c, nil
	}
	c := CRS{ID: "WKT", Name: name, WKT: w, A: grs80A, InvF: grs80InvF, K0: 1, Unit: 1}
	if m := wktSpherRe.FindStringSubmatch(w); m != nil {
		c.A, _ = strconv.ParseFloat(m[1], 64)
		c.InvF, _ = strconv.ParseFloat(m[2], 64)
	}
	if u := wktUnitRe.FindAllStringSubmatch(w, -1); len(u) > 0 {
		c.Unit, _ = strconv.ParseFloat(u[len(u)-1][2], 64)
	}
	p := map[string]float64{}
	for _, m := range wktParamRe.FindAllStringSubmatch(w, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		p[strings.ToLower(m[1])] = v
	}
	get := func(def float64, keys ...string) float64 {
		for _, k := range keys {
			if v, ok := p[k]; ok {
				return v
			}
		}
		return def
	}
	c.FE = get(0, "false_easting") * c.Unit
	c.FN = get(0, "false_northing") * c.Unit
	c.Lon0 = get(0, "central_meridian", "longitude_of_center", "longitude_of_origin", "longitude_of_natural_origin")
	c.Lat0 = get(0, "latitude_of_origin", "latitude_of_center", "latitude_of_natural_origin")
	c.Lat1 = get(c.Lat0, "standard_parallel_1")
	c.Lat2 = get(c.Lat1, "standard_parallel_2")
	c.K0 = get(1, "scale_factor", "scale_factor_at_natural_origin")
	proj := ""
	if m := wktProjRe.FindStringSubmatch(w); m != nil {
		proj = strings.ToLower(m[1])
	}
	switch {
	case strings.Contains(proj, "transverse_mercator") || strings.Contains(proj, "gauss_kruger"):
		c.Kind = "tm"
	case strings.Contains(proj, "lambert_conformal_conic"):
		c.Kind = "lcc"
	case strings.Contains(proj, "albers"):
		c.Kind = "aea"
	case strings.Contains(proj, "mercator_auxiliary_sphere") || strings.Contains(proj, "pseudo_mercator") || strings.Contains(proj, "popular_visualisation"):
		c.Kind = "merc"
	case strings.Contains(proj, "mercator") && c.InvF == 0:
		c.Kind = "merc"
	default:
		return CRS{}, fmt.Errorf("projection %q is not supported yet; reproject the layer to WGS84 or UTM", proj)
	}
	return c, nil
}

// ToWGS84 maps a source coordinate to lon/lat.
func (c *CRS) ToWGS84(x, y float64) (float64, float64) {
	if !c.ready {
		c.init()
	}
	return c.proj(x, y)
}

func (c *CRS) Fn() func(Pt) Pt {
	return func(p Pt) Pt {
		lon, lat := c.ToWGS84(p[0], p[1])
		return Pt{lon, lat}
	}
}

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
	if c.InvF != 0 {
		f = 1 / c.InvF
	}
	e2 := f * (2 - f)
	e := math.Sqrt(e2)
	lat0, lon0, lat1, lat2 := rad(c.Lat0), rad(c.Lon0), rad(c.Lat1), rad(c.Lat2)
	k0 := c.K0
	if k0 == 0 {
		k0 = 1
	}
	switch c.Kind {
	case "geo":
		c.proj = func(x, y float64) (float64, float64) {
			if c.Swap {
				return y, x
			}
			return x, y
		}
	case "merc":
		c.proj = func(x, y float64) (float64, float64) {
			x, y = x*unit, y*unit
			return x / earthR * deg, (2*math.Atan(math.Exp(y/earthR)) - math.Pi/2) * deg
		}
	case "tm":
		e4, e6 := e2*e2, e2*e2*e2
		mlfn := func(p float64) float64 {
			return a * ((1-e2/4-3*e4/64-5*e6/256)*p - (3*e2/8+3*e4/32+45*e6/1024)*math.Sin(2*p) + (15*e4/256+45*e6/1024)*math.Sin(4*p) - (35*e6/3072)*math.Sin(6*p))
		}
		m0 := mlfn(lat0)
		ep2 := e2 / (1 - e2)
		e1 := (1 - math.Sqrt(1-e2)) / (1 + math.Sqrt(1-e2))
		c.proj = func(x, y float64) (float64, float64) {
			x, y = x*unit-c.FE, y*unit-c.FN
			m := m0 + y/k0
			mu := m / (a * (1 - e2/4 - 3*e4/64 - 5*e6/256))
			p1 := mu + (3*e1/2-27*math.Pow(e1, 3)/32)*math.Sin(2*mu) + (21*e1*e1/16-55*math.Pow(e1, 4)/32)*math.Sin(4*mu) +
				(151*math.Pow(e1, 3)/96)*math.Sin(6*mu) + (1097*math.Pow(e1, 4)/512)*math.Sin(8*mu)
			s, co, t := math.Sin(p1), math.Cos(p1), math.Tan(p1)
			c1 := ep2 * co * co
			t1 := t * t
			n1 := a / math.Sqrt(1-e2*s*s)
			r1 := a * (1 - e2) / math.Pow(1-e2*s*s, 1.5)
			d := x / (n1 * k0)
			lat := p1 - (n1*t/r1)*(d*d/2-(5+3*t1+10*c1-4*c1*c1-9*ep2)*math.Pow(d, 4)/24+
				(61+90*t1+298*c1+45*t1*t1-252*ep2-3*c1*c1)*math.Pow(d, 6)/720)
			lon := lon0 + (d-(1+2*t1+c1)*math.Pow(d, 3)/6+(5-2*c1+28*t1-3*c1*c1+8*ep2+24*t1*t1)*math.Pow(d, 5)/120)/co
			return lon * deg, lat * deg
		}
	case "lcc":
		mf := func(p float64) float64 { return math.Cos(p) / math.Sqrt(1-e2*math.Sin(p)*math.Sin(p)) }
		tf := func(p float64) float64 {
			s := math.Sin(p)
			return math.Tan(math.Pi/4-p/2) / math.Pow((1-e*s)/(1+e*s), e/2)
		}
		m1, m2 := mf(lat1), mf(lat2)
		t0, t1, t2 := tf(lat0), tf(lat1), tf(lat2)
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
			x, y = x*unit-c.FE, rho0-(y*unit-c.FN)
			rho := sn * math.Hypot(x, y)
			theta := math.Atan2(sn*x, sn*y)
			t := math.Pow(rho/(a*k0*F), 1/n)
			lat := math.Pi/2 - 2*math.Atan(t)
			for i := 0; i < 15; i++ {
				s := math.Sin(lat)
				nl := math.Pi/2 - 2*math.Atan(t*math.Pow((1-e*s)/(1+e*s), e/2))
				if math.Abs(nl-lat) < 1e-12 {
					lat = nl
					break
				}
				lat = nl
			}
			return (theta/n + lon0) * deg, lat * deg
		}
	case "aea":
		mf := func(p float64) float64 { return math.Cos(p) / math.Sqrt(1-e2*math.Sin(p)*math.Sin(p)) }
		qf := func(p float64) float64 {
			s := math.Sin(p)
			if e == 0 {
				return 2 * s
			}
			return (1 - e2) * (s/(1-e2*s*s) - 1/(2*e)*math.Log((1-e*s)/(1+e*s)))
		}
		m1, m2 := mf(lat1), mf(lat2)
		q0, q1, q2 := qf(lat0), qf(lat1), qf(lat2)
		n := math.Sin(lat1)
		if math.Abs(lat1-lat2) > 1e-10 {
			n = (m1*m1 - m2*m2) / (q2 - q1)
		}
		C := m1*m1 + n*q1
		rho0 := a * math.Sqrt(C-n*q0) / n
		c.proj = func(x, y float64) (float64, float64) {
			x, y = x*unit-c.FE, y*unit-c.FN
			rho := math.Hypot(x, rho0-y)
			theta := math.Atan2(x, rho0-y)
			if n < 0 {
				rho, theta = -rho, math.Atan2(-x, -(rho0-y))
			}
			q := (C - rho*rho*n*n/(a*a)) / n
			lat := math.Asin(math.Max(-1, math.Min(1, q/2)))
			for i := 0; i < 20 && e > 0; i++ {
				s, co := math.Sin(lat), math.Cos(lat)
				d := (1 - e2*s*s) * (1 - e2*s*s) / (2 * co) * (q/(1-e2) - s/(1-e2*s*s) + 1/(2*e)*math.Log((1-e*s)/(1+e*s)))
				lat += d
				if math.Abs(d) < 1e-12 {
					break
				}
			}
			return (lon0 + theta/n) * deg, lat * deg
		}
	default:
		c.proj = func(x, y float64) (float64, float64) { return math.NaN(), math.NaN() }
	}
}

// CRSGuess is a candidate interpretation of unlabelled coordinates.
type CRSGuess struct {
	CRS    CRS    `json:"crs"`
	Reason string `json:"reason"`
}

// guessCRS ranks likely systems for a sample of raw coordinates. hint is a lon/lat near the data
// (another layer, or the map view) used to pick a UTM zone.
func guessCRS(sample []Pt, hint Pt) []CRSGuess {
	if len(sample) == 0 {
		return nil
	}
	var geo, geoSwap, utm, merc, bcalb int
	for _, p := range sample {
		x, y := p[0], p[1]
		switch {
		case math.Abs(x) <= 180 && math.Abs(y) <= 90:
			geo++
		case math.Abs(x) <= 90 && math.Abs(y) <= 180:
			geoSwap++
		}
		if x >= 160000 && x <= 840000 && y >= 0 && y <= 9400000 {
			utm++
		}
		if math.Abs(x) <= 20037509 && math.Abs(y) <= 20048967 && (x < -200 || math.Abs(x) > 1000000) {
			merc++
		}
		if x >= 200000 && x <= 1900000 && y >= 300000 && y <= 1800000 {
			bcalb++
		}
	}
	n := len(sample)
	var out []CRSGuess
	most := func(k int) bool { return k*10 >= n*9 }
	if most(geo) {
		out = append(out, CRSGuess{crsWGS84, "Values look like longitude and latitude in degrees"})
	}
	if most(geoSwap) && !most(geo) {
		c := crsWGS84
		c.Swap = true
		c.Name = "Latitude/longitude (columns swapped)"
		out = append(out, CRSGuess{c, "Values look like degrees with latitude first"})
	}
	if most(utm) {
		zone := 10
		if hint != (Pt{}) {
			zone = int(math.Floor((hint[0]+180)/6)) + 1
		}
		south := hint[1] < 0
		reason := fmt.Sprintf("Eastings and northings in metres; zone %d is a guess from the map", zone)
		out = append(out, CRSGuess{utmCRS(zone, south, true), reason})
	}
	if most(bcalb) {
		c, _ := crsFromEPSG(3005)
		out = append(out, CRSGuess{c, "Coordinates fit the BC Albers range"})
	}
	if most(merc) && !most(utm) {
		c, _ := crsFromEPSG(3857)
		out = append(out, CRSGuess{c, "Large metre values typical of web maps"})
	}
	return out
}

// commonCRS is the picker list offered when nothing could be detected.
func commonCRS() []CRS {
	var out []CRS
	for _, code := range []int{4326, 3857, 3005, 5070, 3347} {
		c, _ := crsFromEPSG(code)
		out = append(out, c)
	}
	for z := 1; z <= 23; z++ {
		out = append(out, utmCRS(z, false, true))
	}
	return out
}
