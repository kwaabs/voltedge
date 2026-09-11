// Command uncompile is the inverse of cmd/compile: `go run ./cmd/uncompile
// <FEATURE_CLASS_CODE>` drops that class's physical gis.<code> table (and
// its Martin tile function) and resets the class metadata to the
// un-compiled state, leaving every other metadata row (fields, subtypes,
// terminals, symbology/CIM/connectivity rules) intact so a following
// `compile` rebuilds the table from scratch.
//
// Its reason to exist is the additive compiler's one blind spot: it can't
// ALTER TABLE ADD COLUMN a required (NOT NULL) field onto a class that
// already has rows (Postgres rejects it -- no value for the existing
// rows). Drop the table with this, recompile, and the fresh empty table
// takes the NOT NULL column cleanly. See compiler.Uncompile for the full
// rationale.
//
// DESTRUCTIVE: this discards every row in the table. It prints the row
// count it dropped so that's explicit, but there is no undo -- back the
// data up first if it matters.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"

	"github.com/kwaabs/voltedge/api/internal/compiler"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: uncompile <FEATURE_CLASS_CODE>")
	}
	code := os.Args[1]

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	result, err := compiler.Uncompile(context.Background(), db, code)
	if err != nil {
		log.Fatalf("uncompile %s: %v", code, err)
	}

	if result.TableExisted {
		log.Printf("uncompiled %s -> dropped %s (%d row(s) discarded); metadata reset to draft. Run `compile %s` to rebuild.",
			result.FeatureClassCode, result.PhysicalTable, result.RowsDropped, result.FeatureClassCode)
	} else {
		log.Printf("uncompiled %s -> no physical table to drop; metadata reset to draft. Run `compile %s` to build.",
			result.FeatureClassCode, result.FeatureClassCode)
	}
}
