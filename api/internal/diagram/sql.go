package diagram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// diagramTypeDomain is the fixed domain code every diagram_type value
// belongs to -- "just another row in domains, not a new lookup table"
// per docs/metadata-core.md, same pattern trace direction and snapping
// target_type already use.
const diagramTypeDomain = "DIAGRAM_TYPE"

// DiagramInstance mirrors runtime.diagram_instances (migrations/0019).
// Fixed schema known at compile time, same as api/internal/connectivity's
// Connectivity model, so it's a plain bun model rather than hand-built
// SQL.
type DiagramInstance struct {
	bun.BaseModel `bun:"table:runtime.diagram_instances,alias:diagram_instances"`

	ID                      string    `bun:"id,pk,nullzero"`
	ContainerFeatureClassID string    `bun:"container_feature_class_id"`
	ContainerFeatureID      string    `bun:"container_feature_id"`
	DiagramTypeValueID      string    `bun:"diagram_type_value_id"`
	Name                    *string   `bun:"name"`
	// SourceTemplateID is the diagram_templates row this instance was
	// auto-generated from (assembly single-line builder, migration 0122), or nil
	// for a hand-built diagram. Regenerate re-applies this exact template; the SET
	// NULL FK means deleting the template just disables regenerate, never the row.
	SourceTemplateID *string   `bun:"source_template_id,nullzero"`
	CreatedAt        time.Time `bun:"created_at,nullzero"`
}

// DiagramPlacement mirrors runtime.diagram_placements (migrations/0019).
type DiagramPlacement struct {
	bun.BaseModel `bun:"table:runtime.diagram_placements,alias:diagram_placements"`

	ID                string          `bun:"id,pk,nullzero"`
	DiagramInstanceID string          `bun:"diagram_instance_id"`
	FeatureClassID    string          `bun:"feature_class_id"`
	FeatureID         string          `bun:"feature_id"`
	X                 float64         `bun:"x"`
	Y                 float64         `bun:"y"`
	Rotation          float64         `bun:"rotation"`
	// Path is the drawn polyline for a line placement (migrations/0033):
	// a jsonb [[x, y], ...] array in the diagram's local coordinate
	// space, null for point placements. nullzero so an absent path
	// writes SQL NULL rather than an empty json literal.
	Path json.RawMessage `bun:"path,nullzero"`
	// LabelHidden suppresses this placement's name label on the canvas
	// (migration 0054). Toggled on its own via PATCH, never through the
	// place/move upsert, so geometry edits never disturb it.
	LabelHidden bool `bun:"label_hidden"`
	// Label transform (migration 0056): the name label's offset from its
	// natural anchor (glyph-local frame, so it rotates with the glyph),
	// its own rotation in degrees, and a font-size multiplier. Defaults
	// 0/0/0/1 reproduce the old pinned position. Owned by PATCH, never the
	// place/move upsert, exactly like LabelHidden.
	LabelDX       float64   `bun:"label_dx"`
	LabelDY       float64   `bun:"label_dy"`
	LabelRotation float64   `bun:"label_rotation"`
	LabelScale    float64   `bun:"label_scale"`
	CreatedAt     time.Time `bun:"created_at,nullzero"`
}

// DiagramPlacementRule mirrors metadata.diagram_placement_rules
// (migrations/0068): one row = one feature class allowed on a given
// (container class × diagram type) pairing. Read-only from this package
// -- the allow-list is authored via seed today (no admin API yet).
type DiagramPlacementRule struct {
	bun.BaseModel `bun:"table:metadata.diagram_placement_rules,alias:diagram_placement_rules"`

	ID                      string    `bun:"id,pk,nullzero"`
	ContainerFeatureClassID string    `bun:"container_feature_class_id"`
	DiagramTypeValueID      string    `bun:"diagram_type_value_id"`
	FeatureClassID          string    `bun:"feature_class_id"`
	CreatedAt               time.Time `bun:"created_at,nullzero"`
}

// resolvedFeature is one side of a request after its feature class and
// specific feature row have been resolved and validated -- same shape
// api/internal/relationship's identical helper uses.
type resolvedFeature struct {
	ref    featureRef
	loaded *metadata.Loaded
}

