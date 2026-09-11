package fieldpack

// The field-sync apply policy: one pipeline, two personalities (Phase 23 Slice 3,
// docs/phase-23-sync-inbox.md).
//
// Slice 1 gave every captured op the same fate: land 'received', the worker applies
// it immediately. This adds the gate that decides, at capture time, whether an op
// instead lands 'pending_review' -- held for a supervisor to POST before it touches
// production (the "system-of-record / QA" personality). The decision is data:
// ops.sync_policies (migration 0044) maps a feature_class -> mode, with a sentinel
// '*' row for the global default and a built-in fallback of 'auto' when neither
// exists. So an empty table = Slice 1 behaviour, and QA mode is turned on per class
// (or globally) by writing a row -- no code change, no redeploy.

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

const (
	// syncModeAuto = the worker applies on receive (trusted field-capture; Slice 1).
	syncModeAuto = "auto"
	// syncModeReview = ops land pending_review; a supervisor posts them (QA mode).
	syncModeReview = "review"

	// syncPolicyGlobalKey is the reserved feature_class of the global-default row.
	// A real class is never named "*", so this can't collide with a class override.
	syncPolicyGlobalKey = "*"
)

// SyncPolicy is one ops.sync_policies row: the apply mode for a feature_class (or
// the global default when FeatureClass == syncPolicyGlobalKey). UpdatedBy is the
// admin who last set it (nullable -- the FK is ON DELETE SET NULL, so a departed
// admin's row keeps the policy but loses attribution).
type SyncPolicy struct {
	bun.BaseModel `bun:"table:ops.sync_policies,alias:sp"`

	FeatureClass    string    `bun:"feature_class,pk"`
	Mode            string    `bun:"mode"`
	DetectConflicts bool      `bun:"detect_conflicts"`
	UpdatedBy       *string   `bun:"updated_by"`
	UpdatedAt       time.Time `bun:"updated_at,scanonly"`
}

// SyncPolicySet is the resolved policy for a single sync request: the global default
// plus any per-class overrides, loaded once so a batch spanning several classes does
// one query, not one per op. It's read-only after loadSyncPolicy builds it. mode
// (auto|review) and detect_conflicts are independent dimensions -- a class can
// auto-apply AND still be conflict-checked (Phase 23 Slice 4).
type SyncPolicySet struct {
	def           string
	byClass       map[string]string
	detectDef     bool
	detectByClass map[string]bool
}

// ModeFor returns the apply mode governing a feature_class: its own override if it
// has one, else the global default. An op with no class (an establish_*, whose class
// column is empty) falls to the global default -- correct, since the per-class
// override addresses feature ops.
func (s *SyncPolicySet) ModeFor(class string) string {
	if class != "" {
		if m, ok := s.byClass[class]; ok {
			return m
		}
	}
	return s.def
}

// DetectFor reports whether this class's update/delete ops should be conflict-checked
// at apply time (base_version vs the current production row). Like ModeFor: the class's
// own override if set, else the global default. An op with no class (an establish_*)
// falls to the global default -- moot, since only update/delete ops are ever checked.
func (s *SyncPolicySet) DetectFor(class string) bool {
	if class != "" {
		if d, ok := s.detectByClass[class]; ok {
			return d
		}
	}
	return s.detectDef
}

// InitialStatus is the inbox status a freshly captured op of this class should land
// in: 'pending_review' under review mode (held for a post), else 'received' (the
// worker applies it). This is the whole behavioural difference between the two
// personalities -- everything downstream (worker, engines, idempotency) is identical.
func (s *SyncPolicySet) InitialStatus(class string) string {
	if s.ModeFor(class) == syncModeReview {
		return "pending_review"
	}
	return "received"
}

// loadSyncPolicy reads ops.sync_policies into a SyncPolicySet. The sentinel '*' row
// (if present) sets the global default; every other row is a per-class override. An
// empty table yields the built-in default (auto), so this is safe and cheap even
// before any policy has ever been configured.
func loadSyncPolicy(ctx context.Context, db bun.IDB) (*SyncPolicySet, error) {
	var rows []SyncPolicy
	if err := db.NewSelect().Model(&rows).Column("feature_class", "mode", "detect_conflicts").Scan(ctx); err != nil {
		return nil, err
	}
	set := &SyncPolicySet{
		def:           syncModeAuto,
		byClass:       make(map[string]string, len(rows)),
		detectDef:     false,
		detectByClass: make(map[string]bool, len(rows)),
	}
	for _, row := range rows {
		if row.FeatureClass == syncPolicyGlobalKey {
			set.def = row.Mode
			set.detectDef = row.DetectConflicts
			continue
		}
		set.byClass[row.FeatureClass] = row.Mode
		set.detectByClass[row.FeatureClass] = row.DetectConflicts
	}
	return set, nil
}

// listSyncPolicies returns every configured policy row (global + per-class),
// newest-set first, for the admin policy view. Absence of a '*' row means the
// caller should present the global default as the built-in 'auto'.
func listSyncPolicies(ctx context.Context, db bun.IDB) ([]SyncPolicy, error) {
	var rows []SyncPolicy
	err := db.NewSelect().Model(&rows).
		Column("feature_class", "mode", "detect_conflicts", "updated_by", "updated_at").
		Order("updated_at DESC").
		Scan(ctx)
	return rows, err
}

// setSyncPolicy upserts the mode for a feature_class (or the '*' global default),
// stamping the admin who set it. Re-setting an existing class is an in-place update
// (ON CONFLICT), so there's only ever one row per class.
func setSyncPolicy(ctx context.Context, db bun.IDB, featureClass, mode string, detectConflicts bool, updatedBy string) error {
	row := &SyncPolicy{
		FeatureClass:    featureClass,
		Mode:            mode,
		DetectConflicts: detectConflicts,
		UpdatedBy:       &updatedBy,
	}
	_, err := db.NewInsert().Model(row).
		On("CONFLICT (feature_class) DO UPDATE").
		Set("mode = EXCLUDED.mode").
		Set("detect_conflicts = EXCLUDED.detect_conflicts").
		Set("updated_by = EXCLUDED.updated_by").
		Set("updated_at = now()").
		Exec(ctx)
	return err
}

// deleteSyncPolicy removes a class's override (or the global-default row), reverting
// it to the inherited mode -- a per-class row falls back to the global default, and
// removing the '*' row falls back to the built-in 'auto'. Returns whether a row was
// actually removed (false = there was no such policy to clear).
func deleteSyncPolicy(ctx context.Context, db bun.IDB, featureClass string) (bool, error) {
	res, err := db.NewDelete().Model((*SyncPolicy)(nil)).
		Where("feature_class = ?", featureClass).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
