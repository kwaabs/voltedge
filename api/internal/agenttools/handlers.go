package agenttools

// The tool handlers -- each a thin adapter over an already-exported engine
// function, moved intact from internal/mcpserver in Phase 19 Slice 2 so both
// front doors run the same code. A handler unmarshals its arguments, validates
// only the presence of the required refs (the engines re-validate everything
// else), and returns the engine's result or error verbatim for Execute's caller
// to report in its own protocol.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/assetlife"
	"github.com/kwaabs/voltedge/api/internal/boq"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/ownership"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// searchDefaultLimit/searchMaxLimit bound a search page so a tool call never
// accidentally pulls an entire feature class; an omitted or out-of-range
// limit is corrected rather than rejected.
const (
	searchDefaultLimit = 50
	searchMaxLimit     = 500
)

// --- output shapes (their own wire types, JSON-tagged, like the REST
// handlers keep their own response copies) ---

type featureClassOut struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	GeometryType string `json:"geometry_type"`
	NetworkRole  string `json:"network_role"`
	// TraceTier is the class's rank in the source-to-load hierarchy (migration
	// 0126): hundreds 0 (grid infeed) .. 600 (service/meter), gaps of 100 so a
	// layer can slot in later. Omitted (null) for an untiered class -- one that
	// doesn't constrain trace/analysis direction. Pointer so tier 0 (a real,
	// most-upstream tier) is distinct from "untiered".
	TraceTier *int `json:"trace_tier,omitempty"`
}

type fieldOut struct {
	Name         string   `json:"name"`
	DataType     string   `json:"data_type"`
	Nullable     bool     `json:"nullable"`
	DomainCode   string   `json:"domain_code,omitempty"`
	DomainValues []string `json:"domain_values,omitempty"`
}

type subtypeOut struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}

type terminalOut struct {
	Code         string `json:"code"`
	Role         string `json:"role"`
	PhaseCapable bool   `json:"phase_capable"`
	SubtypeCode  *int   `json:"subtype_code,omitempty"`
}

type describeOut struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	GeometryType string `json:"geometry_type"`
	// TraceTier ranks this class in the source-to-load hierarchy (migration
	// 0126): hundreds 0 (grid infeed) .. 600 (service/meter). Omitted (null)
	// for an untiered class. See featureClassOut.TraceTier.
	TraceTier *int          `json:"trace_tier,omitempty"`
	Subtypes  []subtypeOut  `json:"subtypes"`
	Fields    []fieldOut    `json:"fields"`
	Terminals []terminalOut `json:"terminals,omitempty"`
}

// --- handlers ---

func toolListFeatureClasses(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]featureClassOut, len(classes))
	for i, fc := range classes {
		out[i] = featureClassOut{
			Code: fc.Code, Name: fc.Name,
			GeometryType: fc.GeometryType, NetworkRole: fc.NetworkRole,
			TraceTier: fc.TraceTier,
		}
	}
	return map[string]any{"feature_classes": out, "count": len(out)}, nil
}

func toolDescribeFeatureClass(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}

	loaded, err := metadata.Load(ctx, db, in.Code)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.Code, err)
	}

	subtypes := make([]subtypeOut, len(loaded.Subtypes))
	subtypeCodeByID := make(map[string]int, len(loaded.Subtypes))
	for i, st := range loaded.Subtypes {
		subtypes[i] = subtypeOut{Code: st.Code, Name: st.Name}
		subtypeCodeByID[st.ID] = st.Code
	}

	fields := make([]fieldOut, len(loaded.Fields))
	for i, f := range loaded.Fields {
		fo := fieldOut{Name: f.Name, DataType: f.DataType, Nullable: f.Nullable}
		if f.DomainID != nil {
			if d, ok := loaded.Domains[*f.DomainID]; ok {
				fo.DomainCode = d.Code
				if d.DomainType == "coded" {
					fo.DomainValues = loaded.DomainValues[d.ID]
				}
			}
		}
		fields[i] = fo
	}

	terminals := make([]terminalOut, len(loaded.Terminals))
	for i, t := range loaded.Terminals {
		to := terminalOut{Code: t.Code, Role: t.Role, PhaseCapable: t.PhaseCapable}
		if t.SubtypeID != nil {
			if code, ok := subtypeCodeByID[*t.SubtypeID]; ok {
				to.SubtypeCode = &code
			}
		}
		terminals[i] = to
	}

	return describeOut{
		Code:         loaded.FeatureClass.Code,
		Name:         loaded.FeatureClass.Name,
		GeometryType: loaded.FeatureClass.GeometryType,
		TraceTier:    loaded.FeatureClass.TraceTier,
		Subtypes:     subtypes,
		Fields:       fields,
		Terminals:    terminals,
	}, nil
}