// loadFeatureClass mirrors connectivity/relationship's identical copy --
// translating sql.ErrNoRows into this package's own notFoundError so
// writeError never has to know about another package's error types.
func loadFeatureClass(ctx context.Context, db bun.IDB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return loaded, nil
}

// resolveFeature validates one side of a request: the feature class
// must exist and be compiled, and the specific feature row must exist
// and not be soft-deleted -- placing a deleted feature on a diagram
// makes no more sense than connecting or relating one does.
func resolveFeature(ctx context.Context, db bun.IDB, ref featureRef) (*resolvedFeature, error) {
	loaded, err := loadFeatureClass(ctx, db, ref.FeatureClass)
	if err != nil {
		return nil, err
	}

	row, err := crud.GetFeature(ctx, db, loaded, ref.FeatureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", ref.FeatureID, ref.FeatureClass)
		}
		return nil, err
	}
	if row["deleted_at"] != nil {
		return nil, invalid("feature %s (%s) is deleted -- restore it before placing on a diagram", ref.FeatureID, ref.FeatureClass)
	}

	return &resolvedFeature{ref: ref, loaded: loaded}, nil
}

// createInstance is the only path that turns a container feature into a
// real diagram: resolve the container, require its feature class have
// is_diagram_container = true (zero trust by omission, same discipline
// network_rules/relationship_classes enforce for their own kind of
// legality), resolve the requested diagram type against the
// DIAGRAM_TYPE domain, then write. No idempotency/upsert here unlike
// connectivity and relationship's establish -- a container can
// plausibly host more than one diagram (docs/metadata-core.md: "e.g. a
// one-line and a control-scheme diagram for the same substation"), so
// there's no natural uniqueness to upsert against.
func createInstance(ctx context.Context, db bun.IDB, req createInstanceRequest) (*instanceResponse, error) {
	container, err := resolveFeature(ctx, db, req.Container)
	if err != nil {
		return nil, err
	}
	if !container.loaded.FeatureClass.IsDiagramContainer {
		return nil, invalid("feature class %q is not a diagram container (is_diagram_container = false)", req.Container.FeatureClass)
	}

	diagramType, err := metadata.ResolveDomainValueByCode(ctx, db, diagramTypeDomain, req.DiagramType)
	if err != nil {
		return nil, invalid("diagram_type %q is not a valid DIAGRAM_TYPE value: %v", req.DiagramType, err)
	}

	inst := &DiagramInstance{
		ContainerFeatureClassID: container.loaded.FeatureClass.ID,
		ContainerFeatureID:      req.Container.FeatureID,
		DiagramTypeValueID:      diagramType.ID,
		Name:                    req.Name,
		SourceTemplateID:        req.SourceTemplateID,
	}
	if _, err := db.NewInsert().Model(inst).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: create instance: %w", err)
	}

	return &instanceResponse{
		ID:          inst.ID,
		Container:   req.Container,
		DiagramType: req.DiagramType,
		Name:        inst.Name,
		Regenerable: inst.SourceTemplateID != nil,
		CreatedAt:   inst.CreatedAt,
	}, nil
}

// getInstance fetches one diagram instance by id.
func getInstance(ctx context.Context, db bun.IDB, id string) (*instanceResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", id)
		}
		return nil, fmt.Errorf("diagram: get instance: %w", err)
	}
	return formatInstance(ctx, db, inst)
}

// listInstances returns every diagram instance hosted by one container
// feature.
func listInstances(ctx context.Context, db bun.IDB, containerFeatureClassCode, containerFeatureID string) ([]instanceResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, containerFeatureClassCode)
	if err != nil {
		return nil, err
	}

	var rows []DiagramInstance
	if err := db.NewSelect().Model(&rows).
		Where("container_feature_class_id = ? AND container_feature_id = ?", loaded.FeatureClass.ID, containerFeatureID).
		Order("created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list instances: %w", err)
	}

	out := make([]instanceResponse, 0, len(rows))
	for i := range rows {
		formatted, err := formatInstance(ctx, db, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *formatted)
	}
	return out, nil
}

// deleteInstance removes a diagram instance and, via ON DELETE CASCADE
// (migrations/0019), every placement on it. No soft-delete/restore pair
// -- same reasoning as connectivity/relationship's own revoke: this
// isn't a feature with a draft/published life cycle of its own.
func deleteInstance(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*DiagramInstance)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: delete instance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: delete instance: %w", err)
	}
	if n == 0 {
		return notFound("diagram instance %s not found", id)
	}
	return nil
}

