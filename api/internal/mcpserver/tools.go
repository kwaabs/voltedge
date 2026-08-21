package mcpserver

// The Slice 1 read-only tool set (docs/phase-18-mcp.md). Each tool is a
// thin adapter over an already-exported engine function -- no query or
// validation logic is reimplemented here -- and each declares the RBAC
// permission its REST equivalent requires (all four: features:read), which
// handleToolsCall enforces against the token's user before running the
// handler. The suggestion/validation and mutating tools the design
// envisions need engine work (ranked-candidate logic, an exported
// validate-only path, the establish/create/update writes) and land in later
// slices.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// searchDefaultLimit/searchMaxLimit bound a search page so a tool call never
// accidentally pulls an entire feature class; an omitted or out-of-range
// limit is corrected rather than rejected.
const (
	searchDefaultLimit = 50
	searchMaxLimit     = 500
)

// orderedToolNames fixes the tools/list ordering (map iteration is random),
// so the advertised list is stable across calls.
var orderedToolNames = []string{
	"list_feature_classes",
	"describe_feature_class",
	"search_features",
	"get_feature",
}

func (s *Server) registerReadTools() {
	s.tools["list_feature_classes"] = tool{
		requiredPerm: auth.PermFeaturesRead,
		handler:      toolListFeatureClasses,
		def: toolDef{
			Name: "list_feature_classes",
			Description: "List every compiled (published) VoltEdge feature class -- the " +
				"catalog of asset types on the network. Returns each class's code " +
				"(use it as feature_class in the other tools), display name, geometry " +
				"type, and network role. Takes no arguments.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
	}

	s.tools["describe_feature_class"] = tool{
		requiredPerm: auth.PermFeaturesRead,
		handler:      toolDescribeFeatureClass,
		def: toolDef{
			Name: "describe_feature_class",
			Description: "Describe one feature class's schema: its geometry type, subtypes, " +
				"fields (name, data type, nullability, and any coded-domain values), and " +
				"connection terminals. Use this to learn what fields exist before calling " +
				"search_features or reading a feature.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The feature class code, e.g. from list_feature_classes."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["search_features"] = tool{
		requiredPerm: auth.PermFeaturesRead,
		handler:      toolSearchFeatures,
		def: toolDef{
			Name: "search_features",
			Description: "Browse a feature class's features (soft-deleted rows excluded), " +
				"with an optional case-insensitive name match, status filter, sort, and " +
				"paging. Each result is the full feature envelope (id, name, status, " +
				"geometry, and a nested attributes map). Returns at most 500 per call.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code to search."},
					"name_query":{"type":"string","description":"Case-insensitive partial match against the feature name."},
					"status":{"type":"string","enum":["draft","published"],"description":"Filter to a single status; omit for both."},
					"sort":{"type":"string","description":"Column to sort by (a system column or a field name); defaults to created_at."},
					"desc":{"type":"boolean","description":"Sort descending; defaults to ascending."},
					"limit":{"type":"integer","description":"Page size, 1-500 (default 50)."},
					"offset":{"type":"integer","description":"Rows to skip for paging (default 0)."}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["get_feature"] = tool{
		requiredPerm: auth.PermFeaturesRead,
		handler:      toolGetFeature,
		def: toolDef{
			Name: "get_feature",
			Description: "Fetch a single feature by its id within a feature class. Returns the " +
				"full feature envelope, including a soft-deleted row (with deleted_at set) " +
				"so a caller can tell a deleted feature from a missing one.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
		},
	}
}

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
