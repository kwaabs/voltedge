package assembly

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// Authoring surface for the general kit catalog (metadata.assembly_templates /
// assembly_items). The fan-out engine (fanout.go) and the map's Assemblies menu
// (options.go) already read these tables straight, so a kit authored here needs no
// engine change -- it appears in the menu and places itself the moment its rows
// exist. This file is the missing CRUD: list/get/create/update/delete, structural
// validation, a best-effort placement dry-run, and -- so an authored kit gets a
// single-line for free -- auto-generation of a matching runtime.diagram_templates
// skeleton (linked via metadata.assembly_templates.diagram_template_name, migration
// 0163). Gated assemblies:write at the HTTP layer (http.go); a kit is a reusable
// arrangement of existing classes, which is why it sits below metadata:write.

// --- bun row models (the metadata tables) ---------------------------------

type kitRow struct {
	bun.BaseModel `bun:"table:metadata.assembly_templates,alias:at"`

	ID                   string  `bun:"id,pk,default:gen_random_uuid()"`
	AnchorFeatureClassID string  `bun:"anchor_feature_class_id"`
	Code                 string  `bun:"code"`
	Name                 string  `bun:"name"`
	AnchorSubtypeCode    *int    `bun:"anchor_subtype_code"`
	DiagramTemplateName  *string `bun:"diagram_template_name"`
	// The assembly's own geographic-MAP glyph (migration 0192): a managed map-context
	// symbology rule keyed on the anchor's assembly_ref renders it. NULL = no assembly
	// map symbol (the anchor keeps its plain class symbology).
	MapIconRef *string `bun:"map_icon_ref"`
	MapColor   *string `bun:"map_color"`
	// Template defaults applied to the ANCHOR feature at placement (migration 0194),
	// merged UNDER the placing caller's attributes. Lets a kit pin its anchor's
	// required config field (e.g. package_config=COMPACT) so the recipe supplies it.
	AnchorAttributes map[string]any `bun:"anchor_attributes,type:jsonb"`
}

type kitItemRow struct {
	bun.BaseModel `bun:"table:metadata.assembly_items,alias:ai"`

	ID                      string         `bun:"id,pk,default:gen_random_uuid()"`
	TemplateID              string         `bun:"template_id"`
	ComponentFeatureClassID string         `bun:"component_feature_class_id"`
	SubtypeCode             *int           `bun:"subtype_code"`
	Attributes              map[string]any `bun:"attributes,type:jsonb"`
	SupportKind             *string        `bun:"support_kind"`
	SupportParentPosition   *int           `bun:"support_parent_position"`
	WireToPosition          *int           `bun:"wire_to_position"`
	OwnTerminal             *string        `bun:"own_terminal"`
	UpstreamTerminal        *string        `bun:"upstream_terminal"`
	Position                int            `bun:"position"`
	OffsetX                 float64        `bun:"offset_x"`
	OffsetY                 float64        `bun:"offset_y"`
	Rotation                float64        `bun:"rotation"`
}

// --- API DTOs (feature-class CODES, never ids -- friendlier + matches the app) -

// KitComponentInput is one component in a kit recipe, as the API speaks it.
type KitComponentInput struct {
	ComponentFeatureClass string         `json:"component_feature_class"`
	SubtypeCode           *int           `json:"subtype_code,omitempty"`
	Attributes            map[string]any `json:"attributes,omitempty"`
	SupportKind           *string        `json:"support_kind,omitempty"`
	SupportParentPosition *int           `json:"support_parent_position,omitempty"`
	WireToPosition        *int           `json:"wire_to_position,omitempty"`
	OwnTerminal           *string        `json:"own_terminal,omitempty"`
	UpstreamTerminal      *string        `json:"upstream_terminal,omitempty"`
	Position              int            `json:"position"`
	OffsetX               float64        `json:"offset_x"`
	OffsetY               float64        `json:"offset_y"`
	Rotation              float64        `json:"rotation"`
}

// KitInput is the create/update body for a kit.
type KitInput struct {
	AnchorFeatureClass  string              `json:"anchor_feature_class"`
	Code                string              `json:"code"`
	Name                string              `json:"name"`
	AnchorSubtypeCode   *int                `json:"anchor_subtype_code,omitempty"`
	DiagramTemplateName *string             `json:"diagram_template_name,omitempty"`
	// The assembly's geographic-map symbol. map_icon_ref references a symbology icon;
	// map_color is a hex string. Empty/omitted clears the assembly's map glyph.
	MapIconRef *string             `json:"map_icon_ref,omitempty"`
	MapColor   *string             `json:"map_color,omitempty"`
	// Defaults applied to the anchor at placement (migration 0194), caller wins.
	AnchorAttributes map[string]any      `json:"anchor_attributes,omitempty"`
	Components       []KitComponentInput `json:"components"`
}

