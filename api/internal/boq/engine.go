// Package boq is the Bill of Quantities engine (Slice 1): it prices a scope of
// the network by multiplying scoped quantities (discrete feature counts and
// linear geometry lengths) by catalog rates, and rolls the result up into a
// grouped, totalled schedule.
//
// Two rate sources feed one schedule. A LINEAR class (line geometry) is priced
// per km, preferring the cost_per_km of the material it selects
// (material_specs, via the domain value the feature's material field holds),
// falling back to a class-level per_km cost_rate. A DISCRETE class is priced per
// item from a cost_rate keyed by (class, subtype). Every stored rate is in one
// base currency; the response converts to a chosen display currency. Quantities
// that resolve no rate are surfaced as an explicit "unpriced" bucket, never
// folded into the total as zero.
package boq

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// RunBoQ prices the requested scope and returns the grouped schedule. db is a
// bun.IDB so both the HTTP route (*bun.DB) and the MCP tool front door share it.
func RunBoQ(ctx context.Context, db bun.IDB, req Request) (*Response, error) {
	// Material resolution maps (mirrors export.loadMaterialSpecs): a coded field
	// value -> its material spec, and the set of domains whose values are
	// material-backed (used to find each class's material field).
	specs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return nil, err
	}
	specByID := make(map[string]*metadata.MaterialSpec, len(specs))
	for i := range specs {
		specByID[specs[i].ID] = &specs[i]
	}
	values, err := metadata.ListDomainValues(ctx, db)
	if err != nil {
		return nil, err
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

	rates, err := metadata.ListCostRates(ctx, db)
	if err != nil {
		return nil, err
	}
	ratesByClass := map[string][]metadata.CostRate{}
	for _, rt := range rates {
		ratesByClass[rt.FeatureClassCode] = append(ratesByClass[rt.FeatureClassCode], rt)
	}

	// Per-feature overrides (the "+ overrides" half of the cost model): a feature
	// with one is priced off it, pulled out of its catalog-rated bucket. Indexed
	// class -> feature id -> override unit cost (base currency).
	overrides, err := metadata.ListFeatureCostOverrides(ctx, db, "")
	if err != nil {
		return nil, err
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

	currencies, err := metadata.ListCurrencies(ctx, db)
	if err != nil {
		return nil, err
	}
	base, display, err := resolveCurrencies(currencies, req.DisplayCurrency)
	if err != nil {
		return nil, err
	}

	classIDs, classOrder := classSelection(ctx, db, req)

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

	resp := &Response{
		Currency:     display.Code,
		CurrencyName: display.Name,
		BaseCurrency: base.Code,
		PerBase:      display.PerBase,
		Lines:        []Line{},
		Unpriced:     []UnpricedLine{},
		Currencies:   currencyOut(currencies),
	}

	for _, code := range classOrder {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			continue // unknown / unresolvable class -- skip rather than fail the run
		}
		fc := loaded.FeatureClass
		if fc.PhysicalTable == nil {
			continue // not compiled yet -- nothing to count
		}
		linear := fc.GeometryType == "line"

		clsScope := scope
		clsScope.FeatureIDs = classIDs[code]
		// A spatial scope can neither locate a geometry-less class nor read a
		// territory_id off it -- skip it rather than error the whole run.
		if clsScope.IsSpatial() && fc.GeometryType == "none" {
			continue
		}

		materialField := findMaterialField(loaded, materialDomainIDs)

		overrideRates := overrideByClass[code]
		var overrideIDs []string
		for id := range overrideRates {
			overrideIDs = append(overrideIDs, id)
		}

		// Grouped catalog-rated buckets, with overridden features excluded (they
		// price individually below).
		groups, err := crud.ClassQuantities(ctx, db, loaded, materialField, linear, clsScope, overrideIDs)
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

		for _, g := range groups {
			basis, unit := "each", "each"
			qty := float64(g.Count)
			if linear {
				basis, unit = "per_km", "km"
				qty = g.LengthKm
			}
			if qty <= 0 {
				continue // e.g. only unplaced conductors -- zero length, nothing to price
			}

			stName := ""
			if g.Subtype != nil {
				stName = subtypeName[*g.Subtype]
			}
			matName := ""
			if g.MaterialCode != nil {
				if s := specByValueCode[*g.MaterialCode]; s != nil {
					matName = s.Name
				}
			}

			rate, ok := resolveRate(linear, g, specByValueCode, ratesByClass[code])
			if !ok {
				resp.Unpriced = append(resp.Unpriced, UnpricedLine{
					FeatureClass:     code,
					FeatureClassName: fc.Name,
					Subtype:          g.Subtype,
					SubtypeName:      stName,
					MaterialCode:     g.MaterialCode,
					UnitBasis:        basis,
					Quantity:         round3(qty),
					Unit:             unit,
					Reason:           unpricedReason(linear, g),
				})
				continue
			}

			baseTotal := rate * qty
			resp.TotalBase += baseTotal
			resp.Lines = append(resp.Lines, Line{
				FeatureClass:     code,
				FeatureClassName: fc.Name,
				Subtype:          g.Subtype,
				SubtypeName:      stName,
				MaterialCode:     g.MaterialCode,
				MaterialName:     matName,
				UnitBasis:        basis,
				Quantity:         round3(qty),
				Unit:             unit,
				UnitRate:         round2(rate * display.PerBase),
				LineTotal:        round2(baseTotal * display.PerBase),
			})
		}

		// Per-feature overrides: each overridden, in-scope feature priced off its
		// own rate, then collapsed by (subtype, material, rate) so identical
		// hand-set prices share one flagged line.
		if len(overrideIDs) > 0 {
			orows, err := crud.ClassOverrideRows(ctx, db, loaded, materialField, linear, clsScope, overrideIDs)
			if err != nil {
				if crud.IsNotFoundError(err) {
					continue
				}
				return nil, err
			}
			type oAgg struct {
				subtype  *int
				material *string
				rate     float64
				qty      float64
			}
			aggs := map[string]*oAgg{}
			var order []string
			for _, r := range orows {
				qty := 1.0
				if linear {
					qty = r.LengthKm
				}
				if qty <= 0 {
					continue
				}
				rate := overrideRates[r.FeatureID]
				key := fmt.Sprintf("%d|%s|%v", subtypeKey(r.Subtype), matKey(r.MaterialCode), rate)
				a := aggs[key]
				if a == nil {
					a = &oAgg{subtype: r.Subtype, material: r.MaterialCode, rate: rate}
					aggs[key] = a
					order = append(order, key)
				}
				a.qty += qty
			}
			for _, key := range order {
				a := aggs[key]
				basis, unit := "each", "each"
				if linear {
					basis, unit = "per_km", "km"
				}
				stName := ""
				if a.subtype != nil {
					stName = subtypeName[*a.subtype]
				}
				matName := ""
				if a.material != nil {
					if s := specByValueCode[*a.material]; s != nil {
						matName = s.Name
					}
				}
				baseTotal := a.rate * a.qty
				resp.TotalBase += baseTotal
				resp.Lines = append(resp.Lines, Line{
					FeatureClass:     code,
					FeatureClassName: fc.Name,
					Subtype:          a.subtype,
					SubtypeName:      stName,
					MaterialCode:     a.material,
					MaterialName:     matName,
					UnitBasis:        basis,
					Quantity:         round3(a.qty),
					Unit:             unit,
					UnitRate:         round2(a.rate * display.PerBase),
					LineTotal:        round2(baseTotal * display.PerBase),
					Overridden:       true,
				})
			}
		}
	}

	sortLines(resp.Lines)
	sortUnpriced(resp.Unpriced)
	resp.PricedCount = len(resp.Lines)
	resp.Total = round2(resp.TotalBase * display.PerBase)
	resp.TotalBase = round2(resp.TotalBase)
	return resp, nil
}

