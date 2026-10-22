package relationship

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// FeatureRelationship mirrors runtime.feature_relationships
// (migrations/0017). Fixed schema known at compile time, same as
// api/internal/connectivity's Connectivity model, so it's a plain bun
// model rather than hand-built SQL.
//
// alias:feature_relationships is not optional decoration -- without it
// bun derives the insert's SQL alias from the Go struct name
// ("feature_relationship", singular, lowercased) instead of the table's
// own name, which broke api/internal/connectivity's identical upsert
// pattern in exactly this way (see docs/phase-2-connectivity.md's bug
// writeup). Pinning it here avoids repeating that bug.
type FeatureRelationship struct {
	bun.BaseModel `bun:"table:runtime.feature_relationships,alias:feature_relationships"`

	ID                        string    `bun:"id,pk,nullzero"`
	RelationshipClassID       string    `bun:"relationship_class_id"`
	OriginFeatureClassID      string    `bun:"origin_feature_class_id"`
	OriginFeatureID           string    `bun:"origin_feature_id"`
	DestinationFeatureClassID string    `bun:"destination_feature_class_id"`
	DestinationFeatureID      string    `bun:"destination_feature_id"`
	EstablishedAt             time.Time `bun:"established_at,nullzero"`
	EstablishedBy             *string   `bun:"established_by"`
}

// resolvedFeature is one side of an establish request after its feature
// class and specific feature row have been resolved and validated.
// Simpler than connectivity's resolvedEndpoint -- relationship_classes
// has no terminal concept, so there's nothing to resolve beyond
// existence and deleted_at.
type resolvedFeature struct {
	ref    featureRef
	loaded *metadata.Loaded
}

// loadFeatureClass mirrors crud's loadOrNotFound and connectivity's copy
// of the same -- translating sql.ErrNoRows into this package's own
// notFoundError so writeError never has to know about another package's
// error types.
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

// resolveFeature validates one side of an establish request: the
// feature class must exist and be compiled, and the specific feature row
// must exist and not be soft-deleted -- relating a deleted feature makes
// no more sense here than connecting one does.
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
		return nil, invalid("feature %s (%s) is deleted -- restore it before relating", ref.FeatureID, ref.FeatureClass)
	}

	return &resolvedFeature{ref: ref, loaded: loaded}, nil
}

// establishRelationship is the only path that turns a candidate
// structural relationship into a recorded fact: resolve both features,
// require a matching relationship_classes row for their feature classes
// (zero trust -- matchClass rejects if none exists, or if the request's
// kind doesn't disambiguate an ambiguous match), require the matched
// class's cardinality not already be exhausted, then write. The insert
// is an idempotent upsert on the table's (relationship_class_id,
// origin_feature_id, destination_feature_id) unique constraint --
// establishing the same relationship twice is a safe no-op, matching
// this codebase's existing idempotency pattern.
func establishRelationship(ctx context.Context, db bun.IDB, req establishRequest) (*relationshipResponse, error) {
	origin, err := resolveFeature(ctx, db, req.Origin)
	if err != nil {
		return nil, err
	}
	destination, err := resolveFeature(ctx, db, req.Destination)
	if err != nil {
		return nil, err
	}

	classes, err := metadata.LoadRelationshipClasses(ctx, db, origin.loaded.FeatureClass.ID, destination.loaded.FeatureClass.ID)
	if err != nil {
		return nil, err
	}
	class, err := matchClass(classes, req.Kind)
	if err != nil {
		return nil, err
	}

	if err := checkCardinality(ctx, db, class, req.Origin.FeatureID, req.Destination.FeatureID); err != nil {
		return nil, err
	}

	// Stamp the acting user (Slice E): established_by holds the actor's
	// uuid now, not the old "api" literal. NULL on a session-less path.
	rel := &FeatureRelationship{
		RelationshipClassID:       class.ID,
		OriginFeatureClassID:      origin.loaded.FeatureClass.ID,
		OriginFeatureID:           req.Origin.FeatureID,
		DestinationFeatureClassID: destination.loaded.FeatureClass.ID,
		DestinationFeatureID:      req.Destination.FeatureID,
	}
	if actor := auth.ActorID(ctx); actor != "" {
		rel.EstablishedBy = &actor
	}

	_, err = db.NewInsert().Model(rel).
		On("CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO UPDATE").
		Set("established_at = feature_relationships.established_at").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("relationship: establish: %w", err)
	}

	// Best-effort: the relationship above is already committed by this
	// point, so a plausibility-check failure (a query error, not a
	// "too far apart" result) shouldn't turn a successful establish
	// into a failed request -- it just means this response goes out
	// with no warning either way, same as if the two features
	// happened to be close enough that none was warranted.
	warning, _ := checkPlausibility(ctx, db, origin, destination)

	// Audit the establish on BOTH features' timelines (Slice A): a relationship
	// never writes to either feature's own gis row, so without this the feature a
	// thing is attached to -- the pole a street light rides, say -- would show
	// nothing in its History. The write shares the relationship's fate the same way
	// crud's own history does: atomic with it when the caller passed a transaction
	// (the fan-out/attach engines), a second statement on the plain REST path. We
	// don't propagate a history error to the caller -- the relationship itself is
	// the thing that had to succeed -- but a feature_history insert has no FK and a
	// well-formed row, so in practice it only fails if the whole request is already
	// failing.
	recordRelationshipHistory(ctx, db, crud.HistoryRelate, rel.ID, class.Name, class.Kind,
		origin.loaded.FeatureClass.Code, req.Origin.FeatureID,
		destination.loaded.FeatureClass.Code, req.Destination.FeatureID)

	resp := formatRelationship(rel, class, origin, destination)
	resp.PlausibilityWarning = warning
	return resp, nil
}

