// Command compile is the schema compiler's CLI entrypoint for Phase 1:
// `go run ./cmd/compile <FEATURE_CLASS_CODE>` reads that feature class's
// metadata and makes the physical schema in gis match it. There's no HTTP
// API for this yet -- the generic CRUD engine and its publish workflow
// are Phase 2 -- so this CLI is the only interface until then.
//
// A convenience mode compiles every registered class in one run:
//
//	go run ./cmd/compile --all
//
// This enumerates metadata.feature_classes and compiles each class (order is
// irrelevant -- compiled gis.<code> tables have no cross-table dependencies).
// It is what `task seed-all` uses to materialise every physical table once all
// class metadata has been seeded, before the instance/network seeds INSERT into
// them. Compiling is idempotent, so --all is safe to re-run.
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
		log.Fatal("usage: compile <FEATURE_CLASS_CODE> | compile --all")
	}
	arg := os.Args[1]

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	ctx := context.Background()

	if arg == "--all" || arg == "ALL" {
		compileAll(ctx, db)
		return
	}

	compileOne(ctx, db, arg)
}

func compileOne(ctx context.Context, db *bun.DB, code string) {
	result, err := compiler.Compile(ctx, db, code)
	if err != nil {
		log.Fatalf("compile %s: %v", code, err)
	}
	logResult(result)
}

// compileAll compiles every class registered in metadata.feature_classes. It
// continues past a class that fails to compile so one bad class can't hide the
// others, then exits non-zero if any failed -- the caller (task seed-all) halts
// on that so the operator sees exactly which class needs attention.
func compileAll(ctx context.Context, db *bun.DB) {
	var codes []string
	if err := db.NewSelect().
		ColumnExpr("code").
		TableExpr("metadata.feature_classes").
		OrderExpr("code").
		Scan(ctx, &codes); err != nil {
		log.Fatalf("compile --all: listing feature classes: %v", err)
	}
	if len(codes) == 0 {
		log.Fatal("compile --all: no feature classes registered (seed metadata first)")
	}

	var failed []string
	for _, code := range codes {
		result, err := compiler.Compile(ctx, db, code)
		if err != nil {
			log.Printf("compile %s: FAILED: %v", code, err)
			failed = append(failed, code)
			continue
		}
		logResult(result)
	}

	log.Printf("compile --all: %d/%d class(es) compiled", len(codes)-len(failed), len(codes))
	if len(failed) > 0 {
		log.Fatalf("compile --all: %d class(es) failed: %v", len(failed), failed)
	}
}

func logResult(result *compiler.Result) {
	switch {
	case result.Created:
		log.Printf("compiled %s -> %s (table created, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.MetadataVersionID)
	case len(result.ColumnsAdded) > 0:
		log.Printf("compiled %s -> %s (columns added: %v, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.ColumnsAdded, result.MetadataVersionID)
	default:
		log.Printf("compiled %s -> %s (already up to date, metadata_version %s)", result.FeatureClassCode, result.PhysicalTable, result.MetadataVersionID)
	}
}
