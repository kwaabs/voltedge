package export

// Asset-register jobs on the shared ops.export_jobs queue (migration 0153).
//
// The BoQ asset register reuses this package's whole async machinery -- queue,
// SKIP LOCKED claim, heartbeat lease, poison reaper and retention sweep -- rather
// than duplicating it. The one thing that differs is the work: instead of
// encoding geometry, the worker builds a per-feature .xlsx. That build lives in
// the boq package, which imports this one; to keep the dependency one-way (and
// avoid an import cycle), the build is INJECTED as a function rather than
// imported -- cmd/exportworker calls SetAssetRegisterBuilder(boq.Build...) at
// startup, and runClaimedJob dispatches to it for kind="asset_register".

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

// xlsxContentType is the OOXML spreadsheet MIME type a register artifact is
// stored and served under.
const xlsxContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// AssetRegisterBuilder builds the register .xlsx for a job's stored scope,
// returning the bytes, a download filename, and the row count (recorded as the
// job's feature_count). The ctx it receives already carries the requesting user
// (the worker reconstructs and injects them), so a scoped build reads exactly
// what the requester may see.
type AssetRegisterBuilder func(ctx context.Context, db bun.IDB, scope json.RawMessage) (data []byte, filename string, count int, err error)

// assetRegisterBuilder is the injected builder (nil until a process wires one).
// The API server never runs the worker, so only cmd/exportworker sets this.
var assetRegisterBuilder AssetRegisterBuilder

// SetAssetRegisterBuilder installs the function the worker calls to build an
// asset-register job. Called once at worker startup with boq.BuildAssetRegisterXLSXForJob.
func SetAssetRegisterBuilder(b AssetRegisterBuilder) { assetRegisterBuilder = b }

// runAssetRegisterJob builds one claimed asset-register job and stores it in
// RustFS. Unlike the streaming geometry path it materialises the .xlsx in memory
// (a tabular register, bounded at the engine's row cap) and uploads it whole,
// then marks the job succeeded. The requesting user is reconstructed and injected
// so the build is scoped identically to how the requester would see it
// synchronously.
func runAssetRegisterJob(ctx context.Context, db *bun.DB, store *ObjectStore, job *ExportJob) {
	if assetRegisterBuilder == nil {
		markFailed(ctx, db, job.ID, "this worker has no asset-register builder configured")
		return
	}

	// Reconstruct the requester's full authenticated snapshot (perms + territory
	// scope + draft-visibility subordinates) and run the build under it, so the
	// detached worker enforces exactly the requester's read scope -- the same
	// reason the geometry path freezes its authz into the scope.
	user, err := auth.LoadSessionUser(ctx, db, job.RequestedBy)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("resolving the requesting user: %v", err))
		return
	}
	buildCtx := auth.WithUser(ctx, user)

	data, filename, count, err := assetRegisterBuilder(buildCtx, db, job.Scope)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("building the asset register: %v", err))
		return
	}

	key := assetRegisterObjectKey(job.ID, filename)
	size, err := store.Upload(ctx, key, bytes.NewReader(data), xlsxContentType)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("uploading the asset register: %v", err))
		return
	}

	if err := completeExport(ctx, db, job.ID, key, size, count); err != nil {
		// The object is uploaded but its row didn't record it; the retention sweep
		// reaps aged terminal jobs' keys, so a stuck 'running' row is the only
		// residue. Nothing to serve, so log.
		logx.From(ctx).Error("asset register uploaded but marking complete failed", "err", err)
	}
}

// assetRegisterObjectKey is the RustFS key a register artifact lands under: a
// readable prefix, the job id (so keys never collide and one job maps to one
// object), and the human filename (so the download and the store browser both
// show it).
func assetRegisterObjectKey(jobID, filename string) string {
	return "asset-registers/" + jobID + "/" + filename
}