// Kit is one kit returned to the client (header + ordered components).
type Kit struct {
	ID                     string              `json:"id"`
	AnchorFeatureClass     string              `json:"anchor_feature_class"`
	AnchorFeatureClassName string              `json:"anchor_feature_class_name"`
	Code                   string              `json:"code"`
	Name                   string              `json:"name"`
	AnchorSubtypeCode      *int                `json:"anchor_subtype_code,omitempty"`
	DiagramTemplateName    *string             `json:"diagram_template_name,omitempty"`
	MapIconRef             *string             `json:"map_icon_ref,omitempty"`
	MapColor               *string             `json:"map_color,omitempty"`
	AnchorAttributes       map[string]any      `json:"anchor_attributes,omitempty"`
	Components             []KitComponentInput `json:"components"`
}

// KitSummary is one row of the catalog list.
type KitSummary struct {
	ID                     string  `json:"id"`
	AnchorFeatureClass     string  `json:"anchor_feature_class"`
	AnchorFeatureClassName string  `json:"anchor_feature_class_name"`
	Code                   string  `json:"code"`
	Name                   string  `json:"name"`
	ComponentCount         int     `json:"component_count"`
	DiagramTemplateName    *string `json:"diagram_template_name,omitempty"`
}

// KitValidation is the result of POST /assemblies/templates/validate. Errors are
// structural problems that block a save; PlacementCheck is the best-effort live
// fan-out dry-run -- empty when it passed, or the first failure it hit (advisory:
// it exercises the real relationship/network-rule catalog, but can false-fail on
// an unrelated reason like a required territory, so it is reported, not enforced).
type KitValidation struct {
	Valid          bool     `json:"valid"`
	Errors         []string `json:"errors"`
	PlacementCheck string   `json:"placement_check,omitempty"`
}

// --- read -----------------------------------------------------------------