// renameInstance updates a diagram instance's own display name --
// there's no admin UI or metadata concept behind it, just a plain
// column set once at creation (createInstance) that a diagram canvas
// now also needs to be able to change after the fact.
func renameInstance(ctx context.Context, db bun.IDB, id string, name *string) (*instanceResponse, error) {
	inst := new(DiagramInstance)
	res, err := db.NewUpdate().Model(inst).
		Set("name = ?", name).
		Where("id = ?", id).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("diagram: rename instance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("diagram: rename instance: %w", err)
	}
	if n == 0 {
		return nil, notFound("diagram instance %s not found", id)
	}
	return formatInstance(ctx, db, inst)
}

// placeFeature puts (or moves) one feature on one diagram: resolve the
// diagram instance and the feature, then upsert on the table's own
// (diagram_instance_id, feature_class_id, feature_id) unique constraint
// -- placing the same feature again just updates its x/y/rotation,
// never creates a second placement.
func placeFeature(ctx context.Context, db bun.IDB, diagramInstanceID string, req placeRequest) (*placementResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", diagramInstanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: place feature: %w", err)
	}

	feature, err := resolveFeature(ctx, db, featureRef{FeatureClass: req.FeatureClass, FeatureID: req.FeatureID})
	if err != nil {
		return nil, err
	}
	// A diagram is electrical: only a class flagged is_diagram_placeable
	// may go on one (Phase 12, migrations/0032), mirroring the
	// is_diagram_container gate createInstance enforces above -- zero
	// trust by omission, so a class that never opted in is rejected here
	// rather than silently accepted.
	if !feature.loaded.FeatureClass.IsDiagramPlaceable {
		return nil, invalid("feature class %q cannot be placed on a diagram (is_diagram_placeable = false)", req.FeatureClass)
	}
	// The global gate above says the class MAY appear on some diagram; this
	// per-(container class × diagram type) allow-list narrows that to a
	// specific pairing (Phase 9, migrations/0068). It's additive/opt-in: a
	// pairing with no rules is unconstrained, so every existing diagram is
	// unaffected. This is what makes a meter-chamber schematic accept only
	// LV_BUSBAR + METER while a one-line stays open to any placeable class.
	if err := enforcePlacementAllowList(ctx, db, inst.ContainerFeatureClassID, inst.DiagramTypeValueID, feature.loaded.FeatureClass.ID, req.FeatureClass); err != nil {
		return nil, err
	}

	// Gate the path against the class's DIAGRAM shape, not its map shape:
	// a busbar is geometry_type = 'point' geographically but may set
	// diagram_geometry_type = 'line', which is what makes a drawn polyline
	// legal here (migration 0053). DiagramGeom() falls back to GeometryType
	// when there's no override, so every existing class is unchanged.
	path, err := validatePlacementPath(req.Path, feature.loaded.FeatureClass.DiagramGeom())
	if err != nil {
		return nil, err
	}

	placement := &DiagramPlacement{
		DiagramInstanceID: diagramInstanceID,
		FeatureClassID:    feature.loaded.FeatureClass.ID,
		FeatureID:         req.FeatureID,
		X:                 req.X,
		Y:                 req.Y,
		Rotation:          req.Rotation,
		Path:              path,
		// A fresh placement starts at the default label transform (scale 1;
		// dx/dy/rotation zero). On a move/re-place these are NOT in the
		// conflict update below, so an arranged label survives untouched.
		LabelScale: 1,
	}
	_, err = db.NewInsert().Model(placement).
		On("CONFLICT (diagram_instance_id, feature_class_id, feature_id) DO UPDATE").
		Set("x = EXCLUDED.x, y = EXCLUDED.y, rotation = EXCLUDED.rotation, path = EXCLUDED.path").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("diagram: place feature: %w", err)
	}

	resp := toPlacementResponse(placement, req.FeatureClass)
	return &resp, nil
}

