package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// deleteFeatureClass removes a feature class and everything under it
// (subtypes, fields, subtype_field_rules, terminals, symbology_rules --
// all ON DELETE CASCADE from feature_class_id, see migrations 0008/
// 0009/0012) -- but only while it's still draft. An already-compiled
// class has a real physical table (gis.<code>) and, potentially, real
// feature rows in it; deleting the metadata out from under that is the
// same unresolved "what happens to the physical data" question
// soft-delete already answers for one feature row, just not yet worked
// through for a whole class -- see docs/phase-6-admin-metadata.md.
func deleteFeatureClass(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}
		if fc.Status != "draft" {
			return invalid("only a draft (not yet compiled) feature class can be deleted -- %q is %s", code, fc.Status)
		}
		if _, err := tx.NewDelete().Model(fc).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("deleting feature class %q: %w", code, err)
		}
		return nil
	})
}

// deleteDomain removes a domain (and its values, ON DELETE CASCADE --
// migrations/0004) -- but only when no field currently constrains its
// values to it. fields.domain_id has no cascade of its own (a field
// losing its domain silently would be a real, surprising data-model
// change), so this pre-check exists purely to turn what would
// otherwise be a raw foreign-key-violation 500 into a clear 400 naming
// the actual reason, same discipline codeExists (create.go) already
// applies on the insert side.
func deleteDomain(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		d := new(metadata.Domain)
		if err := tx.NewSelect().Model(d).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("domain %q does not exist", code)
			}
			return fmt.Errorf("loading domain %q: %w", code, err)
		}
		referenced, err := tx.NewSelect().Model((*metadata.Field)(nil)).Where("domain_id = ?", d.ID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking domain %q references: %w", code, err)
		}
		if referenced {
			return invalid("domain %q is referenced by at least one field -- remove that reference first", code)
		}
		if _, err := tx.NewDelete().Model(d).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("deleting domain %q: %w", code, err)
		}
		return nil
	})
}

// deleteFieldGroup mirrors deleteDomain -- same reasoning, just against
// fields.group_id instead of fields.domain_id.
func deleteFieldGroup(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		g := new(metadata.FieldGroup)
		if err := tx.NewSelect().Model(g).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("field group %q does not exist", code)
			}
			return fmt.Errorf("loading field group %q: %w", code, err)
		}
		referenced, err := tx.NewSelect().Model((*metadata.Field)(nil)).Where("group_id = ?", g.ID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking field group %q references: %w", code, err)
		}
		if referenced {
			return invalid("field group %q is referenced by at least one field -- remove that reference first", code)
		}
		if _, err := tx.NewDelete().Model(g).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("deleting field group %q: %w", code, err)
		}
		return nil
	})
}
