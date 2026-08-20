package auth

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// localAuthEnabled reads the break-glass switch from iam.system_settings.
// A missing row is treated as off (fail-closed) -- ordinary local login
// stays refused unless something explicitly turned it on.
func (s *service) localAuthEnabled(ctx context.Context) (bool, error) {
	var row SystemSetting
	err := s.db.NewSelect().Model(&row).
		Where("key = ?", settingLocalAuthEnabled).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	enabled, parseErr := strconv.ParseBool(row.Value)
	if parseErr != nil {
		return false, nil
	}
	return enabled, nil
}

// setLocalAuthEnabled flips the switch and records who did it. Every
// toggle is also written to the audit log by the caller.
func (s *service) setLocalAuthEnabled(ctx context.Context, enabled bool, byUserID string) error {
	_, err := s.db.NewUpdate().Model((*SystemSetting)(nil)).
		Set("value = ?", strconv.FormatBool(enabled)).
		Set("updated_at = ?", time.Now()).
		Set("updated_by = ?", byUserID).
		Where("key = ?", settingLocalAuthEnabled).
		Exec(ctx)
	return err
}