// enforcePlacementAllowList applies the optional per-(container class ×
// diagram type) placement allow-list (migrations/0068). A pairing with no
// rules is unconstrained -- every is_diagram_placeable class may be placed,
// which is every existing diagram (opt-in by absence, the same discipline
// domain_transitions uses for coded domains). Once a pairing carries any
// rules, only the listed feature classes are legal on it.
func enforcePlacementAllowList(ctx context.Context, db bun.IDB, containerClassID, diagramTypeValueID, featureClassID, featureClassCode string) error {
	total, err := db.NewSelect().
		Model((*DiagramPlacementRule)(nil)).
		Where("container_feature_class_id = ? AND diagram_type_value_id = ?", containerClassID, diagramTypeValueID).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("diagram: placement allow-list: %w", err)
	}
	if total == 0 {
		// Unconstrained pairing -- the global is_diagram_placeable gate is the
		// only rule, so leave the decision to it.
		return nil
	}

	allowed, err := db.NewSelect().
		Model((*DiagramPlacementRule)(nil)).
		Where("container_feature_class_id = ? AND diagram_type_value_id = ? AND feature_class_id = ?",
			containerClassID, diagramTypeValueID, featureClassID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("diagram: placement allow-list: %w", err)
	}
	if !allowed {
		return invalid("feature class %q is not on this diagram type's placement allow-list", featureClassCode)
	}
	return nil
}

// toPlacementResponse maps a stored row + its resolved feature-class code to
// the wire shape. Single source of truth so the three call sites (place,
// update, list) never drift on which columns they surface -- notably the
// label transform (migration 0056).
func toPlacementResponse(row *DiagramPlacement, featureClass string) placementResponse {
	return placementResponse{
		ID:            row.ID,
		FeatureClass:  featureClass,
		FeatureID:     row.FeatureID,
		X:             row.X,
		Y:             row.Y,
		Rotation:      row.Rotation,
		Path:          row.Path,
		LabelHidden:   row.LabelHidden,
		LabelDX:       row.LabelDX,
		LabelDY:       row.LabelDY,
		LabelRotation: row.LabelRotation,
		LabelScale:    row.LabelScale,
	}
}

// updatePlacement applies a narrow, geometry-free change to one placement --
// its label visibility (migration 0054) and label transform (migration 0056).
// Scoped by both id and diagram so a placement id from another diagram can't be
// touched, same guard removePlacement uses. Every request field is a pointer,
// so an omitted field is a no-op; only the columns actually supplied are
// written, and a request that changes nothing issues no UPDATE at all.
func updatePlacement(ctx context.Context, db bun.IDB, diagramInstanceID, placementID string, req updatePlacementRequest) (*placementResponse, error) {
	row := new(DiagramPlacement)
	if err := db.NewSelect().Model(row).
		Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("placement %s not found on diagram %s", placementID, diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: update placement: %w", err)
	}

	var cols []string
	if req.LabelHidden != nil && *req.LabelHidden != row.LabelHidden {
		row.LabelHidden = *req.LabelHidden
		cols = append(cols, "label_hidden")
	}
	if req.LabelDX != nil && *req.LabelDX != row.LabelDX {
		row.LabelDX = *req.LabelDX
		cols = append(cols, "label_dx")
	}
	if req.LabelDY != nil && *req.LabelDY != row.LabelDY {
		row.LabelDY = *req.LabelDY
		cols = append(cols, "label_dy")
	}
	if req.LabelRotation != nil && *req.LabelRotation != row.LabelRotation {
		row.LabelRotation = *req.LabelRotation
		cols = append(cols, "label_rotation")
	}
	if req.LabelScale != nil && *req.LabelScale != row.LabelScale {
		row.LabelScale = *req.LabelScale
		cols = append(cols, "label_scale")
	}
	if len(cols) > 0 {
		if _, err := db.NewUpdate().Model(row).
			Column(cols...).
			Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
			Exec(ctx); err != nil {
			return nil, fmt.Errorf("diagram: update placement: %w", err)
		}
	}

	code, err := loadFeatureClassCodes(ctx, db, []string{row.FeatureClassID})
	if err != nil {
		return nil, err
	}
	resp := toPlacementResponse(row, code[row.FeatureClassID])
	return &resp, nil
}

