package spatial

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Vegetation clearance / encroachment analysis: the reliability workhorse of land
// management. It finds the vegetation that is TOO CLOSE to a conductor -- within
// the clearance that conductor's voltage requires -- so a trimming crew can be
// sent before a tree causes a fault or a fire. Built on the same geodesic
// ST_DWithin the spatial engine uses, plus a per-voltage clearance rule.

// ConductorClasses are the line classes a vegetation scan treats as conductors. A
// package var (like oms.ProtectiveDeviceClasses) so a deployment can override it.
var ConductorClasses = []string{"MV_CONDUCTOR", "LV_CONDUCTOR"}

// vegetationClasses are the classes a scan treats as vegetation. Overridable per
// request; this is the default.
var vegetationClasses = []string{"VEGETATION_ZONE"}

// defaultClearanceByVoltage is the required clearance (metres) by VOLTAGE_CLASS
// code -- illustrative values; higher voltage needs more clearance. A request can
// override with a single uniform clearance, and an unknown/absent voltage falls
// back to fallbackClearanceM.
var defaultClearanceByVoltage = map[string]float64{
	"230V": 1.0, "400V": 1.0, "415V": 1.0,
	"6.6KV": 2.0, "11KV": 2.5, "22KV": 2.5, "33KV": 3.0,
	"66KV": 3.5, "132KV": 4.5,
}

const fallbackClearanceM = 2.0

// clearanceFor returns the clearance (metres) for a conductor: the request's
// uniform override when set, else the per-voltage default, else the fallback.
func clearanceFor(voltageClass string, override float64) float64 {
	if override > 0 {
		return override
	}
	if c, ok := defaultClearanceByVoltage[strings.ToUpper(strings.TrimSpace(voltageClass))]; ok {
		return c
	}
	return fallbackClearanceM
}

// maxClearance is the widest clearance any conductor could require -- the join
// search radius; each row is then filtered to its conductor's own clearance.
func maxClearance(override float64) float64 {
	if override > 0 {
		return override
	}
	m := fallbackClearanceM
	for _, v := range defaultClearanceByVoltage {
		if v > m {
			m = v
		}
	}
	return m
}

// Encroachment is one conductor-vs-vegetation breach: the vegetation sits
// DistanceM from the conductor, inside the ClearanceM its voltage requires, so it
// breaches by BreachM. Vegetation detail (species/height/type) rides along for the
// trim decision.
type Encroachment struct {
	ConductorClass  string   `json:"conductor_class"`
	ConductorID     string   `json:"conductor_id"`
	VoltageClass    *string  `json:"voltage_class,omitempty"`
	VegetationClass string   `json:"vegetation_class"`
	VegetationID    string   `json:"vegetation_id"`
	DistanceM       float64  `json:"distance_m"`
	ClearanceM      float64  `json:"clearance_m"`
	BreachM         float64  `json:"breach_m"`
	Species         *string  `json:"species,omitempty"`
	HeightM         *float64 `json:"height_m,omitempty"`
	VegType         *string  `json:"veg_type,omitempty"`
}

// EncroachmentInput scopes a scan: a SINGLE conductor (ConductorClass+ConductorID)
// or a whole CircuitID (every conductor on the feeder). ClearanceM, when > 0, is a
// uniform override; otherwise per-voltage defaults apply. A whole-network scan is
// deliberately not offered -- name a conductor or a circuit.
type EncroachmentInput struct {
	ConductorClass    string
	ConductorID       string
	CircuitID         string
	VegetationClasses []string
	ClearanceM        float64
	Limit             int
}

