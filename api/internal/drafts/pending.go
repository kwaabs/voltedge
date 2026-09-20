package drafts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// This file is the Private-drafts Phase B primitive: the storage and read-overlay
// helpers for a scoped user's PRIVATE, unpublished EDIT or DELETE of an
// already-published feature. Phase A (drafts.go) hid never-published features
// behind a WHERE predicate; Phase B is different in kind -- the base gis.<code>
// row stays public and untouched, and the author sees a DIFFERENT version of it
// (their pending change overlaid), so it is an actor-dependent value
// substitution, not a filter.
//
// The overlay is deliberately DISPLAY-ONLY (v1): a pending edit's values show in
// the author's reads (GET / list / query / their own tiles) but do NOT
// participate in WHERE/ORDER BY, and the storage is ONE generic table so the
// compiler and per-class DDL stay untouched. See migration 0112 for the why.
//
// Lives in the drafts package (not crud) so tiles can share the storage without
// importing crud, exactly as the Phase A WhereClause is shared. The staging
// DECISION (turning a validated update into a patch) stays in crud, which knows
// the feature's fields and geometry; this file only stores/reads/merges.

// PendingEdit mirrors one runtime.feature_pending_edits row: a single author's
// private overlay for a single already-published feature.
type PendingEdit struct {
	bun.BaseModel `bun:"table:runtime.feature_pending_edits"`

	ID               string          `bun:"id,pk"`
	FeatureClassCode string          `bun:"feature_class_code"`
	FeatureID        string          `bun:"feature_id"`
	AuthorID         string          `bun:"author_id"`
	// Patch is the changed-pieces envelope (name/subtype/territory_id/attributes),
	// shaped like formatRow so ApplyPatch merges it key-by-key. Geometry is NOT
	// here -- see GeomJSON.
	Patch json.RawMessage `bun:"patch,type:jsonb"`
	// GeomJSON is a privately changed geometry as GeoJSON text; nil = unchanged.
	GeomJSON *string `bun:"geom_json"`
	// IsDelete marks a private delete: the author's reads hide the feature, the
	// public row stays live until the delete is applied.
	IsDelete bool `bun:"is_delete"`
	// BaseVersion is the base row's updated_at when first staged; the apply path
	// blocks when the live row has moved past it (someone else published first).
	BaseVersion *time.Time `bun:"base_version"`
	CreatedAt   time.Time  `bun:"created_at,scanonly"`
	UpdatedAt   time.Time  `bun:"updated_at,scanonly"`
}

// OverlayAuthor resolves whose private pending edits overlay the acting request's
// reads, and whether any overlay applies at all.
//
// v1 overlays the VIEWER'S OWN edits only -- author == the acting user -- unlike
// the Phase A visibility set, which also includes subordinates. A pending edit is
// the actor's private workspace over a row that is ALREADY public (a manager
// already sees the base feature fine), so there is no orphaned-draft recovery
// need that drove the management-chain visibility in Phase A, and viewer-own-only
// sidesteps the "two authors edited the same feature, whose overlay wins" case
// entirely. Subordinate-visible pending edits are a deliberate follow-up.
//
// active is false -- "no overlay, read the base rows as-is" -- under the same
// gates as VisibleAuthors: the rollout switch is off, there is no session, or the
// user is Unscoped / BreakGlass (an admin / the omnipotent account reads and
// writes public data directly, never through a private overlay).
func OverlayAuthor(ctx context.Context, db bun.IDB) (author string, active bool, err error) {
	enabled, err := Enabled(ctx, db)
	if err != nil {
		return "", false, err
	}
	if !enabled {
		return "", false, nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return "", false, nil
	}
	if user.Unscoped || user.BreakGlass {
		return "", false, nil
	}
	return user.ID, true, nil
}