// validatePlacementPath checks an optional hand-drawn line path (Phase
// 12, option B2) against the class's DIAGRAM shape (its DiagramGeom() --
// diagram_geometry_type override, else geometry_type; migration 0053). A
// nil or JSON-null path is fine for any class -- it just means a point
// placement, or a line placed without a drawn polyline (which falls back
// to the connectivity-derived elbow on the canvas). A non-empty path is
// only legal when the diagram shape is "line" and must be an ordered
// array of at least two [x, y] pairs. Returns the path to persist (nil
// when there's nothing to store) so the caller writes SQL NULL rather
// than an empty json literal.
func validatePlacementPath(raw json.RawMessage, geometryType string) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var pts [][]float64
	if err := json.Unmarshal(raw, &pts); err != nil {
		return nil, invalid("path must be an array of [x, y] pairs: %v", err)
	}
	if geometryType != "line" {
		return nil, invalid("path is only valid for a feature drawn as a line on the diagram (diagram shape = %q here)", geometryType)
	}
	if len(pts) < 2 {
		return nil, invalid("path must have at least two [x, y] points, got %d", len(pts))
	}
	for i, p := range pts {
		if len(p) != 2 {
			return nil, invalid("path point %d must be a two-element [x, y] pair, got %d values", i, len(p))
		}
	}
	return raw, nil
}

// listPlacements returns every feature placed on one diagram.
func listPlacements(ctx context.Context, db bun.IDB, diagramInstanceID string) ([]placementResponse, error) {
	var rows []DiagramPlacement
	if err := db.NewSelect().Model(&rows).
		Where("diagram_instance_id = ?", diagramInstanceID).
		Order("created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list placements: %w", err)
	}
	if len(rows) == 0 {
		return []placementResponse{}, nil
	}

	fcIDs := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		fcIDs[r.FeatureClassID] = struct{}{}
	}
	classes, err := loadFeatureClasses(ctx, db, keys(fcIDs))
	if err != nil {
		return nil, err
	}

	// A soft-deleted (or purged) feature must not draw on the diagram, mirroring
	// resolveFeature's refusal to place one in the first place. The placement row
	// is deliberately left intact -- a soft delete is reversible, so restoring the
	// feature brings its placement back unchanged; the placement is only filtered
	// out of this read while the feature is gone. (Feature delete has no cascade
	// to diagram_placements, so this filter is what keeps the canvas honest.)
	live, err := livePlacementKeys(ctx, db, rows, classes)
	if err != nil {
		return nil, err
	}

	out := make([]placementResponse, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		if !live[placementLiveKey(r.FeatureClassID, r.FeatureID)] {
			continue
		}
		code := ""
		if fc := classes[r.FeatureClassID]; fc != nil {
			code = fc.Code
		}
		out = append(out, toPlacementResponse(r, code))
	}
	return out, nil
}

// placementLiveKey namespaces a feature id by its class so the live-set lookup
// can't collide across classes (ids are uuids, but the namespacing keeps the
// key honest regardless).
func placementLiveKey(featureClassID, featureID string) string {
	return featureClassID + "\x00" + featureID
}

// livePlacementKeys returns the set of (feature class, feature id) keys among
// the given placements whose underlying feature row still exists and is not
// soft-deleted -- one grouped `id IN (...)` probe per feature class against its
// physical table. A placement whose class was never compiled (physical_table
// nil) or whose feature was purged simply never enters the set, and the caller
// reads absence as "don't draw it".
func livePlacementKeys(ctx context.Context, db bun.IDB, rows []DiagramPlacement, classes map[string]*metadata.FeatureClass) (map[string]bool, error) {
	idsByClass := make(map[string][]string)
	for _, r := range rows {
		idsByClass[r.FeatureClassID] = append(idsByClass[r.FeatureClassID], r.FeatureID)
	}

	live := make(map[string]bool)
	for classID, ids := range idsByClass {
		fc := classes[classID]
		if fc == nil || fc.PhysicalTable == nil {
			continue
		}
		// The table name is trusted metadata (physical_table); the ids are bound
		// via bun.In, never interpolated.
		query := fmt.Sprintf("SELECT id FROM %s WHERE id IN (?) AND deleted_at IS NULL", *fc.PhysicalTable)
		var liveIDs []string
		if err := db.NewRaw(query, bun.In(ids)).Scan(ctx, &liveIDs); err != nil {
			return nil, fmt.Errorf("diagram: checking live placements for %s: %w", fc.Code, err)
		}
		for _, id := range liveIDs {
			live[placementLiveKey(classID, id)] = true
		}
	}
	return live, nil
}