// VegetationEncroachments returns the vegetation breaching clearance for the
// scoped conductors, worst breach first.
func VegetationEncroachments(ctx context.Context, db bun.IDB, in EncroachmentInput) ([]Encroachment, error) {
	single := in.ConductorClass != "" && in.ConductorID != ""
	byCircuit := in.CircuitID != ""
	if !single && !byCircuit {
		return nil, invalidf("scope the scan: give a conductor (feature_class + feature_id) or a circuit_id")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}

	condClasses := ConductorClasses
	if single {
		condClasses = []string{in.ConductorClass}
	}
	vegClasses := in.VegetationClasses
	if len(vegClasses) == 0 {
		vegClasses = vegetationClasses
	}
	radius := maxClearance(in.ClearanceM)

	// Resolve each vegetation class once (table + which detail columns it carries).
	type vegResolved struct {
		code, table                      string
		speciesCol, heightCol, vegTypeCol string
	}
	vegs := make([]vegResolved, 0, len(vegClasses))
	for _, vc := range vegClasses {
		loaded, err := metadata.Load(ctx, db, vc)
		if err != nil {
			continue // unknown veg class -> skip
		}
		if loaded.FeatureClass.PhysicalTable == nil {
			continue
		}
		vegs = append(vegs, vegResolved{
			code: vc, table: *loaded.FeatureClass.PhysicalTable,
			speciesCol: colOrNull(loaded, "species", "text"),
			heightCol:  colOrNull(loaded, "height_m", "numeric"),
			vegTypeCol: colOrNull(loaded, "veg_type", "text"),
		})
	}
	if len(vegs) == 0 {
		return nil, invalidf("no compiled vegetation class to scan (seed + compile VEGETATION_ZONE first)")
	}

	out := make([]Encroachment, 0, 32)
	for _, cc := range condClasses {
		loaded, err := metadata.Load(ctx, db, cc)
		if err != nil || loaded.FeatureClass.PhysicalTable == nil {
			continue
		}
		condTable := *loaded.FeatureClass.PhysicalTable
		voltageCol := colOrNull(loaded, "voltage_class", "text")
		hasCircuit := loaded.FieldByName("circuit_id") != nil
		if byCircuit && !hasCircuit {
			continue // this conductor class can't be circuit-scoped
		}

		for _, v := range vegs {
			rows, err := scanEncroach(ctx, db, condTable, v.table, voltageCol, v.speciesCol, v.heightCol, v.vegTypeCol, single, in.ConductorID, in.CircuitID, radius)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				vc := ""
				if r.VoltageClass != nil {
					vc = *r.VoltageClass
				}
				clear := clearanceFor(vc, in.ClearanceM)
				if r.DistanceM > clear {
					continue // within the search radius but outside this conductor's clearance
				}
				out = append(out, Encroachment{
					ConductorClass:  cc,
					ConductorID:     r.ConductorID,
					VoltageClass:    r.VoltageClass,
					VegetationClass: v.code,
					VegetationID:    r.VegID,
					DistanceM:       r.DistanceM,
					ClearanceM:      clear,
					BreachM:         clear - r.DistanceM,
					Species:         r.Species,
					HeightM:         r.HeightM,
					VegType:         r.VegType,
				})
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].BreachM > out[j].BreachM })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type encroachRow struct {
	ConductorID  string   `bun:"conductor_id"`
	VoltageClass *string  `bun:"voltage_class"`
	VegID        string   `bun:"veg_id"`
	Species      *string  `bun:"species"`
	HeightM      *float64 `bun:"height_m"`
	VegType      *string  `bun:"veg_type"`
	DistanceM    float64  `bun:"distance_m"`
}

// scanEncroach joins one conductor table to one vegetation table within the search
// radius (geodesic). Column names come from metadata (trusted); scope ids bind.
func scanEncroach(ctx context.Context, db bun.IDB, condTable, vegTable, voltageCol, speciesCol, heightCol, vegTypeCol string, single bool, conductorID, circuitID string, radius float64) ([]encroachRow, error) {
	scope := "c.circuit_id = ?"
	arg := circuitID
	if single {
		scope = "c.id = ?"
		arg = conductorID
	}
	query := fmt.Sprintf(`
SELECT c.id::text AS conductor_id, %s AS voltage_class,
       v.id::text AS veg_id, %s AS species, %s AS height_m, %s AS veg_type,
       ST_Distance(c.geom::geography, v.geom::geography) AS distance_m
FROM %s AS c
JOIN %s AS v ON ST_DWithin(c.geom::geography, v.geom::geography, ?)
WHERE c.deleted_at IS NULL AND v.deleted_at IS NULL
  AND c.geom IS NOT NULL AND v.geom IS NOT NULL
  AND %s`,
		voltageCol, speciesCol, heightCol, vegTypeCol, condTable, vegTable, scope)

	var rows []encroachRow
	if err := db.NewRaw(query, radius, arg).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("spatial: vegetation scan %s x %s: %w", condTable, vegTable, err)
	}
	return rows, nil
}

// colOrNull returns `alias` qualified column reference when the class carries that
// field, else a typed NULL -- so a scan works whether or not the class has the
// optional detail column. The alias is c for a conductor (voltage) and v for
// vegetation (species/height/veg_type); callers pass the already-known columns.
func colOrNull(loaded *metadata.Loaded, field, pgType string) string {
	alias := "v"
	if field == "voltage_class" {
		alias = "c"
	}
	if loaded.FieldByName(field) != nil {
		return alias + "." + field
	}
	return "NULL::" + pgType
}