// ListKits returns the whole kit catalog (newest-authored patterns alongside the
// seeded built-ins), with each kit's component count.
func ListKits(ctx context.Context, db bun.IDB) ([]KitSummary, error) {
	const query = `
SELECT at.id, afc.code, afc.name, at.code, at.name, at.diagram_template_name,
       (SELECT count(*) FROM metadata.assembly_items ai WHERE ai.template_id = at.id)
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
ORDER BY afc.name, at.name`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("assembly: listing kits: %w", err)
	}
	defer rows.Close()

	var out []KitSummary
	for rows.Next() {
		var s KitSummary
		if err := rows.Scan(&s.ID, &s.AnchorFeatureClass, &s.AnchorFeatureClassName, &s.Code, &s.Name, &s.DiagramTemplateName, &s.ComponentCount); err != nil {
			return nil, fmt.Errorf("assembly: scanning kit: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetKit returns one kit with its ordered components.
func GetKit(ctx context.Context, db bun.IDB, id string) (*Kit, error) {
	var k Kit
	const headerQuery = `
SELECT afc.code, afc.name, at.code, at.name, at.anchor_subtype_code, at.diagram_template_name,
       at.map_icon_ref, at.map_color, at.anchor_attributes
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
WHERE at.id = ?`
	var rawAnchorAttrs []byte
	switch err := db.QueryRowContext(ctx, headerQuery, id).Scan(
		&k.AnchorFeatureClass, &k.AnchorFeatureClassName, &k.Code, &k.Name, &k.AnchorSubtypeCode, &k.DiagramTemplateName,
		&k.MapIconRef, &k.MapColor, &rawAnchorAttrs,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, notFoundf("assembly kit %q not found", id)
	case err != nil:
		return nil, fmt.Errorf("assembly: loading kit: %w", err)
	}
	if len(rawAnchorAttrs) > 0 {
		if err := json.Unmarshal(rawAnchorAttrs, &k.AnchorAttributes); err != nil {
			return nil, fmt.Errorf("assembly: decoding anchor_attributes: %w", err)
		}
	}
	k.ID = id

	const itemsQuery = `
SELECT cfc.code, ai.subtype_code, ai.attributes, ai.support_kind, ai.support_parent_position,
       ai.wire_to_position, ai.own_terminal, ai.upstream_terminal, ai.position,
       ai.offset_x, ai.offset_y, ai.rotation
FROM metadata.assembly_items ai
JOIN metadata.feature_classes cfc ON cfc.id = ai.component_feature_class_id
WHERE ai.template_id = ?
ORDER BY ai.position`
	rows, err := db.QueryContext(ctx, itemsQuery, id)
	if err != nil {
		return nil, fmt.Errorf("assembly: loading kit items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c KitComponentInput
		var rawAttrs []byte
		if err := rows.Scan(&c.ComponentFeatureClass, &c.SubtypeCode, &rawAttrs, &c.SupportKind, &c.SupportParentPosition,
			&c.WireToPosition, &c.OwnTerminal, &c.UpstreamTerminal, &c.Position, &c.OffsetX, &c.OffsetY, &c.Rotation); err != nil {
			return nil, fmt.Errorf("assembly: scanning kit item: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &c.Attributes); err != nil {
				return nil, fmt.Errorf("assembly: decoding kit item attributes: %w", err)
			}
		}
		k.Components = append(k.Components, c)
	}
	return &k, rows.Err()
}

// --- write ----------------------------------------------------------------

// CreateKit validates then inserts a kit (header + items) in one transaction, and
// -- unless the author linked an existing template -- generates a matching
// single-line template and links it. Returns the stored kit.
func CreateKit(ctx context.Context, db *bun.DB, in KitInput) (*Kit, error) {
	loaded, err := validateKitStructure(ctx, db, in)
	if err != nil {
		return nil, err
	}

	anchorID := loaded.anchor.FeatureClass.ID
	var newID string
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		header := &kitRow{
			AnchorFeatureClassID: anchorID,
			Code:                 in.Code,
			Name:                 in.Name,
			AnchorSubtypeCode:    in.AnchorSubtypeCode,
			DiagramTemplateName:  in.DiagramTemplateName,
			MapIconRef:           nilIfEmpty(in.MapIconRef),
			MapColor:             nilIfEmpty(in.MapColor),
			AnchorAttributes:     emptyIfNil(in.AnchorAttributes),
		}
		if _, err := tx.NewInsert().Model(header).Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("assembly: inserting kit: %w", err)
		}
		newID = header.ID
		if err := insertItems(ctx, tx, newID, in, loaded); err != nil {
			return err
		}
		if err := linkOrGenerateDiagram(ctx, tx, newID, in, loaded); err != nil {
			return err
		}
		return manageAssemblyMapRule(ctx, tx, newID, anchorID, loaded.anchor.FeatureClass.PhysicalTable, in.AnchorSubtypeCode, in.Code, in.MapIconRef, in.MapColor)
	}); err != nil {
		return nil, err
	}
	return GetKit(ctx, db, newID)
}

// UpdateKit replaces a kit's header + items (and regenerates/relinks its diagram).
func UpdateKit(ctx context.Context, db *bun.DB, id string, in KitInput) (*Kit, error) {
	loaded, err := validateKitStructure(ctx, db, in)
	if err != nil {
		return nil, err
	}
	anchorAttrsJSON, err := json.Marshal(emptyIfNil(in.AnchorAttributes))
	if err != nil {
		return nil, fmt.Errorf("assembly: encoding anchor_attributes: %w", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*kitRow)(nil)).
			Set("anchor_feature_class_id = ?", loaded.anchor.FeatureClass.ID).
			Set("code = ?", in.Code).
			Set("name = ?", in.Name).
			Set("anchor_subtype_code = ?", in.AnchorSubtypeCode).
			Set("diagram_template_name = ?", in.DiagramTemplateName).
			Set("map_icon_ref = ?", nilIfEmpty(in.MapIconRef)).
			Set("map_color = ?", nilIfEmpty(in.MapColor)).
			Set("anchor_attributes = ?::jsonb", string(anchorAttrsJSON)).
			Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("assembly: updating kit: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFoundf("assembly kit %q not found", id)
		}
		if _, err := tx.NewDelete().Model((*kitItemRow)(nil)).Where("template_id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("assembly: clearing kit items: %w", err)
		}
		if err := insertItems(ctx, tx, id, in, loaded); err != nil {
			return err
		}
		if err := linkOrGenerateDiagram(ctx, tx, id, in, loaded); err != nil {
			return err
		}
		return manageAssemblyMapRule(ctx, tx, id, loaded.anchor.FeatureClass.ID, loaded.anchor.FeatureClass.PhysicalTable, in.AnchorSubtypeCode, in.Code, in.MapIconRef, in.MapColor)
	}); err != nil {
		return nil, err
	}
	return GetKit(ctx, db, id)
}