// UpsertPending stages (or re-stages) one author's private edit of a feature.
// The unique (feature_class_code, feature_id, author_id) key makes a repeated
// stage overwrite the same row rather than pile up duplicates; base_version is
// only set on the first stage (COALESCE) so the conflict token reflects the
// oldest un-applied basis, not the latest re-stage.
func UpsertPending(ctx context.Context, db bun.IDB, classCode, featureID, authorID string, patch json.RawMessage, geomJSON *string, isDelete bool, baseVersion *time.Time) error {
	if len(patch) == 0 {
		patch = json.RawMessage("{}")
	}
	_, err := db.NewInsert().
		Model(&PendingEdit{
			FeatureClassCode: classCode,
			FeatureID:        featureID,
			AuthorID:         authorID,
			Patch:            patch,
			GeomJSON:         geomJSON,
			IsDelete:         isDelete,
			BaseVersion:      baseVersion,
		}).
		On("CONFLICT (feature_class_code, feature_id, author_id) DO UPDATE").
		Set("patch = EXCLUDED.patch").
		Set("geom_json = EXCLUDED.geom_json").
		Set("is_delete = EXCLUDED.is_delete").
		Set("base_version = COALESCE(feature_pending_edits.base_version, EXCLUDED.base_version)").
		Set("updated_at = now()").
		Exec(ctx)
	return err
}

// GetPending returns one author's pending edit for one feature, or (nil, nil)
// when there is none.
func GetPending(ctx context.Context, db bun.IDB, classCode, featureID, authorID string) (*PendingEdit, error) {
	pe := new(PendingEdit)
	err := db.NewSelect().
		Model(pe).
		Where("feature_class_code = ?", classCode).
		Where("feature_id = ?", featureID).
		Where("author_id = ?", authorID).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return pe, nil
}

// PendingForRows batches the overlay lookup for a page of listed rows: one query
// returns the author's pending edits for the given feature ids, keyed by feature
// id. An empty ids slice returns an empty map without hitting the DB.
func PendingForRows(ctx context.Context, db bun.IDB, classCode, authorID string, ids []string) (map[string]*PendingEdit, error) {
	out := make(map[string]*PendingEdit, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var edits []*PendingEdit
	err := db.NewSelect().
		Model(&edits).
		Where("feature_class_code = ?", classCode).
		Where("author_id = ?", authorID).
		Where("feature_id IN (?)", bun.In(ids)).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range edits {
		out[e.FeatureID] = e
	}
	return out, nil
}

// DeletePending removes one author's pending edit for one feature -- the discard
// action, and the cleanup step after an edit is applied (published).
func DeletePending(ctx context.Context, db bun.IDB, classCode, featureID, authorID string) error {
	_, err := db.NewDelete().
		Model((*PendingEdit)(nil)).
		Where("feature_class_code = ?", classCode).
		Where("feature_id = ?", featureID).
		Where("author_id = ?", authorID).
		Exec(ctx)
	return err
}

// ApplyPatch overlays a pending edit onto a formatRow-shaped feature map, in
// place: each patch key replaces the same top-level key, "attributes" merges
// field-by-field (so unchanged fields keep their published value), and a non-nil
// GeomJSON replaces "geometry". It does NOT act on IsDelete -- the caller decides
// whether to drop the row from a list or 404 a GET. Returns an error only if the
// stored jsonb is unparseable (which would mean corruption, never normal input).
func ApplyPatch(feature map[string]any, pe *PendingEdit) error {
	if pe == nil {
		return nil
	}
	if len(pe.Patch) > 0 {
		var patch map[string]any
		if err := json.Unmarshal(pe.Patch, &patch); err != nil {
			return err
		}
		for k, v := range patch {
			if k == "attributes" {
				incoming, _ := v.(map[string]any)
				attrs, _ := feature["attributes"].(map[string]any)
				if attrs == nil {
					attrs = map[string]any{}
				}
				for fk, fv := range incoming {
					attrs[fk] = fv
				}
				feature["attributes"] = attrs
				continue
			}
			feature[k] = v
		}
	}
	if pe.GeomJSON != nil {
		var geom any
		if err := json.Unmarshal([]byte(*pe.GeomJSON), &geom); err != nil {
			return err
		}
		feature["geometry"] = geom
	}
	return nil
}
