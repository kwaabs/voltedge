package crud

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the crud side of Private-drafts Phase B: turning a validated edit
// or delete of an ALREADY-PUBLISHED feature into a PRIVATE pending change instead
// of a public write, and overlaying an author's pending changes back onto their
// own reads. The base gis.<code> row is never touched by a staged edit -- it
// stays public and unchanged for everyone -- so a scoped user can revise or
// retire a live feature without anyone else seeing it until they apply it.
//
// The storage and the merge live in package drafts (shared with tiles); this file
// holds only what needs crud's knowledge of a feature's fields, geometry and
// writers: building the patch, reconstructing an update input to apply, and the
// history wiring. The staging DECISION keys off drafts.OverlayAuthor plus the
// feature's already-public state (see stagingActive); it never fires while
// enforcement is off, for an unscoped/break-glass actor, or on a never-published
// draft (which Phase A already keeps private, so the author edits it in place).

// pendingCtxKey marks a context as "applying a staged edit," so the write
// diversion below is bypassed. Without it, applyPendingEdit's real write of the
// staged change would just re-stage it forever. It is set only by
// withApplyPending, only inside ApplyPendingEdit's transaction.
type pendingCtxKey struct{}

func withApplyPending(ctx context.Context) context.Context {
	return context.WithValue(ctx, pendingCtxKey{}, true)
}

func isApplyingPending(ctx context.Context) bool {
	v, _ := ctx.Value(pendingCtxKey{}).(bool)
	return v
}

// timePtr pulls a scanned timestamptz (a time.Time out of pgdriver) out of a
// formatRow map as a *time.Time, or nil if it is absent/another type. Used for
// the base_version conflict token.
func timePtr(v any) *time.Time {
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

// stagingActive reports whether a write should be diverted into a private pending
// edit rather than mutating the public base row, and (when so) the acting
// author. It is true only when the draft overlay is active for the actor (gate
// on, scoped, non-break-glass -- see drafts.OverlayAuthor) AND the target feature
// is already public (its change would otherwise be visible to everyone). A
// never-published draft returns false: Phase A already keeps it private to its
// author chain, so it is edited in place. An apply-in-progress context (see
// withApplyPending) also returns false, so applying a staged edit performs the
// real write instead of re-staging it.
func stagingActive(ctx context.Context, db bun.IDB, existing map[string]any) (author string, active bool, err error) {
	if isApplyingPending(ctx) {
		return "", false, nil
	}
	author, active, err = drafts.OverlayAuthor(ctx, db)
	if err != nil || !active {
		return "", false, err
	}
	// Already-public test, mirroring drafts.IsVisible's public arm: a currently
	// published row, or one ever published (the sticky marker). A never-public
	// draft is edited in place.
	status, _ := existing["status"].(string)
	if status != "published" && existing["published_at"] == nil {
		return "", false, nil
	}
	return author, true, nil
}

// stagePrivateEdit records a scoped author's private edit of an already-published
// feature into runtime.feature_pending_edits and returns the would-be (overlaid)
// state -- the same envelope a normal update returns, plus a pending_private_edit
// marker so the caller/UI can badge it. The patch carries only the changed pieces
// (name/subtype/territory/attributes as a formatRow-shaped envelope; geometry in
// its own geom_json column), so the read overlay merges it key-by-key. base is
// the feature's current public state (fetched by UpdateFeature); its updated_at
// becomes the conflict token for a later apply.
func stagePrivateEdit(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, author, id string, base map[string]any, geometry []byte, clearGeometry bool, name *string, territoryID *string, assignTerritory bool, subtypeCode *int, values map[string]any) (map[string]any, error) {
	patch := map[string]any{}
	if name != nil {
		patch["name"] = *name
	}
	if subtypeCode != nil {
		patch["subtype"] = *subtypeCode
	}
	// A resolved territory (from an explicit override or a geometry move). A nil
	// here is a clear that UpdateFeature will re-derive from the geometry on
	// apply, so it is not worth storing -- only a concrete reassignment is.
	if assignTerritory && territoryID != nil {
		patch["territory_id"] = *territoryID
	}
	if len(values) > 0 {
		patch["attributes"] = values
	}

	var geomJSON *string
	if len(geometry) > 0 {
		s := string(geometry)
		geomJSON = &s
	} else if clearGeometry {
		// Rare: a private demotion to diagram-only. Represented in the patch as an
		// explicit null geometry (the Go overlay honors it; the tile overlay, which
		// only substitutes a replacement geom, leaves the base geometry -- an
		// accepted display-only edge, since the change still shows in the panel).
		patch["geometry"] = nil
	}

	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, invalid("could not encode the private edit: %v", err)
	}
	baseVersion := timePtr(base["updated_at"])
	if err := drafts.UpsertPending(ctx, db, loaded.FeatureClass.Code, id, author, raw, geomJSON, false, baseVersion); err != nil {
		return nil, err
	}

	// Build the would-be state for the response and the audit snapshot by
	// overlaying the just-stored patch onto the base row.
	pe := &drafts.PendingEdit{Patch: raw, GeomJSON: geomJSON}
	if err := drafts.ApplyPatch(base, pe); err != nil {
		return nil, err
	}
	base["pending_private_edit"] = true
	// Audit trail: staging is a change too. The snapshot is the would-be merged
	// state, so the timeline shows what the author privately made it look like
	// before they applied (or discarded) it.
	if err := recordFeatureHistory(ctx, db, loaded, historyStageEdit, base); err != nil {
		return nil, err
	}
	return base, nil
}