// DeleteKit removes a kit; its items cascade (FK ON DELETE CASCADE, migration 0093).
func DeleteKit(ctx context.Context, db *bun.DB, id string) error {
	res, err := db.NewDelete().Model((*kitRow)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("assembly: deleting kit: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFoundf("assembly kit %q not found", id)
	}
	return nil
}

// emptyIfNil returns a non-nil map so the jsonb NOT NULL anchor_attributes column
// stores '{}' rather than a SQL NULL when a kit declares no anchor defaults.
func emptyIfNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// nilIfEmpty trims a *string and returns nil when it's empty, so an empty map symbol
// stores as NULL rather than "".
func nilIfEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// manageAssemblyMapRule upserts (or clears) the MANAGED map-context symbology rule that
// gives this assembly its own geographic-map glyph. The rule is owned by the template
// (symbology_rules.template_id, unique) and keys on the anchor's assembly_ref = the
// recipe code (migrations 0192/0193). It's render_context 'map' only, so the DIAGRAM /
// internal symbology of the anchor and its components is never touched. A nil/empty
// iconRef clears the symbol (the delete alone). Called inside the create/update tx.
//
// When a symbol is set it also BACKFILLS the marker onto units already placed as this
// kit (backfillAssemblyMarker), so setting a map symbol styles existing placements, not
// just future ones -- the rule itself is inert for a unit whose assembly_ref is null.
func manageAssemblyMapRule(ctx context.Context, tx bun.Tx, templateID, anchorClassID string, anchorTable *string, subtype *int, code string, iconRef, color *string) error {
	// Clear any prior managed rule for this template first -- handles a changed code or
	// anchor class, and clearing the symbol. template_id is unique among managed rules.
	if _, err := tx.ExecContext(ctx, `DELETE FROM metadata.symbology_rules WHERE template_id = ?`, templateID); err != nil {
		return fmt.Errorf("assembly: clearing managed map rule: %w", err)
	}
	icon := ""
	if iconRef != nil {
		icon = strings.TrimSpace(*iconRef)
	}
	if icon == "" {
		return nil // no assembly map symbol
	}
	style := map[string]any{"icon_ref": icon}
	if color != nil {
		if c := strings.TrimSpace(*color); c != "" {
			style["color"] = c
		}
	}
	styleJSON, err := json.Marshal(style)
	if err != nil {
		return fmt.Errorf("assembly: encoding map style: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO metadata.symbology_rules
			(feature_class_id, render_context, match_field, match_value, style_def, template_id)
		VALUES (?, 'map', 'assembly_ref', ?, ?::jsonb, ?)`,
		anchorClassID, code, string(styleJSON), templateID); err != nil {
		return fmt.Errorf("assembly: writing managed map rule: %w", err)
	}
	return backfillAssemblyMarker(ctx, tx, anchorClassID, templateID, anchorTable, subtype, code)
}

// backfillAssemblyMarker stamps assembly_ref = code onto anchors already placed as this
// kit that don't yet carry the marker, so setting a kit's map symbol styles EXISTING
// units, not only ones placed afterwards (the marker is otherwise stamped only at
// placement, fanout.go / migration 0192). Scoped and guarded so it can never bleed onto
// unrelated features:
//   - geometry-less anchor (no physical table) -> nothing draws on the map, no-op;
//   - the assembly_ref column must exist (migration 0192 applied); if not, a silent
//     no-op, so kit authoring never errors on a DB where 0192 hasn't landed yet;
//   - UNAMBIGUOUS-owner guard: if any OTHER kit on the same anchor class has an
//     overlapping subtype scope (same subtype, or either side pins none), an un-marked
//     anchor of that subtype could belong to either kit -- we skip rather than guess;
//   - only un-marked anchors (assembly_ref IS NULL) within the kit's subtype scope (all
//     subtypes when the kit pins none); a unit already marked at its own placement is
//     left untouched.
//
// Note: within a (class, subtype) owned by exactly one kit, this also marks units that
// were created directly (not via the kit) -- deliberate, since they are the same asset
// type and the author asked for every such unit to carry the kit's map glyph.
func backfillAssemblyMarker(ctx context.Context, tx bun.Tx, anchorClassID, templateID string, anchorTable *string, subtype *int, code string) error {
	if anchorTable == nil {
		return nil // geometry-less anchor: nothing on the map to style
	}
	var hasColumn bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema || '.' || table_name = ? AND column_name = 'assembly_ref'
		)`, *anchorTable).Scan(&hasColumn); err != nil {
		return fmt.Errorf("assembly: checking assembly_ref column: %w", err)
	}
	if !hasColumn {
		return nil // migration 0192 not applied here yet -- marker/tile column absent
	}
	var overlappingKits int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM metadata.assembly_templates
		WHERE anchor_feature_class_id = ?
		  AND id <> ?
		  AND (?::int IS NULL OR anchor_subtype_code IS NULL OR anchor_subtype_code = ?)`,
		anchorClassID, templateID, subtype, subtype).Scan(&overlappingKits); err != nil {
		return fmt.Errorf("assembly: checking kit ownership ambiguity: %w", err)
	}
	if overlappingKits > 0 {
		return nil // another kit shares this (class, subtype) -- can't tell old units apart
	}
	q := fmt.Sprintf(`UPDATE %s SET assembly_ref = ? WHERE assembly_ref IS NULL AND (?::int IS NULL OR subtype = ?)`, *anchorTable)
	if _, err := tx.ExecContext(ctx, q, code, subtype, subtype); err != nil {
		return fmt.Errorf("assembly: backfilling assembly_ref: %w", err)
	}
	return nil
}

// insertItems writes a kit's component rows, resolving each class code to its id.
func insertItems(ctx context.Context, tx bun.Tx, templateID string, in KitInput, loaded *loadedKit) error {
	for _, c := range in.Components {
		attrs := c.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		row := &kitItemRow{
			TemplateID:              templateID,
			ComponentFeatureClassID: loaded.components[c.ComponentFeatureClass].FeatureClass.ID,
			SubtypeCode:             c.SubtypeCode,
			Attributes:              attrs,
			SupportKind:             c.SupportKind,
			SupportParentPosition:   c.SupportParentPosition,
			WireToPosition:          c.WireToPosition,
			OwnTerminal:             c.OwnTerminal,
			UpstreamTerminal:        c.UpstreamTerminal,
			Position:                c.Position,
			OffsetX:                 c.OffsetX,
			OffsetY:                 c.OffsetY,
			Rotation:                c.Rotation,
		}
		if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
			return fmt.Errorf("assembly: inserting kit item %s: %w", c.ComponentFeatureClass, err)
		}
	}
	return nil
}

// --- validation -----------------------------------------------------------

// loadedKit holds the resolved metadata for a kit's anchor + component classes, so
// validation and insert don't reload them.
type loadedKit struct {
	anchor     *metadata.Loaded
	components map[string]*metadata.Loaded // by class code
}

// validateKitStructure checks everything resolvable without placing features: the
// classes exist, components are geometry-optional (the fan-out creates them without
// geometry), subtypes/terminals are valid, and every position reference points
// backward. Returns the loaded metadata for reuse. Deeper checks (a relationship
// class / network rule actually authorising a leg) surface at placement, or via the
// best-effort ValidateKit dry-run.
func validateKitStructure(ctx context.Context, db bun.IDB, in KitInput) (*loadedKit, error) {
	if strings.TrimSpace(in.AnchorFeatureClass) == "" {
		return nil, invalidf("anchor_feature_class is required")
	}
	if strings.TrimSpace(in.Code) == "" {
		return nil, invalidf("code is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, invalidf("name is required")
	}
	if len(in.Components) == 0 {
		return nil, invalidf("a kit needs at least one component")
	}

	anchor, err := metadata.Load(ctx, db, in.AnchorFeatureClass)
	if err != nil {
		return nil, invalidf("anchor feature class %q not found", in.AnchorFeatureClass)
	}
	if in.AnchorSubtypeCode != nil && !hasSubtype(anchor, *in.AnchorSubtypeCode) {
		return nil, invalidf("anchor subtype %d is not a subtype of %s", *in.AnchorSubtypeCode, in.AnchorFeatureClass)
	}

	lk := &loadedKit{anchor: anchor, components: map[string]*metadata.Loaded{}}

	seen := map[int]bool{anchorPosition: true} // positions created so far (0 = anchor)
	for i, c := range in.Components {
		if strings.TrimSpace(c.ComponentFeatureClass) == "" {
			return nil, invalidf("component %d: component_feature_class is required", i+1)
		}
		if c.Position < 1 {
			return nil, invalidf("component %d (%s): position must be >= 1", i+1, c.ComponentFeatureClass)
		}
		if seen[c.Position] {
			return nil, invalidf("component %d (%s): position %d is used more than once", i+1, c.ComponentFeatureClass, c.Position)
		}

		comp, ok := lk.components[c.ComponentFeatureClass]
		if !ok {
			comp, err = metadata.Load(ctx, db, c.ComponentFeatureClass)
			if err != nil {
				return nil, invalidf("component feature class %q not found", c.ComponentFeatureClass)
			}
			lk.components[c.ComponentFeatureClass] = comp
		}
		if !comp.FeatureClass.GeometryOptional {
			return nil, invalidf("component %s must be geometry-optional to ride in a kit (it is created without map geometry) -- set geometry_optional and recompile it", c.ComponentFeatureClass)
		}
		if c.SubtypeCode != nil && !hasSubtype(comp, *c.SubtypeCode) {
			return nil, invalidf("component %s: subtype %d is not a subtype of that class", c.ComponentFeatureClass, *c.SubtypeCode)
		}
		if c.OwnTerminal != nil && !hasTerminal(comp, *c.OwnTerminal) {
			return nil, invalidf("component %s: own_terminal %q is not a terminal of that class", c.ComponentFeatureClass, *c.OwnTerminal)
		}
		// Position references must point backward (0 = anchor, or an earlier item).
		if c.WireToPosition != nil {
			if *c.WireToPosition != anchorPosition && !seen[*c.WireToPosition] {
				return nil, invalidf("component %s: wire_to_position %d is not the anchor (0) or an earlier component", c.ComponentFeatureClass, *c.WireToPosition)
			}
			if c.UpstreamTerminal != nil {
				if err := validateUpstreamTerminal(in, lk, *c.WireToPosition, *c.UpstreamTerminal); err != nil {
					return nil, err
				}
			}
		}
		if c.SupportParentPosition != nil && *c.SupportParentPosition != anchorPosition && !seen[*c.SupportParentPosition] {
			return nil, invalidf("component %s: support_parent_position %d is not the anchor (0) or an earlier component", c.ComponentFeatureClass, *c.SupportParentPosition)
		}
		if c.SupportParentPosition != nil && c.SupportKind == nil {
			return nil, invalidf("component %s: support_parent_position is set but support_kind is empty", c.ComponentFeatureClass)
		}
		seen[c.Position] = true
	}
	return lk, nil
}

// validateUpstreamTerminal checks the named terminal exists on the wiring target's
// class (the anchor, or an earlier component resolved by position).
func validateUpstreamTerminal(in KitInput, lk *loadedKit, targetPos int, terminal string) error {
	if targetPos == anchorPosition {
		if !hasTerminal(lk.anchor, terminal) {
			return invalidf("upstream_terminal %q is not a terminal of the anchor class %s", terminal, in.AnchorFeatureClass)
		}
		return nil
	}
	for _, c := range in.Components {
		if c.Position == targetPos {
			comp := lk.components[c.ComponentFeatureClass]
			if comp != nil && !hasTerminal(comp, terminal) {
				return invalidf("upstream_terminal %q is not a terminal of %s (the wire_to target)", terminal, c.ComponentFeatureClass)
			}
			return nil
		}
	}
	return nil
}

func hasSubtype(l *metadata.Loaded, code int) bool {
	for _, s := range l.Subtypes {
		if s.Code == code {
			return true
		}
	}
	return false
}

func hasTerminal(l *metadata.Loaded, code string) bool {
	for _, t := range l.Terminals {
		if t.Code == code {
			return true
		}
	}
	return false
}

// ValidateKit runs the structural checks and then a best-effort live dry-run: it
// executes the real fan-out against a throwaway anchor geometry inside a transaction
// that is always rolled back, so it exercises the actual relationship and network-
// rule catalog (the deep checks structure alone can't make) without writing anything.
func ValidateKit(ctx context.Context, db *bun.DB, in KitInput) (*KitValidation, error) {
	v := &KitValidation{Valid: true, Errors: []string{}}
	loaded, err := validateKitStructure(ctx, db, in)
	if err != nil {
		if IsInvalid(err) {
			v.Valid = false
			v.Errors = append(v.Errors, err.Error())
			return v, nil
		}
		return nil, err
	}
	if err := dryRunKit(ctx, db, in, loaded); err != nil {
		v.PlacementCheck = err.Error()
	}
	return v, nil
}

// errDryRunRollback forces the dry-run transaction to roll back after a successful
// fan-out, so validation never persists anything.
var errDryRunRollback = errors.New("assembly: dry-run rollback")

// dryRunKit runs fanOut in a rolled-back transaction with a dummy anchor geometry.
// A nil return means the kit would place cleanly; any other error is the first real
// problem the fan-out hit (a missing network rule, an unauthorised leg, ...).
func dryRunKit(ctx context.Context, db *bun.DB, in KitInput, loaded *loadedKit) error {
	resolved := &metadata.AssemblyTemplateResolved{AnchorSubtypeCode: in.AnchorSubtypeCode}
	compMeta := map[string]*metadata.Loaded{}
	for _, c := range in.Components {
		resolved.Items = append(resolved.Items, metadata.AssemblyItem{
			ComponentFeatureClass: c.ComponentFeatureClass,
			SubtypeCode:           c.SubtypeCode,
			Attributes:            c.Attributes,
			SupportKind:           c.SupportKind,
			SupportParentPosition: c.SupportParentPosition,
			WireToPosition:        c.WireToPosition,
			OwnTerminal:           c.OwnTerminal,
			UpstreamTerminal:      c.UpstreamTerminal,
			Position:              c.Position,
			OffsetX:               c.OffsetX,
			OffsetY:               c.OffsetY,
			Rotation:              c.Rotation,
		})
		compMeta[c.ComponentFeatureClass] = loaded.components[c.ComponentFeatureClass]
	}
	ci := CreateAssemblyInput{
		AnchorFeatureClass: in.AnchorFeatureClass,
		Config:             in.Code,
		Subtype:            in.AnchorSubtypeCode,
		Geometry:          dummyGeometry(loaded.anchor.FeatureClass.GeometryType),
		Attributes:         map[string]any{},
	}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var result CreateAssemblyResult
		if e := fanOut(ctx, tx, ci, loaded.anchor, resolved, compMeta, in.AnchorSubtypeCode, &result); e != nil {
			return e
		}
		return errDryRunRollback
	})
	if errors.Is(err, errDryRunRollback) {
		return nil
	}
	return err
}

// dummyGeometry returns a minimal valid GeoJSON for the anchor's geometry type, so
// the dry-run's anchor create passes geometry validation.
func dummyGeometry(geomType string) json.RawMessage {
	switch geomType {
	case "line":
		return json.RawMessage(`{"type":"LineString","coordinates":[[0,0],[0.001,0.001]]}`)
	case "polygon":
		return json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[0.001,0],[0.001,0.001],[0,0]]]}`)
	default:
		return json.RawMessage(`{"type":"Point","coordinates":[0,0]}`)
	}
}

