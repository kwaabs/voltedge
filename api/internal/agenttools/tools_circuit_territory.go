package agenttools

// Circuit and territory tools -- thin adapters over the already-exported
// api/internal/circuit and api/internal/territory engines, each declaring the
// RBAC permission its REST route requires. The circuit tools mirror the
// circuits catalog (read = features:read, write = circuits:write; the
// membership stamp additionally needs features:write). The territory tools
// mirror the admin territories surface (read = features:read, write =
// territories:write). Reads leave Annotations nil so buildRegistry stamps them
// readOnlyHint:true; writes carry their own annotations so a client confirms
// with a human first.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/circuit"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// --- circuit tools ---

// circuitTools returns the circuit catalog tools in advertised order: the two
// reads (features:read), then the create/update/delete writes (circuits:write),
// then the membership assign (circuits:write + features:write, matching its
// two-permission REST route).
func circuitTools() []Tool {
	return []Tool{
		{
			Name: "circuit_list",
			Description: "List the circuit catalog -- the named MV/LV circuits features can belong " +
				"to, the same list the REST GET /circuits route returns (the circuit_id picker). " +
				"Each entry includes the circuit's id, name, network, nominal voltage, and source " +
				"feature reference. Pass an optional network filter to narrow to just MV or LV.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"network":{"type":"string","enum":["MV","LV"],"description":"Optional network filter; omit for both."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolCircuitList,
		},
		{
			Name: "circuit_get",
			Description: "Fetch a single circuit by its id -- the same read the REST GET /circuits/{id} " +
				"route returns. Returns the circuit's full record (name, network, nominal voltage, " +
				"and source feature reference). Use circuit_list to discover ids.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The circuit's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolCircuitGet,
		},
		{
			Name: "circuit_create",
			Description: "Create a new circuit in the catalog -- the same validated write the REST " +
				"POST /circuits route performs. The name must be unique within its network (MV or " +
				"LV); voltage and the source feature reference are optional (a source needs both a " +
				"feature class and a feature id, or neither). Returns the created circuit's record " +
				"with its new id. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"name":{"type":"string","description":"The circuit's name, unique within its network."},
					"network":{"type":"string","enum":["MV","LV"],"description":"The network namespace this circuit belongs to."},
					"voltage_kv":{"type":"number","description":"Optional nominal voltage in kV; must be positive when given."},
					"source_feature_class":{"type":"string","description":"Optional source feature's class code; required together with source_feature_id, or omit both."},
					"source_feature_id":{"type":"string","description":"Optional source feature's id (uuid); required together with source_feature_class, or omit both."}
				},
				"required":["name","network"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create circuit",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it creates another circuit
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermCircuitsWrite,
			Handler:      toolCircuitCreate,
		},
		{
			Name: "circuit_update",
			Description: "Update an existing circuit -- the same validated partial write the REST " +
				"PATCH /circuits/{id} route performs. Every field is optional and only what you send " +
				"changes; a rename is re-checked for uniqueness within the (immutable) network. Set " +
				"clear_voltage to remove the voltage, or clear_source to remove the source reference. " +
				"Returns the updated circuit's record. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The circuit's id (uuid)."},
					"name":{"type":"string","description":"New name; omit to keep the current name. Must stay unique within the network."},
					"voltage_kv":{"type":"number","description":"New nominal voltage in kV (positive); omit to leave it unchanged. Ignored when clear_voltage is set."},
					"clear_voltage":{"type":"boolean","description":"Remove the nominal voltage."},
					"source_feature_class":{"type":"string","description":"New source feature's class code; set together with source_feature_id. Ignored when clear_source is set."},
					"source_feature_id":{"type":"string","description":"New source feature's id (uuid); set together with source_feature_class. Ignored when clear_source is set."},
					"clear_source":{"type":"boolean","description":"Remove the source feature reference."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update circuit",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field update overwrites, it doesn't delete the circuit
				IdempotentHint:  &trueHint,  // re-applying the same change yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermCircuitsWrite,
			Handler:      toolCircuitUpdate,
		},
		{
			Name: "circuit_delete",
			Description: "Delete a circuit from the catalog -- the same write the REST DELETE " +
				"/circuits/{id} route performs. This removes the catalog record outright (there is " +
				"no soft-delete/restore for a circuit); features that referenced it keep their " +
				"circuit_id column and are not cleaned up here. Returns a small acknowledgement. This " +
				"WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The circuit's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete circuit",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the catalog record outright, no restore
				IdempotentHint:  &falseHint, // a second delete of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermCircuitsWrite,
			Handler:      toolCircuitDelete,
		},
		{
			Name: "circuit_assign",
			Description: "Recompute a circuit's membership -- trace downstream from the circuit's " +
				"source feature and bulk-stamp circuit_id (and circuit_ref where that column exists) " +
				"onto every reached feature, the same write the REST POST /circuits/{id}/assign route " +
				"performs. This is a full sync: it first clears the circuit off every in-scope class, " +
				"then stamps the freshly-traced set, so features that no longer trace from the source " +
				"(and any manual overrides on them) are dropped. The circuit must have a source " +
				"feature set. Returns a per-class summary of features reached, stamped, and cleared. " +
				"This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The circuit's id (uuid) -- must have a source feature set."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Assign circuit membership",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it re-syncs membership stamps, it doesn't delete features
				IdempotentHint:  &falseHint, // membership tracks live topology, so a re-run can change it
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermCircuitsWrite,
			ExtraPerms:   []string{auth.PermFeaturesWrite}, // the stamp is a bulk feature write
			Handler:      toolCircuitAssign,
		},
		{
			Name: "circuit_assign_preview",
			Description: "DRY-RUN a circuit assign -- trace from the source and report the membership " +
				"churn an assign WOULD apply, WITHOUT writing anything (the same GET /circuits/{id}/" +
				"assign/preview the admin page's Preview shows). Returns, per class and in total, how " +
				"many features would join (will_add), how many stale members would be dropped " +
				"(will_remove), and how many stay (unchanged), plus any reached features whose class " +
				"can't be stamped. Use this before circuit_assign to see the impact of a full sync. " +
				"Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The circuit's id (uuid) -- must have a source feature set."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolCircuitAssignPreview,
		},
		{
			Name: "circuit_assign_stale",
			Description: "Re-sync EVERY stale circuit in one call -- run assign on each circuit whose " +
				"stamped membership has drifted since it was last assigned (a member was edited), the " +
				"same write the REST POST /circuits/assign-stale route performs. Up-to-date and " +
				"never-assigned circuits are left untouched. Each circuit's assign is its own " +
				"transaction, so one failure (e.g. a circuit whose source was removed) is reported in " +
				"`failures` and the rest still run. Returns the aggregate stamped/cleared counts and " +
				"the per-circuit results. This WRITES to the database (potentially many rows across " +
				"many circuits): confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{},
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Assign all stale circuits",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it re-syncs membership stamps, it doesn't delete features
				IdempotentHint:  &falseHint, // membership tracks live topology, so a re-run can change it
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermCircuitsWrite,
			ExtraPerms:   []string{auth.PermFeaturesWrite}, // the stamps are bulk feature writes
			Handler:      toolCircuitAssignStale,
		},
	}
}