// stagePrivateDelete records a scoped author's private retirement of an
// already-published feature: the base row stays live for everyone else, the
// author's own reads hide it. Returns the base state marked pending_private_edit
// + pending_private_delete so the caller knows the delete is staged, not applied.
func stagePrivateDelete(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, author, id string, base map[string]any) (map[string]any, error) {
	baseVersion := timePtr(base["updated_at"])
	if err := drafts.UpsertPending(ctx, db, loaded.FeatureClass.Code, id, author, json.RawMessage("{}"), nil, true, baseVersion); err != nil {
		return nil, err
	}
	base["pending_private_edit"] = true
	base["pending_private_delete"] = true
	if err := recordFeatureHistory(ctx, db, loaded, historyStageDelete, base); err != nil {
		return nil, err
	}
	return base, nil
}

// overlayFeature applies the acting author's pending private edit to one already-
// read feature row, in place. It returns hidden=true when the author privately
// deleted the feature (the caller then answers 404, exactly as a missing row
// would), and leaves the row untouched when the overlay is inactive or there is
// no pending edit for this feature. Called at the GET read boundary only -- never
// on the internal getFeature the writers use to read a base row.
func overlayFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, feature map[string]any) (hidden bool, err error) {
	author, active, err := drafts.OverlayAuthor(ctx, db)
	if err != nil || !active {
		return false, err
	}
	id, _ := feature["id"].(string)
	pe, err := drafts.GetPending(ctx, db, loaded.FeatureClass.Code, id, author)
	if err != nil || pe == nil {
		return false, err
	}
	if pe.IsDelete {
		return true, nil
	}
	if err := drafts.ApplyPatch(feature, pe); err != nil {
		return false, err
	}
	feature["pending_private_edit"] = true
	return false, nil
}

