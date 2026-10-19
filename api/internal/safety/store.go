package safety

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// ---------------------------------------------------------------------------
// The operate gate (consumed by the OMS switching path in a later slice).
// ---------------------------------------------------------------------------

// OperationBlocked reports whether operating a device is barred by safety: it
// returns the oldest ACTIVE, BLOCKING tag on the device (danger/out_of_service),
// or nil when the device is free to operate. The caller (the switching-step
// executor) refuses the operation and surfaces the tag's reason. A read-only probe
// on the partial index tags_device_blocking_idx (migration 0151) -- cheap enough
// to run on every open/close step.
func OperationBlocked(ctx context.Context, db bun.IDB, deviceClass, deviceID string) (*Tag, error) {
	t := new(Tag)
	err := db.NewSelect().Model(t).
		Where("t.device_class = ?", deviceClass).
		Where("t.device_id = ?", deviceID).
		Where("t.status = ?", StatusActive).
		Where("t.blocks_operation = ?", true).
		OrderExpr("t.placed_at ASC").
		Limit(1).
		Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

// PlaceTagInput places a restriction on a device. BlocksOperation is optional: a
// nil value derives from the tag type (danger/out_of_service block, caution/
// information advise). ClearanceID optionally binds the tag to an issued clearance.
type PlaceTagInput struct {
	DeviceClass     string
	DeviceID        string
	TagType         string
	BlocksOperation *bool
	Reason          *string
	ClearanceID     *string
	Notes           *string
}

// PlaceTag records a tag on a device. The acting user is stamped from ctx.
func PlaceTag(ctx context.Context, db bun.IDB, in PlaceTagInput) (*Tag, error) {
	in.DeviceClass = strings.TrimSpace(in.DeviceClass)
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	if in.DeviceClass == "" || in.DeviceID == "" {
		return nil, invalidf("device_class and device_id are required")
	}
	if !tagTypes[in.TagType] {
		return nil, invalidf("tag_type %q is not one of danger, out_of_service, caution, information", in.TagType)
	}
	if err := requireIssuedClearance(ctx, db, in.ClearanceID); err != nil {
		return nil, err
	}
	blocks := blockingTagTypes[in.TagType]
	if in.BlocksOperation != nil {
		blocks = *in.BlocksOperation
	}
	tag := &Tag{
		DeviceClass:     in.DeviceClass,
		DeviceID:        in.DeviceID,
		TagType:         in.TagType,
		BlocksOperation: blocks,
		Reason:          trimPtr(in.Reason),
		ClearanceID:     in.ClearanceID,
		Status:          StatusActive,
		PlacedBy:        actorArg(ctx),
		Notes:           trimPtr(in.Notes),
	}
	if _, err := db.NewInsert().Model(tag).
		ExcludeColumn("id", "placed_at", "created_at").
		Returning("id, placed_at, created_at").Exec(ctx); err != nil {
		return nil, err
	}
	return tag, nil
}

// RemoveTag clears an active tag (status -> removed, stamped). A tag that is
// already removed is a clean 400 rather than a silent no-op.
func RemoveTag(ctx context.Context, db bun.IDB, id string) (*Tag, error) {
	tag := new(Tag)
	if err := db.NewSelect().Model(tag).Where("t.id = ?", id).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, invalidf("safety tag %q does not exist", id)
		}
		return nil, err
	}
	if tag.Status != StatusActive {
		return nil, invalidf("safety tag %q is already removed", id)
	}
	if _, err := db.NewUpdate().Model((*Tag)(nil)).
		Set("status = ?", StatusRemoved).
		Set("removed_by = ?", actorArg(ctx)).
		Set("removed_at = now()").
		Where("id = ?", id).Exec(ctx); err != nil {
		return nil, err
	}
	return getTag(ctx, db, id)
}

