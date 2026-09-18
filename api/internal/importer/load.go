package importer

// Import load core (S4, docs/etl-import-engine.md). This is the per-job work a
// claimed import runs: resolve the profile, stream the uploaded NDJSON artifact,
// and apply every row through the validated crud engine as an insert or a
// natural-key upsert -- the one genuinely new piece of logic in the engine, since
// nothing else in the app matches on anything but the primary key.
//
// It reuses the S3 apply logic wholesale (ResolveProfile / BuildAttributes /
// AssembleGeometry from mapping.go), so the dry-run pre-flight and the committed
// load can never disagree about how a row maps. The only thing added here is the
// match-and-write decision: for insert_only every row is a create; for upsert a
// row SELECTs the target table on its match columns -- 0 hits create, 1 updates,
// >1 is an ambiguous row skipped and reported.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/export"
)

// progressFlush is how many rows the worker streams between flushing its running
// tallies to the job row. Small enough that a run's progress is visibly live,
// large enough that the flush isn't a per-row write.
const progressFlush = 200

// importNamespace seeds the deterministic per-row UUIDs a load mints for its
// inserts. Deriving each insert's id from (job id, row number) makes the whole
// run idempotent under a reclaim: if a worker crashes mid-stream and another
// reclaims the job, re-inserting a row hits CreateFeature's ON CONFLICT (id) DO
// NOTHING and is a no-op rather than a duplicate -- the same idempotency the
// field-tool sync path gets from client-minted UUIDs. Upsert rows are already
// idempotent (they re-match and update), but a deterministic id makes the insert
// branch safe on the same terms.
var importNamespace = uuid.MustParse("1f8b4e2a-0c3d-4f6a-9b7e-2d5a8c1e6f40")

// rowResult is one row's applied outcome, driving which job counter it lands in.
type rowResult int

const (
	rowInserted rowResult = iota
	rowUpdated
	rowSkipped // matched more than one existing feature -- reported, not applied
	rowErrored // build/geometry/validation/write failure -- reported, not applied
)

// errAmbiguousMatch is the sentinel an upsert raises inside its per-row
// transaction when the match columns select more than one live feature: the row
// is rolled back, counted as skipped, and reported.
var errAmbiguousMatch = errors.New("ambiguous match")

// loader is the resolved, per-job apply context: the profile compiled against
// live metadata, the job (for dry-run flag + deterministic ids), and the target
// table for the upsert SELECT.
type loader struct {
	db    *bun.DB
	rp    *ResolvedProfile
	job   *ImportJob
	table string
}

