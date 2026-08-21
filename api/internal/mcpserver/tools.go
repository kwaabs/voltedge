package mcpserver

// The read-only tool set (docs/phase-18-mcp.md). Each tool is a thin adapter
// over an already-exported engine function -- no query or validation logic is
// reimplemented here -- and each declares the RBAC permission its REST
// equivalent requires, which handleToolsCall enforces against the token's user
// before running the handler. Slice 1 added the four feature tools
// (features:read); Slice 2 adds the two trace tools (trace:run), backed by the
// engine wrappers in internal/trace/export.go; Slice 3 adds validate_feature
// (features:write -- it previews a create, whose REST equivalent needs write),
// backed by crud.ValidateFeature; Slice 4 adds the two suggestion tools
// (suggest_connections -> connectivity:read, suggest_relationships ->
// relationships:read), backed by the new ranked-candidate engines
// connectivity.SuggestConnections / relationship.SuggestRelationships; Slice 5
// adds the three composite trace tools (run_isolation_trace, run_feeder_trace,
// run_section_trace -- all trace:run, the same gate the /trace mount uses),
// backed by the trace.Isolate / trace.FeederTrace / trace.SectionTrace
// wrappers. Slices 1-5 are all read-only. Slice 6 opens the write surface with
// create_feature (registerWriteTools, gated features:write), backed by the
// shared crud.CreateFeature; write safety is the client-side MCP annotation
// model (see toolAnnotations in server.go) -- the tool advertises
// readOnlyHint:false so a client confirms with a human before calling. The
// remaining mutating tools (establish_*/update_) land in later slices.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
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

