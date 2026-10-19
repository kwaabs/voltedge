package spatial

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Land-maintenance "due" tracking (Slice D): the proactive side of land
// management. EASEMENTs that are expired or expiring, and VEGETATION_ZONEs due
// (or overdue) for their trim cycle -- date queries over the land classes'
// attribute columns, so a legal officer renews a wayleave before it lapses and a
// vegetation program schedules its rounds. Degrades to an empty list when a class
// isn't seeded/compiled yet (the land seed is applied separately).

// col returns alias.field when the class carries that field, else a typed NULL --
// so a query works whether or not the (optional) column exists.
func col(loaded *metadata.Loaded, field, alias, pgType string) string {
	if loaded.FieldByName(field) != nil {
		return alias + "." + field
	}
	return "NULL::" + pgType
}

// loadedOrNil loads a class, returning (nil, nil) -- not an error -- when it isn't
// seeded or hasn't been compiled (no physical table), so a "due" report degrades
// to empty rather than failing on a deployment without the land classes.
func loadedOrNil(ctx context.Context, db bun.IDB, code string) (*metadata.Loaded, string, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if loaded.FeatureClass.PhysicalTable == nil || *loaded.FeatureClass.PhysicalTable == "" {
		return nil, "", nil
	}
	return loaded, *loaded.FeatureClass.PhysicalTable, nil
}

// EasementDue is one easement at or near expiry. DaysUntilExpiry is negative when
// it has already lapsed.
type EasementDue struct {
	FeatureClass    string  `json:"feature_class"`
	FeatureID       string  `json:"feature_id"`
	EasementRef     *string `json:"easement_ref,omitempty"`
	EasementType    *string `json:"easement_type,omitempty"`
	EasementStatus  *string `json:"easement_status,omitempty"`
	Grantor         *string `json:"grantor,omitempty"`
	ExpiryDate      string  `json:"expiry_date"`
	DaysUntilExpiry int     `json:"days_until_expiry"`
	Expired         bool    `json:"expired"`
}

// ExpiringEasements returns every EASEMENT whose expiry_date is within withinDays
// from today (already-expired ones included), soonest first. withinDays defaults
// to 90 and is clamped to [0, 3650].
func ExpiringEasements(ctx context.Context, db bun.IDB, withinDays int) ([]EasementDue, error) {
	if withinDays <= 0 {
		withinDays = 90
	}
	if withinDays > 3650 {
		withinDays = 3650
	}
	loaded, table, err := loadedOrNil(ctx, db, "EASEMENT")
	if err != nil {
		return nil, err
	}
	if loaded == nil || loaded.FieldByName("expiry_date") == nil {
		return []EasementDue{}, nil
	}

	query := fmt.Sprintf(`
SELECT e.id::text AS feature_id,
       %s AS easement_ref, %s AS easement_type, %s AS easement_status, %s AS grantor,
       e.expiry_date::text AS expiry_date,
       (e.expiry_date - CURRENT_DATE) AS days_until_expiry
FROM %s AS e
WHERE e.deleted_at IS NULL AND e.expiry_date IS NOT NULL
  AND e.expiry_date <= CURRENT_DATE + ?
ORDER BY e.expiry_date ASC`,
		col(loaded, "easement_ref", "e", "text"),
		col(loaded, "easement_type", "e", "text"),
		col(loaded, "easement_status", "e", "text"),
		col(loaded, "grantor", "e", "text"),
		table)

	var rows []struct {
		FeatureID       string  `bun:"feature_id"`
		EasementRef     *string `bun:"easement_ref"`
		EasementType    *string `bun:"easement_type"`
		EasementStatus  *string `bun:"easement_status"`
		Grantor         *string `bun:"grantor"`
		ExpiryDate      string  `bun:"expiry_date"`
		DaysUntilExpiry int     `bun:"days_until_expiry"`
	}
	if err := db.NewRaw(query, withinDays).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("spatial: expiring easements: %w", err)
	}
	out := make([]EasementDue, 0, len(rows))
	for _, r := range rows {
		out = append(out, EasementDue{
			FeatureClass: "EASEMENT", FeatureID: r.FeatureID,
			EasementRef: r.EasementRef, EasementType: r.EasementType,
			EasementStatus: r.EasementStatus, Grantor: r.Grantor,
			ExpiryDate: r.ExpiryDate, DaysUntilExpiry: r.DaysUntilExpiry,
			Expired: r.DaysUntilExpiry < 0,
		})
	}
	return out, nil
}

