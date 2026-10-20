package boq

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/kwaabs/voltedge/api/internal/xlsx"
	"github.com/uptrace/bun"
)

// assetRegisterMaxRows bounds a single register run so a whole-network scope can
// never stream an unbounded result into memory (and a spreadsheet). A register
// that hits it comes back Truncated, the same fail-loud convention the trace and
// powerflow engines use at their caps.
const assetRegisterMaxRows = 100000

// RunAssetRegister returns the in-scope features as an itemised asset register --
// one row per feature, the per-feature sibling of RunBoQ's grouped schedule. It
// shares the scope resolution (classSelection + crud.boqWhere) and, when
// IncludeValuation is set, the exact catalog/override/material pricing path, so a
// register's value reconciles with the priced schedule for the same scope.
func RunAssetRegister(ctx context.Context, db bun.IDB, req RegisterRequest) (*RegisterResponse, error) {
	// Scope + class selection reuse the priced engine's logic verbatim.
	base := Request{FeatureClasses: req.FeatureClasses, Scope: req.Scope, FeatureRefs: req.FeatureRefs}
	classIDs, classOrder := classSelection(ctx, db, base)

	var scope crud.BoQScope
	if req.Scope != nil {
		scope = crud.BoQScope{
			Kind:        req.Scope.Kind,
			TerritoryID: req.Scope.TerritoryID,
			Center:      req.Scope.Center,
			RadiusM:     req.Scope.RadiusM,
			BBox:        req.Scope.BBox,
		}
	}

	// Territory id -> name, so the register shows a readable territory, not a uuid.
	// A small, global set; a failure here degrades to bare ids rather than erroring.
	territoryName := map[string]string{}
	if terrs, err := territory.ListTerritories(ctx, db); err == nil {
		for _, t := range terrs {
			territoryName[t.ID] = t.Name
		}
	}

	// Material maps are always built (two lightweight list queries): the register
	// surfaces each asset's Material regardless of whether a value is computed.
	specByValueCode, materialDomainIDs, err := loadMaterialMaps(ctx, db)
	if err != nil {
		return nil, err
	}

	resp := &RegisterResponse{
		Rows:             []RegisterRow{},
		IncludeValuation: req.IncludeValuation,
		Currencies:       []CurrencyOut{},
	}

	// Rate/override maps and currency conversion are only built when a valuation
	// was asked for -- an inventory register pays none of those queries.
	var (
		ratesByClass    map[string][]metadata.CostRate
		overrideByClass map[string]map[string]float64
		perBase         = 1.0
	)
	currencies, err := metadata.ListCurrencies(ctx, db)
	if err != nil {
		return nil, err
	}
	resp.Currencies = currencyOut(currencies)
	if req.IncludeValuation {
		ratesByClass, overrideByClass, err = loadRateMaps(ctx, db)
		if err != nil {
			return nil, err
		}
		baseCur, display, err := resolveCurrencies(currencies, req.DisplayCurrency)
		if err != nil {
			return nil, err
		}
		perBase = display.PerBase
		resp.Currency = display.Code
		resp.CurrencyName = display.Name
		resp.BaseCurrency = baseCur.Code
		resp.PerBase = display.PerBase
		var zero float64
		resp.TotalValue = &zero
	}

	for _, code := range classOrder {
		if len(resp.Rows) >= assetRegisterMaxRows {
			resp.Truncated = true
			break
		}
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			continue
		}
		fc := loaded.FeatureClass
		if fc.PhysicalTable == nil {
			continue // not compiled -- nothing to list
		}
		linear := fc.GeometryType == "line"

		clsScope := scope
		clsScope.FeatureIDs = classIDs[code]
		if clsScope.IsSpatial() && fc.GeometryType == "none" {
			continue // a spatial scope can't locate a geometry-less class
		}

		materialField := findMaterialField(loaded, materialDomainIDs)

		remaining := assetRegisterMaxRows - len(resp.Rows)
		rows, err := crud.AssetRegisterRows(ctx, db, loaded, materialField, linear, clsScope, remaining)
		if err != nil {
			if crud.IsNotFoundError(err) {
				continue
			}
			return nil, err
		}

		subtypeName := map[int]string{}
		for _, st := range loaded.Subtypes {
			subtypeName[st.Code] = st.Name
		}

		for _, r := range rows {
			row := RegisterRow{
				FeatureClass:     code,
				FeatureClassName: fc.Name,
				FeatureID:        r.FeatureID,
				Name:             r.Name.String,
				Status:           r.Status.String,
				CircuitID:        r.CircuitID.String,
				Ownership:        r.Ownership.String,
				ExternalRef:      r.ExternalRef.String,
				CreatedAt:        r.CreatedAt.String,
			}
			if r.Subtype.Valid {
				v := int(r.Subtype.Int64)
				row.Subtype = &v
				row.SubtypeName = subtypeName[v]
			}
			if r.MaterialCode.Valid && r.MaterialCode.String != "" {
				v := r.MaterialCode.String
				row.MaterialCode = &v
				if s := specByValueCode[v]; s != nil {
					row.MaterialName = s.Name
				}
			}
			if linear {
				row.Quantity = round3(r.LengthKm)
				row.Unit = "km"
			} else {
				row.Quantity = 1
				row.Unit = "each"
			}
			if r.Lon.Valid {
				v := round6(r.Lon.Float64)
				row.Longitude = &v
			}
			if r.Lat.Valid {
				v := round6(r.Lat.Float64)
				row.Latitude = &v
			}
			if r.ToLon.Valid {
				v := round6(r.ToLon.Float64)
				row.ToLongitude = &v
			}
			if r.ToLat.Valid {
				v := round6(r.ToLat.Float64)
				row.ToLatitude = &v
			}
			if r.TerritoryID.Valid && r.TerritoryID.String != "" {
				row.TerritoryID = r.TerritoryID.String
				row.TerritoryName = territoryName[r.TerritoryID.String]
			}

			if req.IncludeValuation {
				rateBase, src := rowRate(linear, r, code, specByValueCode, ratesByClass, overrideByClass)
				row.PricedSource = src
				if src == "unpriced" {
					ur, val := 0.0, 0.0
					row.UnitRate = &ur
					row.Value = &val
				} else {
					ur := round2(rateBase * perBase)
					val := round2(rateBase * row.Quantity * perBase)
					row.UnitRate = &ur
					row.Value = &val
					if resp.TotalValue != nil {
						*resp.TotalValue += val
					}
				}
			}

			resp.Rows = append(resp.Rows, row)
		}
	}

	if resp.TotalValue != nil {
		v := round2(*resp.TotalValue)
		resp.TotalValue = &v
	}
	resp.Count = len(resp.Rows)
	return resp, nil
}

