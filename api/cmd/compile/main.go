// Command compile is the schema compiler's CLI entrypoint for Phase 1:
// `go run ./cmd/compile <FEATURE_CLASS_CODE>` reads that feature class's
// metadata and makes the physical schema in gis match it. There's no HTTP
// API for this yet -- the generic CRUD engine and its publish workflow
// are Phase 2 -- so this CLI is the only interface until then.
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
		log.Fatal("usage: compile <FEATURE_CLASS_CODE>")
	}
	code := os.Args[1]

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	result, err := compiler.Compile(context.Background(), db, code)
	if err != nil {
		log.Fatalf("compile %s: %v", code, err)
	}

	switch {
	case result.Created:
		log.Printf("compiled %s -> %s (table created, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.MetadataVersionID)
	case len(result.ColumnsAdded) > 0:
		log.Printf("compiled %s -> %s (columns added: %v, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.ColumnsAdded, result.MetadataVersionID)
	default:
		log.Printf("compiled %s -> %s (already up to date, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.MetadataVersionID)
	}
}
