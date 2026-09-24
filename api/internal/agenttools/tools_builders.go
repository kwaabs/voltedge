package agenttools

// The composite "place a whole assembly" builder tools -- each a thin adapter
// over an already-exported fan-out engine (rmu/packagedsub/assembly/tie/coupler/
// duct/panel), the same call its REST POST route makes. Every builder creates a
// whole pre-wired assembly (the anchor plus its fanned-out components, contained
// and connected) in one transaction, so each is a write tool carrying its own
// annotations for human confirmation.
//
// MULTI-PERM GATING: a builder's POST / route mounts under features:write and
// layers the commit perms its fan-out exercises (relationships:commit for the
// containment/support relationships, connectivity:commit for the wiring). The
// engines do NOT re-check RBAC, so each write tool declares RequiredPerm plus
// ExtraPerms covering EVERY perm the REST route requires -- an under-privileged
// caller must not slip past through the assistant door. The paired options
// listers are plain features:read reads (Annotations nil -> auto readOnly).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/assembly"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/coupler"
	"github.com/kwaabs/voltedge/api/internal/duct"
	"github.com/kwaabs/voltedge/api/internal/packagedsub"
	"github.com/kwaabs/voltedge/api/internal/panel"
	"github.com/kwaabs/voltedge/api/internal/rmu"
	"github.com/kwaabs/voltedge/api/internal/tie"
	"github.com/uptrace/bun"
)