func toolSearchFeatures(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		NameQuery    string `json:"name_query"`
		Status       string `json:"status"`
		Sort         string `json:"sort"`
		Desc         bool   `json:"desc"`
		Limit        int    `json:"limit"`
		Offset       int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}

	features, err := crud.SearchFeatures(ctx, db, loaded, crud.SearchOptions{
		Limit:     limit,
		Offset:    offset,
		NameQuery: in.NameQuery,
		Status:    in.Status,
		Sort:      in.Sort,
		Desc:      in.Desc,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"feature_class": in.FeatureClass,
		"features":      features,
		"count":         len(features),
		"limit":         limit,
		"offset":        offset,
	}, nil
}

func toolGetFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		ID           string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.ID == "" {
		return nil, fmt.Errorf("feature_class and id are required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	// GetFeatureScoped (not GetFeature) so the agent/assistant surface applies the
	// same Phase-58 territory + Phase-61 draft gates a scoped user's GET would --
	// the tool must not read a feature its caller couldn't open.
	feature, err := crud.GetFeatureScoped(ctx, db, loaded, in.ID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, fmt.Errorf("feature %s not found in %q", in.ID, in.FeatureClass)
		}
		return nil, err
	}
	// A soft-deleted feature must not be surfaced here. GetFeatureScoped mirrors the
	// HTTP GET, which still returns a deleted row so the panel can offer Restore --
	// but this read-only tool has no such need, so a deleted feature answers
	// not-found like any other hidden row.
	if feature["deleted_at"] != nil {
		return nil, fmt.Errorf("feature %s not found in %q", in.ID, in.FeatureClass)
	}
	return feature, nil
}

