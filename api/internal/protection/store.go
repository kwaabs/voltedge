// Package protection is the per-device protection (relay) settings register
// (migration 0185): the as-built overcurrent settings -- pickups, time dials,
// instantaneous, CT ratio, reclose scheme -- that a TCC curve alone does not
// capture. One row per protective device, UPSERTed on (feature_class, feature_id).
package protection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Settings mirrors one ops.protection_settings row. The numeric fields are
// pointers so "unset" is distinct from zero.
type Settings struct {
	bun.BaseModel `bun:"table:ops.protection_settings,alias:ps"`

	ID               string `bun:"id,pk" json:"id"`
	FeatureClassCode string `bun:"feature_class_code" json:"feature_class_code"`
	FeatureID        string `bun:"feature_id" json:"feature_id"`

	RelayModel *string `bun:"relay_model" json:"relay_model"`
	CTRatio    *string `bun:"ct_ratio" json:"ct_ratio"`

	PhasePickupA  *float64 `bun:"phase_pickup_a" json:"phase_pickup_a"`
	PhaseTimeDial *float64 `bun:"phase_time_dial" json:"phase_time_dial"`
	PhaseCurve    *string  `bun:"phase_curve" json:"phase_curve"`
	PhaseInstA    *float64 `bun:"phase_instantaneous_a" json:"phase_instantaneous_a"`

	GroundPickupA  *float64 `bun:"ground_pickup_a" json:"ground_pickup_a"`
	GroundTimeDial *float64 `bun:"ground_time_dial" json:"ground_time_dial"`
	GroundCurve    *string  `bun:"ground_curve" json:"ground_curve"`
	GroundInstA    *float64 `bun:"ground_instantaneous_a" json:"ground_instantaneous_a"`

	RecloseOperations *int    `bun:"reclose_operations" json:"reclose_operations"`
	RecloseIntervals  *string `bun:"reclose_intervals" json:"reclose_intervals"`

	// Source is the provenance of this row's LAST write: "manual" for a register
	// UI edit, the external system's code for a webhook-ingested one (migration
	// 0187). Null on pre-0187 rows.
	Source *string `bun:"source" json:"source"`

	Notes     *string   `bun:"notes" json:"notes"`
	UpdatedBy *string   `bun:"updated_by" json:"updated_by,omitempty"`
	CreatedAt time.Time `bun:"created_at,scanonly" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,scanonly" json:"updated_at"`
}

// ProtectionDeviceClasses is the set of feature classes a protection-settings
// record may attach to -- protective and switching devices (the union of OMS's
// switching + protective device sets, plus the relay). A deployment modelling
// protection differently can override it. Anything outside it (a transformer, a
// conductor, a service point) has no protection function to configure, so the
// register refuses it -- both a typo'd class and a pasted id that belongs to the
// wrong kind of feature.
var ProtectionDeviceClasses = []string{
	"MV_BREAKER", "MV_RECLOSER", "MV_FUSE", "MV_SWITCH",
	"MV_ISOLATOR", "MV_SECTIONALIZER", "LV_SWITCH", "PROTECTIVE_RELAY",
}

// IsProtectionClass reports whether a feature class can carry protection settings.
func IsProtectionClass(code string) bool {
	return slices.Contains(ProtectionDeviceClasses, code)
}

// UpsertParams carries the settings to write for one device. The device ref is
// required; every setting is optional (a partially-known relay is normal).
type UpsertParams struct {
	FeatureClassCode  string
	FeatureID         string
	RelayModel        *string
	CTRatio           *string
	PhasePickupA      *float64
	PhaseTimeDial     *float64
	PhaseCurve        *string
	PhaseInstA        *float64
	GroundPickupA     *float64
	GroundTimeDial    *float64
	GroundCurve       *string
	GroundInstA       *float64
	RecloseOperations *int
	RecloseIntervals  *string
	Notes             *string
	// Source is the provenance to stamp: "manual" (UI) or an external system_code
	// (webhook ingestion). Empty/nil leaves source null.
	Source *string
}

