package main

import (
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Column roles. A dataset's Roles slice parallels its columns.
const (
	RoleKey      = "key"
	RoleLat      = "lat"
	RoleLon      = "lon"
	RoleX        = "x"
	RoleY        = "y"
	RoleLatLon   = "latlon"  // "49.1, -122.6" in one column
	RoleGeom     = "geom"    // WKT or GeoJSON text
	RoleGrid     = "gridref" // MGRS / USNG, UTM text, Plus code or geohash
	RoleTotal    = "total"
	RoleLand     = "land"
	RoleImpr     = "impr"
	RoleTax      = "tax"
	RoleClass    = "class"      // tax code / property class per row
	RoleClassVal = "classvalue" // wide layout: one value column per class (Class holds the class)
	RoleArea     = "area"
	RoleAreaUnit = "areaunit"
	RoleAddress  = "address"
	RoleHouse    = "house"
	RoleStreet   = "street"
	RoleRateCode = "ratecode"
	RoleRate     = "rate"
	RoleRateDesc = "ratedesc"
	RoleYear     = "year" // roll or report year: only the latest year's rows are used
)

// singular roles may be held by one column only.
var singular = map[string]bool{RoleKey: true, RoleLat: true, RoleLon: true, RoleX: true, RoleY: true, RoleLatLon: true, RoleGeom: true, RoleGrid: true,
	RoleTotal: true, RoleLand: true, RoleImpr: true, RoleTax: true, RoleClass: true, RoleArea: true, RoleAreaUnit: true,
	RoleAddress: true, RoleHouse: true, RoleStreet: true, RoleRateCode: true, RoleRate: true, RoleRateDesc: true, RoleYear: true}

type ColRole struct {
	Role  string `json:"role"`
	Class string `json:"class,omitempty"` // classvalue: the tax class this column holds
	Unit  string `json:"unit,omitempty"`  // area: acre, ha, sqft, m2, crs, auto
	Score int    `json:"score"`           // 0 none, 1 guess, 2 likely, 3 sure
}

// parseNum reads "1,234.50", "$1,234", "(12.5)", "12 %" and similar.
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.ContainsAny(s, "$€£,% ") {
		s = strings.NewReplacer("$", "", "€", "", "£", "", ",", "", "%", "", " ", "").Replace(s)
	}
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

var dmsNums = regexp.MustCompile(`\d+(?:\.\d+)?`)

// parseDMS reads 49°10'12.3"N, 49 10 12.3 N, N49.17, -122.5 and the like.
func parseDMS(s string) (float64, bool) {
	if v, ok := parseNum(s); ok {
		return v, true
	}
	u := strings.ToUpper(strings.TrimSpace(s))
	if u == "" || !strings.ContainsAny(u, "0123456789") {
		return 0, false
	}
	nums := dmsNums.FindAllString(u, 3)
	if len(nums) == 0 {
		return 0, false
	}
	v := 0.0
	for i, n := range nums {
		f, _ := strconv.ParseFloat(n, 64)
		v += f / math.Pow(60, float64(i))
	}
	if strings.ContainsAny(u, "SW") || strings.HasPrefix(u, "-") {
		v = -v
	}
	return v, true
}

var pairRe = regexp.MustCompile(`^\s*[\(\[]?\s*(-?\d+(?:\.\d+)?)\s*[,;\s]\s*(-?\d+(?:\.\d+)?)\s*[\)\]]?\s*$`)

func parsePair(s string) (float64, float64, bool) {
	m := pairRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.ParseFloat(m[1], 64)
	b, _ := strconv.ParseFloat(m[2], 64)
	return a, b, true
}

var (
	reUTMText = regexp.MustCompile(`(?i)^\s*(?:zone\s*)?(\d{1,2})\s*([C-HJ-NP-X])\s*[,;\s]\s*(\d{5,7}(?:\.\d+)?)\s*m?E?\s*[,;\s]\s*(\d{6,8}(?:\.\d+)?)\s*m?N?\s*$`)
	reMGRS    = regexp.MustCompile(`(?i)^\s*(\d{1,2})\s*([C-HJ-NP-X])\s*([A-HJ-NP-Z])\s*([A-HJ-NP-V])\s*(\d{2,10})\s*(\d{0,5})\s*$`)
	rePlus    = regexp.MustCompile(`(?i)^\s*([23456789CFGHJMPQRVWX]{8})\+([23456789CFGHJMPQRVWX]{2,7})\s*$`)
	utmZones  sync.Map // zone (negative south) -> *CRS
)

// utmToLonLat converts a WGS 84 UTM coordinate.
func utmToLonLat(zone int, south bool, e, n float64) (float64, float64) {
	key := zone
	if south {
		key = -zone
	}
	v, ok := utmZones.Load(key)
	if !ok {
		code := 32600 + zone
		if south {
			code += 100
		}
		c, err := crsFromCode(code)
		if err != nil {
			return math.NaN(), math.NaN()
		}
		c.init()
		v, _ = utmZones.LoadOrStore(key, &c)
	}
	return v.(*CRS).proj(e, n)
}

// parseGridRef reads the grid references people put in one column: MGRS / USNG ("10U EK 25199 32031"),
// UTM ("10U 525199 5432031") and full Plus codes ("84VVJP9V+5V"). It returns lon/lat in degrees.
func parseGridRef(s string) (float64, float64, bool) {
	if m := rePlus.FindStringSubmatch(s); m != nil {
		return decodePlus(strings.ToUpper(m[1] + m[2]))
	}
	if m := reMGRS.FindStringSubmatch(s); m != nil && (len(m[5])+len(m[6]))%2 == 0 {
		zone, _ := strconv.Atoi(m[1])
		band, col, row := strings.ToUpper(m[2])[0], strings.ToUpper(m[3]), strings.ToUpper(m[4])
		digits := m[5] + m[6]
		half := len(digits) / 2
		scale := math.Pow(10, float64(5-half))
		e, _ := strconv.ParseFloat(digits[:half], 64)
		n, _ := strconv.ParseFloat(digits[half:], 64)
		e, n = e*scale, n*scale
		if zone < 1 || zone > 60 || half > 5 {
			return 0, 0, false
		}
		cols := []string{"STUVWXYZ", "ABCDEFGH", "JKLMNPQR"}[zone%3]
		ci := strings.Index(cols, col)
		rows := "ABCDEFGHJKLMNPQRSTUV"
		ri := strings.Index(rows, row)
		if ci < 0 || ri < 0 {
			return 0, 0, false
		}
		if zone%2 == 0 {
			ri = (ri + 15) % 20 // even zones start the rows at F
		}
		e += float64(ci+1) * 100000
		n += float64(ri) * 100000
		// The row letters repeat every 2000 km; the latitude band says which repeat.
		minN := map[byte]float64{'C': 1100000, 'D': 2000000, 'E': 2800000, 'F': 3700000, 'G': 4600000, 'H': 5500000, 'J': 6400000, 'K': 7300000,
			'L': 8200000, 'M': 9100000, 'N': 0, 'P': 800000, 'Q': 1700000, 'R': 2600000, 'S': 3500000, 'T': 4400000, 'U': 5300000, 'V': 6200000,
			'W': 7000000, 'X': 7900000}[band]
		for n < minN {
			n += 2000000
		}
		lon, lat := utmToLonLat(zone, band < 'N', e, n)
		return lon, lat, !math.IsNaN(lon)
	}
	if m := reUTMText.FindStringSubmatch(s); m != nil {
		zone, _ := strconv.Atoi(m[1])
		letter := strings.ToUpper(m[2])[0]
		e, _ := strconv.ParseFloat(m[3], 64)
		n, _ := strconv.ParseFloat(m[4], 64)
		if zone < 1 || zone > 60 {
			return 0, 0, false
		}
		// "10N" means the northern hemisphere and "10S" the southern, unless S is the latitude band
		// (32 to 40 degrees north) that the northing fits.
		south := letter < 'N'
		if letter == 'S' && (n < 3500000 || n > 4500000) {
			south = true
		}
		lon, lat := utmToLonLat(zone, south, e, n)
		return lon, lat, !math.IsNaN(lon)
	}
	return 0, 0, false
}

// decodePlus decodes a full Open Location Code (Plus code) to the centre of its area.
func decodePlus(code string) (float64, float64, bool) {
	const alpha = "23456789CFGHJMPQRVWX"
	lat, lon, res := -90.0, -180.0, 20.0
	for i := 0; i+1 < len(code) && i < 10; i += 2 {
		a, b := strings.IndexByte(alpha, code[i]), strings.IndexByte(alpha, code[i+1])
		if a < 0 || b < 0 {
			return 0, 0, false
		}
		lat += float64(a) * res
		lon += float64(b) * res
		res /= 20
	}
	res *= 20
	latRes, lonRes := res, res
	for _, ch := range code[10:] { // grid refinement: 4 rows by 5 columns
		k := strings.IndexRune(alpha, ch)
		latRes, lonRes = latRes/5, lonRes/4
		lat += float64(k/4) * latRes
		lon += float64(k%4) * lonRes
	}
	return lon + lonRes/2, lat + latRes/2, true
}

// decodeGeohash decodes a geohash to the centre of its cell.
func decodeGeohash(s string) (float64, float64, bool) {
	const b32 = "0123456789bcdefghjkmnpqrstuvwxyz"
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 4 || len(s) > 12 {
		return 0, 0, false
	}
	lat, lon := [2]float64{-90, 90}, [2]float64{-180, 180}
	even := true
	for _, ch := range s {
		v := strings.IndexRune(b32, ch)
		if v < 0 {
			return 0, 0, false
		}
		for bit := 4; bit >= 0; bit-- {
			r := &lat
			if even {
				r = &lon
			}
			mid := (r[0] + r[1]) / 2
			if v>>bit&1 == 1 {
				r[0] = mid
			} else {
				r[1] = mid
			}
			even = !even
		}
	}
	return (lon[0] + lon[1]) / 2, (lat[0] + lat[1]) / 2, true
}

func looksGeomText(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 8 {
		return false
	}
	u := strings.ToUpper(s[:min(len(s), 20)])
	if strings.HasPrefix(u, "SRID=") || strings.HasPrefix(u, "POINT") || strings.HasPrefix(u, "POLYGON") || strings.HasPrefix(u, "MULTIPOLYGON") || strings.HasPrefix(u, "MULTIPOINT") {
		return true
	}
	return s[0] == '{' && strings.Contains(s, "coordinates") || looksHexWKB(s)
}

// looksHexWKB spots the hex (E)WKB that PostGIS and Carto write into CSV geometry columns.
func looksHexWKB(s string) bool {
	if len(s) < 42 || len(s)%2 != 0 || (s[:2] != "00" && s[:2] != "01") {
		return false
	}
	_, err := hex.DecodeString(s[:18])
	return err == nil
}

func parseGeomText(s string) (Geom, int) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return parseGeoJSONGeom([]byte(s)), 0
	}
	if looksHexWKB(s) {
		return parseHexWKB(s)
	}
	return parseWKT(s)
}