// overlayFeatures applies the acting author's pending private edits to a page of
// listed rows: privately-edited rows are merged (and badged), privately-deleted
// rows are dropped. One batched lookup for the whole page. A no-op when the
// overlay is inactive or the author has no pending edits among these rows.
func overlayFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, rows []map[string]any) ([]map[string]any, error) {
	author, active, err := drafts.OverlayAuthor(ctx, db)
	if err != nil || !active {
		return rows, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if id, ok := row["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	edits, err := drafts.PendingForRows(ctx, db, loaded.FeatureClass.Code, author, ids)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return rows, nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id, _ := row["id"].(string)
		pe := edits[id]
		if pe == nil {
			out = append(out, row)
			continue
		}
		if pe.IsDelete {
			continue // privately retired -- hidden from its author's list
		}
		if err := drafts.ApplyPatch(row, pe); err != nil {
			return nil, err
		}
		row["pending_private_edit"] = true
		out = append(out, row)
	}
	return out, nil
}

// pendingToInput reconstructs an UpdateFeatureInput from a stored patch so the
// apply path can run the change through the normal, fully-validating UpdateFeature
// (under withApplyPending, so it writes for real instead of re-staging). A nil
// territory in the patch was a geometry-driven reassignment that UpdateFeature
// re-derives from the geometry, so it is intentionally not set here.
func pendingToInput(pe *drafts.PendingEdit) UpdateFeatureInput {
	var in UpdateFeatureInput
	var patch map[string]any
	_ = json.Unmarshal(pe.Patch, &patch)
	if v, ok := patch["name"]; ok {
		if s, ok := v.(string); ok {
			in.Name = &s
		}
	}
	if v, ok := patch["subtype"]; ok {
		if n, ok := toInt(v); ok {
			in.Subtype = &n
		}
	}
	if v, ok := patch["territory_id"]; ok {
		if s, ok := v.(string); ok {
			in.TerritoryID = &s
		}
	}
	if v, ok := patch["attributes"]; ok {
		if m, ok := v.(map[string]any); ok {
			in.Attributes = m
		}
	}
	if pe.GeomJSON != nil {
		in.Geometry = json.RawMessage(*pe.GeomJSON)
	} else if v, ok := patch["geometry"]; ok && v == nil {
		in.Geometry = json.RawMessage("null") // an explicit private geometry clear
	}
	return in
}

// ApplyPendingEdit publishes the acting author's staged private edit/delete onto
// the public base row, in one transaction: it re-checks visibility, blocks on a
// conflict (the base row moved since the edit was staged), performs the real
// write (a normal update, or a soft delete), then clears the pending row. The
// change becomes public here -- the historyUpdate/historyDelete the base writers
// emit completes the audit trail the earlier stage_edit/stage_delete opened.
func ApplyPendingEdit(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	author, active, err := drafts.OverlayAuthor(ctx, db)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, invalid("private drafts aren't enabled for you, so there's no pending edit to apply")
	}
	pe, err := drafts.GetPending(ctx, db, loaded.FeatureClass.Code, id, author)
	if err != nil {
		return nil, err
	}
	if pe == nil {
		return nil, notFound("no pending private edit for feature %s", id)
	}

	var result map[string]any
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		base, err := getFeature(ctx, tx, loaded, id)
		if err != nil {
			return err
		}
		if err := enforceReadScope(ctx, tx, loaded, base); err != nil {
			return err
		}
		if err := enforceDraftScope(ctx, tx, base); err != nil {
			return err
		}
		// Conflict: someone else published a change since this edit was staged.
		if pe.BaseVersion != nil {
			if cur := timePtr(base["updated_at"]); cur != nil && cur.After(*pe.BaseVersion) {
				return conflict("this feature was changed by someone else since you staged your private edit -- discard it and redo against the current version")
			}
		}
		actx := withApplyPending(ctx)
		if pe.IsDelete {
			if base["deleted_at"] != nil {
				result = base // already deleted publicly -- nothing to apply
			} else if result, err = setDeletedAt(actx, tx, loaded, id, true); err != nil {
				return err
			}
		} else if result, err = UpdateFeature(actx, tx, loaded, id, pendingToInput(pe)); err != nil {
			return err
		}
		return drafts.DeletePending(ctx, tx, loaded.FeatureClass.Code, id, author)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DiscardPendingEdit throws away the acting author's staged private edit/delete
// without touching the public base row, and records a discard in the timeline (so
// the audit shows the withdrawal). Idempotent-ish: a discard with nothing staged
// is a 404, matching apply. If the base row is already gone (purged), the pending
// row is still removed but no history is written -- there is no feature to append
// to.
func DiscardPendingEdit(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, id string) error {
	author, active, err := drafts.OverlayAuthor(ctx, db)
	if err != nil {
		return err
	}
	if !active {
		return invalid("private drafts aren't enabled for you, so there's no pending edit to discard")
	}
	pe, err := drafts.GetPending(ctx, db, loaded.FeatureClass.Code, id, author)
	if err != nil {
		return err
	}
	if pe == nil {
		return notFound("no pending private edit for feature %s", id)
	}
	if err := drafts.DeletePending(ctx, db, loaded.FeatureClass.Code, id, author); err != nil {
		return err
	}
	base, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		if isNotFoundError(err) {
			return nil // base purged -- nothing to record against
		}
		return err
	}
	return recordFeatureHistory(ctx, db, loaded, historyDiscard, base)
}