// --- diagram generation ---------------------------------------------------

// linkOrGenerateDiagram sets the kit's diagram_template_name. If the author linked
// an existing template, it is left as stored (already written by the header insert/
// update). Otherwise a simple vertical-stack single-line template is generated to
// match the kit's components and the kit is linked to it.
func linkOrGenerateDiagram(ctx context.Context, tx bun.Tx, kitID string, in KitInput, loaded *loadedKit) error {
	if in.DiagramTemplateName != nil && strings.TrimSpace(*in.DiagramTemplateName) != "" {
		return nil // author linked an existing/hand-authored template
	}
	name := fmt.Sprintf("Assembly kit — %s / %s", in.AnchorFeatureClass, in.Code)
	payload, err := generateKitDiagramPayload(in, loaded)
	if err != nil {
		return err
	}
	if err := upsertDiagramTemplate(ctx, tx, name, "Auto-generated single-line for the "+in.Name+" kit.", payload); err != nil {
		return err
	}
	if _, err := tx.NewUpdate().Model((*kitRow)(nil)).
		Set("diagram_template_name = ?", name).Where("id = ?", kitID).Exec(ctx); err != nil {
		return fmt.Errorf("assembly: linking generated diagram: %w", err)
	}
	return nil
}

type diagElement struct {
	Kind      string            `json:"kind"`
	X         float64           `json:"x"`
	Y         float64           `json:"y"`
	Rotation  float64           `json:"rotation"`
	ZIndex    int               `json:"z_index"`
	SlotClass string            `json:"slot_class,omitempty"`
	Path      [][]float64       `json:"path,omitempty"`
	Text      string            `json:"text,omitempty"`
	Style     map[string]string `json:"style,omitempty"`
}

