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

// deleteDomainValue removes one coded value from a domain -- unlike
// deleteDomain/deleteFieldGroup above, this needs no referenced-by-a-
// field precheck: fields.domain_id is a foreign key to metadata.domains
// itself, not to any specific domain_values row, so there's nothing for
// Postgres to reject at the FK level either way. The real safety
// question is whether real feature data already holds this code, and
// that's answered the same way removing a value has always been safe in
// this project (see compiler/ddl.go's syncCheckConstraints doc comment):
// deleting the metadata row here doesn't touch any physical CHECK by
// itself, and if a later recompile then tries to narrow a domain-linked
// field's CHECK to no longer include this code, Postgres's own ADD
// CONSTRAINT validation rejects it outright, loudly, against whatever
// real rows still use it -- never a silent narrowing.
func deleteDomainValue(ctx context.Context, db *bun.DB, domainCode, valueCode string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		d := new(metadata.Domain)
		if err := tx.NewSelect().Model(d).Where("code = ?", domainCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("domain %q does not exist", domainCode)
			}
			return fmt.Errorf("loading domain %q: %w", domainCode, err)
		}
		res, err := tx.NewDelete().Model((*metadata.DomainValue)(nil)).
			Where("domain_id = ? AND code = ?", d.ID, valueCode).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting domain value %q: %w", valueCode, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("domain %q has no value with code %q", domainCode, valueCode)
		}
		return nil
	})
}

// deleteMaterialSpec mirrors deleteDomain/deleteFieldGroup -- but for a
// real reason this time, unlike deleteDomainValue right above: a domain
// value's material_spec_id is a genuine foreign key to material_specs
// (migrations/0021), so deleting a referenced spec would otherwise
// surface as a raw Postgres FK-violation 500. This precheck turns that
// into a clear 400 naming the actual reason instead.
func deleteMaterialSpec(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.MaterialSpec)
		if err := tx.NewSelect().Model(m).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("material spec %q does not exist", code)
			}
			return fmt.Errorf("loading material spec %q: %w", code, err)
		}
		referenced, err := tx.NewSelect().Model((*metadata.DomainValue)(nil)).Where("material_spec_id = ?", m.ID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking material spec %q references: %w", code, err)
		}
		if referenced {
			return invalid("material spec %q is referenced by at least one domain value -- remove that reference first", code)
		}
		if _, err := tx.NewDelete().Model(m).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("deleting material spec %q: %w", code, err)
		}
		return nil
	})
}

// --- Bill of Quantities cost catalog (migrations 0127/0128) ------------------

// deleteCurrency removes a presentation currency. The base currency is the
// denomination every stored cost uses, so deleting it would orphan the whole
// price book -- refused with a clear 400 (set a different base first).
func deleteCurrency(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.Currency)
		if err := tx.NewSelect().Model(m).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("currency %q does not exist", code)
			}
			return fmt.Errorf("loading currency %q: %w", code, err)
		}
		if m.IsBase {
			return invalid("the base currency cannot be deleted -- set a different base first")
		}
		if _, err := tx.NewDelete().Model(m).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("deleting currency %q: %w", code, err)
		}
		return nil
	})
}

// deleteCostRate removes one unit-rate row by id. No FK precheck: a cost_rates
// row is referenced by nothing (the BoQ engine reads the catalog whole).
func deleteCostRate(ctx context.Context, db *bun.DB, id string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.CostRate)
		if err := tx.NewSelect().Model(m).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("cost rate %q does not exist", id)
			}
			return fmt.Errorf("loading cost rate %q: %w", id, err)
		}
		if _, err := tx.NewDelete().Model(m).Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("deleting cost rate %q: %w", id, err)
		}
		return nil
	})
}

// deleteFeatureCostOverride clears the per-feature override for (class, feature)
// -- how the feature panel's "reset to catalog" removes a hand-set price.
func deleteFeatureCostOverride(ctx context.Context, db *bun.DB, classCode, featureID string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.FeatureCostOverride)
		if err := tx.NewSelect().Model(m).
			Where("feature_class_code = ?", classCode).Where("feature_id = ?", featureID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("no cost override for %s/%s", classCode, featureID)
			}
			return fmt.Errorf("loading feature cost override: %w", err)
		}
		if _, err := tx.NewDelete().Model(m).Where("id = ?", m.ID).Exec(ctx); err != nil {
			return fmt.Errorf("deleting feature cost override: %w", err)
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