// loadFeatureClasses loads full feature-class rows by id (code + physical_table
// + flags), keyed by id. loadFeatureClassCodes stays for callers that need only
// the code; this keeps the whole row for callers that also need the physical
// table -- e.g. checking a placement's feature is still live.
func loadFeatureClasses(ctx context.Context, db bun.IDB, ids []string) (map[string]*metadata.FeatureClass, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: loading feature classes: %w", err)
	}
	out := make(map[string]*metadata.FeatureClass, len(fcs))
	for i := range fcs {
		out[fcs[i].ID] = &fcs[i]
	}
	return out, nil
}

// removePlacement un-places one feature from one diagram -- a real
// delete, not a soft one, same as everywhere else in this package.
func removePlacement(ctx context.Context, db bun.IDB, diagramInstanceID, placementID string) error {
	res, err := db.NewDelete().Model((*DiagramPlacement)(nil)).
		Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: remove placement: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: remove placement: %w", err)
	}
	if n == 0 {
		return notFound("placement %s not found on diagram %s", placementID, diagramInstanceID)
	}
	return nil
}

// DiagramElement mirrors runtime.diagram_elements (migration 0046) -- the
// non-feature-backed sibling of DiagramPlacement. Unlike a placement it points
// at no real feature: it is diagram furniture (a box, a text label, a
// schematic-only line, and later title blocks/legends/slots) living in the
// diagram's own local x/y space.
type DiagramElement struct {
	bun.BaseModel `bun:"table:runtime.diagram_elements,alias:diagram_elements"`

	ID                string  `bun:"id,pk,nullzero"`
	DiagramInstanceID string  `bun:"diagram_instance_id"`
	Kind              string  `bun:"kind"`
	X                 float64 `bun:"x"`
	Y                 float64 `bun:"y"`
	// Width/Height are nullable (only box-like kinds use them), so pointers.
	Width    *float64 `bun:"width"`
	Height   *float64 `bun:"height"`
	Rotation float64  `bun:"rotation"`
	// Path is the drawn polyline for a line element -- jsonb [[x, y], ...],
	// null for non-line kinds. nullzero so an absent path writes SQL NULL.
	Path json.RawMessage `bun:"path,nullzero"`
	Text *string         `bun:"text"`
	// Style is a jsonb object, NOT NULL in the schema (default '{}') -- never
	// marked nullzero, and always given a non-empty value before insert.
	Style       json.RawMessage `bun:"style"`
	ZIndex      int             `bun:"z_index"`
	SlotClass   *string         `bun:"slot_class"`
	SlotSubtype *int            `bun:"slot_subtype"`
	SlotRole    *string         `bun:"slot_role"`
	BoundClass  *string         `bun:"bound_class"`
	BoundID     *string         `bun:"bound_id"`
	CreatedAt   time.Time       `bun:"created_at,nullzero"`
	UpdatedAt   *time.Time      `bun:"updated_at,nullzero"`
}

// createElement adds one element to a diagram: verify the diagram exists,
// validate the kind against the closed set (mirroring migration 0046's CHECK),
// then write. An element points at no feature, so there's no resolveFeature /
// is_diagram_placeable gate the way placeFeature has -- the only structural
// requirements are a valid kind and an x/y anchor.
func createElement(ctx context.Context, db bun.IDB, diagramInstanceID string, req elementRequest) (*elementResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", diagramInstanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: create element: %w", err)
	}
	if !elementKinds[req.Kind] {
		return nil, invalid("kind %q is not a valid element kind", req.Kind)
	}
	if req.X == nil || req.Y == nil {
		return nil, invalid("x and y are required")
	}

	path, err := parseElementPath(req.Path)
	if err != nil {
		return nil, err
	}
	if pathKinds[req.Kind] && path == nil {
		return nil, invalid("a %s element requires a path of at least two [x, y] points", req.Kind)
	}

	style, err := validateStyle(req.Style)
	if err != nil {
		return nil, err
	}
	// A symbol element is the stencil-palette glyph (Phase 24 Slice 2): it is
	// backed by no feature and carries only its icon in style.iconRef, resolved
	// against metadata.symbology_icons on the canvas exactly like a placement's
	// feature icon. Require that ref up front, so a symbol never renders blank.
	if req.Kind == "symbol" {
		if err := requireSymbolIconRef(style); err != nil {
			return nil, err
		}
	}
	if style == nil {
		// The style column is NOT NULL default '{}'; a nil json.RawMessage
		// would insert SQL NULL, so substitute the empty object explicitly.
		style = json.RawMessage("{}")
	}

	el := &DiagramElement{
		DiagramInstanceID: diagramInstanceID,
		Kind:              req.Kind,
		X:                 *req.X,
		Y:                 *req.Y,
		Width:             req.Width,
		Height:            req.Height,
		Path:              path,
		Text:              req.Text,
		Style:             style,
		SlotClass:         req.SlotClass,
		SlotSubtype:       req.SlotSubtype,
		SlotRole:          req.SlotRole,
	}
	if req.Rotation != nil {
		el.Rotation = *req.Rotation
	}
	if req.ZIndex != nil {
		el.ZIndex = *req.ZIndex
	}
	if _, err := db.NewInsert().Model(el).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: create element: %w", err)
	}

	resp := toElementResponse(el)
	return &resp, nil
}

