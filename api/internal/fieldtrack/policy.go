package fieldtrack

// Org-wide field-tracking switch (field breadcrumb audit trail). Unlike the
// private-drafts / territory switches -- which default OFF, dark until an admin
// turns them on -- this defaults ON, because location tracking is an existing,
// shipped capability. So the switch is an OPT-OUT an organisation flips when a
// jurisdiction or its own policy forbids recording staff location. All-or-nothing:
// off stops BOTH the movement trail and the located edits. Enforced two ways: the
// device reads it (from the field-login response) to stop recording and to drive
// the in-app disclosure, and the server drops any trail/waypoint write when off,
// so a stale device can't keep recording either.

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/uptrace/bun"
)

// SettingTrackingEnabled is the iam.system_settings key for the switch.
const SettingTrackingEnabled = "field_tracking_enabled"

// SettingDisclosureEnabled gates whether the field app TELLS the crew they are
// being recorded (the in-app disclosure icon + banner). Separate from tracking
// itself: an org may record and disclose (transparent), or disclose off where it
// chooses not to tell crews -- a deliberate, auditable choice the org owns, with
// its own legal weight. Defaults ON (inform by default); the org opts out.
const SettingDisclosureEnabled = "field_tracking_disclosure_enabled"

// boolSettingOnByDefault reads a boolean iam.system_settings row. A missing or
// unparseable row -- and, deliberately, a read error -- reads as ON: these are
// kill-switches over existing behaviour, so they fail OPEN. Only an explicit
// "false" turns one off.
func boolSettingOnByDefault(ctx context.Context, db bun.IDB, key string) (bool, error) {
	var value string
	err := db.NewSelect().
		ColumnExpr("value").
		TableExpr("iam.system_settings").
		Where("key = ?", key).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	enabled, perr := strconv.ParseBool(value)
	if perr != nil {
		return true, nil
	}
	return enabled, nil
}

// setBoolSetting upserts a boolean iam.system_settings row (mirrors drafts.SetEnabled).
func setBoolSetting(ctx context.Context, db bun.IDB, key string, enabled bool) error {
	value := "true"
	if !enabled {
		value = "false"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO iam.system_settings (key, value)
		VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	return err
}

// TrackingEnabled reports whether field tracking is on (default ON, fails open).
func TrackingEnabled(ctx context.Context, db bun.IDB) (bool, error) {
	return boolSettingOnByDefault(ctx, db, SettingTrackingEnabled)
}

// SetTrackingEnabled flips the org-wide tracking switch.
func SetTrackingEnabled(ctx context.Context, db bun.IDB, enabled bool) error {
	return setBoolSetting(ctx, db, SettingTrackingEnabled, enabled)
}

// DisclosureEnabled reports whether the field app discloses tracking to crews
// (default ON, fails open).
func DisclosureEnabled(ctx context.Context, db bun.IDB) (bool, error) {
	return boolSettingOnByDefault(ctx, db, SettingDisclosureEnabled)
}

// SetDisclosureEnabled flips whether crews are told they are being recorded.
func SetDisclosureEnabled(ctx context.Context, db bun.IDB, enabled bool) error {
	return setBoolSetting(ctx, db, SettingDisclosureEnabled, enabled)
}