// rowRate resolves one feature's per-unit rate (base currency) for valuation: a
// per-feature override wins ("override"), else the catalog rate the priced engine
// would apply ("catalog"), else "unpriced" (rate 0). It mirrors RunBoQ's
// precedence so a register and a schedule agree.
func rowRate(linear bool, r crud.AssetRegisterRow, code string, specByValueCode map[string]*metadata.MaterialSpec, ratesByClass map[string][]metadata.CostRate, overrideByClass map[string]map[string]float64) (float64, string) {
	var subtype *int
	if r.Subtype.Valid {
		v := int(r.Subtype.Int64)
		subtype = &v
	}
	var material *string
	if r.MaterialCode.Valid && r.MaterialCode.String != "" {
		v := r.MaterialCode.String
		material = &v
	}
	return rateForFeature(linear, r.FeatureID, subtype, material, code, specByValueCode, ratesByClass, overrideByClass)
}

// rateForFeature is rowRate over primitive identity (used by both the JSON
// register and the XLSX export, which carry a feature's subtype/material
// differently): per-feature override, else catalog rate, else "unpriced".
func rateForFeature(linear bool, featureID string, subtype *int, material *string, code string, specByValueCode map[string]*metadata.MaterialSpec, ratesByClass map[string][]metadata.CostRate, overrideByClass map[string]map[string]float64) (float64, string) {
	if m := overrideByClass[code]; m != nil {
		if v, ok := m[featureID]; ok {
			return v, "override"
		}
	}
	g := crud.BoQGroup{Subtype: subtype, MaterialCode: material}
	if rate, ok := resolveRate(linear, g, specByValueCode, ratesByClass[code]); ok {
		return rate, "catalog"
	}
	return 0, "unpriced"
}