var unitWordRe = regexp.MustCompile(`(?i)^\s*(acres?|ac|sq\.? ?f(ee|oo)?t\.?|square ?f(ee|oo)t|sf|ft2|hectares?|ha|sq\.? ?m\.?|square ?met(re|er)s?|m2|sqm)\s*$`)

func unitFromWord(s string) string {
	u := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(u, "ac"):
		return "acre"
	case strings.HasPrefix(u, "h"):
		return "ha"
	case strings.Contains(u, "f") || u == "sf":
		return "sqft"
	case u != "":
		return "m2"
	}
	return ""
}

// areaFactor converts an area unit to square metres.
func areaFactor(unit string, crsUnit float64) float64 {
	switch unit {
	case "acre":
		return 4046.8564224
	case "ha":
		return 10000
	case "sqft":
		return 0.09290304
	case "crs":
		if crsUnit > 0 {
			return crsUnit * crsUnit
		}
	}
	return 1
}

type colStat struct {
	nonEmpty, num, geom, pair, unitWord, dms, grid int
	nums                                           []float64
	distinct                                       int
	uniq                                           float64
	maxLen                                         int
}

func (s colStat) frac(k int) float64 {
	if s.nonEmpty == 0 {
		return 0
	}
	return float64(k) / float64(s.nonEmpty)
}

