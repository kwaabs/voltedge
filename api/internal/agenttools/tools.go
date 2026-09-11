package agenttools

// The tool definitions (docs/phase-18-mcp.md for the per-tool history, moved
// here intact in Phase 19 Slice 2). Each tool is a thin adapter over an
// already-exported engine function -- no query or validation logic is
// reimplemented here -- and each declares the RBAC permission its REST
// equivalent requires, which Execute enforces against the acting user before
// running the handler.
//
// readTools returns the read-only surface in advertised order: the four feature
// tools (features:read), validate_feature (features:write -- it previews a
// create), the trace tools (trace:run), and the two suggestion tools
// (connectivity:read / relationships:read); buildRegistry stamps them
// readOnlyHint:true. writeTools returns the mutating surface (create_feature and
// update_feature -> features:write, establish_connection -> connectivity:commit,
// establish_relationship -> relationships:commit), each carrying its own
// annotations (readOnlyHint:false) so a client confirms with a human first.

import (
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// falseHint/trueHint back the *bool annotation fields (Go has no bool-literal
// address), so a write tool can advertise explicit hint values. establish_* and
// update_feature use trueHint for idempotentHint: re-running them yields the same
// state (an existing edge's ON CONFLICT no-op, or the same partial change
// re-applied).
var (
	falseHint = false
	trueHint  = true
)

// readTools returns the read-only tools in advertised order.
func readTools() []Tool {
	return []Tool{
		{
			Name: "list_feature_classes",
			Description: "List every compiled (published) VoltEdge feature class -- the " +
				"catalog of asset types on the network. Returns each class's code " +
				"(use it as feature_class in the other tools), display name, geometry " +
				"type, and network role. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListFeatureClasses,
		},
		{
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
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolDescribeFeatureClass,
		},
		{
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
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolSearchFeatures,
		},
		{
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
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolGetFeature,
		},
		{
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
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolValidateFeature,
		},
		{
			Name: "list_trace_configurations",
			Description: "List every available network trace configuration -- the named " +
				"profiles run_trace can run. Returns each configuration's code (use it as " +
				"trace_configuration in run_trace), display name, and the direction it walks " +
				"(CONNECTED, DOWNSTREAM, UPSTREAM, or SHORTEST_PATH). Call this first to " +
				"discover valid codes. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolListTraceConfigurations,
		},
		{
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
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolRunTrace,
		},
		{
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
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolRunIsolationTrace,
		},
		{
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
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolRunFeederTrace,
		},
		{
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
					"boundary_class":{"type":"string","description":"Feature class that bounds the section (a sectionalizing device); defaults to MV_SWITCH."}
				},
				"required":["trace_configuration","start"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolRunSectionTrace,
		},
		{
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
			RequiredPerm: auth.PermConnectivityRead,
			Handler:      toolSuggestConnections,
		},
		{
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
			RequiredPerm: auth.PermRelationshipsRead,
			Handler:      toolSuggestRelationships,
		},
	}
}

// writeTools returns the mutating tools in advertised order. Unlike the read
// tools these carry their own annotations (readOnlyHint:false) so a client
// surfaces them for human confirmation before calling -- the write-safety model
// this tool set chose. Each is gated by exactly the RBAC permission its REST
// equivalent needs.
func writeTools() []Tool {
	return []Tool{
		{
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
			Annotations: &Annotations{
				Title:           "Create feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it creates another feature
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolCreateFeature,
		},
		{
			Name: "update_feature",
			Description: "Update an existing feature -- the same validated partial write the REST " +
				"PATCH route performs. Every field is optional and only what you send is changed: " +
				"omit geometry or territory_id to leave them, omit name to keep it (send an empty " +
				"string to clear it), and send only the attributes you want to change. Subtype " +
				"cannot be changed. Fields locked after publish are rejected while the feature is " +
				"published, a published feature is re-checked for completeness, and a deleted " +
				"feature must be restored first. Returns the updated feature's full envelope. This " +
				"WRITES to the database: confirm the change with a human before calling it. Use " +
				"get_feature to see current values first and describe_feature_class for the fields.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."},
					"name":{"type":"string","description":"New label; omit to keep the current name, send an empty string to clear it."},
					"geometry":{"type":"object","description":"Replacement GeoJSON geometry; omit to leave the geometry unchanged. Only valid for a geometric class."},
					"territory_id":{"type":"string","description":"Territory override (uuid); omit to leave the assignment (re-resolved automatically if geometry changes). Only valid for a geometric class."},
					"attributes":{"type":"object","description":"Only the field values to change, keyed by field name; omit or empty to change no attributes.","additionalProperties":true}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field update overwrites, it doesn't delete the feature
				IdempotentHint:  &trueHint,  // re-applying the same change yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolUpdateFeature,
		},
		{
			Name: "establish_connection",
			Description: "Establish a connection between two features -- the same zero-trust write " +
				"the REST connectivity route performs. The connection commits only when an active " +
				"network rule authorizes the from/to feature classes, subtypes, and terminals " +
				"(and phasing); with no matching rule, or a matching rule marked invalid, it is " +
				"rejected. Returns the committed connection and the network rule that authorized " +
				"it. This WRITES to the database: confirm with a human before calling, and prefer " +
				"suggest_connections first to find legal, ranked candidates. Re-establishing an " +
				"already-connected pair is a harmless no-op (the original is kept).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"from":{
						"type":"object",
						"description":"One side of the connection.",
						"properties":{
							"feature_class":{"type":"string","description":"The feature class code."},
							"feature_id":{"type":"string","description":"The feature's id (uuid)."},
							"terminal":{"type":"string","description":"Optional terminal code on this feature; required when the authorizing rule names one."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"to":{
						"type":"object",
						"description":"The other side of the connection.",
						"properties":{
							"feature_class":{"type":"string","description":"The feature class code."},
							"feature_id":{"type":"string","description":"The feature's id (uuid)."},
							"terminal":{"type":"string","description":"Optional terminal code on this feature; required when the authorizing rule names one."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					}
				},
				"required":["from","to"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Establish connection",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it links, it doesn't remove
				IdempotentHint:  &trueHint,  // re-establishing the same pair is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermConnectivityCommit,
			Handler:      toolEstablishConnection,
		},
		{
			Name: "establish_relationship",
			Description: "Establish a structural/spatial relationship between two features (e.g. a " +
				"pole supporting a conductor span) -- the same explicit-confirm write the REST " +
				"relationships route performs. It commits only when a matching relationship class " +
				"exists for the origin/destination pairing (and the optional kind) and that " +
				"class's cardinality isn't already exhausted; otherwise it is rejected. Returns " +
				"the committed relationship, and a plausibility_warning when the two features' " +
				"geometries are further apart than expected for the class (a warning, not a " +
				"rejection). This WRITES to the database: confirm with a human before calling, and " +
				"prefer suggest_relationships first. Re-establishing an existing relationship is a " +
				"harmless no-op.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"origin":{
						"type":"object",
						"description":"The origin feature.",
						"properties":{
							"feature_class":{"type":"string","description":"The feature class code."},
							"feature_id":{"type":"string","description":"The feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"destination":{
						"type":"object",
						"description":"The destination feature.",
						"properties":{
							"feature_class":{"type":"string","description":"The feature class code."},
							"feature_id":{"type":"string","description":"The feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"kind":{"type":"string","description":"Optional relationship kind -- only needed to disambiguate when more than one relationship class exists for the same origin/destination pairing."}
				},
				"required":["origin","destination"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Establish relationship",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it relates, it doesn't remove
				IdempotentHint:  &trueHint,  // re-establishing the same pairing is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermRelationshipsCommit,
			Handler:      toolEstablishRelationship,
		},
	}
}