// runClaimedImport is the whole body of one claimed import: resolve the profile,
// reconstruct the requester's authenticated context from the scope snapshot,
// stream the artifact, and apply each row -- recording per-row failures and
// flushing tallies as it goes. A job-level problem (corrupt scope, missing
// profile, uncompiled class, unreadable artifact, malformed stream) marks the job
// failed; a per-row problem is skip-and-reported and the run continues.
func runClaimedImport(ctx context.Context, db *bun.DB, store *export.ObjectStore, job *ImportJob) {
	var scope ImportScope
	if err := json.Unmarshal(job.Scope, &scope); err != nil {
		markImportFailed(ctx, db, job.ID, fmt.Sprintf("corrupt job scope: %v", err))
		return
	}

	rp, err := ResolveProfile(ctx, db, scope.ProfileCode)
	if err != nil {
		markImportFailed(ctx, db, job.ID, fmt.Sprintf("resolving import profile: %v", err))
		return
	}
	if rp.Loaded.FeatureClass.PhysicalTable == nil {
		markImportFailed(ctx, db, job.ID, fmt.Sprintf("feature class %q has not been compiled yet -- no physical table to write", job.FeatureClassCode))
		return
	}

	// Reconstruct the requester's authenticated context from the enqueue-time
	// snapshot, so CreateFeature/UpdateFeature stamp the right actor and enforce
	// exactly the caller's Phase 58 territory scope (readScope reads UserFrom).
	// Permissions aren't needed here -- the enqueue already gated features:write.
	user := &auth.SessionUser{
		ID:                 job.RequestedBy,
		AllowedTerritories: scope.TerritoryIDs,
		Unscoped:           scope.Unscoped,
	}
	applyCtx := auth.WithUser(ctx, user)

	rc, err := store.Open(ctx, job.ObjectKey)
	if err != nil {
		markImportFailed(ctx, db, job.ID, fmt.Sprintf("opening source artifact: %v", err))
		return
	}
	defer rc.Close()

	l := &loader{db: db, rp: rp, job: job, table: *rp.Loaded.FeatureClass.PhysicalTable}
	reader := NewReader(rc)

	var t importTally
	var fatal string
	for {
		// A lost lease / shutdown cancels ctx; stop cleanly and let the reclaiming
		// worker re-run (deterministic ids + upsert idempotency keep that safe).
		if ctx.Err() != nil {
			return
		}

		f, row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			// A decode error desyncs the json stream, so the rest is unreadable:
			// record it against the row, count it, and stop as a fatal failure.
			t.total++
			t.errored++
			if err := recordRowError(ctx, db, job.ID, row, fmt.Sprintf("malformed source row: %v", readErr)); err != nil {
				log.Printf("import: job %s could not record row %d error: %v", job.ID, row, err)
			}
			fatal = fmt.Sprintf("source artifact is malformed at row %d: %v", row, readErr)
			break
		}

		t.total++
		res, msg := l.applyRow(applyCtx, f, row)
		switch res {
		case rowInserted:
			t.inserted++
		case rowUpdated:
			t.updated++
		case rowSkipped:
			t.skipped++
		default:
			t.errored++
		}
		if msg != "" {
			if err := recordRowError(ctx, db, job.ID, row, msg); err != nil {
				log.Printf("import: job %s could not record row %d error: %v", job.ID, row, err)
			}
		}

		if t.total%progressFlush == 0 {
			if err := updateImportProgress(ctx, db, job.ID, t); err != nil {
				log.Printf("import: job %s progress flush failed: %v", job.ID, err)
			}
		}
	}

	status := "succeeded"
	var errMsg *string
	if fatal != "" {
		status = "failed"
		errMsg = &fatal
	}
	if err := completeImport(ctx, db, job.ID, t, status, errMsg); err != nil {
		log.Printf("import: job %s finished but marking %s failed: %v", job.ID, status, err)
	}
}

// applyRow maps one source row and applies it, returning its outcome and (for a
// skipped/errored row) the message to record. It shares BuildAttributes /
// AssembleGeometry with the dry-run, then either validates only (dry_run) or
// writes through crud in its own transaction so a bad row rolls back just itself.
func (l *loader) applyRow(ctx context.Context, f *Feature, row int) (rowResult, string) {
	rp := l.rp

	attrs, err := rp.BuildAttributes(f)
	if err != nil {
		return rowErrored, err.Error()
	}
	geom, err := rp.AssembleGeometry(ctx, l.db, f)
	if err != nil {
		return rowErrored, err.Error()
	}

	upsert := rp.Profile.WriteMode == "upsert"

	// Upsert needs a value for every match column, or it can't identify a target.
	if upsert {
		for _, fld := range rp.Profile.MatchFields {
			if v, ok := attrs[fld]; !ok || v == nil {
				return rowErrored, fmt.Sprintf("upsert match field %q has no value in this row", fld)
			}
		}
	}

	if l.job.DryRun {
		return l.dryApply(ctx, geom, attrs, upsert)
	}

	// Live write: one row per transaction so a failure (validation, ambiguous
	// match, constraint) rolls back only this row and the stream proceeds.
	var result rowResult
	writeErr := l.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if upsert {
			id, count, err := matchExisting(ctx, tx, l.table, rp.Profile.MatchFields, attrs)
			if err != nil {
				return err
			}
			if count > 1 {
				return errAmbiguousMatch
			}
			if count == 1 {
				if _, err := crud.UpdateFeature(ctx, tx, rp.Loaded, id, crud.UpdateFeatureInput{
					Geometry:   geom,
					Attributes: attrs,
				}); err != nil {
					return err
				}
				result = rowUpdated
				return nil
			}
		}
		newID := deterministicID(l.job.ID, row)
		if _, err := crud.CreateFeature(ctx, tx, rp.Loaded, crud.CreateFeatureInput{
			ID:         &newID,
			Subtype:    rp.SubtypeCode,
			Geometry:   geom,
			Attributes: attrs,
		}); err != nil {
			return err
		}
		result = rowInserted
		return nil
	})
	if writeErr != nil {
		if errors.Is(writeErr, errAmbiguousMatch) {
			return rowSkipped, fmt.Sprintf("row matches more than one existing feature on %v -- skipped (resolve the duplicate before re-importing)", rp.Profile.MatchFields)
		}
		return rowErrored, writeErr.Error()
	}
	return result, ""
}