// updateElement applies a partial change to one element (move, resize, restyle,
// relabel, reshape). Fields absent from the request are left as they are: a nil
// pointer means "unchanged", so it fetches the existing row first, mutates only
// the present fields, then writes the whole row back. Scoped by both element id
// and diagram id so an element can't be reached through the wrong diagram.
func updateElement(ctx context.Context, db bun.IDB, diagramInstanceID, elementID string, req elementRequest) (*elementResponse, error) {
	el := new(DiagramElement)
	if err := db.NewSelect().Model(el).
		Where("id = ? AND diagram_instance_id = ?", elementID, diagramInstanceID).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("element %s not found on diagram %s", elementID, diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: update element: %w", err)
	}

	if req.Kind != "" {
		if !elementKinds[req.Kind] {
			return nil, invalid("kind %q is not a valid element kind", req.Kind)
		}
		el.Kind = req.Kind
	}
	if req.X != nil {
		el.X = *req.X
	}
	if req.Y != nil {
		el.Y = *req.Y
	}
	if req.Width != nil {
		el.Width = req.Width
	}
	if req.Height != nil {
		el.Height = req.Height
	}
	if req.Rotation != nil {
		el.Rotation = *req.Rotation
	}
	if req.ZIndex != nil {
		el.ZIndex = *req.ZIndex
	}
	if req.Text != nil {
		el.Text = req.Text
	}
	if req.SlotClass != nil {
		el.SlotClass = req.SlotClass
	}
	if req.SlotSubtype != nil {
		el.SlotSubtype = req.SlotSubtype
	}
	if req.SlotRole != nil {
		el.SlotRole = req.SlotRole
	}
	// Path: an explicit JSON null clears it, a valid array replaces it, an
	// absent path leaves it unchanged (the point of the len check).
	if len(req.Path) > 0 {
		if string(req.Path) == "null" {
			el.Path = nil
		} else {
			path, err := parseElementPath(req.Path)
			if err != nil {
				return nil, err
			}
			el.Path = path
		}
	}
	// Style: a present, valid object replaces it; absent leaves it unchanged.
	if len(req.Style) > 0 {
		style, err := validateStyle(req.Style)
		if err != nil {
			return nil, err
		}
		if style != nil {
			el.Style = style
		}
	}

	now := time.Now()
	el.UpdatedAt = &now
	if _, err := db.NewUpdate().Model(el).WherePK().Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: update element: %w", err)
	}

	resp := toElementResponse(el)
	return &resp, nil
}

// listElements returns every element on one diagram, drawn back-to-front:
// z_index ascending, then creation order as a stable tiebreak.
func listElements(ctx context.Context, db bun.IDB, diagramInstanceID string) ([]elementResponse, error) {
	var rows []DiagramElement
	if err := db.NewSelect().Model(&rows).
		Where("diagram_instance_id = ?", diagramInstanceID).
		Order("z_index", "created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list elements: %w", err)
	}

	out := make([]elementResponse, 0, len(rows))
	for i := range rows {
		out = append(out, toElementResponse(&rows[i]))
	}
	return out, nil
}

