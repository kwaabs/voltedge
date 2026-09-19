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

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
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
	Code         string        `json:"code"`
	Name         string        `json:"name"`
	GeometryType string        `json:"geometry_type"`
	Subtypes     []subtypeOut  `json:"subtypes"`
	Fields       []fieldOut    `json:"fields"`
	Terminals    []terminalOut `json:"terminals,omitempty"`
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

	feature, err := crud.GetFeature(ctx, db, loaded, in.ID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, fmt.Errorf("feature %s not found in %q", in.ID, in.FeatureClass)
		}
		return nil, err
	}
	return feature, nil
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
		BoundaryClass string `json:"boundary_class"`
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