// resolveCurrencies picks the base currency (the denomination of every stored
// rate) and the requested display currency. An empty request currency displays
// in the base; an unknown one is a 400. A missing base row (should not happen --
// migration 0127 seeds one) falls back to a synthetic unit base so the engine
// still runs.
func resolveCurrencies(currencies []metadata.Currency, want string) (base, display metadata.Currency, err error) {
	for _, c := range currencies {
		if c.IsBase {
			base = c
		}
	}
	if base.Code == "" {
		if len(currencies) > 0 {
			base = currencies[0]
		} else {
			base = metadata.Currency{Code: "BASE", Name: "Base currency", PerBase: 1, IsBase: true}
		}
	}

	want = strings.TrimSpace(want)
	if want == "" {
		return base, base, nil
	}
	for _, c := range currencies {
		if strings.EqualFold(c.Code, want) {
			return base, c, nil
		}
	}
	return base, display, invalid("unknown display currency %q", want)
}

// classSelection resolves which classes to price and, for the explicit
// feature-refs mode, the ids per class. FeatureRefs takes precedence and defines
// the classes; otherwise the request's FeatureClasses, or every active class when
// none is given. classOrder preserves first-seen order for a stable schedule.
func classSelection(ctx context.Context, db bun.IDB, req Request) (map[string][]string, []string) {
	classIDs := map[string][]string{}
	var classOrder []string

	if len(req.FeatureRefs) > 0 {
		for _, ref := range req.FeatureRefs {
			c, id := strings.TrimSpace(ref.Class), strings.TrimSpace(ref.ID)
			if c == "" || id == "" {
				continue
			}
			if _, seen := classIDs[c]; !seen {
				classOrder = append(classOrder, c)
			}
			classIDs[c] = append(classIDs[c], id)
		}
		return classIDs, classOrder
	}

	classes := req.FeatureClasses
	if len(classes) == 0 {
		if all, err := metadata.ListFeatureClasses(ctx, db); err == nil {
			for _, c := range all {
				classes = append(classes, c.Code)
			}
		}
	}
	for _, c := range classes {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, seen := classIDs[c]; !seen {
			classOrder = append(classOrder, c)
			classIDs[c] = nil
		}
	}
	return classIDs, classOrder
}