func getTag(ctx context.Context, db bun.IDB, id string) (*Tag, error) {
	tag := new(Tag)
	if err := db.NewSelect().Model(tag).Where("t.id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return tag, nil
}

// TagFilter narrows ListTags. Empty fields are ignored; ActiveOnly limits to
// status='active' (the default board view).
type TagFilter struct {
	DeviceClass string
	DeviceID    string
	ClearanceID string
	ActiveOnly  bool
}

// ListTags returns tags newest-first for the filter.
func ListTags(ctx context.Context, db bun.IDB, f TagFilter) ([]Tag, error) {
	tags := []Tag{}
	q := db.NewSelect().Model(&tags).OrderExpr("t.placed_at DESC")
	if f.DeviceClass != "" {
		q = q.Where("t.device_class = ?", f.DeviceClass)
	}
	if f.DeviceID != "" {
		q = q.Where("t.device_id = ?", f.DeviceID)
	}
	if f.ClearanceID != "" {
		q = q.Where("t.clearance_id = ?", f.ClearanceID)
	}
	if f.ActiveOnly {
		q = q.Where("t.status = ?", StatusActive)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return tags, nil
}

// ---------------------------------------------------------------------------
// Grounds
// ---------------------------------------------------------------------------

// ApplyGroundInput applies a safety earth at a network point.
type ApplyGroundInput struct {
	FeatureClass string
	FeatureID    string
	Reason       *string
	ClearanceID  *string
	Notes        *string
}

// ApplyGround records an applied earth. The acting user is stamped from ctx.
func ApplyGround(ctx context.Context, db bun.IDB, in ApplyGroundInput) (*Ground, error) {
	in.FeatureClass = strings.TrimSpace(in.FeatureClass)
	in.FeatureID = strings.TrimSpace(in.FeatureID)
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, invalidf("feature_class and feature_id are required")
	}
	if err := requireIssuedClearance(ctx, db, in.ClearanceID); err != nil {
		return nil, err
	}
	g := &Ground{
		FeatureClass: in.FeatureClass,
		FeatureID:    in.FeatureID,
		Reason:       trimPtr(in.Reason),
		ClearanceID:  in.ClearanceID,
		Status:       StatusActive,
		AppliedBy:    actorArg(ctx),
		Notes:        trimPtr(in.Notes),
	}
	if _, err := db.NewInsert().Model(g).
		ExcludeColumn("id", "applied_at", "created_at").
		Returning("id, applied_at, created_at").Exec(ctx); err != nil {
		return nil, err
	}
	return g, nil
}

// RemoveGround clears an applied earth (status -> removed, stamped).
func RemoveGround(ctx context.Context, db bun.IDB, id string) (*Ground, error) {
	g := new(Ground)
	if err := db.NewSelect().Model(g).Where("g.id = ?", id).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, invalidf("safety ground %q does not exist", id)
		}
		return nil, err
	}
	if g.Status != StatusActive {
		return nil, invalidf("safety ground %q is already removed", id)
	}
	if _, err := db.NewUpdate().Model((*Ground)(nil)).
		Set("status = ?", StatusRemoved).
		Set("removed_by = ?", actorArg(ctx)).
		Set("removed_at = now()").
		Where("id = ?", id).Exec(ctx); err != nil {
		return nil, err
	}
	g2 := new(Ground)
	if err := db.NewSelect().Model(g2).Where("g.id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return g2, nil
}

// GroundFilter narrows ListGrounds.
type GroundFilter struct {
	FeatureClass string
	FeatureID    string
	ClearanceID  string
	ActiveOnly   bool
}

// ListGrounds returns grounds newest-first for the filter.
func ListGrounds(ctx context.Context, db bun.IDB, f GroundFilter) ([]Ground, error) {
	grounds := []Ground{}
	q := db.NewSelect().Model(&grounds).OrderExpr("g.applied_at DESC")
	if f.FeatureClass != "" {
		q = q.Where("g.feature_class = ?", f.FeatureClass)
	}
	if f.FeatureID != "" {
		q = q.Where("g.feature_id = ?", f.FeatureID)
	}
	if f.ClearanceID != "" {
		q = q.Where("g.clearance_id = ?", f.ClearanceID)
	}
	if f.ActiveOnly {
		q = q.Where("g.status = ?", StatusActive)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return grounds, nil
}

// ---------------------------------------------------------------------------
// Clearances (permit-to-work)
// ---------------------------------------------------------------------------

// CreateClearanceInput issues a permit-to-work. Reference is required; the rest
// are optional metadata and correlations.
type CreateClearanceInput struct {
	Reference        string
	IncidentID       *string
	SwitchingOrderID *string
	WorkDescription  *string
	IssuedTo         *string
	Notes            *string
}

// CreateClearance issues a clearance at status 'issued' (stamped). Tags and grounds
// are bound to it afterward by their clearance_id.
func CreateClearance(ctx context.Context, db bun.IDB, in CreateClearanceInput) (*Clearance, error) {
	in.Reference = strings.TrimSpace(in.Reference)
	if in.Reference == "" {
		return nil, invalidf("reference is required")
	}
	actor := actorArg(ctx)
	now := time.Now().UTC()
	c := &Clearance{
		Reference:        in.Reference,
		IncidentID:       in.IncidentID,
		SwitchingOrderID: in.SwitchingOrderID,
		WorkDescription:  trimPtr(in.WorkDescription),
		IssuedTo:         trimPtr(in.IssuedTo),
		Status:           ClearanceIssued,
		RequestedBy:      actor,
		IssuedBy:         actor,
		IssuedAt:         &now, // issued immediately; requested_at/created_at/updated_at use DB defaults
		Notes:            trimPtr(in.Notes),
	}
	if _, err := db.NewInsert().Model(c).
		ExcludeColumn("id", "requested_at", "created_at", "updated_at").
		Returning("id, requested_at, created_at, updated_at").Exec(ctx); err != nil {
		return nil, err
	}
	return GetClearance(ctx, db, c.ID)
}

// SurrenderClearance ends a clearance and RELEASES everything protecting it: its
// active tags and grounds are removed in the same transaction. This is the core
// hand-back: once the permit is surrendered, the devices it tagged may be operated
// again. An already-surrendered or cancelled clearance is a clean 400.
func SurrenderClearance(ctx context.Context, db *bun.DB, id string) (*Clearance, error) {
	return closeClearance(ctx, db, id, ClearanceSurrendered)
}

// CancelClearance voids a clearance (issued in error) and likewise releases its
// active tags and grounds.
func CancelClearance(ctx context.Context, db *bun.DB, id string) (*Clearance, error) {
	return closeClearance(ctx, db, id, ClearanceCancelled)
}

func closeClearance(ctx context.Context, db *bun.DB, id, newStatus string) (*Clearance, error) {
	var out *Clearance
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		c := new(Clearance)
		if err := tx.NewSelect().Model(c).Where("c.id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return invalidf("clearance %q does not exist", id)
			}
			return err
		}
		if c.Status == ClearanceSurrendered || c.Status == ClearanceCancelled {
			return invalidf("clearance %q is already %s", id, c.Status)
		}

		actor := actorArg(ctx)
		stampCol := "surrendered"
		upd := tx.NewUpdate().Model((*Clearance)(nil)).
			Set("status = ?", newStatus).
			Set("updated_at = now()").
			Set(stampCol+"_by = ?", actor).
			Set(stampCol+"_at = now()").
			Where("id = ?", id)
		if _, err := upd.Exec(ctx); err != nil {
			return err
		}

		// Release the protections this clearance owned.
		if _, err := tx.NewUpdate().Model((*Tag)(nil)).
			Set("status = ?", StatusRemoved).
			Set("removed_by = ?", actor).
			Set("removed_at = now()").
			Where("clearance_id = ?", id).
			Where("status = ?", StatusActive).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*Ground)(nil)).
			Set("status = ?", StatusRemoved).
			Set("removed_by = ?", actor).
			Set("removed_at = now()").
			Where("clearance_id = ?", id).
			Where("status = ?", StatusActive).Exec(ctx); err != nil {
			return err
		}

		loaded, err := getClearance(ctx, tx, id)
		if err != nil {
			return err
		}
		out = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClearanceFilter narrows ListClearances.