// recordRelationshipHistory writes an audit entry on BOTH features a relationship
// touches, so the change reads on each one's History timeline (whichever feature
// an operator opens, they see it). Each side's snapshot names the OTHER feature
// as the related one and records this feature's role (origin/destination) in the
// link, plus the relationship class/kind/id. A per-row history error is swallowed
// rather than propagated -- the relationship change is what had to succeed -- and
// the row shares that change's transaction when the caller supplied one. action
// is crud.HistoryRelate or crud.HistoryUnrelate.
func recordRelationshipHistory(ctx context.Context, db bun.IDB, action, relID, className, kind, originClass, originID, destClass, destID string) {
	// Skip a side whose class code didn't resolve (only possible on the revoke
	// path's best-effort lookup) rather than writing a history row with a blank
	// feature_class_code. On the origin's timeline the counterpart is the
	// destination, and vice versa.
	if originClass != "" {
		_ = crud.RecordRelationshipEvent(ctx, db, originClass, originID, action, map[string]any{
			"id":                    originID,
			"relationship_id":       relID,
			"relationship_class":    className,
			"kind":                  kind,
			"end":                   "origin",
			"related_feature_class": destClass,
			"related_feature_id":    destID,
		})
	}
	if destClass != "" {
		_ = crud.RecordRelationshipEvent(ctx, db, destClass, destID, action, map[string]any{
			"id":                    destID,
			"relationship_id":       relID,
			"relationship_class":    className,
			"kind":                  kind,
			"end":                   "destination",
			"related_feature_class": originClass,
			"related_feature_id":    originID,
		})
	}
}

// listRelationships returns every relationship involving one feature, on
// either side.
//
// Like the feature registry, this returns only the related features' REFS (id +
// class + human name), never their attributes or geometry -- those stay gated by
// the CRUD/tiles/export/pack read paths. It does not apply the Phase-58 territory
// scope nor the Phase-61 private-drafts filter to the far side of each edge, so a
// relationship touching a peer's out-of-scope or unpublished feature still lists
// that ref. That is an id/existence leak only, and it is the same access-scoping
// follow-up the registry documents: both want the far feature's created_by /
// published_at / territory_id, which means a per-row join to each referenced
// class's gis table. Deferred together so they land as one consistent pass.
func listRelationships(ctx context.Context, db bun.IDB, featureClassCode, featureID string) ([]relationshipResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClassCode)
	if err != nil {
		return nil, err
	}
	fcID := loaded.FeatureClass.ID

	var rows []FeatureRelationship
	if err := db.NewSelect().Model(&rows).
		Where("(origin_feature_id = ? AND origin_feature_class_id = ?) OR (destination_feature_id = ? AND destination_feature_class_id = ?)",
			featureID, fcID, featureID, fcID).
		// A soft-deleted feature must not exist anywhere in the system: a
		// relationship to a deleted peer is hidden from this list the moment either
		// endpoint is deleted, so a deleted feature stops showing on its former
		// peers' Relationships tab (the row survives for lossless restore -- see
		// crud.softDeleteFeature -- but it's invisible until purge/cleanup removes
		// it). "Live" == a feature_registry row with deleted_at IS NULL, the same
		// cross-class liveness definition the connectivity audit/cleanup use.
		Where("origin_feature_id IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)").
		Where("destination_feature_id IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)").
		Order("established_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("relationship: list: %w", err)
	}

	return formatRelationships(ctx, db, rows)
}

