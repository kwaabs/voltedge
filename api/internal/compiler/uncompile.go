package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// UncompileResult summarizes what Uncompile actually did, for the CLI to
// report -- in particular RowsDropped, so a destructive drop is never
// silent about how much data it discarded.
type UncompileResult struct {
	FeatureClassCode string
	PhysicalTable    string
	TableExisted     bool
	RowsDropped      int
}

// Uncompile is the inverse of Compile: it drops the physical gis.<code>
// table (and its Martin tile function, and -- via CASCADE -- the spatial
// index) for one feature class, and resets that class's metadata back to
// the un-compiled state (status 'draft', physical_table NULL). It leaves
// ALL other metadata untouched -- fields, subtypes, terminals, domain
// links, and every rule that references the class (symbology, CIM,
// connectivity, snapping) -- so a subsequent Compile rebuilds the table
// from scratch with the current field set.
//
// Why this exists: the compiler is additive-only (ddl.go) -- it ADD
// COLUMNs a new field but never rewrites the table. That can't add a
// required (NOT NULL) field to a class that already has rows: Postgres
// rejects `ALTER TABLE ... ADD COLUMN ... NOT NULL` on a populated table
// (SQLSTATE 23502, no value for the existing rows). The recovery is to
// drop the table and recompile it fresh -- an empty table takes the NOT
// NULL column without complaint. Uncompile is that "drop the table, keep
// the class" step, so an operator never has to hand-write DROP TABLE +
// figure out the tile function + reset the metadata columns by hand.
//
// Deliberately destructive: it discards every row in the table. The CLI
// prints RowsDropped so that's never a surprise. Idempotent -- run against
// an already-uncompiled class it drops nothing and just re-asserts the
// draft/NULL metadata state.
func Uncompile(ctx context.Context, db *bun.DB, code string) (*UncompileResult, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	fc := loaded.FeatureClass

	tableName := strings.ToLower(fc.Code)
	qualifiedTable := PhysicalSchema + "." + tableName

	exists, err := tableExists(ctx, db, PhysicalSchema, tableName)
	if err != nil {
		return nil, err
	}

	result := &UncompileResult{FeatureClassCode: fc.Code, PhysicalTable: qualifiedTable, TableExisted: exists}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if exists {
			// Count first, inside the same tx, so the reported figure is
			// exactly what the DROP below removes.
			count, err := tx.NewSelect().TableExpr(qualifiedTable).Count(ctx)
			if err != nil {
				return fmt.Errorf("count rows: %w", err)
			}
			result.RowsDropped = count

			// Drop the Martin tile function first -- gis.<table>(z,x,y) -> bytea
			// (migration 0038), only ever created for a geometric class. IF
			// EXISTS makes it a no-op for a geometry-less class that never had
			// one. Done before the table drop so a failure here rolls the whole
			// thing back rather than leaving a function pointing at a gone table.
			if fc.GeometryType != "none" {
				dropFn := fmt.Sprintf("DROP FUNCTION IF EXISTS %s(integer, integer, integer)", qualifiedTable)
				if _, err := tx.ExecContext(ctx, dropFn); err != nil {
					return fmt.Errorf("drop tile function: %w", err)
				}
			}

			// CASCADE also removes the spatial index and anything else that
			// depends on the table; a fresh Compile rebuilds them.
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", qualifiedTable)); err != nil {
				return fmt.Errorf("drop table: %w", err)
			}
		}

		// Reset the class to the un-compiled state so the metadata is honest
		// about there being no physical table -- exactly what a never-compiled
		// class looks like (seeded status 'draft', physical_table NULL). Compile
		// re-sets both when the table is rebuilt.
		if _, err := tx.NewUpdate().Model(fc).
			Set("physical_table = NULL").
			Set("status = ?", "draft").
			Where("id = ?", fc.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("reset feature_classes: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}