// findMaterialField returns the name of the class's material-backed coded field
// (the value that resolves to a material_specs cost) -- the first active field
// whose domain carries at least one material-backed value -- or "" when none.
func findMaterialField(loaded *metadata.Loaded, materialDomainIDs map[string]bool) string {
	for _, f := range loaded.Fields {
		if f.DomainID != nil && materialDomainIDs[*f.DomainID] {
			return f.Name
		}
	}
	return ""
}

// resolveRate returns the per-unit rate (in the base currency) for a bucket. A
// linear bucket prefers the selected material's cost_per_km, then a class/subtype
// per_km cost_rate; a discrete bucket takes an 'each' cost_rate. ok is false when
// nothing prices it.
func resolveRate(linear bool, g crud.BoQGroup, specByValueCode map[string]*metadata.MaterialSpec, rates []metadata.CostRate) (float64, bool) {
	if linear {
		if g.MaterialCode != nil {
			if s := specByValueCode[*g.MaterialCode]; s != nil && s.CostPerKm != nil {
				return *s.CostPerKm, true
			}
		}
		return lookupRate(rates, g.Subtype, "per_km")
	}
	return lookupRate(rates, g.Subtype, "each")
}

// lookupRate finds the cost_rate for (subtype, basis): a subtype-specific row
// wins, else the class-wide default (nil subtype_code). Rows of the wrong basis
// are ignored.
func lookupRate(rates []metadata.CostRate, subtype *int, basis string) (float64, bool) {
	var classDefault *metadata.CostRate
	for i := range rates {
		rt := &rates[i]
		if rt.UnitBasis != basis {
			continue
		}
		if rt.SubtypeCode == nil {
			if classDefault == nil {
				classDefault = rt
			}
			continue
		}
		if subtype != nil && *rt.SubtypeCode == *subtype {
			return rt.UnitRate, true
		}
	}
	if classDefault != nil {
		return classDefault.UnitRate, true
	}
	return 0, false
}

func unpricedReason(linear bool, g crud.BoQGroup) string {
	if linear {
		if g.MaterialCode != nil {
			return "the selected material has no cost_per_km, and no per_km rate is set for this class"
		}
		return "no material is selected and no per_km rate is set for this class"
	}
	return "no 'each' rate is set for this class/subtype"
}

func currencyOut(currencies []metadata.Currency) []CurrencyOut {
	out := make([]CurrencyOut, 0, len(currencies))
	for _, c := range currencies {
		out = append(out, CurrencyOut{Code: c.Code, Name: c.Name, PerBase: c.PerBase, IsBase: c.IsBase})
	}
	return out
}

func sortLines(lines []Line) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].FeatureClass != lines[j].FeatureClass {
			return lines[i].FeatureClass < lines[j].FeatureClass
		}
		si, sj := subtypeKey(lines[i].Subtype), subtypeKey(lines[j].Subtype)
		if si != sj {
			return si < sj
		}
		return matKey(lines[i].MaterialCode) < matKey(lines[j].MaterialCode)
	})
}

func sortUnpriced(lines []UnpricedLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].FeatureClass != lines[j].FeatureClass {
			return lines[i].FeatureClass < lines[j].FeatureClass
		}
		return subtypeKey(lines[i].Subtype) < subtypeKey(lines[j].Subtype)
	})
}

func subtypeKey(s *int) int {
	if s == nil {
		return -1
	}
	return *s
}

func matKey(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