// revokeRelationship permanently removes an established relationship.
// No soft-delete/restore pair, same reasoning as connectivity's revoke:
// a relationship is a binary fact, established or not, with no
// draft/published life cycle of its own to preserve.
// ListRelationshipsForFeature returns every relationship touching the given
// feature (on either the origin or destination end), in established order -- the
// exported entry point the shared agent-tool surface calls, delegating to the
// same query the REST GET /relationships?feature_class=&feature_id= handler uses.
// It returns the rows as `any` (the concrete element type is package-private, but
// its fields are exported so it serializes verbatim) plus the count, so a caller
// can present the same list -- including each relationship's id, which the revoke
// tool needs -- without this package exposing its response type.
func ListRelationshipsForFeature(ctx context.Context, db bun.IDB, featureClassCode, featureID string) (any, int, error) {
	rels, err := listRelationships(ctx, db, featureClassCode, featureID)
	if err != nil {
		return nil, 0, err
	}
	return rels, len(rels), nil
}

// RevokeRelationship permanently removes an established relationship by id. It is
// the exported entry point the shared agent-tool surface (internal/agenttools)
// calls, delegating to the exact path the REST DELETE /relationships/{id}
// handler uses so both front doors behave identically (and both are gated on
// relationships:commit by their callers).
func RevokeRelationship(ctx context.Context, db bun.IDB, id string) error {
	return revokeRelationship(ctx, db, id)
}

func revokeRelationship(ctx context.Context, db bun.IDB, id string) error {
	// Load the row before deleting it: the unrelate audit (Slice A) needs both
	// endpoints and the relationship class, which the bare id doesn't carry.
	var rel FeatureRelationship
	if err := db.NewSelect().Model(&rel).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("relationship %s not found", id)
		}
		return fmt.Errorf("relationship: revoke: %w", err)
	}

	res, err := db.NewDelete().Model((*FeatureRelationship)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("relationship: revoke: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("relationship: revoke: %w", err)
	}
	if n == 0 {
		return notFound("relationship %s not found", id)
	}

	// Resolve the class codes + relationship class for the audit snapshot, then
	// record the unrelate on both ends (best-effort, after the delete committed).
	fcCodes, err := loadFeatureClassCodes(ctx, db, []string{rel.OriginFeatureClassID, rel.DestinationFeatureClassID})
	if err != nil {
		// The delete succeeded; losing just the audit snapshot's class names isn't
		// worth failing the request. Record with whatever we have.
		fcCodes = map[string]string{}
	}
	className, kind := "", ""
	if classes, err := loadRelationshipClassesByID(ctx, db, []string{rel.RelationshipClassID}); err == nil {
		if c, ok := classes[rel.RelationshipClassID]; ok {
			className, kind = c.Name, c.Kind
		}
	}
	recordRelationshipHistory(ctx, db, crud.HistoryUnrelate, rel.ID, className, kind,
		fcCodes[rel.OriginFeatureClassID], rel.OriginFeatureID,
		fcCodes[rel.DestinationFeatureClassID], rel.DestinationFeatureID)
	return nil
}