// List returns every device's settings, most-recently-updated first.
func List(ctx context.Context, db bun.IDB) ([]Settings, error) {
	var rows []Settings
	if err := db.NewSelect().Model(&rows).Order("updated_at DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("protection: listing settings: %w", err)
	}
	return rows, nil
}

// GetByFeature returns one device's settings, or notFound when the device has
// none recorded yet.
func GetByFeature(ctx context.Context, db bun.IDB, class, id string) (*Settings, error) {
	s := new(Settings)
	err := db.NewSelect().Model(s).
		Where("feature_class_code = ?", class).
		Where("feature_id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("no protection settings recorded for %s/%s", class, id)
	}
	if err != nil {
		return nil, fmt.Errorf("protection: reading settings: %w", err)
	}
	return s, nil
}

// Upsert writes (or replaces) one device's settings, keyed on the (class, id)
// unique index, and returns the stored row.
func Upsert(ctx context.Context, db bun.IDB, p UpsertParams) (*Settings, error) {
	class := strings.TrimSpace(p.FeatureClassCode)
	id := strings.TrimSpace(p.FeatureID)
	if class == "" || id == "" {
		return nil, invalid("feature_class_code and feature_id are required")
	}
	// Guard the register's domain: settings only make sense on a protective /
	// switching device, never on a transformer, conductor or the like. This bars a
	// bad write even when it comes straight at the API, not just the picker.
	if !IsProtectionClass(class) {
		return nil, invalid("%s is not a protective/switching device class -- protection settings attach only to: %s", class, strings.Join(ProtectionDeviceClasses, ", "))
	}

	row := &Settings{
		FeatureClassCode:  class,
		FeatureID:         id,
		RelayModel:        p.RelayModel,
		CTRatio:           p.CTRatio,
		PhasePickupA:      p.PhasePickupA,
		PhaseTimeDial:     p.PhaseTimeDial,
		PhaseCurve:        p.PhaseCurve,
		PhaseInstA:        p.PhaseInstA,
		GroundPickupA:     p.GroundPickupA,
		GroundTimeDial:    p.GroundTimeDial,
		GroundCurve:       p.GroundCurve,
		GroundInstA:       p.GroundInstA,
		RecloseOperations: p.RecloseOperations,
		RecloseIntervals:  p.RecloseIntervals,
		Notes:             p.Notes,
		Source:            p.Source,
	}
	if actor := auth.ActorID(ctx); actor != "" {
		row.UpdatedBy = &actor
	}

	// ExcludeColumn("id") so the DB default fills the pk; created_at/updated_at are
	// scanonly (never written here). On conflict, replace every setting + stamp.
	if _, err := db.NewInsert().Model(row).
		ExcludeColumn("id").
		On("CONFLICT (feature_class_code, feature_id) DO UPDATE").
		Set("relay_model = EXCLUDED.relay_model, ct_ratio = EXCLUDED.ct_ratio, " +
			"phase_pickup_a = EXCLUDED.phase_pickup_a, phase_time_dial = EXCLUDED.phase_time_dial, " +
			"phase_curve = EXCLUDED.phase_curve, phase_instantaneous_a = EXCLUDED.phase_instantaneous_a, " +
			"ground_pickup_a = EXCLUDED.ground_pickup_a, ground_time_dial = EXCLUDED.ground_time_dial, " +
			"ground_curve = EXCLUDED.ground_curve, ground_instantaneous_a = EXCLUDED.ground_instantaneous_a, " +
			"reclose_operations = EXCLUDED.reclose_operations, reclose_intervals = EXCLUDED.reclose_intervals, " +
			"source = EXCLUDED.source, notes = EXCLUDED.notes, updated_by = EXCLUDED.updated_by, updated_at = now()").
		Returning("*").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("protection: upserting settings: %w", err)
	}
	return row, nil
}

// DeleteByFeature removes one device's settings. A missing record is not an error
// (idempotent delete).
func DeleteByFeature(ctx context.Context, db bun.IDB, class, id string) error {
	if _, err := db.NewDelete().Model((*Settings)(nil)).
		Where("feature_class_code = ?", class).
		Where("feature_id = ?", id).
		Exec(ctx); err != nil {
		return fmt.Errorf("protection: deleting settings: %w", err)
	}
	return nil
}
