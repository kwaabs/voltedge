package agenttools

// The tool definitions (docs/phase-18-mcp.md for the per-tool history, moved
// here intact in Phase 19 Slice 2). Each tool is a thin adapter over an
// already-exported engine function -- no query or validation logic is
// reimplemented here -- and each declares the RBAC permission its REST
// equivalent requires, which Execute enforces against the acting user before
// running the handler.
//
// readTools returns the read-only surface in advertised order: the five feature
// tools (features:read -- list/describe/search/get plus query_features_aggregate,
// the count/count-by/spatial-scope analysis behind the map's Aggregate and Radius
// tools), validate_feature (features:write -- it previews a
// create), the trace tools (trace:run), the two suggestion tools and the two
// existing-edge list tools (connectivity:read / relationships:read);
// buildRegistry stamps them readOnlyHint:true. writeTools returns the mutating
// surface -- the feature lifecycle create_feature / update_feature /
// publish_feature / unpublish_feature / delete_feature / restore_feature /
// purge_feature (all features:write), establish_connection / revoke_connection
// -> connectivity:commit, and establish_relationship / revoke_relationship ->
// relationships:commit -- each carrying its own annotations (readOnlyHint:false)
// so a client confirms with a human first; the destructive writes (revoke_*,
// delete_feature, purge_feature) additionally set destructiveHint:true.

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
				"type, network role, and trace_tier -- its rank in the source-to-load " +
				"hierarchy (0 grid infeed .. 600 service/meter; absent = untiered). " +
				"Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListFeatureClasses,
		},
		{
			Name: "describe_feature_class",
			Description: "Describe one feature class's schema: its geometry type, trace_tier " +
				"(source-to-load rank; absent = untiered), subtypes, fields (name, data " +
				"type, nullability, and any coded-domain values), and connection terminals. " +
				"Use this to learn what fields exist before calling search_features or " +
				"reading a feature.",
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
			Name: "query_features_aggregate",
			Description: "Count and aggregate a feature class instead of listing rows -- the " +
				"numeric+spatial analysis behind the map's Aggregate and Radius query tools. " +
				"With no group_by it returns one overall total; with group_by it returns a " +
				"count per distinct value of that field (a \"count by subtype/status/...\"), " +
				"biggest first. An optional aggregate {fn,field} adds a sum/avg/min/max over a " +
				"numeric field (overall, or per group). An optional scope narrows to a " +
				"territory and its whole subtree, or to a radius (metres) around a " +
				"[longitude, latitude] point -- e.g. \"how many of each class are within 500 m " +
				"of here\" (call this once per class) or \"count transformers by rating in this " +
				"territory\". Respects the same read scoping as every other read. Use " +
				"describe_feature_class to learn valid field names first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code to aggregate."},
					"group_by":{"type":"string","description":"A field (or system column) to count by; omit for a single overall total."},
					"aggregate":{
						"type":"object",
						"description":"An optional secondary aggregate over a numeric field, computed overall and per group.",
						"properties":{
							"fn":{"type":"string","enum":["sum","avg","min","max"],"description":"The aggregate function."},
							"field":{"type":"string","description":"The numeric field to aggregate."}
						},
						"required":["fn","field"],
						"additionalProperties":false
					},
					"scope":{
						"type":"object",
						"description":"An optional narrowing. territory/radius require a class with geometry; circuit narrows by the Phase-37 circuit membership (classes without it contribute nothing).",
						"properties":{
							"kind":{"type":"string","enum":["none","territory","radius","circuit"],"description":"The scope kind."},
							"territory_id":{"type":"string","description":"For kind=territory: the territory whose subtree to count within."},
							"center":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2,"description":"For kind=radius: the [longitude, latitude] center."},
							"radius_m":{"type":"number","description":"For kind=radius: the radius in metres."},
							"circuit_id":{"type":"string","description":"For kind=circuit: the circuit/feeder (gis.circuits uuid) to count within."}
						},
						"required":["kind"],
						"additionalProperties":false
					},
					"limit":{"type":"integer","description":"Max group rows to return when group_by is set (default/cap 1000); the total is always exact."}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolQueryFeaturesAggregate,
		},
		{
			Name: "run_bill_of_quantities",
			Description: "Price a scope of the network into a Bill of Quantities -- the costing " +
				"behind the map's BoQ panel. It counts scoped quantities (discrete feature " +
				"counts priced 'each', linear geometry priced per_km from the selected " +
				"material's cost_per_km) and multiplies by catalog rates, returning a grouped, " +
				"totalled schedule in the chosen currency. Price an explicit set of features " +
				"with feature_refs (a trace result or selection -- takes precedence and defines " +
				"the classes), or scan whole classes with feature_classes, optionally narrowed " +
				"by scope to a territory subtree, a radius around a point, or a bounding box. " +
				"Quantities with no resolvable rate are surfaced as 'unpriced' (never folded to " +
				"zero) so costing gaps stay visible. display_currency is a currency code; omit " +
				"for the base currency. Respects the same read scoping as every other read.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_classes":{"type":"array","items":{"type":"string"},"description":"Class codes to scan and price; ignored when feature_refs is given."},
					"scope":{
						"type":"object",
						"description":"An optional spatial narrowing for a class scan. Requires classes with geometry.",
						"properties":{
							"kind":{"type":"string","enum":["none","territory","radius","bbox"],"description":"The scope kind; none/omitted scans every class in full."},
							"territory_id":{"type":"string","description":"For kind=territory: the territory whose subtree to price within."},
							"center":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2,"description":"For kind=radius: the [longitude, latitude] center."},
							"radius_m":{"type":"number","description":"For kind=radius: the radius in metres."},
							"bbox":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"For kind=bbox: [min_lon, min_lat, max_lon, max_lat]."}
						},
						"required":["kind"],
						"additionalProperties":false
					},
					"feature_refs":{
						"type":"array",
						"description":"An explicit set of features to price (e.g. a trace result). Takes precedence over feature_classes/scope.",
						"items":{
							"type":"object",
							"properties":{
								"class":{"type":"string","description":"The feature class code."},
								"id":{"type":"string","description":"The feature id."}
							},
							"required":["class","id"],
							"additionalProperties":false
						}
					},
					"display_currency":{"type":"string","description":"Currency code for the priced schedule; omit for the base currency."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolRunBillOfQuantities,
		},
		{
			Name: "run_asset_register",
			Description: "Build an itemised asset register -- one row per feature -- for a scope of the " +
				"network (the per-feature sibling of run_bill_of_quantities, behind the map's BoQ " +
				"panel in Asset register mode). Each row carries a flat set of common columns: " +
				"asset id, class, subtype, name, status, material, quantity/unit, longitude/latitude, " +
				"territory, circuit, ownership, external refs and created date. Scope it the same way " +
				"as a BoQ: an explicit feature_refs set (takes precedence and defines the classes), or " +
				"whole feature_classes optionally narrowed by scope (territory subtree / radius / bbox). " +
				"Set include_valuation to also value each asset through the same catalog/override/material " +
				"path the priced schedule uses (unit_rate, value, priced_source=catalog|override|unpriced), " +
				"in display_currency. Respects the same read scoping as every other read; capped at " +
				"100000 rows (truncated=true when hit).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_classes":{"type":"array","items":{"type":"string"},"description":"Class codes to list; ignored when feature_refs is given."},
					"scope":{
						"type":"object",
						"description":"An optional spatial narrowing for a class scan. Requires classes with geometry.",
						"properties":{
							"kind":{"type":"string","enum":["none","territory","radius","bbox"],"description":"The scope kind; none/omitted lists every class in full."},
							"territory_id":{"type":"string","description":"For kind=territory: the territory whose subtree to list within."},
							"center":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2,"description":"For kind=radius: the [longitude, latitude] center."},
							"radius_m":{"type":"number","description":"For kind=radius: the radius in metres."},
							"bbox":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"For kind=bbox: [min_lon, min_lat, max_lon, max_lat]."}
						},
						"required":["kind"],
						"additionalProperties":false
					},
					"feature_refs":{
						"type":"array",
						"description":"An explicit set of features to register (e.g. a trace result or selection). Takes precedence over feature_classes/scope.",
						"items":{
							"type":"object",
							"properties":{
								"class":{"type":"string","description":"The feature class code."},
								"id":{"type":"string","description":"The feature id."}
							},
							"required":["class","id"],
							"additionalProperties":false
						}
					},
					"include_valuation":{"type":"boolean","description":"When true, value each asset through the cost catalog (unit_rate, value, priced_source)."},
					"display_currency":{"type":"string","description":"Currency code for the valuation; omit for the base currency. Only used when include_valuation is true."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolRunAssetRegister,
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
				"(a target is required only then). A downstream trace also returns " +
				"transformer_boundaries -- the transformers it stopped at; set cross_transformers " +
				"true to continue past them into the LV network. Use list_trace_configurations to " +
				"find a valid trace_configuration code first.",
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
					},
					"cross_transformers":{"type":"boolean","description":"Downstream only: override whether the trace continues past transformers into their LV/secondary network. Omit to use the configuration's default; false stops at each transformer, true continues past."}
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
					"boundary_class":{"type":"string","description":"Optional: a single feature class that bounds the section. When omitted, the section is bounded by the network-sectioning backbone (boundary_role): protective + switching devices and the transformer."},
					"protection_zone":{"type":"boolean","description":"When true, return the PROTECTION ZONE (bounded only by fault-interrupting protective devices + the transformer -- what de-energises on a fault) instead of the default SWITCHING SECTION (bounded by any isolating device too -- what you can isolate to work on it)."}
				},
				"required":["trace_configuration","start"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTraceRun,
			Handler:      toolRunSectionTrace,
		},
		{
			Name: "run_service_trace",
			Description: "Find the consumption below a feature: walk downstream (respecting the " +
				"configuration's barriers) to the load and roll the reached SERVICE_POINTs up by " +
				"total demand (kW/kVA), customer count, and restoration priority tier (P1 life-" +
				"support, P2 essential, P3 priority, P4 standard; critical = rank <= P2), with the " +
				"per-service-point breakdown behind it. Answers 'how much load, how many customers, " +
				"how much of it critical, sits below here'. Requires a DOWNSTREAM trace_configuration. " +
				"Read-only. (Every other trace tool also returns this rollup under 'service'.)",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"trace_configuration":{"type":"string","description":"A DOWNSTREAM trace configuration code from list_trace_configurations."},
					"start":{
						"type":"object",
						"description":"The feature to summarise consumption below.",
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
			Handler:      toolRunServiceTrace,
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
			Name: "connectivity_audit",
			Description: "Run a read-only consistency audit of the logical connectivity graph against " +
				"the live features and their geometry. Writes nothing. Reports two kinds of finding: " +
				"'orphaned_connectivity' (a connection pointing at a feature that is deleted or missing), " +
				"and 'coincident_unconnected' (two features whose geometries coincide within a tolerance, " +
				"for a class pair that could legally connect, but which have no connection -- the map " +
				"looks connected while the model is not). Terminals are non-positional, so coincidence is " +
				"feature-to-feature and findings are reviewable signals, not definitive.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"tolerance_m":{"type":"number","description":"Coincidence distance in meters for the geometric check (default 1.0)."},
					"max_findings":{"type":"integer","description":"Cap on findings returned, 1-5000 (default 500); summary counts are not capped."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermConnectivityRead,
			Handler:      toolConnectivityAudit,
		},
		{
			Name: "validation_issues",
			Description: "List the standing validation-issue queue -- the self-pruning backlog the " +
				"validation worker keeps fresh, the same list the Network-audit 'Issue queue' panel " +
				"shows. Covers two families of finding: connectivity (topology -- 'orphaned_connection', " +
				"'orphaned_relationship', 'coincident_unconnected') and attribute/data-quality (kinds " +
				"prefixed 'attr_' -- 'attr_required_missing', 'attr_out_of_domain' and 'attr_out_of_range' " +
				"are drift of an existing row away from the CURRENT metadata, and 'attr_completeness' is a " +
				"field an admin declared should be populated via a data-quality rule). Each issue carries " +
				"its kind, severity, message, the feature(s) it points at, status and first/last-seen " +
				"timestamps. Read-only. Default returns the live queue (open + acknowledged), errors first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["open","acknowledged","resolved","suppressed"],"description":"Filter by status; omit for the live queue (open + acknowledged)."},
					"severity":{"type":"string","enum":["error","warning"],"description":"Filter by severity."},
					"kind":{"type":"string","description":"Filter by exact finding kind, e.g. 'coincident_unconnected' or 'attr_out_of_domain'."},
					"limit":{"type":"integer","description":"Max issues to return, 1-1000 (default 500)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermConnectivityRead,
			Handler:      toolValidationIssues,
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
		{
			Name: "list_connections",
			Description: "List the connections a feature currently has -- the same list the REST " +
				"GET /connectivity route returns. Each entry includes the connection's id (pass " +
				"it to revoke_connection to remove it), the two endpoints (feature class, id, and " +
				"terminal), the authorizing network rule, and when it was established. Use this to " +
				"see what a feature is already wired to before establishing or revoking anything.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code of the feature whose connections to list."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermConnectivityRead,
			Handler:      toolListConnections,
		},
		{
			Name: "list_relationships",
			Description: "List the structural/spatial relationships a feature currently has (on " +
				"either end) -- the same list the REST GET /relationships route returns. Each entry " +
				"includes the relationship's id (pass it to revoke_relationship to remove it), the " +
				"relationship class with its kind and cardinality, the origin and destination " +
				"features, and when it was established. Use this to see a feature's existing " +
				"relationships before establishing or revoking anything.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code of the feature whose relationships to list."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermRelationshipsRead,
			Handler:      toolListRelationships,
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
				"string to clear it), and send only the attributes you want to change. Subtype can " +
				"be changed by sending a new subtype code, but only if the feature still satisfies " +
				"the new subtype's required fields (send those field values in the same call). " +
				"Fields locked after publish are rejected while the feature is " +
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
					"subtype":{"type":"integer","description":"New subtype code; omit to keep the current subtype. Only valid for a class with subtypes, and only accepted if the feature still satisfies the new subtype's required fields."},
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
			Name: "publish_feature",
			Description: "Publish a feature -- transition it from draft to published, the same write " +
				"the REST POST /{code}/{id}/publish route performs (the feature panel's Publish " +
				"action). The feature must satisfy its subtype's required fields; an incomplete " +
				"draft is rejected. Idempotent: publishing an already-published feature returns " +
				"its current state. Returns the feature's full envelope. This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Publish feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status change, reversible via unpublish
				IdempotentHint:  &trueHint,  // publishing an already-published row is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolPublishFeature,
		},
		{
			Name: "unpublish_feature",
			Description: "Unpublish a feature -- transition it from published back to draft, the same " +
				"write the REST POST /{code}/{id}/unpublish route performs (the feature panel's " +
				"Unpublish action). No completeness gate -- a published row can always return to " +
				"draft. Idempotent: unpublishing an already-draft feature returns its current " +
				"state. Returns the feature's full envelope. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Unpublish feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status change, reversible via publish
				IdempotentHint:  &trueHint,  // unpublishing an already-draft row is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolUnpublishFeature,
		},
		{
			Name: "delete_feature",
			Description: "Soft-delete a feature -- mark it deleted (deleted_at set) without removing " +
				"the row, the same write the REST DELETE /{code}/{id} route performs (the feature " +
				"panel's Delete action). This is REVERSIBLE with restore_feature, and orthogonal " +
				"to status (a published feature can be deleted without unpublishing first). " +
				"Idempotent: deleting an already-deleted feature returns its current state. " +
				"Returns the feature's full envelope (with deleted_at set). This WRITES to the " +
				"database: confirm with a human before calling. To permanently remove a row, use " +
				"purge_feature after this.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the feature from active use (reversible, but a removal)
				IdempotentHint:  &trueHint,  // deleting an already-deleted row is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolDeleteFeature,
		},
		{
			Name: "restore_feature",
			Description: "Restore a soft-deleted feature -- clear deleted_at so it's active again, the " +
				"same write the REST POST /{code}/{id}/restore route performs (the feature panel's " +
				"Restore action). Idempotent: restoring a feature that isn't deleted returns its " +
				"current state. Returns the feature's full envelope. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Restore feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it un-deletes, the additive inverse of delete
				IdempotentHint:  &trueHint,  // restoring a non-deleted row is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolRestoreFeature,
		},
		{
			Name: "purge_feature",
			Description: "Permanently remove a feature row -- a hard delete, the same write the REST " +
				"DELETE /{code}/{id}/purge route performs. The feature MUST already be soft-deleted " +
				"(call delete_feature first); the engine rejects a live row. This is DESTRUCTIVE " +
				"and NOT reversible -- there is no restore after a purge, the row is gone. Returns " +
				"a small acknowledgement. This WRITES to the database: confirm with a human before " +
				"calling, and prefer delete_feature (reversible) unless a permanent removal is " +
				"truly intended.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid) -- must already be soft-deleted."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Purge feature",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it physically removes the row, no restore
				IdempotentHint:  &falseHint, // a second purge of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolPurgeFeature,
		},
		{
			Name: "wipe_feature_class",
			Description: "Bulk soft-delete EVERY feature in a class -- the class-scale twin of " +
				"delete_feature, the same write the REST POST /features/{code}/wipe route performs " +
				"(the Features browser's 'Wipe class' danger action). It marks every live feature " +
				"deleted_at = now(); this is REVERSIBLE per feature via restore_feature until " +
				"purge_deleted_feature_class permanently removes them. Respects territory scope: a " +
				"scoped caller wipes only their own territory's rows, an unscoped admin the whole " +
				"class. Returns the number soft-deleted. This is a DESTRUCTIVE bulk WRITE gated on " +
				"the admin-tier features:wipe permission: always confirm with a human first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code to wipe."}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Wipe feature class",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes every feature in the class from active use (reversible, but bulk)
				IdempotentHint:  &trueHint,  // wiping an already-wiped class is a no-op (nothing live left)
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWipe,
			Handler:      toolWipeFeatureClass,
		},
		{
			Name: "purge_deleted_feature_class",
			Description: "Permanently remove EVERY soft-deleted feature in a class -- the class-scale " +
				"twin of purge_feature, the same write the REST POST /features/{code}/purge-deleted " +
				"route performs. Only touches rows already soft-deleted (run wipe_feature_class or " +
				"delete_feature first); live rows are left alone. It also cleans up the class's " +
				"topology/relationship references. This is DESTRUCTIVE and NOT reversible -- there is " +
				"no restore after a purge. Respects territory scope. Returns the number purged. Gated " +
				"on the admin-tier features:wipe permission: always confirm with a human first, and " +
				"prefer wipe_feature_class (reversible) unless permanent removal is truly intended.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code whose soft-deleted rows to purge."}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Purge deleted feature class",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it physically removes rows, no restore
				IdempotentHint:  &falseHint, // a second purge finds nothing, but the first is a real removal
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWipe,
			Handler:      toolPurgeDeletedFeatureClass,
		},
		{
			Name: "split_line",
			Description: "Split a line feature at a chosen point -- shorten the original to the near " +
				"half and create a new feature from the far half, both in one transaction, the " +
				"same write the REST POST /{code}/{id}/split route performs (the map's split tool). " +
				"Only valid for a line class. The cut point is given as lng/lat (EPSG:4326); the " +
				"far half's geometry is computed by PostGIS, so you supply only the new feature's " +
				"subtype/name/attributes (no geometry), which validate exactly like a create. " +
				"Connections and relationships stay on the original. Returns the new (far-half) " +
				"feature's envelope. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The line feature class code."},
					"id":{"type":"string","description":"The line feature's id (uuid)."},
					"lng":{"type":"number","description":"Longitude (EPSG:4326) of the cut point; must land on the line's interior, not an endpoint."},
					"lat":{"type":"number","description":"Latitude (EPSG:4326) of the cut point."},
					"new_feature":{
						"type":"object",
						"description":"How to fill out the new feature the far half becomes -- no geometry (it's computed).",
						"properties":{
							"subtype":{"type":"integer","description":"Subtype code for the new part -- required when the class has subtypes."},
							"name":{"type":"string","description":"Optional label for the new part."},
							"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from the computed geometry."},
							"attributes":{"type":"object","description":"Field values for the new part, keyed by field name.","additionalProperties":true}
						},
						"additionalProperties":false
					}
				},
				"required":["feature_class","id","lng","lat"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Split line",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it shortens + adds, it doesn't delete the original
				IdempotentHint:  &falseHint, // re-running it splits again / creates another feature
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolSplitLine,
		},
		{
			Name: "apply_pending_edit",
			Description: "Apply your staged private edit of an already-published feature onto the " +
				"public row (conflict-checked), the same write the REST POST /{code}/{id}/apply-pending " +
				"route performs (the feature panel's Apply banner, Phase 61 private drafts). Only " +
				"meaningful when you have a pending edit staged on this feature; publishes your " +
				"private change so peers see it. Returns the updated public envelope. This WRITES " +
				"to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid) -- must have your pending edit staged."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Apply pending edit",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it publishes a staged change onto the public row
				IdempotentHint:  &falseHint, // once applied there's no pending edit to re-apply
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolApplyPendingEdit,
		},
		{
			Name: "discard_pending_edit",
			Description: "Throw away your staged private edit of an already-published feature without " +
				"touching the public row, the same write the REST DELETE /{code}/{id}/pending route " +
				"performs (the feature panel's Discard banner, Phase 61 private drafts). Only " +
				"meaningful when you have a pending edit staged. Returns a small acknowledgement. " +
				"This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"id":{"type":"string","description":"The feature's id (uuid) -- must have your pending edit staged."}
				},
				"required":["feature_class","id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Discard pending edit",
				ReadOnlyHint:    &falseHint, // this tool writes (removes your staged edit)
				DestructiveHint: &trueHint,  // it discards your staged change; there's no undo
				IdempotentHint:  &falseHint, // a second discard has nothing to discard
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolDiscardPendingEdit,
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
		{
			Name: "revoke_connection",
			Description: "Permanently remove an established connection by its id -- the same write " +
				"the REST DELETE /connectivity/{id} route performs (the frontend's un-connect " +
				"action). A connection has no soft-delete/restore: this deletes the record " +
				"outright, so it is DESTRUCTIVE and NOT reversible -- to restore the link you must " +
				"establish it again. Use list_connections to find the id first. This WRITES to the " +
				"database: confirm with a human before calling. Revoking an id that doesn't exist " +
				"(e.g. already revoked) returns a not-found error.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The connection's id (uuid), e.g. from list_connections."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Revoke connection",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it deletes the connection outright, no restore
				IdempotentHint:  &falseHint, // a second revoke of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermConnectivityCommit,
			Handler:      toolRevokeConnection,
		},
		{
			Name: "revoke_relationship",
			Description: "Permanently remove an established relationship by its id -- the same write " +
				"the REST DELETE /relationships/{id} route performs (the frontend's un-relate " +
				"action). A relationship has no soft-delete/restore: this deletes the record " +
				"outright, so it is DESTRUCTIVE and NOT reversible -- to restore it you must " +
				"establish it again. Use list_relationships to find the id first. This WRITES to " +
				"the database: confirm with a human before calling. Revoking an id that doesn't " +
				"exist (e.g. already revoked) returns a not-found error.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The relationship's id (uuid), e.g. from list_relationships."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Revoke relationship",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it deletes the relationship outright, no restore
				IdempotentHint:  &falseHint, // a second revoke of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermRelationshipsCommit,
			Handler:      toolRevokeRelationship,
		},
		{
			Name: "mark_ownership_downstream_private",
			Description: "Declare a feature (typically a distribution transformer) privately owned " +
				"and stamp ownership=private onto it plus every feature electrically downstream of " +
				"it -- the same bulk write the REST POST /ownership/mark-private route performs (the " +
				"feature panel's 'Mark this & downstream private' action, the asset-ownership axis). " +
				"Ownership propagates as a connectivity property: the walk is downstream, does NOT " +
				"stop at open switches, and crosses the transformer into the LV, so the whole of the " +
				"customer's own network below the boundary is marked. Returns how many features were " +
				"reached and stamped, by class. This WRITES to the database: confirm with a human " +
				"before calling. Re-running it is idempotent (already-private features are no-ops).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The boundary feature's class code (e.g. DIST_XFMR)."},
					"feature_id":{"type":"string","description":"The boundary feature's id (uuid) -- ownership propagates downstream from here."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Mark ownership downstream private",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it stamps an attribute; reversible by re-syncing / re-declaring
				IdempotentHint:  &trueHint,  // re-stamping an already-private set is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolMarkOwnershipDownstreamPrivate,
		},
		{
			Name: "resync_ownership",
			Description: "Recompute the whole asset-ownership map from scratch -- the same write the " +
				"REST POST /ownership/resync route performs (the 'Re-sync all' action). It reads " +
				"every transformer currently marked private, clears ownership everywhere, then " +
				"re-stamps each private transformer and everything downstream of it. Use it to fix " +
				"drift after a re-wire, so ownership always equals 'what traces from a private " +
				"transformer right now'. Returns the number of boundaries, cleared and re-stamped " +
				"features. This WRITES to the database (potentially many rows): confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{},
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Re-sync ownership",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it recomputes a derived attribute, not topology
				IdempotentHint:  &trueHint,  // running it twice with no change yields the same map
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolResyncOwnership,
		},
	}
}