type ClearanceFilter struct {
	Status     string
	IncidentID string
	OpenOnly   bool // only issued (live) clearances
}

// ListClearances returns clearances newest-first (without their tags/grounds -- the
// board stays lean; a single GET carries the protections).
func ListClearances(ctx context.Context, db bun.IDB, f ClearanceFilter) ([]Clearance, error) {
	rows := []Clearance{}
	q := db.NewSelect().Model(&rows).OrderExpr("c.created_at DESC")
	if f.Status != "" {
		q = q.Where("c.status = ?", f.Status)
	}
	if f.OpenOnly {
		q = q.Where("c.status = ?", ClearanceIssued)
	}
	if f.IncidentID != "" {
		q = q.Where("c.incident_id = ?", f.IncidentID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetClearance returns one clearance with its tags and grounds, or a nil
// *Clearance (not an error) when absent -- the handler maps that to 404.
func GetClearance(ctx context.Context, db bun.IDB, id string) (*Clearance, error) {
	c, err := getClearance(ctx, db, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func getClearance(ctx context.Context, db bun.IDB, id string) (*Clearance, error) {
	c := new(Clearance)
	err := db.NewSelect().Model(c).
		Relation("Tags", func(q *bun.SelectQuery) *bun.SelectQuery { return q.OrderExpr("placed_at DESC") }).
		Relation("Grounds", func(q *bun.SelectQuery) *bun.SelectQuery { return q.OrderExpr("applied_at DESC") }).
		Where("c.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	if c.Tags == nil {
		c.Tags = []Tag{}
	}
	if c.Grounds == nil {
		c.Grounds = []Ground{}
	}
	return c, nil
}

// requireIssuedClearance validates an optional clearance_id a tag/ground is being
// bound to: it must exist and be live (issued). A null id binds nothing (an ad-hoc
// tag/ground), which is allowed.
func requireIssuedClearance(ctx context.Context, db bun.IDB, clearanceID *string) error {
	if clearanceID == nil || strings.TrimSpace(*clearanceID) == "" {
		return nil
	}
	c := new(Clearance)
	if err := db.NewSelect().Model(c).Column("status").Where("c.id = ?", *clearanceID).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return invalidf("clearance %q does not exist", *clearanceID)
		}
		return err
	}
	if c.Status != ClearanceIssued {
		return invalidf("clearance %q is %s, not issued -- cannot attach to it", *clearanceID, c.Status)
	}
	return nil
}

// actorArg reads the acting user's id from ctx, returning nil (NULL column) for a
// system/service write -- the same stamp crud, faults and ops use.
func actorArg(ctx context.Context) *string {
	if id := auth.ActorID(ctx); id != "" {
		return &id
	}
	return nil
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
