package assettag

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/uptrace/bun"
)

// tagColumns is the read shape. feature_id and created_by are uuid columns cast
// to text so their nullable/plain values scan into Go strings, the same
// treatment the faults/oms/wam stores use.
const tagColumns = "tag_code, feature_class_code, feature_id::text AS feature_id, source, is_primary, status, print_count, created_by::text AS created_by, created_at, updated_at"

func selectTags(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).ColumnExpr(tagColumns)
}

// normalizeCode trims a code and rejects the obviously-broken. Generated codes
// are already clean base32; this guards the operator-supplied legacy codes on
// the register path. Case is preserved -- a legacy barcode scans to an exact,
// possibly case-sensitive string, so upper-casing it could stop it matching.
func normalizeCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", invalidf("tag_code is required")
	}
	if len(code) > 64 {
		return "", invalidf("tag_code is too long (max 64 characters)")
	}
	for _, r := range code {
		if r < 0x20 || r == 0x7f {
			return "", invalidf("tag_code contains a control character")
		}
	}
	return code, nil
}

// LookupByCode resolves one tag by its exact code. Returns (nil, nil) when no
// such code exists, so the HTTP layer answers 404 without treating a miss as an
// error. It does NOT filter by status -- the resolver decides which statuses may
// resolve; a management view may want to see a voided tag.
func LookupByCode(ctx context.Context, db bun.IDB, code string) (*Tag, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil
	}
	var t Tag
	err := selectTags(db, &t).Where("t.tag_code = ?", code).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListForFeature returns every tag on a feature, the primary first, then newest.