// orderedToolNames fixes the tools/list ordering (map iteration is random),
// so the advertised list is stable across calls.
var orderedToolNames = []string{
	"list_feature_classes",
	"describe_feature_class",
	"search_features",
	"get_feature",
	"validate_feature",
	"list_trace_configurations",
	"run_trace",
	"run_isolation_trace",
	"run_feeder_trace",
	"run_section_trace",
	"suggest_connections",
	"suggest_relationships",
	"create_feature",
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

	s.tools["validate_feature"] = tool{
		requiredPerm: auth.PermFeaturesWrite,
		handler:      toolValidateFeature,
		def: toolDef{
			Name: "validate_feature",
			Description: "Dry-run the validation a create would perform on a proposed feature, " +
				"without writing anything. Checks the subtype, geometry presence/type, " +
				"required fields for the subtype, that every attribute is a known editable " +
				"field, and each value's type, coded-domain membership, and numeric range. " +
				"Returns {valid:true} or {valid:false, error:<first problem>}. Use " +
				"describe_feature_class to learn the fields first. Note this is a " +
				"metadata-level pre-flight only -- it does not check database constraints " +
				"(uniqueness, referenced-feature existence) or assign a territory, so a " +
				"valid result is not a guaranteed insert.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the proposed feature belongs to."},
					"subtype":{"type":"integer","description":"The subtype code -- required when the class has subtypes, forbidden when it doesn't."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object -- required exactly when the feature class has geometry."},
					"attributes":{"type":"object","description":"Field values keyed by field name.","additionalProperties":true}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["list_trace_configurations"] = tool{
		requiredPerm: auth.PermTraceRun,
		handler:      toolListTraceConfigurations,
		def: toolDef{
			Name: "list_trace_configurations",
			Description: "List every available network trace configuration -- the named " +
				"profiles run_trace can run. Returns each configuration's code (use it as " +
				"trace_configuration in run_trace), display name, and the direction it walks " +
				"(CONNECTED, DOWNSTREAM, UPSTREAM, or SHORTEST_PATH). Call this first to " +
				"discover valid codes. Takes no arguments.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
	}

	s.tools["run_trace"] = tool{
		requiredPerm: auth.PermTraceRun,
		handler:      toolRunTrace,
		def: toolDef{
			Name: "run_trace",
			Description: "Run a network trace from a starting feature, following the named " +
				"trace configuration's direction over the connectivity graph and stopping at " +
				"open devices (barriers) per that configuration. Returns the features reached " +
				"(each as feature_class + feature_id), the subset that were barriers, and -- " +
				"for a SHORTEST_PATH configuration -- whether a path to the target was found " +
				"(a target is required only then). Use list_trace_configurations to find a " +
				"valid trace_configuration code first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"trace_configuration":{"type":"string","description":"A trace configuration code from list_trace_configurations."},
					"start":{
						"type":"object",
						"description":"The feature the trace starts from.",
						"properties":{
							"feature_class":{"type":"string","description":"The start feature's class code."},
							"feature_id":{"type":"string","description":"The start feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"target":{
						"type":"object",
						"description":"The destination feature -- required only for a SHORTEST_PATH configuration, ignored otherwise.",
						"properties":{
							"feature_class":{"type":"string","description":"The target feature's class code."},
							"feature_id":{"type":"string","description":"The target feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					}
				},
				"required":["trace_configuration","start"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["run_isolation_trace"] = tool{
		requiredPerm: auth.PermTraceRun,
		handler:      toolRunIsolationTrace,
		def: toolDef{
			Name: "run_isolation_trace",
			Description: "Analyze isolating a switching device: compute what would be de-energized " +
				"if the device were opened right now, without changing anything. Composes a " +
				"downstream trace (from past the device) with an upstream search for the nearest " +
				"protective boundary. Returns the de-energized features, the upstream boundary " +
				"device (if any), the subset of de-energized features that are critical " +
				"facilities, and possible backfeed paths (live connections this radial analysis " +
				"didn't cross -- report them for a human to verify, not a guarantee). Requires a " +
				"DOWNSTREAM trace_configuration. Read-only: it never opens the device.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"trace_configuration":{"type":"string","description":"A DOWNSTREAM trace configuration code from list_trace_configurations."},
					"device":{
						"type":"object",
						"description":"The switching device to analyze isolating.",
						"properties":{
							"feature_class":{"type":"string","description":"The device's feature class code."},
							"feature_id":{"type":"string","description":"The device's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"boundary_class":{"type":"string","description":"Feature class to treat as the upstream isolation boundary; defaults to the device's own class."}
				},
				"required":["trace_configuration","device"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["run_feeder_trace"] = tool{
		requiredPerm: auth.PermTraceRun,
		handler:      toolRunFeederTrace,
		def: toolDef{
			Name: "run_feeder_trace",
			Description: "Find the whole feeder a feature belongs to, regardless of where along it " +
				"you start. Walks upstream (ignoring switching state) to the feeder head(s) -- the " +
				"network root(s) -- then unions a downstream trace (respecting the configuration's " +
				"barriers) from each head. Returns the head(s) and every feature the feeder reaches. " +
				"Requires a DOWNSTREAM trace_configuration. Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"trace_configuration":{"type":"string","description":"A DOWNSTREAM trace configuration code from list_trace_configurations."},
					"start":{
						"type":"object",
						"description":"Any feature on the feeder.",
						"properties":{
							"feature_class":{"type":"string","description":"The start feature's class code."},
							"feature_id":{"type":"string","description":"The start feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					}
				},
				"required":["trace_configuration","start"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["run_section_trace"] = tool{
		requiredPerm: auth.PermTraceRun,
		handler:      toolRunSectionTrace,
		def: toolDef{
			Name: "run_section_trace",
			Description: "Find the section a feature sits in: everything between the nearest bounding " +
				"device upstream and the nearest bounding device downstream of it (the '\"which " +
				"customers are between these two switches\"' answer for a planned outage). Composes " +
				"an upstream and a downstream search that stop at -- but don't cross -- the first " +
				"boundary-class device in each direction. Returns the section's features and each " +
				"boundary device (either may be absent when the section runs to the network edge). " +
				"Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"trace_configuration":{"type":"string","description":"A trace configuration code from list_trace_configurations."},
					"start":{
						"type":"object",
						"description":"The feature whose section to find.",
						"properties":{
							"feature_class":{"type":"string","description":"The start feature's class code."},
							"feature_id":{"type":"string","description":"The start feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"boundary_class":{"type":"string","description":"Feature class that bounds the section (a sectionalizing device); defaults to DIST_SWITCH."}
				},
				"required":["trace_configuration","start"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["suggest_connections"] = tool{
		requiredPerm: auth.PermConnectivityRead,
		handler:      toolSuggestConnections,
		def: toolDef{
			Name: "suggest_connections",
			Description: "Propose ranked candidate features the given feature could legally " +
				"connect to, without establishing anything. A candidate is permitted by an " +
				"active network rule, not already connected to this feature, not deleted, and " +
				"-- when both feature classes have geometry -- within a search radius, returned " +
				"nearest-first. Each suggestion names the candidate feature, the terminals the " +
				"authorizing rule implies on each side (if any), the rule, and the distance in " +
				"meters. These are candidates, not facts: a phase-gated rule and database " +
				"constraints are still checked when you actually establish the connection.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code of the feature to suggest connections for."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."},
					"limit":{"type":"integer","description":"Max suggestions to return, 1-50 (default 10)."},
					"radius_meters":{"type":"number","description":"Search radius in meters for the proximity ranking (default 1000); ignored for non-geometric classes."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
		},
	}

	s.tools["suggest_relationships"] = tool{
		requiredPerm: auth.PermRelationshipsRead,
		handler:      toolSuggestRelationships,
		def: toolDef{
			Name: "suggest_relationships",
			Description: "Propose ranked candidate features the given feature could legally form " +
				"a structural/spatial relationship with, without establishing anything. A " +
				"candidate is permitted by a relationship class, satisfies that class's " +
				"cardinality, is not already related to this feature, is not deleted, and -- " +
				"when both classes have geometry -- falls within a search radius, nearest-first. " +
				"Each suggestion names the candidate, the role it would take (origin or " +
				"destination), the relationship class with its kind and cardinality, and the " +
				"distance in meters. Candidates, not facts: establishing still runs the full " +
				"checks.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code of the feature to suggest relationships for."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."},
					"limit":{"type":"integer","description":"Max suggestions to return, 1-50 (default 10)."},
					"radius_meters":{"type":"number","description":"Search radius in meters for the proximity ranking (default 1000); ignored for non-geometric classes."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
		},
	}
}

// falseHint backs the *bool annotation fields (Go has no bool-literal address),
// so a write tool can advertise explicit false hint values.
var falseHint = false

// registerWriteTools registers the mutating tools. Unlike the read tools these
// carry their own annotations (readOnlyHint:false) so a client surfaces them
// for human confirmation before calling -- the write-safety model this server
// chose. Each is gated by exactly the RBAC permission its REST equivalent
// needs. markReadOnly runs before this, so these tools keep their own
// annotations rather than being stamped read-only.
func (s *Server) registerWriteTools() {
	s.tools["create_feature"] = tool{
		requiredPerm: auth.PermFeaturesWrite,
		handler:      toolCreateFeature,
		def: toolDef{
			Name: "create_feature",
			Description: "Create a new feature in a feature class -- the same validated write the " +
				"REST create route performs (subtype, geometry, required fields, editable-field " +
				"and per-value type/domain/range checks, then territory assignment and the " +
				"insert). Returns the created feature's full envelope (including its new id). " +
				"This WRITES to the database: confirm the change with a human before calling it, " +
				"and prefer validate_feature first to catch problems without writing. The feature " +
				"is created as its class's default status (typically draft); publishing is a " +
				"separate step. Use describe_feature_class to learn the fields.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code to create the feature in."},
					"name":{"type":"string","description":"Optional human-assigned label for the feature."},
					"subtype":{"type":"integer","description":"The subtype code -- required when the class has subtypes, forbidden when it doesn't."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object -- required exactly when the feature class has geometry."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry. Only valid for a geometric class."},
					"attributes":{"type":"object","description":"Field values keyed by field name.","additionalProperties":true}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
			Annotations: &toolAnnotations{
				Title:           "Create feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it creates another feature
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
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
	// auth.WithUser in handleToolsCall, so the write is attributed to the
	// token's user exactly as a REST create is to the session's. Validation,
	// territory, and DB errors surface in-band as the tool error; success
	// returns the new feature's envelope (with its id).
	return crud.CreateFeature(ctx, db, loaded, crud.CreateFeatureInput{
		Subtype:     in.Subtype,
		Name:        in.Name,
		Geometry:    in.Geometry,
		TerritoryID: in.TerritoryID,
		Attributes:  in.Attributes,
	})
}