// generateKitDiagramPayload builds a deterministic vertical-stack single-line: the
// anchor at the top, each component stacked below in position order with a riser
// between rows, a typed slot per feature (a horizontal bar for a 'line' class like a
// busbar, a point otherwise), and a label. Plain by design -- an author who wants a
// nicer mimic links a hand-authored template instead.
func generateKitDiagramPayload(in KitInput, loaded *loadedKit) (json.RawMessage, error) {
	const (
		trunkX = 200.0
		topY   = 60.0
		gap    = 140.0
		barHalf = 80.0
	)
	stroke := map[string]string{"stroke": "var(--foreground)"}
	var elements []diagElement

	// Rows in draw order: the anchor, then components by position.
	type row struct {
		class string
		label string
		isLine bool
	}
	rows := []row{{class: in.AnchorFeatureClass, label: loaded.anchor.FeatureClass.Name, isLine: isLineShape(loaded.anchor)}}
	comps := append([]KitComponentInput(nil), in.Components...)
	sort.SliceStable(comps, func(i, j int) bool { return comps[i].Position < comps[j].Position })
	for _, c := range comps {
		comp := loaded.components[c.ComponentFeatureClass]
		rows = append(rows, row{class: c.ComponentFeatureClass, label: comp.FeatureClass.Name, isLine: isLineShape(comp)})
	}

	for i, r := range rows {
		y := topY + float64(i)*gap
		// Riser connecting this row to the one above.
		if i > 0 {
			prevY := topY + float64(i-1)*gap
			elements = append(elements, diagElement{
				Kind: "line", X: trunkX, Y: prevY + 30, ZIndex: 0,
				Path: [][]float64{{trunkX, prevY + 30}, {trunkX, y - 30}}, Style: stroke,
			})
		}
		slot := diagElement{Kind: "slot", X: trunkX, Y: y, ZIndex: 20, SlotClass: r.class}
		if r.isLine {
			slot.X = trunkX - barHalf
			slot.Path = [][]float64{{trunkX - barHalf, y}, {trunkX + barHalf, y}}
		}
		elements = append(elements, slot)
		elements = append(elements, diagElement{Kind: "text", X: trunkX + 35, Y: y - 6, ZIndex: 30, Text: r.label})
	}

	raw, err := json.Marshal(map[string]any{"elements": elements})
	if err != nil {
		return nil, fmt.Errorf("assembly: encoding generated diagram: %w", err)
	}
	return raw, nil
}