// It backs the feature-panel Tags tab. No scope check here: the caller has
// already resolved the feature by (class, id) through a scoped read, so listing
// its tags leaks nothing further.
func ListForFeature(ctx context.Context, db bun.IDB, class, id string) ([]Tag, error) {
	var tags []Tag
	err := selectTags(db, &tags).
		Where("t.feature_class_code = ?", class).
		Where("t.feature_id = ?", id).
		OrderExpr("t.is_primary DESC, t.created_at DESC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// PrimaryForFeatures returns the single printable tag for each of the given
// features of ONE class -- the data behind the /features browser's bulk label
// print. Only active tags are eligible; a feature's primary wins, else its
// most-recent active tag stands in (so a feature whose primary was voided still
// prints something). A feature with no active tag is simply absent from the
// result, so len(result) may be < len(ids); the caller prints what came back and
// notes the shortfall. Class-scoped (the browser lists one class at a time),
// which keeps the id filter a plain IN list rather than a tuple match.
func PrimaryForFeatures(ctx context.Context, db bun.IDB, class string, ids []string) ([]Tag, error) {
	class = strings.TrimSpace(class)
	clean := dedupeNonEmpty(ids)
	if class == "" || len(clean) == 0 {
		return []Tag{}, nil
	}
	var tags []Tag
	// DISTINCT ON (feature_id) keeps one row per feature, and the ORDER BY makes it
	// the primary (is_primary DESC) then the newest -- Postgres requires the
	// DISTINCT ON expression to lead the ORDER BY, which it does.
	err := db.NewSelect().Model(&tags).
		ColumnExpr("DISTINCT ON (t.feature_id) " + tagColumns).
		Where("t.feature_class_code = ?", class).
		Where("t.feature_id IN (?)", bun.In(clean)).
		Where("t.status = ?", "active").
		OrderExpr("t.feature_id, t.is_primary DESC, t.created_at DESC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// MarkManyPrinted bumps print_count for each of the given codes in one statement
// -- the batch twin of MarkPrinted, fired when a bulk print sheet goes to the
// printer. Unknown codes are silently skipped; it returns how many rows were
// actually bumped. Any status is eligible: a label physically exists to reprint
// regardless of the record's lifecycle state.
func MarkManyPrinted(ctx context.Context, db bun.IDB, codes []string) (int, error) {
	clean := dedupeNonEmpty(codes)
	if len(clean) == 0 {
		return 0, nil
	}
	res, err := db.NewUpdate().Model((*Tag)(nil)).
		Set("print_count = print_count + 1").Set("updated_at = now()").
		Where("tag_code IN (?)", bun.In(clean)).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// dedupeNonEmpty trims, drops blanks, and de-duplicates a slice of ids/codes
// preserving first-seen order -- so a batch request can't blow up the IN list
// with repeats or empties.
func dedupeNonEmpty(vals []string) []string {
	seen := make(map[string]struct{}, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// featureExists reports whether (class, id) is a live (non-purged) feature, read
// from the registry -- the same cross-class index the tags are minted from. A
// soft-deleted feature still counts (its registry row survives): you may still
// need to register or reprint a tag for an asset that was retired in the model
// but is still standing in the field.
func featureExists(ctx context.Context, db bun.IDB, class, id string) (bool, error) {
	return db.NewSelect().
		TableExpr("runtime.feature_registry").
		Where("feature_class_code = ?", class).
		Where("feature_id = ?", id).
		Exists(ctx)
}

// RegisterInput registers a pre-existing / legacy asset number against a feature.
type RegisterInput struct {
	FeatureClassCode string `json:"feature_class_code"`
	FeatureID        string `json:"feature_id"`
	TagCode          string `json:"tag_code"`
	MakePrimary      bool   `json:"make_primary"`
}

// Register records an external code (source='external') for a feature, optionally
// promoting it to the feature's primary. Runs in one transaction so the
// at-most-one-primary invariant can't be observed mid-flip. actor is the acting
// user's id ("" for none).
func Register(ctx context.Context, db *bun.DB, in RegisterInput, actor string) (*Tag, error) {
	class := strings.TrimSpace(in.FeatureClassCode)
	id := strings.TrimSpace(in.FeatureID)
	if class == "" || id == "" {
		return nil, invalidf("feature_class_code and feature_id are required")
	}
	code, err := normalizeCode(in.TagCode)
	if err != nil {
		return nil, err
	}

	var created Tag
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ok, err := featureExists(ctx, tx, class, id)
		if err != nil {
			return err
		}
		if !ok {
			return invalidf("no feature %s in class %s", id, class)
		}

		exists, err := tx.NewSelect().Model((*Tag)(nil)).
			Where("tag_code = ?", code).Exists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return invalidf("tag_code %q is already in use", code)
		}

		if in.MakePrimary {
			if err := clearPrimary(ctx, tx, class, id); err != nil {
				return err
			}
		}

		tag := &Tag{
			TagCode:          code,
			FeatureClassCode: class,
			FeatureID:        id,
			Source:           "external",
			IsPrimary:        in.MakePrimary,
			Status:           "active",
			CreatedBy:        actorPtr(actor),
		}
		if _, err := tx.NewInsert().Model(tag).Exec(ctx); err != nil {
			return err
		}
		return reloadInto(ctx, tx, code, &created)
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// SetPrimary promotes an active tag to its feature's primary, demoting whichever
// tag held it. Voided/superseded tags can't be made primary (you print the
// primary; a retired code must not be the one printed).
func SetPrimary(ctx context.Context, db *bun.DB, code, actor string) (*Tag, error) {
	code = strings.TrimSpace(code)
	var out Tag
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		t, err := LookupByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if t == nil {
			return notFoundf("no tag %q", code)
		}
		if t.Status != "active" {
			return invalidf("tag %q is %s; only an active tag can be the primary", code, t.Status)
		}
		if t.IsPrimary {
			return reloadInto(ctx, tx, code, &out) // already primary -- idempotent
		}
		if err := clearPrimary(ctx, tx, t.FeatureClassCode, t.FeatureID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*Tag)(nil)).
			Set("is_primary = true").Set("updated_at = now()").
			Where("tag_code = ?", code).Exec(ctx); err != nil {
			return err
		}
		return reloadInto(ctx, tx, code, &out)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Void retires a tag (a damaged/wrong label). The primary can't be voided
// directly: promote another tag first, so a feature is never left with no
// printable code. A voided code is kept (never reused) so a stale scan of the
// old label resolves to a clear "retired", not to some later feature.
func Void(ctx context.Context, db *bun.DB, code, actor string) (*Tag, error) {
	code = strings.TrimSpace(code)
	var out Tag
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		t, err := LookupByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if t == nil {
			return notFoundf("no tag %q", code)
		}
		if t.Status == "void" {
			return reloadInto(ctx, tx, code, &out) // idempotent
		}
		if t.IsPrimary {
			return invalidf("tag %q is the primary; set another tag as primary before voiding it", code)
		}
		if _, err := tx.NewUpdate().Model((*Tag)(nil)).
			Set("status = 'void'").Set("updated_at = now()").
			Where("tag_code = ?", code).Exec(ctx); err != nil {
			return err
		}
		return reloadInto(ctx, tx, code, &out)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Reissue mints a fresh generated code for the same feature and marks the old one
// superseded, carrying primary over if the old tag held it. This is the
// damaged-label path: the physical tag is replaced but the asset is unchanged, so
// the old code stays resolvable (superseded, not void) while the field is
// re-labelled. The new code comes from the same DB sequence + encoder the mint
// trigger uses, so it is indistinguishable from a natively minted one.
func Reissue(ctx context.Context, db *bun.DB, code, actor string) (*Tag, error) {
	code = strings.TrimSpace(code)
	var out Tag
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		old, err := LookupByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if old == nil {
			return notFoundf("no tag %q", code)
		}
		if old.Status == "superseded" {
			return invalidf("tag %q has already been reissued", code)
		}

		var newCode string
		if err := tx.NewRaw(
			"SELECT runtime.encode_asset_tag(nextval('runtime.asset_tag_seq'))").
			Scan(ctx, &newCode); err != nil {
			return err
		}

		if _, err := tx.NewUpdate().Model((*Tag)(nil)).
			Set("status = 'superseded'").Set("is_primary = false").Set("updated_at = now()").
			Where("tag_code = ?", code).Exec(ctx); err != nil {
			return err
		}

		tag := &Tag{
			TagCode:          newCode,
			FeatureClassCode: old.FeatureClassCode,
			FeatureID:        old.FeatureID,
			Source:           "generated",
			IsPrimary:        old.IsPrimary, // the demote above cleared the old one, so no unique clash
			Status:           "active",
			CreatedBy:        actorPtr(actor),
		}
		if _, err := tx.NewInsert().Model(tag).Exec(ctx); err != nil {
			return err
		}
		return reloadInto(ctx, tx, newCode, &out)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkPrinted bumps a tag's print_count -- called when a label is sent to print
// so the count reflects how many times an asset's label has been produced (a
// re-print after a code was reissued, a field re-label, etc.). Any status counts:
// a superseded code can still be re-printed while the field is being re-labelled.
func MarkPrinted(ctx context.Context, db *bun.DB, code string) (*Tag, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, notFoundf("no tag %q", code)
	}
	res, err := db.NewUpdate().Model((*Tag)(nil)).
		Set("print_count = print_count + 1").Set("updated_at = now()").
		Where("tag_code = ?", code).Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFoundf("no tag %q", code)
	}
	var out Tag
	if err := reloadInto(ctx, db, code, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// clearPrimary demotes whatever tag currently holds primary for a feature, so a
// new primary can be set without tripping the asset_tags_one_primary unique
// index. A no-op when the feature has no primary.
func clearPrimary(ctx context.Context, tx bun.Tx, class, id string) error {
	_, err := tx.NewUpdate().Model((*Tag)(nil)).
		Set("is_primary = false").Set("updated_at = now()").
		Where("feature_class_code = ?", class).
		Where("feature_id = ?", id).
		Where("is_primary = true").
		Exec(ctx)
	return err
}

// reloadInto re-reads a tag by code into dst -- used at the end of a mutation so
// the returned row reflects DB defaults/timestamps, not the in-memory input.
func reloadInto(ctx context.Context, db bun.IDB, code string, dst *Tag) error {
	return selectTags(db, dst).Where("t.tag_code = ?", code).Limit(1).Scan(ctx)
}

// actorPtr turns the acting user's id into a nullable column value: "" -> NULL
// (a trigger-minted tag has no actor), any id -> that id.
func actorPtr(actor string) *string {
	if strings.TrimSpace(actor) == "" {
		return nil
	}
	return &actor
}
