package importer

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/crud"
)

// maxDryRunSample bounds how many rows a single synchronous dry-run validates.
// The dry-run is a pre-flight over a SAMPLE the browser slices off the file
// (web/src/lib/etl-import.ts sampleFeatures), not the whole upload -- that is the
// worker's job (S4). The cap keeps one request's work bounded regardless of what
// a caller sends.
const maxDryRunSample = 500

// DryRunRow is one sample row's verdict: whether it would pass create validation,
// the first blocking problem if not, the mapped attributes (so the wizard can
// show what each source row becomes), and whether geometry was assembled.
type DryRunRow struct {
	RowNumber   int            `json:"row_number"`
	Valid       bool           `json:"valid"`
	Error       string         `json:"error,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
	HasGeometry bool           `json:"has_geometry"`
}

// DryRunReport is the whole sample's verdict: the resolved target (echoed back so
// the UI can confirm the profile mapped onto the class it expected), the tallies,
// and the per-row detail.
type DryRunReport struct {
	FeatureClassCode string      `json:"feature_class_code"`
	SubtypeCode      *int        `json:"subtype_code,omitempty"`
	WriteMode        string      `json:"write_mode"`
	GeometryMode     string      `json:"geometry_mode"`
	MatchFields      []string    `json:"match_fields"`
	Total            int         `json:"total"`
	ValidCount       int         `json:"valid"`
	InvalidCount     int         `json:"invalid"`
	Rows             []DryRunRow `json:"rows"`
}

// DryRun validates a sample of source rows against a profile WITHOUT writing
// anything: for each row it applies the profile's field mappings + type coercion,
// assembles geometry, and runs crud.ValidateFeature (the same in-memory checks
// the real create runs -- required fields, coded-domain membership, ranges,
// geometry type). For an upsert profile it additionally requires every match
// field to carry a value, since a null natural key can't match or key an insert.
//
// It is deliberately create-shaped: it answers "would each of these rows insert
// cleanly?", not "does it match an existing feature" -- matching is the load's
// job (S4). Nothing here opens a transaction, so a row whose WKT fails to parse
// is just that row's error, not a poisoned batch.
func DryRun(ctx context.Context, db bun.IDB, profileCode string, sample []*Feature) (*DryRunReport, error) {
	if len(sample) > maxDryRunSample {
		return nil, invalidf("sample too large: %d rows (max %d)", len(sample), maxDryRunSample)
	}

	rp, err := ResolveProfile(ctx, db, profileCode)
	if err != nil {
		return nil, err
	}

	report := &DryRunReport{
		FeatureClassCode: rp.Loaded.FeatureClass.Code,
		SubtypeCode:      rp.SubtypeCode,
		WriteMode:        rp.Profile.WriteMode,
		GeometryMode:     rp.Profile.GeometryMode,
		MatchFields:      rp.Profile.MatchFields,
		Total:            len(sample),
		Rows:             make([]DryRunRow, 0, len(sample)),
	}

	upsert := rp.Profile.WriteMode == "upsert"

	for i, f := range sample {
		row := DryRunRow{RowNumber: i + 1}

		attrs, err := rp.BuildAttributes(f)
		if err != nil {
			row.Error = err.Error()
			report.append(row)
			continue
		}
		row.Attributes = attrs

		geom, err := rp.AssembleGeometry(ctx, db, f)
		if err != nil {
			row.Error = err.Error()
			report.append(row)
			continue
		}
		row.HasGeometry = len(geom) > 0

		// An upsert keys on the match fields, so a row missing one can neither
		// match an existing feature nor be a well-formed insert on that key.
		if missing := missingMatchField(upsert, rp.Profile.MatchFields, attrs); missing != "" {
			row.Error = "match field " + missing + " has no value"
			report.append(row)
			continue
		}

		rep := crud.ValidateFeature(rp.Loaded, crud.ValidateFeatureInput{
			Subtype:    rp.SubtypeCode,
			Geometry:   geom,
			Attributes: attrs,
		})
		row.Valid = rep.Valid
		row.Error = rep.Error
		report.append(row)
	}

	return report, nil
}

// missingMatchField returns the first match field with no assembled value (only
// for an upsert profile), or "" when all are present.
func missingMatchField(upsert bool, matchFields []string, attrs map[string]any) string {
	if !upsert {
		return ""
	}
	for _, name := range matchFields {
		if v, ok := attrs[name]; !ok || v == nil {
			return name
		}
	}
	return ""
}

// append records a row and keeps the valid/invalid tallies in step.
func (r *DryRunReport) append(row DryRunRow) {
	if row.Valid {
		r.ValidCount++
	} else {
		r.InvalidCount++
	}
	r.Rows = append(r.Rows, row)
}