// TrimDue is one vegetation zone at or past its next trim. DaysOverdue is positive
// when the cycle has elapsed, negative when it is merely coming up.
type TrimDue struct {
	FeatureClass    string  `json:"feature_class"`
	FeatureID       string  `json:"feature_id"`
	Species         *string `json:"species,omitempty"`
	VegType         *string `json:"veg_type,omitempty"`
	Condition       *string `json:"condition,omitempty"`
	LastTrimmed     string  `json:"last_trimmed"`
	TrimCycleMonths int     `json:"trim_cycle_months"`
	DueDate         string  `json:"due_date"`
	DaysOverdue     int     `json:"days_overdue"`
}

// VegetationTrimDue returns every VEGETATION_ZONE whose next trim (last_trimmed +
// trim_cycle_months) falls within withinDays from today, most overdue first. Zones
// with no last_trimmed or no cycle are skipped (nothing to schedule from).
// withinDays defaults to 30 and is clamped to [0, 3650].
func VegetationTrimDue(ctx context.Context, db bun.IDB, withinDays int) ([]TrimDue, error) {
	if withinDays <= 0 {
		withinDays = 30
	}
	if withinDays > 3650 {
		withinDays = 3650
	}
	loaded, table, err := loadedOrNil(ctx, db, "VEGETATION_ZONE")
	if err != nil {
		return nil, err
	}
	if loaded == nil || loaded.FieldByName("last_trimmed") == nil || loaded.FieldByName("trim_cycle_months") == nil {
		return []TrimDue{}, nil
	}

	due := `(v.last_trimmed + (v.trim_cycle_months || ' months')::interval)::date`
	query := fmt.Sprintf(`
SELECT v.id::text AS feature_id, %s AS species, %s AS veg_type, %s AS condition,
       v.last_trimmed::text AS last_trimmed, v.trim_cycle_months AS trim_cycle_months,
       %s::text AS due_date,
       (CURRENT_DATE - %s) AS days_overdue
FROM %s AS v
WHERE v.deleted_at IS NULL AND v.last_trimmed IS NOT NULL AND v.trim_cycle_months IS NOT NULL
  AND %s <= CURRENT_DATE + ?
ORDER BY days_overdue DESC`,
		col(loaded, "species", "v", "text"),
		col(loaded, "veg_type", "v", "text"),
		col(loaded, "condition", "v", "text"),
		due, due, table, due)

	var rows []struct {
		FeatureID       string  `bun:"feature_id"`
		Species         *string `bun:"species"`
		VegType         *string `bun:"veg_type"`
		Condition       *string `bun:"condition"`
		LastTrimmed     string  `bun:"last_trimmed"`
		TrimCycleMonths int     `bun:"trim_cycle_months"`
		DueDate         string  `bun:"due_date"`
		DaysOverdue     int     `bun:"days_overdue"`
	}
	if err := db.NewRaw(query, withinDays).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("spatial: vegetation trim-due: %w", err)
	}
	out := make([]TrimDue, 0, len(rows))
	for _, r := range rows {
		out = append(out, TrimDue{
			FeatureClass: "VEGETATION_ZONE", FeatureID: r.FeatureID,
			Species: r.Species, VegType: r.VegType, Condition: r.Condition,
			LastTrimmed: r.LastTrimmed, TrimCycleMonths: r.TrimCycleMonths,
			DueDate: r.DueDate, DaysOverdue: r.DaysOverdue,
		})
	}
	return out, nil
}
