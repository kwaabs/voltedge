package agenttools

// The spatial engine's tool: geometry-derived overlap/proximity across the gis.*
// tables, the same read the REST GET /spatial/related route returns. A thin
// adapter over api/internal/spatial.Query, gated features:read (it reads the
// features themselves).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/spatial"
	"github.com/uptrace/bun"
)

func spatialTools() []Tool {
	return []Tool{
		{
			Name: "spatial_related",
			Description: "Find the features spatially related to one reference feature, derived straight " +
				"from the PostGIS geometry (no authored link needed) -- the same read the REST GET " +
				"/spatial/related route returns. Use it for land/spatial questions: assets INSIDE an " +
				"easement (ref = the easement, mode=contains); the land COVERING an asset (ref = the asset, " +
				"mode=contained_by, classes=EASEMENT,LAND_PARCEL,UTILITY_CORRIDOR); vegetation WITHIN a " +
				"distance of a line (ref = the line, mode=within, distance=5, classes=VEGETATION_ZONE). " +
				"classes limits which classes to search (default: every geometric class). A within result " +
				"carries each match's distance in metres, nearest first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The reference feature's class code."},
					"feature_id":{"type":"string","description":"The reference feature's id (uuid)."},
					"mode":{"type":"string","enum":["intersects","contains","contained_by","within"],"description":"intersects (overlap, default) · contains (ref contains the match) · contained_by (match contains ref) · within (match is within distance metres of ref)."},
					"distance":{"type":"number","description":"Metres; required for mode=within."},
					"classes":{"type":"array","items":{"type":"string"},"description":"Limit targets to these class codes; omit for every geometric class."},
					"limit":{"type":"integer","description":"Max matches (default 500, capped 5000)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolSpatialRelated,
		},
		{
			Name: "vegetation_encroachments",
			Description: "Find the vegetation TOO CLOSE to a conductor -- within the clearance its voltage " +
				"requires -- the vegetation-management trim list (REST GET /spatial/vegetation-encroachments). " +
				"Scope it to a SINGLE conductor (feature_class + feature_id) OR a whole feeder (circuit_id). " +
				"clearance sets a uniform metres override; omit it for the per-voltage defaults. Each result " +
				"is one breach: the conductor, the vegetation (species/height/type), the actual distance, the " +
				"required clearance, and how much it breaches by -- worst breach first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"A single conductor's class code (pair with feature_id)."},
					"feature_id":{"type":"string","description":"A single conductor's id (uuid)."},
					"circuit_id":{"type":"string","description":"Scan every conductor on this circuit (uuid) instead of one."},
					"clearance":{"type":"number","description":"Uniform required clearance in metres; omit for per-voltage defaults."},
					"classes":{"type":"array","items":{"type":"string"},"description":"Vegetation class codes to scan (default VEGETATION_ZONE)."},
					"limit":{"type":"integer","description":"Max breaches (default 1000, capped 10000)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolVegetationEncroachments,
		},
		{
			Name: "easements_expiring",
			Description: "List easements expired or expiring within a window (REST GET /spatial/easements/" +
				"expiring) -- the land-rights renewal report, soonest first. within_days defaults to 90 " +
				"(already-expired easements are included, with a negative days_until_expiry). Empty when the " +
				"EASEMENT class isn't seeded.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"within_days":{"type":"integer","description":"Window in days (default 90, max 3650)."}},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolEasementsExpiring,
		},
		{
			Name: "vegetation_trim_due",
			Description: "List vegetation zones due (or overdue) for their trim cycle within a window (REST " +
				"GET /spatial/vegetation/trim-due) -- the vegetation-management schedule, most overdue first " +
				"(positive days_overdue). Computed from last_trimmed + trim_cycle_months; within_days defaults " +
				"to 30. Empty when the VEGETATION_ZONE class isn't seeded.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"within_days":{"type":"integer","description":"Window in days (default 30, max 3650)."}},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolVegetationTrimDue,
		},
		{
			Name: "raise_vegetation_trim_order",
			Description: "Raise a WAM trim work order for one vegetation-clearance breach (REST POST " +
				"/spatial/vegetation-encroachments/work-order) -- closing the detect-to-dispatch loop. The " +
				"order's asset is the vegetation zone (what gets cut); the conductor and the measured breach " +
				"go in the title/description, and priority is derived from how badly clearance is breached " +
				"(near-contact = urgent) unless overridden. Pass the fields from a vegetation_encroachments " +
				"result. Returns the created work order. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"vegetation_class":{"type":"string","description":"The vegetation zone's class code."},
					"vegetation_id":{"type":"string","description":"The vegetation zone's id (uuid)."},
					"conductor_class":{"type":"string","description":"The conductor's class code (for the title/description)."},
					"conductor_id":{"type":"string","description":"The conductor's id (uuid)."},
					"distance_m":{"type":"number","description":"Measured distance (metres)."},
					"clearance_m":{"type":"number","description":"Required clearance (metres)."},
					"species":{"type":"string","description":"Vegetation species, if known."},
					"priority":{"type":"string","enum":["low","medium","high","urgent"],"description":"Override the derived priority."}
				},
				"required":["vegetation_class","vegetation_id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Raise vegetation trim order", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolRaiseTrimOrder,
		},
	}
}

func toolSpatialRelated(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string   `json:"feature_class"`
		FeatureID    string   `json:"feature_id"`
		Mode         string   `json:"mode"`
		Distance     float64  `json:"distance"`
		Classes      []string `json:"classes"`
		Limit        int      `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	matches, err := spatial.Query(ctx, db, spatial.QueryInput{
		RefClass:      in.FeatureClass,
		RefID:         in.FeatureID,
		TargetClasses: in.Classes,
		Mode:          in.Mode,
		DistanceM:     in.Distance,
		Limit:         in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"matches": matches, "count": len(matches)}, nil
}

func toolVegetationEncroachments(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string   `json:"feature_class"`
		FeatureID    string   `json:"feature_id"`
		CircuitID    string   `json:"circuit_id"`
		Clearance    float64  `json:"clearance"`
		Classes      []string `json:"classes"`
		Limit        int      `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	rows, err := spatial.VegetationEncroachments(ctx, db, spatial.EncroachmentInput{
		ConductorClass:    in.FeatureClass,
		ConductorID:       in.FeatureID,
		CircuitID:         in.CircuitID,
		VegetationClasses: in.Classes,
		ClearanceM:        in.Clearance,
		Limit:             in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"encroachments": rows, "count": len(rows)}, nil
}

func toolEasementsExpiring(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		WithinDays int `json:"within_days"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	rows, err := spatial.ExpiringEasements(ctx, db, in.WithinDays)
	if err != nil {
		return nil, err
	}
	return map[string]any{"easements": rows, "count": len(rows)}, nil
}

func toolVegetationTrimDue(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		WithinDays int `json:"within_days"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	rows, err := spatial.VegetationTrimDue(ctx, db, in.WithinDays)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vegetation": rows, "count": len(rows)}, nil
}

func toolRaiseTrimOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		VegetationClass string  `json:"vegetation_class"`
		VegetationID    string  `json:"vegetation_id"`
		ConductorClass  string  `json:"conductor_class"`
		ConductorID     string  `json:"conductor_id"`
		DistanceM       float64 `json:"distance_m"`
		ClearanceM      float64 `json:"clearance_m"`
		Species         string  `json:"species"`
		Priority        string  `json:"priority"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return spatial.RaiseTrimOrder(ctx, db, spatial.TrimOrderInput{
		ConductorClass:  in.ConductorClass,
		ConductorID:     in.ConductorID,
		VegetationClass: in.VegetationClass,
		VegetationID:    in.VegetationID,
		DistanceM:       in.DistanceM,
		ClearanceM:      in.ClearanceM,
		Species:         in.Species,
		Priority:        in.Priority,
	})
}