func toolCircuitList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Network string `json:"network"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	circuits, err := circuit.ListCircuits(ctx, db, in.Network)
	if err != nil {
		return nil, err
	}
	return map[string]any{"circuits": circuits, "count": len(circuits)}, nil
}

func toolCircuitGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return circuit.GetCircuit(ctx, db, in.ID)
}

func toolCircuitCreate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Name               string   `json:"name"`
		Network            string   `json:"network"`
		VoltageKV          *float64 `json:"voltage_kv"`
		SourceFeatureClass *string  `json:"source_feature_class"`
		SourceFeatureID    *string  `json:"source_feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if in.Network == "" {
		return nil, fmt.Errorf("network is required")
	}
	// The engine re-validates (name uniqueness, network, positive voltage,
	// all-or-nothing source); those errors surface in-band as the tool error.
	return circuit.CreateCircuit(ctx, db, circuit.CreateParams{
		Name:               in.Name,
		Network:            in.Network,
		VoltageKV:          in.VoltageKV,
		SourceFeatureClass: in.SourceFeatureClass,
		SourceFeatureID:    in.SourceFeatureID,
	})
}

func toolCircuitUpdate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID                 string   `json:"id"`
		Name               *string  `json:"name"`
		VoltageKV          *float64 `json:"voltage_kv"`
		ClearVoltage       bool     `json:"clear_voltage"`
		SourceFeatureClass *string  `json:"source_feature_class"`
		SourceFeatureID    *string  `json:"source_feature_id"`
		ClearSource        bool     `json:"clear_source"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return circuit.UpdateCircuit(ctx, db, in.ID, circuit.UpdateParams{
		Name:               in.Name,
		VoltageKV:          in.VoltageKV,
		ClearVoltage:       in.ClearVoltage,
		SourceFeatureClass: in.SourceFeatureClass,
		SourceFeatureID:    in.SourceFeatureID,
		ClearSource:        in.ClearSource,
	})
}

func toolCircuitDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	// The delete itself yields no row, so acknowledge with the id.
	if err := circuit.DeleteCircuit(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}

func toolCircuitAssign(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// AssignCircuit owns the clear+stamp transaction; the acting user (stamped as
	// updated_by on the touched rows) is on ctx via auth.WithUser in Execute.
	return circuit.AssignCircuit(ctx, sdb, in.ID)
}

func toolCircuitAssignPreview(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return circuit.PreviewAssignCircuit(ctx, sdb, in.ID)
}

func toolCircuitAssignStale(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// Each circuit's assign is its own transaction (see AssignAllStale); the acting
	// user is on ctx via auth.WithUser in Execute.
	return circuit.AssignAllStale(ctx, sdb)
}

// --- territory tools ---

// territoryTools returns the territory tools in advertised order: the reads
// (features:read) -- the tree, one node, the level catalog, one level, the
// summaries, and the coverage report -- then the writes (territories:write):
// create/update/delete a node, flip enforcement, and the backfill sweep.
func territoryTools() []Tool {
	return []Tool{
		{
			Name: "territory_list",
			Description: "List the whole territory tree with each node's boundary as GeoJSON -- the " +
				"same read the REST GET /territories route returns. Each entry includes the " +
				"territory's id, parent_id, level, name, and boundary; a client assembles the " +
				"hierarchy from parent_id. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryList,
		},
		{
			Name: "territory_get",
			Description: "Fetch a single territory by its id, boundary included -- the same read the " +
				"REST GET /territories/{id} route returns. Use territory_list to discover ids.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The territory's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryGet,
		},
		{
			Name: "territory_list_levels",
			Description: "List the territory level catalog (e.g. region, district) ordered top-first " +
				"by rank -- the same read the REST GET /territory-levels route returns. Each entry " +
				"includes the level's id, code, name, and rank. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryListLevels,
		},
		{
			Name: "territory_get_level",
			Description: "Fetch a single territory level by its id -- the same read the REST GET " +
				"/territory-levels/{id} route returns. Use territory_list_levels to discover ids.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The territory level's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryGetLevel,
		},
		{
			Name: "territory_list_summaries",
			Description: "List a lightweight summary of every territory (without boundary geometry) -- " +
				"the same read the REST territory summaries route returns, used where the full tree's " +
				"boundaries aren't needed. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryListSummaries,
		},
		{
			Name: "territory_coverage",
			Description: "Report, per geometric feature class, how many live features exist and how " +
				"many still carry no territory_id -- the data behind the admin unassigned-features " +
				"review, the same read the REST coverage route returns. Read-only: it runs no writes. " +
				"Use this before flipping enforcement to see what territory_backfill still leaves " +
				"unassigned. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolTerritoryCoverage,
		},
		{
			Name: "territory_create",
			Description: "Create a new territory node -- the same validated write the REST POST " +
				"/territories route performs. The node's level must sit exactly one rank below its " +
				"parent's (or be the top level at the root, with no parent); an optional boundary " +
				"(GeoJSON Polygon or MultiPolygon) must fall within the parent's boundary when both " +
				"exist. Returns the created territory's record with its new id. This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"level_id":{"type":"string","description":"The territory level's id (uuid) -- must be exactly one rank below the parent's, or the top level for a root."},
					"name":{"type":"string","description":"The territory's name."},
					"parent_id":{"type":"string","description":"Optional parent territory's id (uuid); omit to create a root (top-level) territory."},
					"geom_geojson":{"type":"string","description":"Optional boundary as a GeoJSON Polygon or MultiPolygon string; must fall within the parent's boundary when both exist."}
				},
				"required":["level_id","name"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create territory",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it creates another territory
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTerritoriesWrite,
			Handler:      toolTerritoryCreate,
		},
		{
			Name: "territory_update",
			Description: "Update an existing territory node -- the same validated partial write the " +
				"REST PATCH /territories/{id} route performs. Every field is optional: rename it, " +
				"replace its boundary (geom_geojson) or set clear_geom to remove it, or set reparent " +
				"with a parent_id to move it (parent_id null makes it a root). Any structural change " +
				"is re-checked for rank and containment, and a re-parent is rejected if it would form " +
				"a cycle. Returns the updated territory's record. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The territory's id (uuid)."},
					"name":{"type":"string","description":"New name; omit to keep the current name."},
					"geom_geojson":{"type":"string","description":"Replacement boundary as a GeoJSON Polygon or MultiPolygon string; omit to leave it. Ignored when clear_geom is set."},
					"clear_geom":{"type":"boolean","description":"Remove the boundary (wins over geom_geojson if both are set)."},
					"reparent":{"type":"boolean","description":"Move the node under parent_id; must be set for parent_id to take effect."},
					"parent_id":{"type":"string","description":"New parent's id (uuid) when reparent is set; send null (omit) with reparent to make the node a root."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update territory",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field/structural update overwrites, it doesn't delete the node
				IdempotentHint:  &trueHint,  // re-applying the same change yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTerritoriesWrite,
			Handler:      toolTerritoryUpdate,
		},
		{
			Name: "territory_delete",
			Description: "Delete a leaf territory -- the same write the REST DELETE /territories/{id} " +
				"route performs. A territory with child territories is refused (delete bottom-up); " +
				"features that reference it via territory_id are not checked here. Returns a small " +
				"acknowledgement. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The territory's id (uuid) -- must have no child territories."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete territory",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the node outright, no restore
				IdempotentHint:  &falseHint, // a second delete of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTerritoriesWrite,
			Handler:      toolTerritoryDelete,
		},
		{
			Name: "territory_set_enforcement",
			Description: "Turn territory-based spatial access enforcement on or off for the whole " +
				"install -- the same write the REST territory-enforcement route performs, the final " +
				"Phase 58 rollout step. While on, a scoped user cannot see a geometric feature with a " +
				"NULL territory_id, so run territory_backfill and review territory_coverage first. " +
				"Idempotent: setting it to its current value is a no-op. Returns a small " +
				"acknowledgement. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"enabled":{"type":"boolean","description":"True to turn enforcement on, false to turn it off."}
				},
				"required":["enabled"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set territory enforcement",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it flips a setting, reversible by setting it back
				IdempotentHint:  &trueHint,  // setting it to its current value is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTerritoriesWrite,
			Handler:      toolTerritorySetEnforcement,
		},
		{
			Name: "territory_backfill",
			Description: "Spatially assign a territory to every currently-unassigned live geometric " +
				"feature it can, across all classes in one transaction -- the same write the REST " +
				"territory backfill route performs, the rollout step before enforcement. Each feature " +
				"is placed in the deepest containing territory (at least the minimum feature level); " +
				"features no qualifying territory covers are left unassigned and counted in the " +
				"result. Safe to re-run: it only touches rows still unassigned. Returns a per-class " +
				"summary of assigned and remaining counts. This WRITES to the database: confirm with " +
				"a human before calling.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Annotations: &Annotations{
				Title:           "Backfill territory assignments",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it fills in NULL assignments, it doesn't delete data
				IdempotentHint:  &falseHint, // a re-run after drawing a boundary can assign more
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTerritoriesWrite,
			Handler:      toolTerritoryBackfill,
		},
	}
}

func toolTerritoryList(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	territories, err := territory.ListTerritories(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"territories": territories, "count": len(territories)}, nil
}

func toolTerritoryGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return territory.GetTerritory(ctx, db, in.ID)
}

func toolTerritoryListLevels(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	levels, err := territory.ListLevels(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"levels": levels, "count": len(levels)}, nil
}

func toolTerritoryGetLevel(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return territory.GetLevel(ctx, db, in.ID)
}

func toolTerritoryListSummaries(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	summaries, err := territory.ListSummaries(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"summaries": summaries, "count": len(summaries)}, nil
}

func toolTerritoryCoverage(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	rows, err := territory.Coverage(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"coverage": rows, "count": len(rows)}, nil
}

func toolTerritoryCreate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		LevelID     string  `json:"level_id"`
		Name        string  `json:"name"`
		ParentID    *string `json:"parent_id"`
		GeomGeoJSON *string `json:"geom_geojson"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.LevelID == "" {
		return nil, fmt.Errorf("level_id is required")
	}
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	// The engine re-validates (rank one-below-parent, containment); those errors
	// surface in-band as the tool error.
	return territory.CreateTerritory(ctx, db, territory.CreateParams{
		ParentID:    in.ParentID,
		LevelID:     in.LevelID,
		Name:        in.Name,
		GeomGeoJSON: in.GeomGeoJSON,
	})
}

func toolTerritoryUpdate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID          string  `json:"id"`
		Name        *string `json:"name"`
		GeomGeoJSON *string `json:"geom_geojson"`
		ClearGeom   bool    `json:"clear_geom"`
		ReParent    bool    `json:"reparent"`
		ParentID    *string `json:"parent_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return territory.UpdateTerritory(ctx, db, in.ID, territory.UpdateParams{
		Name:        in.Name,
		GeomGeoJSON: in.GeomGeoJSON,
		ClearGeom:   in.ClearGeom,
		ReParent:    in.ReParent,
		ParentID:    in.ParentID,
	})
}

func toolTerritoryDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := territory.DeleteTerritory(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}

func toolTerritorySetEnforcement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := territory.SetEnforcement(ctx, db, in.Enabled); err != nil {
		return nil, err
	}
	return map[string]any{"enforcement_enabled": in.Enabled}, nil
}

func toolTerritoryBackfill(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// Backfill owns its own transaction; the sweep result is returned verbatim.
	return territory.Backfill(ctx, sdb)
}