func toolQueryFeaturesAggregate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		GroupBy      string `json:"group_by"`
		Aggregate    *struct {
			Fn    string `json:"fn"`
			Field string `json:"field"`
		} `json:"aggregate"`
		Scope *struct {
			Kind        string    `json:"kind"`
			TerritoryID string    `json:"territory_id"`
			Center      []float64 `json:"center"`
			RadiusM     float64   `json:"radius_m"`
			CircuitID   string    `json:"circuit_id"`
		} `json:"scope"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	opts := crud.AggregateOptions{GroupBy: in.GroupBy, Limit: in.Limit}
	if in.Aggregate != nil {
		opts.AggFn = in.Aggregate.Fn
		opts.AggField = in.Aggregate.Field
	}
	if in.Scope != nil {
		opts.Scope = &crud.AggregateScope{
			Kind:        in.Scope.Kind,
			TerritoryID: in.Scope.TerritoryID,
			Center:      in.Scope.Center,
			RadiusM:     in.Scope.RadiusM,
			CircuitID:   in.Scope.CircuitID,
		}
	}

	result, err := crud.AggregateFeatures(ctx, db, loaded, opts)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"feature_class": in.FeatureClass,
		"result":        result,
	}, nil
}

func toolRunBillOfQuantities(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var req boq.Request
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The engine re-validates everything (scope kind, classes, currency); this
	// front door only forwards the request, same as the HTTP handler.
	return boq.RunBoQ(ctx, db, req)
}

func toolRunAssetRegister(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var req boq.RegisterRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return boq.RunAssetRegister(ctx, db, req)
}

func toolValidateFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string          `json:"feature_class"`
		Subtype      *int            `json:"subtype"`
		Geometry     json.RawMessage `json:"geometry"`
		Attributes   map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	// A well-formed-but-invalid feature is a successful validation *result*
	// (valid:false), not a tool error -- the report carries the reason. Only a
	// bad request (unparseable args, unknown class) is a tool error above.
	return crud.ValidateFeature(loaded, crud.ValidateFeatureInput{
		Subtype:    in.Subtype,
		Geometry:   in.Geometry,
		Attributes: in.Attributes,
	}), nil
}

func toolListTraceConfigurations(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	configs, err := trace.ListConfigurations(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"trace_configurations": configs, "count": len(configs)}, nil
}

func toolRunTrace(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TraceConfiguration string `json:"trace_configuration"`
		Start              struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"start"`
		Target *struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"target"`
		CrossTransformers *bool `json:"cross_transformers"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TraceConfiguration == "" {
		return nil, fmt.Errorf("trace_configuration is required")
	}
	if in.Start.FeatureClass == "" || in.Start.FeatureID == "" {
		return nil, fmt.Errorf("start.feature_class and start.feature_id are required")
	}

	input := trace.RunTraceInput{
		TraceConfiguration: in.TraceConfiguration,
		Start:              trace.FeatureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
		CrossTransformers:  in.CrossTransformers,
	}
	if in.Target != nil {
		input.Target = &trace.FeatureRef{FeatureClass: in.Target.FeatureClass, FeatureID: in.Target.FeatureID}
	}

	// The engine re-validates (and, for SHORTEST_PATH, requires target); its
	// validation/not-found errors surface in-band as the tool error.
	result, err := trace.RunTrace(ctx, db, input)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func toolRunIsolationTrace(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TraceConfiguration string `json:"trace_configuration"`
		Device             struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"device"`
		BoundaryClass string `json:"boundary_class"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TraceConfiguration == "" {
		return nil, fmt.Errorf("trace_configuration is required")
	}
	if in.Device.FeatureClass == "" || in.Device.FeatureID == "" {
		return nil, fmt.Errorf("device.feature_class and device.feature_id are required")
	}

	// The engine re-validates (DOWNSTREAM configuration required, feature must
	// exist); its validation/not-found errors surface in-band as the tool error.
	result, err := trace.Isolate(ctx, db, trace.IsolationInput{
		TraceConfiguration: in.TraceConfiguration,
		Device:             trace.FeatureRef{FeatureClass: in.Device.FeatureClass, FeatureID: in.Device.FeatureID},
		BoundaryClass:      in.BoundaryClass,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func toolRunFeederTrace(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TraceConfiguration string `json:"trace_configuration"`
		Start              struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"start"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TraceConfiguration == "" {
		return nil, fmt.Errorf("trace_configuration is required")
	}
	if in.Start.FeatureClass == "" || in.Start.FeatureID == "" {
		return nil, fmt.Errorf("start.feature_class and start.feature_id are required")
	}

	result, err := trace.FeederTrace(ctx, db, trace.FeederInput{
		TraceConfiguration: in.TraceConfiguration,
		Start:              trace.FeatureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func toolRunSectionTrace(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TraceConfiguration string `json:"trace_configuration"`
		Start              struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"start"`
		BoundaryClass  string `json:"boundary_class"`
		ProtectionZone bool   `json:"protection_zone"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TraceConfiguration == "" {
		return nil, fmt.Errorf("trace_configuration is required")
	}
	if in.Start.FeatureClass == "" || in.Start.FeatureID == "" {
		return nil, fmt.Errorf("start.feature_class and start.feature_id are required")
	}

	result, err := trace.SectionTrace(ctx, db, trace.SectionInput{
		TraceConfiguration: in.TraceConfiguration,
		Start:              trace.FeatureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
		BoundaryClass:      in.BoundaryClass,
		ProtectionZone:     in.ProtectionZone,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func toolRunServiceTrace(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TraceConfiguration string `json:"trace_configuration"`
		Start              struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"start"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TraceConfiguration == "" {
		return nil, fmt.Errorf("trace_configuration is required")
	}
	if in.Start.FeatureClass == "" || in.Start.FeatureID == "" {
		return nil, fmt.Errorf("start.feature_class and start.feature_id are required")
	}

	result, err := trace.ServiceTrace(ctx, db, trace.ServiceInput{
		TraceConfiguration: in.TraceConfiguration,
		Start:              trace.FeatureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func toolSuggestConnections(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string  `json:"feature_class"`
		FeatureID    string  `json:"feature_id"`
		Limit        int     `json:"limit"`
		RadiusMeters float64 `json:"radius_meters"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}

	suggestions, err := connectivity.SuggestConnections(ctx, db, connectivity.SuggestInput{
		FeatureClass: in.FeatureClass,
		FeatureID:    in.FeatureID,
		Limit:        in.Limit,
		RadiusMeters: in.RadiusMeters,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"suggestions": suggestions, "count": len(suggestions)}, nil
}

func toolConnectivityAudit(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ToleranceMeters float64 `json:"tolerance_m"`
		MaxFindings     int     `json:"max_findings"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	report, err := connectivity.AuditConnectivity(ctx, db, connectivity.AuditInput{
		ToleranceMeters: in.ToleranceMeters,
		MaxFindings:     in.MaxFindings,
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

func toolValidationIssues(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status   string `json:"status"`
		Severity string `json:"severity"`
		Kind     string `json:"kind"`
		Limit    int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	issues, err := connectivity.ListIssues(ctx, db, connectivity.IssueFilter{
		Status:   in.Status,
		Severity: in.Severity,
		Kind:     in.Kind,
		Limit:    in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"issues": issues, "count": len(issues)}, nil
}

func toolAssetLifeReport(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass   string  `json:"feature_class"`
		DueWithinYears float64 `json:"due_within_years"`
		MaxItems       int     `json:"max_items"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	report, err := assetlife.Aging(ctx, db, assetlife.AgingInput{
		FeatureClass:   in.FeatureClass,
		DueWithinYears: in.DueWithinYears,
		MaxItems:       in.MaxItems,
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

func toolSetFeatureLifecycle(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass    string  `json:"feature_class"`
		FeatureID       string  `json:"feature_id"`
		LifecycleStatus *string `json:"lifecycle_status"`
		CommissionedAt  *string `json:"commissioned_at"`
		RetiredAt       *string `json:"retired_at"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	// The acting user is already on ctx (auth.WithUser in Execute), so SetLifecycle
	// attributes the change and applies the same scope gates as the REST path.
	return crud.SetLifecycle(ctx, db, loaded, in.FeatureID, crud.LifecycleInput{
		Status:         in.LifecycleStatus,
		CommissionedAt: in.CommissionedAt,
		RetiredAt:      in.RetiredAt,
	})
}

func toolSuggestRelationships(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string  `json:"feature_class"`
		FeatureID    string  `json:"feature_id"`
		Limit        int     `json:"limit"`
		RadiusMeters float64 `json:"radius_meters"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}

	suggestions, err := relationship.SuggestRelationships(ctx, db, relationship.SuggestInput{
		FeatureClass: in.FeatureClass,
		FeatureID:    in.FeatureID,
		Limit:        in.Limit,
		RadiusMeters: in.RadiusMeters,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"suggestions": suggestions, "count": len(suggestions)}, nil
}

func toolCreateFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string          `json:"feature_class"`
		Name         *string         `json:"name"`
		Subtype      *int            `json:"subtype"`
		Geometry     json.RawMessage `json:"geometry"`
		TerritoryID  *string         `json:"territory_id"`
		Attributes   map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	// The acting user (stamped as created_by) is already on ctx via
	// auth.WithUser in Execute, so the write is attributed to the acting user
	// exactly as a REST create is to the session's. Validation, territory, and
	// DB errors surface in-band as the tool error; success returns the new
	// feature's envelope (with its id).
	return crud.CreateFeature(ctx, db, loaded, crud.CreateFeatureInput{
		Subtype:     in.Subtype,
		Name:        in.Name,
		Geometry:    in.Geometry,
		TerritoryID: in.TerritoryID,
		Attributes:  in.Attributes,
	})
}

func toolUpdateFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string          `json:"feature_class"`
		ID           string          `json:"id"`
		Name         *string         `json:"name"`
		Subtype      *int            `json:"subtype"`
		Geometry     json.RawMessage `json:"geometry"`
		TerritoryID  *string         `json:"territory_id"`
		Attributes   map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.ID == "" {
		return nil, fmt.Errorf("feature_class and id are required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}

	// The acting user (stamped as updated_by) is already on ctx via
	// auth.WithUser in Execute. The shared crud.UpdateFeature fetches the current
	// row, enforces the same validation as the REST PATCH, and writes; its
	// validation/not-found errors surface in-band as the tool error.
	return crud.UpdateFeature(ctx, db, loaded, in.ID, crud.UpdateFeatureInput{
		Name:        in.Name,
		Subtype:     in.Subtype,
		Geometry:    in.Geometry,
		TerritoryID: in.TerritoryID,
		Attributes:  in.Attributes,
	})
}

// featureRefIn is the shared {feature_class, id} body of the feature-lifecycle
// tools (publish/unpublish/delete/restore/purge). Each loads the class and hands
// the id to the matching exported crud engine function -- the same call the REST
// POST .../publish|unpublish|restore, DELETE .../{id}, and DELETE .../purge
// routes make. The acting user is on ctx via auth.WithUser in Execute, so scope
// and draft enforcement (and history attribution) match the REST path exactly.
type featureRefIn struct {
	FeatureClass string `json:"feature_class"`
	ID           string `json:"id"`
}

func (in featureRefIn) load(ctx context.Context, db bun.IDB) (*metadata.Loaded, error) {
	if in.FeatureClass == "" || in.ID == "" {
		return nil, fmt.Errorf("feature_class and id are required")
	}
	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}
	return loaded, nil
}

func toolMarkOwnershipDownstreamPrivate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	return ownership.MarkDownstreamPrivate(ctx, db, ownership.FeatureRef{
		FeatureClass: in.FeatureClass,
		FeatureID:    in.FeatureID,
	})
}

func toolResyncOwnership(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	return ownership.Resync(ctx, db)
}

func toolPublishFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	// Idempotent -- publishing an already-published row returns its current
	// state; an incomplete draft is rejected in-band as the tool error.
	return crud.PublishFeature(ctx, db, loaded, in.ID)
}

func toolUnpublishFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	return crud.UnpublishFeature(ctx, db, loaded, in.ID)
}

func toolDeleteFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	// Soft delete (deleted_at = now()); reversible via restore_feature.
	// Idempotent -- deleting an already-deleted row returns its current state.
	return crud.DeleteFeature(ctx, db, loaded, in.ID)
}

func toolRestoreFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	return crud.RestoreFeature(ctx, db, loaded, in.ID)
}

func toolPurgeFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	// Hard delete -- physically removes the row, and requires it to already be
	// soft-deleted (the engine rejects a live row). Nothing to hand back on
	// success, so acknowledge with the id.
	if err := crud.PurgeFeature(ctx, db, loaded, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"purged": true, "feature_class": in.FeatureClass, "id": in.ID}, nil
}

// classRefIn is the argument shape for the bulk class-wipe tools -- just the
// feature class code, since these operate on the whole class rather than one row.
type classRefIn struct {
	FeatureClass string `json:"feature_class"`
}

func (in classRefIn) load(ctx context.Context, db bun.IDB) (*metadata.Loaded, error) {
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}
	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}
	return loaded, nil
}

func toolWipeFeatureClass(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in classRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	// Bulk soft-delete (deleted_at = now()) of every live, in-scope row; reversible
	// per-feature via restore_feature until purge_deleted_feature_class runs.
	// WipeClass owns its own transaction, so it needs the concrete *bun.DB.
	real, err := asDB(db)
	if err != nil {
		return nil, err
	}
	wiped, err := crud.WipeClass(ctx, real, loaded)
	if err != nil {
		return nil, err
	}
	return map[string]any{"soft_deleted": wiped, "feature_class": in.FeatureClass}, nil
}

func toolPurgeDeletedFeatureClass(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in classRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	// Permanent purge of every already-soft-deleted, in-scope row, with the
	// topology/relationship cascade. NOT reversible. Owns its own transaction.
	real, err := asDB(db)
	if err != nil {
		return nil, err
	}
	purged, err := crud.PurgeDeletedClass(ctx, real, loaded)
	if err != nil {
		return nil, err
	}
	return map[string]any{"purged": purged, "feature_class": in.FeatureClass}, nil
}

// asDB narrows the handler's bun.IDB to the concrete *bun.DB the
// transaction-owning engines (split, apply/discard pending edit) need to open
// their own RunInTx. Execute is always called with the app's real *bun.DB, so
// this succeeds in practice; the checked assertion turns a hypothetical
// tx-scoped caller into a clear tool error instead of a panic.
func asDB(db bun.IDB) (*bun.DB, error) {
	real, ok := db.(*bun.DB)
	if !ok {
		return nil, fmt.Errorf("this tool needs a top-level database handle")
	}
	return real, nil
}

func toolSplitLine(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string  `json:"feature_class"`
		ID           string  `json:"id"`
		Lng          float64 `json:"lng"`
		Lat          float64 `json:"lat"`
		NewFeature   struct {
			Subtype     *int           `json:"subtype"`
			Name        *string        `json:"name"`
			TerritoryID *string        `json:"territory_id"`
			Attributes  map[string]any `json:"attributes"`
		} `json:"new_feature"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.ID == "" {
		return nil, fmt.Errorf("feature_class and id are required")
	}

	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}
	// Split only makes sense for a line -- reject anything else up front with the
	// same message the REST split handler gives, rather than failing in PostGIS.
	if loaded.FeatureClass.GeometryType != "line" {
		return nil, fmt.Errorf("feature class %q is not a line, so it can't be split", loaded.FeatureClass.Code)
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// The acting user is on ctx via auth.WithUser; SplitLineFeature owns the
	// transaction, does the PostGIS math, and returns the new far-half feature's
	// envelope. Scope/draft/validation errors surface in-band as the tool error.
	return crud.SplitLineFeature(ctx, sdb, loaded, in.ID, in.Lng, in.Lat, crud.SplitNewFeatureInput{
		Subtype:     in.NewFeature.Subtype,
		Name:        in.NewFeature.Name,
		TerritoryID: in.NewFeature.TerritoryID,
		Attributes:  in.NewFeature.Attributes,
	})
}

func toolApplyPendingEdit(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// Applies the acting author's staged private edit onto the public row
	// (conflict-checked). The author is derived from ctx inside the engine.
	return crud.ApplyPendingEdit(ctx, sdb, loaded, in.ID)
}

func toolDiscardPendingEdit(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in featureRefIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	loaded, err := in.load(ctx, db)
	if err != nil {
		return nil, err
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	if err := crud.DiscardPendingEdit(ctx, sdb, loaded, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"discarded": true, "feature_class": in.FeatureClass, "id": in.ID}, nil
}

func toolEstablishConnection(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		From struct {
			FeatureClass string  `json:"feature_class"`
			FeatureID    string  `json:"feature_id"`
			Terminal     *string `json:"terminal"`
		} `json:"from"`
		To struct {
			FeatureClass string  `json:"feature_class"`
			FeatureID    string  `json:"feature_id"`
			Terminal     *string `json:"terminal"`
		} `json:"to"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.From.FeatureClass == "" || in.From.FeatureID == "" {
		return nil, fmt.Errorf("from.feature_class and from.feature_id are required")
	}
	if in.To.FeatureClass == "" || in.To.FeatureID == "" {
		return nil, fmt.Errorf("to.feature_class and to.feature_id are required")
	}

	// The acting user (stamped as established_by) is already on ctx via
	// auth.WithUser in Execute. The engine enforces the network rule; its
	// validation/not-found/rule errors surface in-band as the tool error.
	return connectivity.EstablishConnection(ctx, db, connectivity.EstablishConnectionInput{
		From: connectivity.Endpoint{FeatureClass: in.From.FeatureClass, FeatureID: in.From.FeatureID, Terminal: in.From.Terminal},
		To:   connectivity.Endpoint{FeatureClass: in.To.FeatureClass, FeatureID: in.To.FeatureID, Terminal: in.To.Terminal},
	})
}

func toolEstablishRelationship(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Origin struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"origin"`
		Destination struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"destination"`
		Kind *string `json:"kind"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Origin.FeatureClass == "" || in.Origin.FeatureID == "" {
		return nil, fmt.Errorf("origin.feature_class and origin.feature_id are required")
	}
	if in.Destination.FeatureClass == "" || in.Destination.FeatureID == "" {
		return nil, fmt.Errorf("destination.feature_class and destination.feature_id are required")
	}

	// The acting user (stamped as established_by) is already on ctx via
	// auth.WithUser in Execute. The engine enforces the relationship class and
	// its cardinality; those errors surface in-band as the tool error.
	return relationship.EstablishRelationship(ctx, db, relationship.EstablishRelationshipInput{
		Origin:      relationship.FeatureRef{FeatureClass: in.Origin.FeatureClass, FeatureID: in.Origin.FeatureID},
		Destination: relationship.FeatureRef{FeatureClass: in.Destination.FeatureClass, FeatureID: in.Destination.FeatureID},
		Kind:        in.Kind,
	})
}

func toolListConnections(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}

	// Same query as the REST GET /connectivity list; each row carries its id, so
	// a caller can find the connection to pass to revoke_connection.
	conns, count, err := connectivity.ListConnectionsForFeature(ctx, db, in.FeatureClass, in.FeatureID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"connections": conns, "count": count}, nil
}

func toolListRelationships(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}

	// Same query as the REST GET /relationships list; each row carries its id, so
	// a caller can find the relationship to pass to revoke_relationship.
	rels, count, err := relationship.ListRelationshipsForFeature(ctx, db, in.FeatureClass, in.FeatureID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"relationships": rels, "count": count}, nil
}

func toolRevokeConnection(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	// Same engine path as the REST DELETE /connectivity/{id}; a missing id
	// surfaces in-band as the tool's not-found error. Returns a small
	// acknowledgement (the delete itself yields no row).
	if err := connectivity.RevokeConnection(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"revoked": true, "id": in.ID}, nil
}

func toolRevokeRelationship(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	// Same engine path as the REST DELETE /relationships/{id}; a missing id
	// surfaces in-band as the tool's not-found error.
	if err := relationship.RevokeRelationship(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"revoked": true, "id": in.ID}, nil
}
