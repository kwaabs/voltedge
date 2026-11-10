package export

// Named CIM sync targets (ops.cim_sync_targets, migration 0196): the per-consumer
// state for incremental (difference-model) CIM export. A target defines a scope +
// profile and remembers the BASELINE it last received (the object key + identity of
// a full model). A "run" builds the current full model, diffs it against that
// baseline (cimdiff.go), emits a CIM difference model, and advances the baseline.

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// publishedOnlyMarker forces a sync export to published-only. The private-drafts
// filter (crud/export.go) keeps "public OR authored-by one of {draftAuthors}"; a
// marker that no feature's created_by can equal (the nil uuid) collapses that to
// "public only" -- gate-independent, and correct for feeding an external system a
// stable, authoritative model rather than anyone's in-progress drafts.
const publishedOnlyMarker = "00000000-0000-0000-0000-000000000000"

// CIMBaselineBucket is the SEPARATE object-store bucket CIM sync baselines live in,
// so the exports retention sweep (which only visits ops.export_jobs) never deletes
// them. Wired at startup via SetBaselineStore(NewObjectStoreForBucket(...)).
const CIMBaselineBucket = "voltedge-cim-baselines"

// baselineStore holds CIM sync baselines in a SEPARATE object-store bucket so the
// export retention sweep (which only visits ops.export_jobs) never deletes them.
// Injected at startup via SetBaselineStore (mirrors SetAssetRegisterBuilder). nil =
// sync export not configured -> a run fails with a clear error rather than losing
// the baseline.
var baselineStore *ObjectStore

// SetBaselineStore wires the baseline object store (export.NewObjectStoreForBucket).
func SetBaselineStore(s *ObjectStore) { baselineStore = s }

// SyncTarget mirrors one ops.cim_sync_targets row.
type SyncTarget struct {
	bun.BaseModel `bun:"table:ops.cim_sync_targets,alias:cim_sync_targets"`

	ID          string  `bun:"id,pk,default:gen_random_uuid()"`
	Name        string  `bun:"name"`
	Description *string `bun:"description"`
	Active      bool    `bun:"active"`

	FeatureClasses []string  `bun:"feature_classes,array"`
	Bbox           []float64 `bun:"bbox,type:jsonb"`
	TerritoryIDs   []string  `bun:"territory_ids,array"`
	CimProfile     *string   `bun:"cim_profile"`
	TargetSRID     *int      `bun:"target_srid"`

	// Schedule is the auto-run cadence (hourly|daily|weekly); NULL/other = manual
	// (migration 0197). The export worker's maintenance loop enqueues a run when a
	// scheduled target's last success is older than this.
	Schedule *string `bun:"schedule"`

	BaselineObjectKey    *string    `bun:"baseline_object_key"`
	BaselineModelMRID    *string    `bun:"baseline_model_mrid"`
	BaselineFeatureCount *int       `bun:"baseline_feature_count"`
	LastExportedAt       *time.Time `bun:"last_exported_at"`
	LastJobID            *string    `bun:"last_job_id"`

	CreatedBy *string   `bun:"created_by"`
	CreatedAt time.Time `bun:"created_at,nullzero"`
	UpdatedAt time.Time `bun:"updated_at,nullzero"`
}

// scopeForTarget builds the published-only CIM ExportScope a run encodes from.
func scopeForTarget(t *SyncTarget) ExportScope {
	s := ExportScope{
		Format:         "cim",
		FeatureClasses: t.FeatureClasses,
		TerritoryIDs:   t.TerritoryIDs, // nil = no territory filter; explicit = those ids
		SyncTargetID:   &t.ID,
		DraftAuthors:   []string{publishedOnlyMarker},
	}
	if len(t.Bbox) == 4 {
		s.Bbox = t.Bbox
	}
	if t.CimProfile != nil {
		s.CimProfile = *t.CimProfile
	}
	if t.TargetSRID != nil {
		s.TargetSRID = t.TargetSRID
	}
	return s
}

func listSyncTargets(ctx context.Context, db bun.IDB) ([]SyncTarget, error) {
	var out []SyncTarget
	if err := db.NewSelect().Model(&out).Order("name").Scan(ctx); err != nil {
		return nil, fmt.Errorf("export: listing sync targets: %w", err)
	}
	return out, nil
}

func getSyncTarget(ctx context.Context, db bun.IDB, id string) (*SyncTarget, error) {
	t := new(SyncTarget)
	if err := db.NewSelect().Model(t).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

// advanceBaseline records the full model just delivered as the target's new
// baseline. Called only AFTER the difference artifact is confirmed delivered, so a
// failure between delivery and here leaves the old baseline (the next run re-emits
// the same delta, which is idempotent to re-apply) rather than skipping it.
func advanceBaseline(ctx context.Context, db bun.IDB, targetID, objectKey, modelMRID string, featureCount int, jobID string) error {
	_, err := db.NewUpdate().Model((*SyncTarget)(nil)).
		Set("baseline_object_key = ?", objectKey).
		Set("baseline_model_mrid = ?", modelMRID).
		Set("baseline_feature_count = ?", featureCount).
		Set("last_exported_at = now()").
		Set("last_job_id = ?", jobID).
		Set("updated_at = now()").
		Where("id = ?", targetID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("export: advancing sync-target baseline: %w", err)
	}
	return nil
}