// dryApply is the write-free path for a dry_run job: validate the row exactly as
// a create would, and (for upsert) report whether it would insert or update,
// without touching the target table. It backs a full-file pre-flight report --
// the same validation the committed load runs, minus the write.
func (l *loader) dryApply(ctx context.Context, geom json.RawMessage, attrs map[string]any, upsert bool) (rowResult, string) {
	rp := l.rp
	report := crud.ValidateFeature(rp.Loaded, crud.ValidateFeatureInput{
		Subtype:    rp.SubtypeCode,
		Geometry:   geom,
		Attributes: attrs,
	})
	if !report.Valid {
		return rowErrored, report.Error
	}
	if upsert {
		_, count, err := matchExisting(ctx, l.db, l.table, rp.Profile.MatchFields, attrs)
		if err != nil {
			return rowErrored, err.Error()
		}
		if count > 1 {
			return rowSkipped, fmt.Sprintf("row matches more than one existing feature on %v", rp.Profile.MatchFields)
		}
		if count == 1 {
			return rowUpdated, ""
		}
	}
	return rowInserted, ""
}

// matchExisting runs the natural-key lookup at the heart of the upsert: SELECT id
// FROM <table> WHERE each match column = its mapped value AND deleted_at IS NULL.
// It fetches up to two ids -- enough to tell "no match" (0) from "one match" (1)
// from "ambiguous" (>=2) without counting the whole table. The match field names
// are target field names (= gis column names, admin-authored, not user input) and
// are quoted via bun.Ident; the values are the already-coerced attribute values.
func matchExisting(ctx context.Context, db bun.IDB, table string, matchFields []string, attrs map[string]any) (id string, count int, err error) {
	q := db.NewSelect().
		TableExpr(table).
		Column("id").
		Where("deleted_at IS NULL")
	for _, fld := range matchFields {
		q = q.Where("? = ?", bun.Ident(fld), attrs[fld])
	}

	var ids []string
	if err := q.Limit(2).Scan(ctx, &ids); err != nil {
		return "", 0, err
	}
	if len(ids) == 1 {
		return ids[0], 1, nil
	}
	return "", len(ids), nil
}

// deterministicID derives a stable UUID for one inserted row from the job id and
// the 1-based row number, so a reclaimed/re-run job re-inserts the same ids
// (idempotent via ON CONFLICT (id) DO NOTHING) rather than duplicating rows.
func deterministicID(jobID string, row int) string {
	return uuid.NewSHA1(importNamespace, []byte(jobID+":"+strconv.Itoa(row))).String()
}

// markImportFailed records a job-level failure (empty tallies), logging if even
// that write fails.
func markImportFailed(ctx context.Context, db *bun.DB, id, msg string) {
	if err := completeImport(ctx, db, id, importTally{}, "failed", &msg); err != nil {
		log.Printf("import: job %s failed (%s) and marking failed also failed: %v", id, msg, err)
	}
}