// loadMaterialMaps builds the material-resolution maps RunBoQ builds inline: a
// material value-code -> spec map (for the Material name + cost_per_km) and the
// set of material-backed domain ids (for findMaterialField). Two list queries,
// cheap enough to run on every register.
func loadMaterialMaps(ctx context.Context, db bun.IDB) (map[string]*metadata.MaterialSpec, map[string]bool, error) {
	specs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	specByID := make(map[string]*metadata.MaterialSpec, len(specs))
	for i := range specs {
		specByID[specs[i].ID] = &specs[i]
	}
	values, err := metadata.ListDomainValues(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	specByValueCode := map[string]*metadata.MaterialSpec{}
	materialDomainIDs := map[string]bool{}
	for _, v := range values {
		if v.MaterialSpecID == nil {
			continue
		}
		materialDomainIDs[v.DomainID] = true
		if s, ok := specByID[*v.MaterialSpecID]; ok {
			specByValueCode[v.Code] = s
		}
	}
	return specByValueCode, materialDomainIDs, nil
}

// loadRateMaps builds the valuation-only maps: cost_rates per class and
// per-feature overrides per class (base currency). Shared with the priced engine
// so a register's value can't drift from a schedule's.
func loadRateMaps(ctx context.Context, db bun.IDB) (map[string][]metadata.CostRate, map[string]map[string]float64, error) {
	rates, err := metadata.ListCostRates(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	ratesByClass := map[string][]metadata.CostRate{}
	for _, rt := range rates {
		ratesByClass[rt.FeatureClassCode] = append(ratesByClass[rt.FeatureClassCode], rt)
	}
	overrides, err := metadata.ListFeatureCostOverrides(ctx, db, "")
	if err != nil {
		return nil, nil, err
	}
	overrideByClass := map[string]map[string]float64{}
	for _, o := range overrides {
		m := overrideByClass[o.FeatureClassCode]
		if m == nil {
			m = map[string]float64{}
			overrideByClass[o.FeatureClassCode] = m
		}
		m[o.FeatureID] = o.UnitCost
	}
	return ratesByClass, overrideByClass, nil
}

func round6(v float64) float64 {
	const p = 1e6
	if v < 0 {
		return float64(int64(v*p-0.5)) / p
	}
	return float64(int64(v*p+0.5)) / p
}

// BuildAssetRegisterXLSXForJob is the asset-register worker entry point: it
// unmarshals the queued job's scope (a RegisterRequest) and builds the .xlsx. Its
// signature matches export.AssetRegisterBuilder, so cmd/exportworker injects it
// via export.SetAssetRegisterBuilder -- keeping the export->boq dependency
// one-way (export never imports boq). The ctx already carries the reconstructed
// requesting user, so the build is scoped to what they may read.
func BuildAssetRegisterXLSXForJob(ctx context.Context, db bun.IDB, scope json.RawMessage) ([]byte, string, int, error) {
	var req RegisterRequest
	if err := json.Unmarshal(scope, &req); err != nil {
		return nil, "", 0, fmt.Errorf("corrupt asset-register scope: %w", err)
	}
	return BuildAssetRegisterXLSX(ctx, db, req)
}

// BuildAssetRegisterXLSX renders the asset register as a multi-sheet .xlsx --
// one sheet per feature class, each with that class's full attribute columns
// (the richer, per-class form of the register, where the JSON/CSV path is a flat
// common-column union). It shares the scope + pricing path of RunAssetRegister,
// so an XLSX reconciles with the CSV and the schedule. Returns the file bytes, a
// suggested filename, and the total row count.
func BuildAssetRegisterXLSX(ctx context.Context, db bun.IDB, req RegisterRequest) ([]byte, string, int, error) {
	base := Request{FeatureClasses: req.FeatureClasses, Scope: req.Scope, FeatureRefs: req.FeatureRefs}
	classIDs, classOrder := classSelection(ctx, db, base)

	var scope crud.BoQScope
	if req.Scope != nil {
		scope = crud.BoQScope{Kind: req.Scope.Kind, TerritoryID: req.Scope.TerritoryID, Center: req.Scope.Center, RadiusM: req.Scope.RadiusM, BBox: req.Scope.BBox}
	}

	territoryName := map[string]string{}
	if terrs, err := territory.ListTerritories(ctx, db); err == nil {
		for _, t := range terrs {
			territoryName[t.ID] = t.Name
		}
	}

	specByValueCode, materialDomainIDs, err := loadMaterialMaps(ctx, db)
	if err != nil {
		return nil, "", 0, err
	}

	var (
		ratesByClass    map[string][]metadata.CostRate
		overrideByClass map[string]map[string]float64
		perBase         = 1.0
		currencyCode    string
	)
	if req.IncludeValuation {
		ratesByClass, overrideByClass, err = loadRateMaps(ctx, db)
		if err != nil {
			return nil, "", 0, err
		}
		currencies, err := metadata.ListCurrencies(ctx, db)
		if err != nil {
			return nil, "", 0, err
		}
		_, display, err := resolveCurrencies(currencies, req.DisplayCurrency)
		if err != nil {
			return nil, "", 0, err
		}
		perBase = display.PerBase
		currencyCode = display.Code
	}

	wb := xlsx.New()
	collected := 0

	for _, code := range classOrder {
		if collected >= assetRegisterMaxRows {
			break
		}
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			continue
		}
		fc := loaded.FeatureClass
		if fc.PhysicalTable == nil {
			continue
		}
		linear := fc.GeometryType == "line"
		hasGeom := fc.GeometryType != "none"

		clsScope := scope
		clsScope.FeatureIDs = classIDs[code]
		if clsScope.IsSpatial() && fc.GeometryType == "none" {
			continue
		}

		// The class's own fields, in display order, drive the attribute columns.
		fieldNames := make([]string, len(loaded.Fields))
		fieldNumeric := make([]bool, len(loaded.Fields))
		materialIdx := -1
		materialField := findMaterialField(loaded, materialDomainIDs)
		for i, f := range loaded.Fields {
			fieldNames[i] = f.Name
			fieldNumeric[i] = f.DataType == "numeric" || f.DataType == "integer"
			if f.Name == materialField {
				materialIdx = i
			}
		}

		rows, err := crud.AssetRegisterAttributeRows(ctx, db, loaded, fieldNames, linear, clsScope, assetRegisterMaxRows-collected)
		if err != nil {
			if crud.IsNotFoundError(err) {
				continue
			}
			return nil, "", 0, err
		}
		if len(rows) == 0 {
			continue // don't emit an empty sheet for a class with nothing in scope
		}

		subtypeName := map[int]string{}
		for _, st := range loaded.Subtypes {
			subtypeName[st.Code] = st.Name
		}

		sheet := wb.AddSheet(fc.Name)

		// Header row + a parallel column plan so every value row lines up.
		headers := []string{"Asset ID", "Name", "Status"}
		if len(loaded.Subtypes) > 0 {
			headers = append(headers, "Subtype")
		}
		if linear {
			headers = append(headers, "Length (km)")
		}
		if hasGeom {
			if linear {
				// A line's spatial fact is its two endpoints, not a centroid.
				headers = append(headers, "From longitude", "From latitude", "To longitude", "To latitude", "Territory")
			} else {
				headers = append(headers, "Longitude", "Latitude", "Territory")
			}
		}
		headers = append(headers, fieldNames...)
		if req.IncludeValuation {
			cur := currencyCode
			if cur != "" {
				cur = " (" + cur + ")"
			}
			headers = append(headers, "Unit cost"+cur, "Value"+cur, "Priced")
		}
		headers = append(headers, "External refs", "Created")

		head := make([]xlsx.Cell, len(headers))
		for i, h := range headers {
			head[i] = xlsx.Str(h)
		}
		sheet.AppendRow(head)

		for _, r := range rows {
			cells := []xlsx.Cell{xlsx.Str(r.ID), strCell(r.Name), strCell(r.Status)}
			var subtype *int
			if r.Subtype.Valid {
				v := int(r.Subtype.Int64)
				subtype = &v
			}
			if len(loaded.Subtypes) > 0 {
				name := ""
				if subtype != nil {
					name = subtypeName[*subtype]
				}
				cells = append(cells, xlsx.Str(name))
			}
			if linear {
				cells = append(cells, numCell(r.LengthKm))
			}
			if hasGeom {
				if linear {
					cells = append(cells, numCell(r.Lon), numCell(r.Lat), numCell(r.ToLon), numCell(r.ToLat), xlsx.Str(territoryLabel(r.TerritoryID, territoryName)))
				} else {
					cells = append(cells, numCell(r.Lon), numCell(r.Lat), xlsx.Str(territoryLabel(r.TerritoryID, territoryName)))
				}
			}
			for i := range fieldNames {
				cells = append(cells, fieldCell(r.Fields[i], fieldNumeric[i]))
			}
			if req.IncludeValuation {
				var material *string
				if materialIdx >= 0 && r.Fields[materialIdx].Valid && r.Fields[materialIdx].String != "" {
					v := r.Fields[materialIdx].String
					material = &v
				}
				rateBase, src := rateForFeature(linear, r.ID, subtype, material, code, specByValueCode, ratesByClass, overrideByClass)
				if src == "unpriced" {
					cells = append(cells, xlsx.Str(""), xlsx.Str(""), xlsx.Str("unpriced"))
				} else {
					qty := 1.0
					if linear && r.LengthKm.Valid {
						qty = r.LengthKm.Float64
					}
					cells = append(cells, xlsx.Num(round2(rateBase*perBase)), xlsx.Num(round2(rateBase*qty*perBase)), xlsx.Str(src))
				}
			}
			cells = append(cells, strCell(r.ExternalRef), strCell(r.CreatedAt))
			sheet.AppendRow(cells)
			collected++
		}
	}

	bytesOut, err := wb.Bytes()
	if err != nil {
		return nil, "", 0, err
	}
	filename := "asset-register.xlsx"
	if req.IncludeValuation && currencyCode != "" {
		filename = "asset-register-" + currencyCode + ".xlsx"
	}
	return bytesOut, filename, collected, nil
}

func strCell(s sql.NullString) xlsx.Cell { return xlsx.Str(s.String) }

func numCell(n sql.NullFloat64) xlsx.Cell {
	if n.Valid {
		return xlsx.Num(n.Float64)
	}
	return xlsx.Str("")
}

// fieldCell renders a ::text-scanned field value: a numeric field that parses
// cleanly becomes a real number cell (so Excel can sum/sort it), everything else
// stays text (codes, dates, free text).
func fieldCell(s sql.NullString, numeric bool) xlsx.Cell {
	if !s.Valid || s.String == "" {
		return xlsx.Str("")
	}
	if numeric {
		if f, err := strconv.ParseFloat(s.String, 64); err == nil {
			return xlsx.Num(f)
		}
	}
	return xlsx.Str(s.String)
}

func territoryLabel(id sql.NullString, names map[string]string) string {
	if !id.Valid || id.String == "" {
		return ""
	}
	if n := names[id.String]; n != "" {
		return n
	}
	return id.String
}