// removeElement deletes one element from one diagram -- a real delete, same as
// removePlacement.
func removeElement(ctx context.Context, db bun.IDB, diagramInstanceID, elementID string) error {
	res, err := db.NewDelete().Model((*DiagramElement)(nil)).
		Where("id = ? AND diagram_instance_id = ?", elementID, diagramInstanceID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: remove element: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: remove element: %w", err)
	}
	if n == 0 {
		return notFound("element %s not found on diagram %s", elementID, diagramInstanceID)
	}
	return nil
}

// parseElementPath validates an element's optional polyline: nil/absent or a
// JSON null yields a nil path (no polyline), any other value must be an ordered
// array of at least two [x, y] pairs. Unlike validatePlacementPath there's no
// feature geometry_type to check against -- an element is not a feature.
func parseElementPath(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var pts [][]float64
	if err := json.Unmarshal(raw, &pts); err != nil {
		return nil, invalid("path must be an array of [x, y] pairs: %v", err)
	}
	if len(pts) < 2 {
		return nil, invalid("path must have at least two [x, y] points, got %d", len(pts))
	}
	for i, p := range pts {
		if len(p) != 2 {
			return nil, invalid("path point %d must be a two-element [x, y] pair, got %d values", i, len(p))
		}
	}
	return raw, nil
}

// validateStyle checks the optional style blob is a JSON object (or empty).
// Returns nil for an absent/null style so the caller can substitute the '{}'
// default the NOT NULL column needs.
func validateStyle(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, invalid("style must be a JSON object: %v", err)
	}
	return raw, nil
}

// requireSymbolIconRef enforces the one extra structural rule a kind = "symbol"
// element has over the generic kinds: its style must name a non-empty iconRef
// (the metadata.symbology_icons glyph the canvas renders). style has already
// passed validateStyle, so it's a valid object or nil; a nil/absent/blank
// iconRef is rejected here rather than silently stamping an invisible symbol.
func requireSymbolIconRef(style json.RawMessage) error {
	var obj struct {
		IconRef string `json:"iconRef"`
	}
	if len(style) > 0 {
		// Ignore a decode error: validateStyle already proved it's an object,
		// and a missing iconRef falls through to the empty-string check below.
		_ = json.Unmarshal(style, &obj)
	}
	if obj.IconRef == "" {
		return invalid("a symbol element requires style.iconRef (the stencil glyph to render)")
	}
	return nil
}

// toElementResponse maps a DiagramElement row to its wire shape, guaranteeing
// Style is always a non-empty JSON object even for a row that somehow carried
// an empty one.
func toElementResponse(el *DiagramElement) elementResponse {
	style := el.Style
	if len(style) == 0 {
		style = json.RawMessage("{}")
	}
	return elementResponse{
		ID:          el.ID,
		Kind:        el.Kind,
		X:           el.X,
		Y:           el.Y,
		Width:       el.Width,
		Height:      el.Height,
		Rotation:    el.Rotation,
		Path:        el.Path,
		Text:        el.Text,
		Style:       style,
		ZIndex:      el.ZIndex,
		SlotClass:   el.SlotClass,
		SlotSubtype: el.SlotSubtype,
		SlotRole:    el.SlotRole,
		BoundClass:  el.BoundClass,
		BoundID:     el.BoundID,
	}
}

// formatInstance resolves a raw DiagramInstance row's feature-class ids
// and diagram-type value id into the human-readable codes
// instanceResponse exposes.
func formatInstance(ctx context.Context, db bun.IDB, inst *DiagramInstance) (*instanceResponse, error) {
	fcCodes, err := loadFeatureClassCodes(ctx, db, []string{inst.ContainerFeatureClassID})
	if err != nil {
		return nil, err
	}
	diagramTypeCode, err := metadata.DomainValueCode(ctx, db, inst.DiagramTypeValueID)
	if err != nil {
		return nil, fmt.Errorf("diagram: resolving diagram_type: %w", err)
	}

	return &instanceResponse{
		ID: inst.ID,
		Container: featureRef{
			FeatureClass: fcCodes[inst.ContainerFeatureClassID],
			FeatureID:    inst.ContainerFeatureID,
		},
		DiagramType: diagramTypeCode,
		Name:        inst.Name,
		Regenerable: inst.SourceTemplateID != nil,
		CreatedAt:   inst.CreatedAt,
	}, nil
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func loadFeatureClassCodes(ctx context.Context, db bun.IDB, ids []string) (map[string]string, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}