// builderTools returns the seven composite-builder tools plus their options
// listers. The create tools all set readOnly/destructive/idempotent/openWorld
// false: a build writes, is additive (not a delete), re-running it creates
// another assembly, and it touches only this system's database.
func builderTools() []Tool {
	return []Tool{
		{
			Name: "place_rmu",
			Description: "Place a ring main unit (RMU) as a whole pre-wired assembly -- the same " +
				"write the REST POST /ring-main-units route performs (the map's Assemblies menu). " +
				"From the RMU's config attribute it fans out the internal bus and every switching " +
				"way, contains them in the RMU, and wires each way to the bus, all in one " +
				"transaction. Returns the placed RMU, its bus, and every created way. This WRITES " +
				"to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"rmu_feature_class":{"type":"string","description":"The RMU (container) feature class code to create."},
					"subtype":{"type":"integer","description":"Subtype code for the RMU -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the RMU."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object where the RMU is placed -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"RMU field values keyed by field name -- MUST include the config field that selects the way template (see list_rmu_options).","additionalProperties":true}
				},
				"required":["rmu_feature_class","geometry"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Place RMU",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermRelationshipsCommit, auth.PermConnectivityCommit},
			Handler:      toolPlaceRMU,
		},
		{
			Name: "list_rmu_options",
			Description: "List the RMU feature classes and the config values each supports -- the " +
				"same catalog the REST GET /ring-main-units/options route returns, backing the " +
				"map's Assemblies menu. Use it to pick a valid rmu_feature_class and config " +
				"attribute for place_rmu. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListRMUOptions,
		},
		{
			Name: "place_packaged_substation",
			Description: "Place a packaged (unit) substation as a whole pre-wired assembly -- the " +
				"same write the REST POST /packaged-substations route performs (the map's " +
				"Assemblies menu). From the config attribute it fans out the internal components " +
				"in series, contains them in the enclosure, and wires the chain, all in one " +
				"transaction. Returns the placed enclosure and every created component. This " +
				"WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"packaged_feature_class":{"type":"string","description":"The packaged substation (enclosure) feature class code to create."},
					"subtype":{"type":"integer","description":"Subtype code for the enclosure -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the packaged substation."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object where the unit is placed -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"Enclosure field values keyed by field name -- MUST include the config field that selects the fan-out template (see list_packaged_substation_options).","additionalProperties":true}
				},
				"required":["packaged_feature_class","geometry"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Place packaged substation",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermRelationshipsCommit, auth.PermConnectivityCommit},
			Handler:      toolPlacePackagedSub,
		},
		{
			Name: "list_packaged_substation_options",
			Description: "List the packaged substation feature classes and the config values each " +
				"supports -- the same catalog the REST GET /packaged-substations/options route " +
				"returns, backing the map's Assemblies menu. Use it to pick a valid " +
				"packaged_feature_class and config attribute for place_packaged_substation. Takes " +
				"no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListPackagedSubOptions,
		},
		{
			Name: "create_assembly",
			Description: "Create a guided kit assembly around an anchor feature -- the same write the " +
				"REST POST /assemblies route performs (the map's Assemblies menu). The named config " +
				"selects a kit template whose items fan out from the anchor, each wired to its " +
				"structural parent (a support/containment relationship) and its electrical target, " +
				"all in one transaction. Returns the placed anchor and every created component. " +
				"This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"anchor_feature_class":{"type":"string","description":"The anchor feature class code the kit builds around."},
					"config":{"type":"string","description":"The kit template code for this anchor (see list_assembly_options)."},
					"subtype":{"type":"integer","description":"Subtype code for the anchor -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the anchor."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object where the assembly is placed -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"Anchor field values keyed by field name.","additionalProperties":true}
				},
				"required":["anchor_feature_class","config","geometry"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create assembly",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermRelationshipsCommit, auth.PermConnectivityCommit},
			Handler:      toolCreateAssembly,
		},
		{
			Name: "list_assembly_options",
			Description: "List the anchor feature classes and the kit config templates each offers -- " +
				"the same catalog the REST GET /assemblies/options route returns, backing the map's " +
				"Assemblies menu. Use it to pick a valid anchor_feature_class and config for " +
				"create_assembly. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListAssemblyOptions,
		},
		{
			Name: "create_tie",
			Description: "Create a feeder-to-feeder tie: place one switch and wire it between two " +
				"existing feeders in a single transaction -- the same write the REST POST /ties " +
				"route performs (the map's 'Feeder tie (NOP)' assembly). Feeder A -> switch.T1 and " +
				"switch.T2 -> feeder B; the switch defaults to normally-open/open unless you set " +
				"those attributes. Returns the placed switch and the two connection ids. This " +
				"WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"switch_feature_class":{"type":"string","description":"The switch feature class code to create; defaults to MV_SWITCH when omitted."},
					"subtype":{"type":"integer","description":"Subtype code for the switch -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the switch."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object where the tie switch is placed -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"Switch field values keyed by field name (normal_state/device_state default to NORMALLY_OPEN/OPEN if unset).","additionalProperties":true},
					"feeder_a":{
						"type":"object",
						"description":"One feeder the tie joins.",
						"properties":{
							"feature_class":{"type":"string","description":"The feeder's feature class code."},
							"feature_id":{"type":"string","description":"The feeder's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"feeder_b":{
						"type":"object",
						"description":"The other feeder the tie joins (must differ from feeder_a).",
						"properties":{
							"feature_class":{"type":"string","description":"The feeder's feature class code."},
							"feature_id":{"type":"string","description":"The feeder's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					}
				},
				"required":["geometry","feeder_a","feeder_b"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create feeder tie",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermConnectivityCommit},
			Handler:      toolCreateTie,
		},
		{
			Name: "list_tie_options",
			Description: "List the switch feature classes usable as a tie and the feeder classes each " +
				"can join -- the same catalog the REST GET /ties/options route returns, backing the " +
				"map's feeder-tie picker. Use it to pick a valid switch_feature_class and feeder " +
				"classes for create_tie. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListTieOptions,
		},
		{
			Name: "create_coupler",
			Description: "Create a bus coupler: place one breaker and wire it between two existing " +
				"buses in a single transaction -- the same write the REST POST /couplers route " +
				"performs (the map's Assemblies menu). Bus A.T2 -> breaker.T1 and breaker.T2 -> " +
				"Bus B.T1; switch_function defaults to BUS_COUPLER unless you set it. Returns the " +
				"placed breaker and the two connection ids. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"breaker_feature_class":{"type":"string","description":"The breaker (switch) feature class code to create."},
					"subtype":{"type":"integer","description":"Subtype code for the breaker -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the breaker."},
					"geometry":{"type":"object","description":"The GeoJSON geometry object where the coupler is placed -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"Breaker field values keyed by field name (switch_function defaults to BUS_COUPLER if unset).","additionalProperties":true},
					"bus_a":{
						"type":"object",
						"description":"One bus the coupler bridges.",
						"properties":{
							"feature_class":{"type":"string","description":"The bus's feature class code."},
							"feature_id":{"type":"string","description":"The bus's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"bus_b":{
						"type":"object",
						"description":"The other bus the coupler bridges (must differ from bus_a).",
						"properties":{
							"feature_class":{"type":"string","description":"The bus's feature class code."},
							"feature_id":{"type":"string","description":"The bus's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					}
				},
				"required":["breaker_feature_class","geometry","bus_a","bus_b"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create bus coupler",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermConnectivityCommit},
			Handler:      toolCreateCoupler,
		},
		{
			Name: "list_coupler_options",
			Description: "List the breaker feature classes usable as a bus coupler and the bus classes " +
				"each can bridge -- the same catalog the REST GET /couplers/options route returns, " +
				"backing the map's Assemblies menu. Use it to pick a valid breaker_feature_class " +
				"and bus classes for create_coupler. Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListCouplerOptions,
		},
		{
			Name: "create_duct",
			Description: "Create a cable duct as a whole assembly: place the duct line and fan out the " +
				"N identical way (bore) children its way_count calls for, contained in the duct, " +
				"all in one transaction -- the same write the REST POST /ducts route performs (the " +
				"map's Assemblies menu). Returns the placed duct and every created way. This WRITES " +
				"to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"duct_feature_class":{"type":"string","description":"The cable duct (line) feature class code to create."},
					"subtype":{"type":"integer","description":"Subtype code for the duct -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the duct."},
					"geometry":{"type":"object","description":"The GeoJSON line geometry object where the duct runs -- required."},
					"territory_id":{"type":"string","description":"Optional territory override (uuid); omit to auto-assign from geometry."},
					"attributes":{"type":"object","description":"Duct field values keyed by field name -- MUST include way_count, the number of bores to fan out.","additionalProperties":true}
				},
				"required":["duct_feature_class","geometry"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create cable duct",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermRelationshipsCommit},
			Handler:      toolCreateDuct,
		},
		{
			Name: "list_duct_options",
			Description: "List the cable duct feature classes and the way class each contains -- the " +
				"same catalog the REST GET /ducts/options route returns, backing the map's " +
				"Assemblies menu. Use it to pick a valid duct_feature_class for create_duct. Takes " +
				"no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListDuctOptions,
		},
		{
			Name: "create_panel",
			Description: "Create a switchgear panel on an existing board as a whole assembly: place the " +
				"panel and fan out the standard device set its role calls for, wired into the " +
				"board, all in one transaction -- the same write the REST POST /panels route " +
				"performs (the map's Assemblies menu). The board is an existing feature; the panel " +
				"and its devices are created here. Returns the placed panel and every created " +
				"device. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"board_feature_class":{"type":"string","description":"The existing board/switchboard feature class code the panel attaches to."},
					"board_feature_id":{"type":"string","description":"The existing board's id (uuid)."},
					"panel_feature_class":{"type":"string","description":"The panel feature class code to create."},
					"subtype":{"type":"integer","description":"Subtype code for the panel -- required when the class has subtypes."},
					"name":{"type":"string","description":"Optional label for the panel."},
					"attributes":{"type":"object","description":"Panel field values keyed by field name -- MUST include the role field that selects the device template (see list_panel_options).","additionalProperties":true}
				},
				"required":["board_feature_class","board_feature_id","panel_feature_class"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create switchgear panel",
				ReadOnlyHint:    &falseHint,
				DestructiveHint: &falseHint,
				IdempotentHint:  &falseHint,
				OpenWorldHint:   &falseHint,
			},
			RequiredPerm: auth.PermFeaturesWrite,
			ExtraPerms:   []string{auth.PermRelationshipsCommit},
			Handler:      toolCreatePanel,
		},
		{
			Name: "list_panel_options",
			Description: "List the panel feature classes and the roles/device templates available for a " +
				"given board container class and diagram type -- the same catalog the REST GET " +
				"/panels/options route returns, backing the map's Assemblies menu. Use it to pick a " +
				"valid panel_feature_class and role attribute for create_panel. Both filters are " +
				"required by the underlying query.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"container_feature_class":{"type":"string","description":"The board/container feature class code to list panels for."},
					"diagram_type":{"type":"string","description":"The diagram type to list panels for."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolListPanelOptions,
		},
	}
}

// --- create handlers (each owns its transaction via asDB) ---

func toolPlaceRMU(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		RMUFeatureClass string          `json:"rmu_feature_class"`
		Subtype         *int            `json:"subtype"`
		Name            *string         `json:"name"`
		Geometry        json.RawMessage `json:"geometry"`
		TerritoryID     *string         `json:"territory_id"`
		Attributes      map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.RMUFeatureClass == "" {
		return nil, fmt.Errorf("rmu_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// The acting user is on ctx via auth.WithUser in Execute; CreateRMU owns the
	// transaction and re-validates the config attribute and required fields.
	return rmu.CreateRMU(ctx, sdb, rmu.CreateRMUInput{
		RMUFeatureClass: in.RMUFeatureClass,
		Subtype:         in.Subtype,
		Name:            in.Name,
		Geometry:        in.Geometry,
		TerritoryID:     in.TerritoryID,
		Attributes:      in.Attributes,
	})
}

func toolListRMUOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := rmu.ListRMUOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolPlacePackagedSub(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		PackagedFeatureClass string          `json:"packaged_feature_class"`
		Subtype              *int            `json:"subtype"`
		Name                 *string         `json:"name"`
		Geometry             json.RawMessage `json:"geometry"`
		TerritoryID          *string         `json:"territory_id"`
		Attributes           map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.PackagedFeatureClass == "" {
		return nil, fmt.Errorf("packaged_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return packagedsub.CreatePackagedSubstation(ctx, sdb, packagedsub.CreatePackagedSubInput{
		PackagedFeatureClass: in.PackagedFeatureClass,
		Subtype:              in.Subtype,
		Name:                 in.Name,
		Geometry:             in.Geometry,
		TerritoryID:          in.TerritoryID,
		Attributes:           in.Attributes,
	})
}

func toolListPackagedSubOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := packagedsub.ListPackagedSubOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolCreateAssembly(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		AnchorFeatureClass string          `json:"anchor_feature_class"`
		Config             string          `json:"config"`
		Subtype            *int            `json:"subtype"`
		Name               *string         `json:"name"`
		Geometry           json.RawMessage `json:"geometry"`
		TerritoryID        *string         `json:"territory_id"`
		Attributes         map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.AnchorFeatureClass == "" {
		return nil, fmt.Errorf("anchor_feature_class is required")
	}
	if in.Config == "" {
		return nil, fmt.Errorf("config is required")
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assembly.CreateAssembly(ctx, sdb, assembly.CreateAssemblyInput{
		AnchorFeatureClass: in.AnchorFeatureClass,
		Config:             in.Config,
		Subtype:            in.Subtype,
		Name:               in.Name,
		Geometry:           in.Geometry,
		TerritoryID:        in.TerritoryID,
		Attributes:         in.Attributes,
	})
}

func toolListAssemblyOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := assembly.ListAssemblyOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolCreateTie(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		SwitchFeatureClass string          `json:"switch_feature_class"`
		Subtype            *int            `json:"subtype"`
		Name               *string         `json:"name"`
		Geometry           json.RawMessage `json:"geometry"`
		TerritoryID        *string         `json:"territory_id"`
		Attributes         map[string]any  `json:"attributes"`
		FeederA            struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"feeder_a"`
		FeederB struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"feeder_b"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	if in.FeederA.FeatureClass == "" || in.FeederA.FeatureID == "" {
		return nil, fmt.Errorf("feeder_a.feature_class and feeder_a.feature_id are required")
	}
	if in.FeederB.FeatureClass == "" || in.FeederB.FeatureID == "" {
		return nil, fmt.Errorf("feeder_b.feature_class and feeder_b.feature_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return tie.CreateTie(ctx, sdb, tie.CreateTieInput{
		SwitchFeatureClass: in.SwitchFeatureClass,
		Subtype:            in.Subtype,
		Name:               in.Name,
		Geometry:           in.Geometry,
		TerritoryID:        in.TerritoryID,
		Attributes:         in.Attributes,
		FeederA:            tie.FeederRef{FeatureClass: in.FeederA.FeatureClass, FeatureID: in.FeederA.FeatureID},
		FeederB:            tie.FeederRef{FeatureClass: in.FeederB.FeatureClass, FeatureID: in.FeederB.FeatureID},
	})
}

func toolListTieOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := tie.ListTieOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolCreateCoupler(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		BreakerFeatureClass string          `json:"breaker_feature_class"`
		Subtype             *int            `json:"subtype"`
		Name                *string         `json:"name"`
		Geometry            json.RawMessage `json:"geometry"`
		TerritoryID         *string         `json:"territory_id"`
		Attributes          map[string]any  `json:"attributes"`
		BusA                struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"bus_a"`
		BusB struct {
			FeatureClass string `json:"feature_class"`
			FeatureID    string `json:"feature_id"`
		} `json:"bus_b"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.BreakerFeatureClass == "" {
		return nil, fmt.Errorf("breaker_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	if in.BusA.FeatureClass == "" || in.BusA.FeatureID == "" {
		return nil, fmt.Errorf("bus_a.feature_class and bus_a.feature_id are required")
	}
	if in.BusB.FeatureClass == "" || in.BusB.FeatureID == "" {
		return nil, fmt.Errorf("bus_b.feature_class and bus_b.feature_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return coupler.CreateCoupler(ctx, sdb, coupler.CreateCouplerInput{
		BreakerFeatureClass: in.BreakerFeatureClass,
		Subtype:             in.Subtype,
		Name:                in.Name,
		Geometry:            in.Geometry,
		TerritoryID:         in.TerritoryID,
		Attributes:          in.Attributes,
		BusA:                coupler.BusRef{FeatureClass: in.BusA.FeatureClass, FeatureID: in.BusA.FeatureID},
		BusB:                coupler.BusRef{FeatureClass: in.BusB.FeatureClass, FeatureID: in.BusB.FeatureID},
	})
}

func toolListCouplerOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := coupler.ListCouplerOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolCreateDuct(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DuctFeatureClass string          `json:"duct_feature_class"`
		Subtype          *int            `json:"subtype"`
		Name             *string         `json:"name"`
		Geometry         json.RawMessage `json:"geometry"`
		TerritoryID      *string         `json:"territory_id"`
		Attributes       map[string]any  `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DuctFeatureClass == "" {
		return nil, fmt.Errorf("duct_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, fmt.Errorf("geometry is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return duct.CreateDuct(ctx, sdb, duct.CreateDuctInput{
		DuctFeatureClass: in.DuctFeatureClass,
		Subtype:          in.Subtype,
		Name:             in.Name,
		Geometry:         in.Geometry,
		TerritoryID:      in.TerritoryID,
		Attributes:       in.Attributes,
	})
}

func toolListDuctOptions(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	opts, err := duct.ListDuctOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}

func toolCreatePanel(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		BoardFeatureClass string         `json:"board_feature_class"`
		BoardFeatureID    string         `json:"board_feature_id"`
		PanelFeatureClass string         `json:"panel_feature_class"`
		Subtype           *int           `json:"subtype"`
		Name              *string        `json:"name"`
		Attributes        map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.BoardFeatureClass == "" || in.BoardFeatureID == "" {
		return nil, fmt.Errorf("board_feature_class and board_feature_id are required")
	}
	if in.PanelFeatureClass == "" {
		return nil, fmt.Errorf("panel_feature_class is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return panel.CreatePanel(ctx, sdb, panel.CreatePanelInput{
		BoardFeatureClass: in.BoardFeatureClass,
		BoardFeatureID:    in.BoardFeatureID,
		PanelFeatureClass: in.PanelFeatureClass,
		Subtype:           in.Subtype,
		Name:              in.Name,
		Attributes:        in.Attributes,
	})
}

func toolListPanelOptions(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ContainerFeatureClass string `json:"container_feature_class"`
		DiagramType           string `json:"diagram_type"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The lister itself requires both filters and errors in-band when either is
	// missing, so surface its message rather than duplicating the check here.
	opts, err := panel.ListPanelOptions(ctx, db, in.ContainerFeatureClass, in.DiagramType)
	if err != nil {
		return nil, err
	}
	return map[string]any{"options": opts, "count": len(opts)}, nil
}