// isLineShape reports whether a class draws as a line on a diagram (busbar,
// conductor) -- its slot then gets a horizontal bar path instead of a point.
func isLineShape(l *metadata.Loaded) bool {
	shape := l.FeatureClass.GeometryType
	if l.FeatureClass.DiagramGeometryType != nil && *l.FeatureClass.DiagramGeometryType != "" {
		shape = *l.FeatureClass.DiagramGeometryType
	}
	return shape == "line"
}

// upsertDiagramTemplate writes a runtime.diagram_templates row by name: UPDATE in
// place if the name exists (preserving its id so existing diagrams' regenerate links
// survive), else INSERT. runtime.diagram_templates has no unique(name), so this is
// an explicit update-else-insert.
func upsertDiagramTemplate(ctx context.Context, tx bun.Tx, name, description string, payload json.RawMessage) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE runtime.diagram_templates SET description = ?, payload = ?::jsonb WHERE name = ?`,
		description, string(payload), name)
	if err != nil {
		return fmt.Errorf("assembly: updating diagram template: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO runtime.diagram_templates (name, description, payload) VALUES (?, ?, ?::jsonb)`,
		name, description, string(payload)); err != nil {
		return fmt.Errorf("assembly: inserting diagram template: %w", err)
	}
	return nil
}

// --- errors ---------------------------------------------------------------

// notFoundError is a not-found problem the HTTP layer maps to 404.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFoundf(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

// IsNotFound reports whether err is (or wraps) a notFoundError.
func IsNotFound(err error) bool {
	var ne *notFoundError
	return errors.As(err, &ne)
}