// RepointFeature moves every relationship end that references
// (classID, oldFeatureID) onto newFeatureID, staying within the same feature
// class -- the row-level half of the diagram engine's "replace this stub with
// the real map feature" swap (api/internal/diagram/replace.go), the sibling of
// connectivity.RepointFeature. Only the feature id changes.
//
// The tuple (relationship_class_id, origin_feature_id, destination_feature_id)
// is UNIQUE, so a rewrite that would duplicate a relationship the replacement
// already carries is dropped instead of updated -- idempotent, and it never
// trips the constraint. Returns the number of rows moved (rewrites + dropped
// duplicates). The caller runs this inside the swap's transaction.
func RepointFeature(ctx context.Context, db bun.IDB, classID, oldFeatureID, newFeatureID string) (int, error) {
	if oldFeatureID == newFeatureID {
		return 0, nil
	}

	var rows []FeatureRelationship
	if err := db.NewSelect().Model(&rows).
		Where("(origin_feature_class_id = ? AND origin_feature_id = ?) OR (destination_feature_class_id = ? AND destination_feature_id = ?)",
			classID, oldFeatureID, classID, oldFeatureID).
		Scan(ctx); err != nil {
		return 0, fmt.Errorf("relationship: repoint: %w", err)
	}

	moved := 0
	for i := range rows {
		r := rows[i]
		originID, destID := r.OriginFeatureID, r.DestinationFeatureID
		if r.OriginFeatureClassID == classID && r.OriginFeatureID == oldFeatureID {
			originID = newFeatureID
		}
		if r.DestinationFeatureClassID == classID && r.DestinationFeatureID == oldFeatureID {
			destID = newFeatureID
		}

		exists, err := db.NewSelect().Model((*FeatureRelationship)(nil)).
			Where("id <> ?", r.ID).
			Where("relationship_class_id = ?", r.RelationshipClassID).
			Where("origin_feature_id = ?", originID).
			Where("destination_feature_id = ?", destID).
			Exists(ctx)
		if err != nil {
			return moved, fmt.Errorf("relationship: repoint check: %w", err)
		}
		if exists {
			if _, err := db.NewDelete().Model((*FeatureRelationship)(nil)).Where("id = ?", r.ID).Exec(ctx); err != nil {
				return moved, fmt.Errorf("relationship: repoint drop dup: %w", err)
			}
			moved++
			continue
		}

		if _, err := db.NewUpdate().Model((*FeatureRelationship)(nil)).
			Set("origin_feature_id = ?", originID).
			Set("destination_feature_id = ?", destID).
			Where("id = ?", r.ID).Exec(ctx); err != nil {
			return moved, fmt.Errorf("relationship: repoint update: %w", err)
		}
		moved++
	}
	return moved, nil
}

// formatRelationship builds the response for a relationship just
// established, reusing the already-validated request-side featureRefs
// verbatim.
func formatRelationship(rel *FeatureRelationship, class *metadata.RelationshipClass, origin, destination *resolvedFeature) *relationshipResponse {
	return &relationshipResponse{
		ID:                    rel.ID,
		RelationshipClassID:   class.ID,
		RelationshipClassName: class.Name,
		Kind:                  class.Kind,
		Cardinality:           class.Cardinality,
		Origin:                origin.ref,
		Destination:           destination.ref,
		EstablishedAt:         rel.EstablishedAt,
	}
}

// formatRelationships turns raw FeatureRelationship rows into the same
// human-readable shape formatRelationship produces for a fresh
// establish, via a handful of batched lookups rather than one query per
// row's referenced feature class/relationship class.
func formatRelationships(ctx context.Context, db bun.IDB, rows []FeatureRelationship) ([]relationshipResponse, error) {
	if len(rows) == 0 {
		return []relationshipResponse{}, nil
	}

	fcIDs := map[string]struct{}{}
	classIDs := map[string]struct{}{}
	for _, r := range rows {
		fcIDs[r.OriginFeatureClassID] = struct{}{}
		fcIDs[r.DestinationFeatureClassID] = struct{}{}
		classIDs[r.RelationshipClassID] = struct{}{}
	}

	fcCodes, err := loadFeatureClassCodes(ctx, db, keys(fcIDs))
	if err != nil {
		return nil, err
	}
	classes, err := loadRelationshipClassesByID(ctx, db, keys(classIDs))
	if err != nil {
		return nil, err
	}

	out := make([]relationshipResponse, 0, len(rows))
	for _, r := range rows {
		class := classes[r.RelationshipClassID]
		out = append(out, relationshipResponse{
			ID:                    r.ID,
			RelationshipClassID:   r.RelationshipClassID,
			RelationshipClassName: class.Name,
			Kind:                  class.Kind,
			Cardinality:           class.Cardinality,
			Origin: featureRef{
				FeatureClass: fcCodes[r.OriginFeatureClassID],
				FeatureID:    r.OriginFeatureID,
			},
			Destination: featureRef{
				FeatureClass: fcCodes[r.DestinationFeatureClassID],
				FeatureID:    r.DestinationFeatureID,
			},
			EstablishedAt: r.EstablishedAt,
		})
	}
	return out, nil
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
		return nil, fmt.Errorf("relationship: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}

func loadRelationshipClassesByID(ctx context.Context, db bun.IDB, ids []string) (map[string]metadata.RelationshipClass, error) {
	var classes []metadata.RelationshipClass
	if err := db.NewSelect().Model(&classes).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("relationship: loading relationship classes: %w", err)
	}
	out := make(map[string]metadata.RelationshipClass, len(classes))
	for _, c := range classes {
		out[c.ID] = c
	}
	return out, nil
}