func (s colStat) numeric() bool { return s.nonEmpty > 0 && s.frac(s.num) >= 0.8 }

func (s colStat) within(lo, hi float64) bool {
	if len(s.nums) == 0 {
		return false
	}
	in := 0
	for _, v := range s.nums {
		if v >= lo && v <= hi {
			in++
		}
	}
	return float64(in) >= 0.95*float64(len(s.nums))
}

func (s colStat) median() float64 {
	if len(s.nums) == 0 {
		return 0
	}
	c := append([]float64(nil), s.nums...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func statColumn(t *Table, ci int) colStat {
	var st colStat
	n := len(t.Rows)
	step := max(1, n/600)
	for r := 0; r < n; r += step {
		v := strings.TrimSpace(t.col(ci, r))
		if v == "" {
			continue
		}
		st.nonEmpty++
		st.maxLen = max(st.maxLen, len(v))
		if f, ok := parseNum(v); ok {
			st.num++
			st.nums = append(st.nums, f)
		} else if looksGeomText(v) {
			st.geom++
		} else if a, b, ok := parsePair(v); ok && math.Abs(a) <= 180 && math.Abs(b) <= 180 {
			st.pair++ // degrees only: "7910655;17" is a legal description, not a location
		} else if _, _, ok := parseGridRef(v); ok {
			st.grid++
		} else if unitWordRe.MatchString(v) {
			st.unitWord++
		} else if strings.ContainsAny(v, "°'\"NSEWnsew") && dmsNums.MatchString(v) && len(v) < 24 {
			st.dms++
		}
	}
	seen := map[string]struct{}{}
	filled := 0
	for r := 0; r < n && r < 20000; r++ {
		if v := t.col(ci, r); v != "" {
			seen[v] = struct{}{}
			filled++
		}
	}
	st.distinct = len(seen)
	if filled > 0 {
		st.uniq = float64(st.distinct) / float64(filled)
	}
	return st
}

var normRe = regexp.MustCompile(`[^a-z0-9]+`)

// normName lowercases a header and drops table prefixes: "ParcelMaster.ParcelNumber" -> "parcelnumber".
func normName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	// Split camelCase so TaxableValue reads as taxable_value.
	var b strings.Builder
	prevLower := false
	for _, r := range s {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && prevLower {
			b.WriteByte('_')
		}
		b.WriteRune(r)
		prevLower = (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}
	return strings.Trim(normRe.ReplaceAllString(strings.ToLower(b.String()), "_"), "_")
}

func re(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var (
	reKey     = re(`^(pid|pin|apn|ain|folio|folio_?(no|num(ber)?|id)|roll|roll_?(no|num(ber)?)|parcel|parcel_?(id|no|num(ber)?|key|code|pin)|parcelno|parcelnum(ber)?|parcelid|mstparcel|account|acct|account_?(no|num(ber)?|id)|acct_?(no|num(ber)?)|tax_?(id|key|map|account|acct)|prop(erty)?_?(id|no|num(ber)?|key)|lot_?id|map_?lot|maplot|sbl|bbl|gis_?link|gislink|property_?number|asmt_?(no|num(ber)?)|assessment_?(no|num(ber)?|id)|strap|pidn|parid|geo_?id|land_?id|tax_?parcel(_?(id|no|num(ber)?))?|parcel_?number)$`)
	reLat     = re(`^(lat|latitude|lat_?dd|lat_?deg|y_?lat|lat_?y|point_?lat|centroid_?lat|lat_?wgs84|wgs84_?lat|ylat|gps_?lat|lat_?decimal)$|latitude`)
	reLon     = re(`^(lon|lng|long|longitude|lon_?dd|long_?dd|lon_?deg|x_?lon|lon_?x|x_?long|point_?lon|centroid_?lon|lon_?wgs84|wgs84_?lon|xlong|gps_?lon|gps_?long|lon_?decimal)$|longitude`)
	reX       = re(`^(x|x_?coord(inate)?|xcoord|easting|east|point_?x|centroid_?x|x_?centroid|coord_?x|utm_?e(asting)?|utm_?x|x_?utm|xc|x_?pos|pos_?x|e)$`)
	reY       = re(`^(y|y_?coord(inate)?|ycoord|northing|north|point_?y|centroid_?y|y_?centroid|coord_?y|utm_?n(orthing)?|utm_?y|y_?utm|yc|y_?pos|pos_?y|n)$`)
	reArea    = re(`(^|_)(acres?|acreage|area|lot_?size|lotsize|land_?size|lot_?area|lot_?sq_?ft|sq_?ft|sqft|square_?f(ee|oo)t|hectares?|ha|shape_?area|st_?area|land_?area|parcel_?area|gis_?acres?|calc_?acres?|calc_?area|deeded_?acres?|land_?sq_?ft|map_?acres?|mapacres|totalacre|total_?acres?|lot_?acres?|site_?area|sqm|sq_?m|m2)($|_)`)
	reAreaNot = re(`floor|flr|bldg|build|living|liv_|heated|gla|frontage|depth|perim|leng|unit|desc|code|zone|zoning|value|val$|price|assess`)
	reTax     = re(`^(tax|taxes|tax_?amount|tax_?amt|total_?tax(es)?|tax_?total|tax_?levy|levy|levy_?amount|annual_?tax(es)?|net_?tax(es)?|taxes_?paid|tax_?paid|tax_?bill|gross_?tax(es)?|current_?tax(es)?|general_?tax(es)?|property_?tax(es)?|municipal_?tax(es)?|total_?levy|amount_?billed|billed_?amount|tax_?due|taxes_?billed|tax_?dollars|total_?taxes_?billed|ad_?valorem(_?tax)?)$`)
	reClass   = re(`^(tax_?(code|class|class_?code|rate_?code|rate_?area|district)|prop(erty)?_?class(_?code)?|class|class_?code|classification|parclass|par_?class|manual_?class|manualclass|assessment_?class|assess_?class|tra|levy_?code|mill_?code|district_?code)$`)
	reUse     = re(`^(use_?code|usecode|land_?use(_?code)?|luc|actual_?use(_?code)?|auc|pc_?code|pccode|prop_?type|property_?type)$`) // a weaker stand-in for a tax class
	reYear    = re(`^((report|tax|roll|assessment|assess|asmt|fiscal|levy|bill|billing|valuation|tax_assessment|assessment_roll)_?)?(year|yr)$`)
	reAddr    = re(`^(address|addr|site_?addr(ess)?|situs(_?addr(ess)?)?|prop(erty)?_?addr(ess)?|propaddr|full_?addr(ess)?|location_?addr(ess)?|street_?address|civic_?address|loc_?addr|siteaddress|situsaddr)$`)
	reHouse   = re(`^(house|house_?(no|numb?(er)?|nbr)|street_?(no|numb?(er)?|nbr)|civic(_?(no|numb?(er)?))?|addr_?(no|numb?(er)?)|mpaddrno|st_?num|hse_?num)$`)
	reStreet  = re(`^(street|street_?name|st_?name|road|road_?name|mpaddrstna|street_?nm|streetname)$`)
	reName    = re(`^(name|label|title)$`)
	reRate    = re(`(^|_)(rate|rates|mill|mills|millage|mill_?rate|levy_?rate|tax_?rate|percent|pct|per_?1000)($|_)`)
	reRateDsc = re(`^(desc|description|name|label|class_?name|title)$`)
	reWide    = re(`^(.+?)_(land|buildings?|improvements?|impr|bldg|imp)$`)
	reWideNot = re(`total|gross|^net$|^net_|assess|taxable|exempt|market|mkt|apprais|current|^curr?$|prior|prev|cert|final`) // value qualifiers, not tax classes
	// Words are matched inside names, so "current" and "county" must not trip "rent" and "count".
	reNotVal = re(`sale|price|sold|loan|mortgage|(^|_)rent|income|year|date|code|desc|class|type|ratio|pct|percent|exempt|deduct|homestead|credit|rebate|prior|prev|last|base|(^|_)counts?($|_)|count$|^num|num_|_num|no$|area|acre|sqft|size|map|grp|qty|sel|table|unit|floor|flr|depth|front|zone|rate|mill|lat|lon|coord`)
)

// valueRole scores a numeric column name as a property value.
func valueRole(n string) (string, int) {
	if reNotVal.MatchString(n) && !strings.Contains(n, "taxable") {
		return "", 0
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	// Taxable value beats assessed or market value, also when it comes split into land and buildings.
	bonus := 0
	if has("taxable") {
		bonus = 2
	}
	switch {
	case has("land", "lnd"):
		return RoleLand, 8 + bonus
	case has("improvement", "impr", "bldg", "building", "structure") || n == "imp" || strings.HasPrefix(n, "imp_"):
		return RoleImpr, 8 + bonus
	case bonus > 0:
		return RoleTotal, 10
	case has("total", "tot_", "totval") && has("value", "val", "assess", "apprais", "market", "mkt", "av"):
		return RoleTotal, 9
	case has("assess", "apprais", "market_value", "mkt_val", "fair_market", "fmv", "just_value"):
		return RoleTotal, 7
	case n == "total" || n == "av" || n == "value" || n == "val" || has("_value", "value_") || strings.HasSuffix(n, "val"):
		return RoleTotal, 4
	}
	return "", 0
}

// detectRoles guesses what every column holds. The UI shows the guesses and lets people correct them.
func detectRoles(t *Table) []ColRole {
	roles := make([]ColRole, len(t.Cols))
	stats := make([]colStat, len(t.Cols))
	names := make([]string, len(t.Cols))
	for i := range t.Cols {
		stats[i] = statColumn(t, i)
		names[i] = normName(t.Cols[i])
	}
	set := func(i int, r ColRole) {
		if roles[i].Role == "" {
			roles[i] = r
		}
	}

	// Rates tables: short, a code column and a rate column, no locations.
	if len(t.Rows) <= 300 && t.Geoms == nil {
		rateCol, codeCol := -1, -1
		for i, n := range names {
			if stats[i].numeric() && reRate.MatchString(n) && rateCol < 0 {
				rateCol = i
			}
		}
		// Unnamed fallback: exactly a text column and a small-number column, nothing that looks like an ID or value.
		if rateCol < 0 && len(t.Cols) == 2 {
			for i, n := range names {
				if stats[i].numeric() && stats[i].within(0, 100) && !reKey.MatchString(n) && !reTax.MatchString(n) {
					rateCol = i
				}
			}
		}
		for i, n := range names {
			if i != rateCol && !stats[i].numeric() && stats[i].uniq > 0.95 && codeCol < 0 && !reRateDsc.MatchString(n) && stats[i].frac(stats[i].geom+stats[i].pair) < 0.5 {
				codeCol = i
			}
		}
		if codeCol < 0 {
			for i := range names {
				if i != rateCol && stats[i].uniq > 0.95 && codeCol < 0 {
					codeCol = i
				}
			}
		}
		latlonLike := false // any location column means this isn't a rates table
		for i, n := range names {
			if reLat.MatchString(n) || reLon.MatchString(n) || reX.MatchString(n) || stats[i].frac(stats[i].geom+stats[i].pair) >= 0.5 {
				latlonLike = true
			}
		}
		if rateCol >= 0 && codeCol >= 0 && !latlonLike && (len(t.Cols) <= 4 || reRate.MatchString(names[rateCol])) {
			roles[rateCol] = ColRole{Role: RoleRate, Score: 3}
			roles[codeCol] = ColRole{Role: RoleRateCode, Score: 3}
			for i, n := range names {
				if roles[i].Role == "" && reRateDsc.MatchString(n) {
					roles[i] = ColRole{Role: RoleRateDesc, Score: 2}
					break
				}
			}
			return roles
		}
	}

	// Wide layouts: Residential_Land, Residential_Buildings, Business_Land ...
	wide := map[string][]int{}
	for i, n := range names {
		if m := reWide.FindStringSubmatch(n); m != nil && stats[i].numeric() {
			p := m[1]
			if reWideNot.MatchString(p) {
				continue
			}
			wide[p] = append(wide[p], i)
		}
	}
	if len(wide) >= 2 {
		for _, cols := range wide {
			for _, i := range cols {
				// Class name from the original header: everything before the last underscore part.
				orig := t.Cols[i]
				if k := strings.LastIndexAny(orig, "_ "); k > 0 {
					orig = orig[:k]
				}
				roles[i] = ColRole{Role: RoleClassVal, Class: orig, Score: 3}
			}
		}
	}

	for i, n := range names {
		st := stats[i]
		switch {
		case roles[i].Role != "":
		case st.frac(st.geom) >= 0.8:
			set(i, ColRole{Role: RoleGeom, Score: 3})
		case st.frac(st.pair) >= 0.8:
			set(i, ColRole{Role: RoleLatLon, Score: 3})
		case st.frac(st.grid) >= 0.8 || strings.Contains(n, "geohash") && !st.numeric() && st.nonEmpty > 0:
			set(i, ColRole{Role: RoleGrid, Score: 3})
		case reLat.MatchString(n) && (st.within(-180, 180) || st.frac(st.dms) > 0.8): // swapped columns are fixed below
			set(i, ColRole{Role: RoleLat, Score: 3})
		case reLon.MatchString(n) && (st.within(-180, 180) || st.frac(st.dms) > 0.8):
			set(i, ColRole{Role: RoleLon, Score: 3})
		case reX.MatchString(n) && st.numeric():
			set(i, ColRole{Role: RoleX, Score: 2})
		case reY.MatchString(n) && st.numeric():
			set(i, ColRole{Role: RoleY, Score: 2})
		case st.frac(st.unitWord) >= 0.7:
			set(i, ColRole{Role: RoleAreaUnit, Score: 3})
		case reKey.MatchString(n) && st.uniq > 0.3:
			set(i, ColRole{Role: RoleKey, Score: 3})
		case reTax.MatchString(n) && st.numeric():
			set(i, ColRole{Role: RoleTax, Score: 3})
		case reArea.MatchString(n) && !reAreaNot.MatchString(n) && st.numeric():
			unit, score := "auto", 1
			switch {
			case strings.Contains(n, "acre") || regexp.MustCompile(`(^|_)ac($|_)`).MatchString(n):
				unit, score = "acre", 3
			case strings.Contains(n, "hectare") || regexp.MustCompile(`(^|_)ha($|_)`).MatchString(n):
				unit, score = "ha", 3
			case regexp.MustCompile(`sq_?ft|sqft|square_?f|(^|_)sf($|_)`).MatchString(n):
				unit, score = "sqft", 3
			case regexp.MustCompile(`sqm|sq_?m($|_)|m2|square_?met|(^|_)sm($|_)`).MatchString(n):
				unit, score = "m2", 3
			case strings.Contains(n, "shape_area") || strings.Contains(n, "st_area"):
				unit, score = "crs", 2
			}
			set(i, ColRole{Role: RoleArea, Unit: unit, Score: score})
		case reYear.MatchString(n) && st.numeric() && st.within(1900, 2100) && st.distinct <= 12: // roll years, not years built
			score := 2
			if strings.Contains(n, "assess") {
				score = 1 // can lag the report or roll year, or be blank
			}
			set(i, ColRole{Role: RoleYear, Score: score})
		case reClass.MatchString(n) && st.distinct <= 200 && st.nonEmpty > 0:
			set(i, ColRole{Role: RoleClass, Score: 2})
		case reUse.MatchString(n) && st.distinct <= 200 && st.nonEmpty > 0:
			set(i, ColRole{Role: RoleClass, Score: 1})
		case reAddr.MatchString(n):
			set(i, ColRole{Role: RoleAddress, Score: 3})
		case reHouse.MatchString(n):
			set(i, ColRole{Role: RoleHouse, Score: 2})
		case reStreet.MatchString(n):
			set(i, ColRole{Role: RoleStreet, Score: 2})
		case reName.MatchString(n) && !st.numeric():
			set(i, ColRole{Role: RoleAddress, Score: 1})
		case st.numeric():
			if r, s := valueRole(n); r != "" {
				set(i, ColRole{Role: r, Score: s}) // fine-grained score also ranks sources across files
			}
		}
	}

	// With per-class value columns the classes are already known; a use-code column is not a tax class.
	if len(wide) >= 2 {
		for i := range roles {
			if roles[i].Role == RoleClass {
				roles[i] = ColRole{}
			}
		}
	}

	// Lat/lon swapped in the source (a "lat" column full of -122s).
	li, lo := indexOfRole(roles, RoleLat), indexOfRole(roles, RoleLon)
	if li >= 0 && lo >= 0 && !stats[li].within(-90, 90) && stats[lo].within(-90, 90) {
		roles[li].Role, roles[lo].Role = RoleLon, RoleLat
	}

	// One column per singular role: keep the best score, earliest wins ties.
	best := map[string]int{}
	for i, r := range roles {
		if !singular[r.Role] {
			continue
		}
		if j, ok := best[r.Role]; !ok || r.Score > roles[j].Score {
			best[r.Role] = i
		}
	}
	for i, r := range roles {
		if singular[r.Role] && best[r.Role] != i {
			roles[i] = ColRole{}
		}
	}
	// An x/y pair whose values are degrees is really lon/lat.
	xi, yi := indexOfRole(roles, RoleX), indexOfRole(roles, RoleY)
	if xi >= 0 && yi >= 0 && stats[xi].within(-180, 180) && stats[yi].within(-90, 90) {
		roles[xi].Role, roles[yi].Role = RoleLon, RoleLat
	}
	return roles
}

func indexOfRole(roles []ColRole, role string) int {
	for i, r := range roles {
		if r.Role == role {
			return i
		}
	}
	return -1
}

// normKey makes IDs comparable across files: "000-561-291" and "561291" both become "561291".
func normKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	k := strings.TrimLeft(b.String(), "0")
	if k == "" && b.Len() > 0 {
		return "0"
	}
	return k
}

// keyCandidates lists columns worth trying as join keys: the key role first, then other ID-like text.
// many allows a key that repeats, as on a values table where every strata unit carries its lot's ID.
func keyCandidates(t *Table, roles []ColRole, many bool) []int {
	var out []int
	if k := indexOfRole(roles, RoleKey); k >= 0 {
		out = append(out, k)
	}
	for i := range t.Cols {
		if len(out) >= 10 {
			break
		}
		if i == indexOfRole(roles, RoleKey) {
			continue
		}
		switch roles[i].Role {
		case "", RoleAddress:
		default:
			continue
		}
		st := statColumn(t, i)
		uniq := st.uniq >= 0.5 || many && st.uniq >= 0.2 && st.distinct >= 50
		if uniq && st.nonEmpty > 0 && st.maxLen >= 3 && st.maxLen <= 40 && st.frac(st.geom+st.pair) < 0.1 {
			// Pure decimals aren't IDs.
			frac := false
			for _, v := range st.nums {
				if v != math.Trunc(v) {
					frac = true
					break
				}
			}
			if !frac {
				out = append(out, i)
			}
		}
	}
	return out
}

// JoinGuess is the best key pair found between two tables.
type JoinGuess struct {
	ColA, ColB int
	Rate       float64 // share of B's sampled keys found in A
}

func bestKeyJoin(a *Table, ra []ColRole, b *Table, rb []ColRole) JoinGuess {
	best := JoinGuess{-1, -1, 0}
	ca, cb := keyCandidates(a, ra, false), keyCandidates(b, rb, true)
	sets := make([]map[string]struct{}, len(ca))
	for k, i := range ca {
		s := make(map[string]struct{}, len(a.Rows))
		for r := range a.Rows {
			if v := normKey(a.col(i, r)); v != "" {
				s[v] = struct{}{}
			}
		}
		sets[k] = s
	}
	n := len(b.Rows)
	step := max(1, n/2000)
	for _, j := range cb {
		var sample []string
		for r := 0; r < n; r += step {
			if v := normKey(b.col(j, r)); v != "" {
				sample = append(sample, v)
			}
		}
		if len(sample) == 0 {
			continue
		}
		for k, i := range ca {
			hit := 0
			for _, v := range sample {
				if _, ok := sets[k][v]; ok {
					hit++
				}
			}
			rate := float64(hit) / float64(len(sample))
			// Prefer declared keys on ties.
			if rate > best.Rate+0.02 || (rate > best.Rate && k == 0) {
				best = JoinGuess{i, j, rate}
			}
		}
	}
	return best
}

// locate turns a table's location columns (or file geometry) into raw coordinates.
// It reports the SRID found in EWKT and whether the values are known to be degrees.
func locate(t *Table, roles []ColRole) (raw []Geom, degrees bool, srid int) {
	if t.Geoms != nil {
		return t.Geoms, false, 0
	}
	raw = make([]Geom, len(t.Rows))
	if gi := indexOfRole(roles, RoleGeom); gi >= 0 {
		for r := range t.Rows {
			g, s := parseGeomText(t.col(gi, r))
			raw[r] = g
			if s != 0 {
				srid = s
			}
			if strings.HasPrefix(strings.TrimSpace(t.col(gi, r)), "{") {
				degrees = true
			}
		}
		return raw, degrees, srid
	}
	if gi := indexOfRole(roles, RoleGrid); gi >= 0 {
		gh := strings.Contains(normName(t.Cols[gi]), "geohash")
		for r := range t.Rows {
			lon, lat, ok := parseGridRef(t.col(gi, r))
			if gh {
				lon, lat, ok = decodeGeohash(t.col(gi, r))
			}
			if ok {
				raw[r] = pointGeom(lon, lat)
			}
		}
		return raw, true, 0
	}
	if pi := indexOfRole(roles, RoleLatLon); pi >= 0 {
		// Decide the order once per column: if the first number exceeds 90 it is a longitude.
		lonFirst := 0
		for r := range t.Rows {
			if a, _, ok := parsePair(t.col(pi, r)); ok && math.Abs(a) > 90 {
				lonFirst++
			}
		}
		for r := range t.Rows {
			if a, b, ok := parsePair(t.col(pi, r)); ok {
				if lonFirst*2 > len(t.Rows) {
					raw[r] = pointGeom(a, b)
				} else {
					raw[r] = pointGeom(b, a)
				}
			}
		}
		return raw, true, 0
	}
	if la, lo := indexOfRole(roles, RoleLat), indexOfRole(roles, RoleLon); la >= 0 && lo >= 0 {
		for r := range t.Rows {
			y, ok1 := parseDMS(t.col(la, r))
			x, ok2 := parseDMS(t.col(lo, r))
			if ok1 && ok2 && !(x == 0 && y == 0) {
				raw[r] = pointGeom(x, y)
			}
		}
		return raw, true, 0
	}
	if xi, yi := indexOfRole(roles, RoleX), indexOfRole(roles, RoleY); xi >= 0 && yi >= 0 {
		for r := range t.Rows {
			x, ok1 := parseNum(t.col(xi, r))
			y, ok2 := parseNum(t.col(yi, r))
			if ok1 && ok2 && !(x == 0 && y == 0) {
				raw[r] = pointGeom(x, y)
			}
		}
		return raw, false, 0
	}
	return nil, false, 0
}

func sampleCoords(raw []Geom, n int) []Pt {
	var out []Pt
	step := max(1, len(raw)/n)
	for i := 0; i < len(raw); i += step {
		if raw[i].OK {
			if raw[i].IsPoly() {
				out = append(out, raw[i].Polys[0][0][0])
			} else {
				out = append(out, raw[i].Pt)
			}
		}
	}
	return out
}
