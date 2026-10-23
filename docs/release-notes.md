Pracre turns parcel and assessment data into value per acre and tax per acre maps in 2D and 3D.

**Downloads**

- **Windows 10 and 11** `Pracre-Windows-x64-Setup.exe` (installer) or `Pracre-Windows-x64-Portable.zip`
- **macOS 12 or later, Intel and Apple silicon** `Pracre-macOS-Universal.dmg`. On first launch, right-click Pracre and choose Open.
- **Ubuntu 22.04 or later, Debian 12** `Pracre-Linux-x64.deb` (`sudo apt install ./Pracre-Linux-x64.deb`)
- **Other Linux** `Pracre-Linux-x64.tar.gz` (needs WebKitGTK 4.1; run `./install.sh`)

**What's in it**

- Imports CSV, Excel, GeoJSON, Esri JSON, Shapefile (zipped or loose), KML and KMZ, by drag and drop or Import Data.
- Recognises IDs, values, tax classes, lot sizes, addresses and locations in any column layout, and shows every guess in an import window where each column's role can be changed.
- Reads parcel polygons or points in latitude/longitude, "lat, lon", degrees-minutes-seconds, WKT or projected X/Y, with coordinate systems from .prj, GeoJSON, Esri metadata or a guess checked on a mini-map.
- Links value tables to parcels by their shared ID, then by location, and reports the match rate in the checklist.
- Folds strata and condo units into the lots they sit on.
- Computes tax from mill rates, percentages or fractions per tax class, or uses tax amounts directly.
- Shows parcels, hexagon tiles or square tiles in 2D or 3D over OpenStreetMap, with quantile, natural breaks, logarithmic or equal interval legends and six colour ramps.
- Exports PNG images, self-contained 2D and 3D web maps with embed code, 3D models (.glb), GeoJSON and CSV.
- Saves whole projects, data included, in one .pracre file.
- Includes a Township of Langley sample (Walnut Grove and Fort Langley) to try everything in one click.
