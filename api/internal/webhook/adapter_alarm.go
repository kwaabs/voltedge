package webhook

// adapter_alarm.go ingests ALARM POINTS directly from an external system (a SCADA /
// EMS / DMS alarm feed) into the operator annunciator (ops.alarms, migration 0198) --
// for conditions that are NOT device operations: a measurement limit violation, a
// comms failure, a station status alarm. Operation-shaped signals (a trip, a lockout)
// come through the "operation" target instead, which already raises an alarm via
// deviceops.Record; this is the complement for everything SCADA alarms that an
// operation log would not capture.
//
// Each alarm is keyed by the sender's external_id, scoped per system, so re-raising the
// same point updates one row (bumping its count) and the sender signals return-to-normal
// with op="clear". The device is OPTIONAL -- a system/comms alarm need name none -- and
// may be given directly (device_class+device_id) or by external id, resolved through the
// external_ref reverse index like the operation adapter.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/alarms"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// alarmChange is one alarm point a sender expresses when the endpoint targets "alarm".
// external_id is the sender's alarm id -- the stable key (raise updates it, clear
// targets it). severity is critical|high|medium|low (default medium).
type alarmChange struct {
	Op               string `json:"op"`          // "raise" (default) | "clear"
	ExternalID       string `json:"external_id"` // the alarm point id / dedup + clear key
	Severity         string `json:"severity"`
	Category         string `json:"category"`
	Message          string `json:"message"`
	DeviceClass      string `json:"device_class"`
	DeviceID         string `json:"device_id"`
	DeviceExternalID string `json:"device_external_id"`
}

type alarmAdapter struct{}

func (alarmAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch alarmChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid alarm change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every alarm change needs an external_id (the alarm point id / dedup key)")
	}
	if err := requirePerm(ctx, auth.PermOMSWrite); err != nil {
		return itemResult{}, err
	}

	// The external system this endpoint's ids belong to: scopes the fingerprint (so two
	// systems' alarm id "17" never collide) and names the source. Fall back to the
	// endpoint id so the fingerprint is always distinct.
	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))
	source := systemCode
	if source == "" {
		source = ep.ID
	}
	fingerprint := "ext:" + source + ":" + externalID

	switch defaulted(strings.ToLower(ch.Op), "raise") {
	case "clear":
		if err := alarms.ClearByFingerprint(ctx, tx, fingerprint); err != nil {
			return itemResult{}, err
		}
		return itemResult{Op: "update", ExternalID: externalID, Target: "alarm"}, nil
	case "raise":
		// handled below
	default:
		return itemResult{}, invalid("alarm change %q: unknown op %q (want raise or clear)", externalID, ch.Op)
	}

	message := strings.TrimSpace(ch.Message)
	if message == "" {
		return itemResult{}, invalid("alarm change %q: message is required to raise", externalID)
	}
	severity := strings.ToLower(strings.TrimSpace(ch.Severity))
	switch severity {
	case "":
		severity = alarms.SeverityMedium
	case alarms.SeverityCritical, alarms.SeverityHigh, alarms.SeverityMedium, alarms.SeverityLow:
	default:
		return itemResult{}, invalid("alarm change %q: severity %q must be one of critical/high/medium/low", externalID, ch.Severity)
	}

	// Optional device correlation. Direct refs win; otherwise resolve the external device
	// id through the external_ref reverse index (migration 0145). A miss is NON-fatal --
	// a system/comms alarm need not name a VoltEdge device.
	deviceClass := strings.TrimSpace(ch.DeviceClass)
	deviceID := strings.TrimSpace(ch.DeviceID)
	if (deviceClass == "" || deviceID == "") && strings.TrimSpace(ch.DeviceExternalID) != "" && systemCode != "" {
		var resolved struct {
			FeatureClassCode string `bun:"feature_class_code"`
			FeatureID        string `bun:"feature_id"`
		}
		err := tx.NewSelect().
			TableExpr("runtime.external_feature_refs").
			Column("feature_class_code", "feature_id").
			Where("system_code = ? AND external_id = ?", systemCode, strings.TrimSpace(ch.DeviceExternalID)).
			Scan(ctx, &resolved)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// no VoltEdge feature claims it -- leave the alarm unlinked
		case err != nil:
			return itemResult{}, fmt.Errorf("webhook: alarm device external-ref lookup: %w", err)
		default:
			deviceClass, deviceID = resolved.FeatureClassCode, resolved.FeatureID
		}
	}

	if err := alarms.Raise(ctx, tx, alarms.RaiseInput{
		Fingerprint: fingerprint,
		Source:      source,
		Category:    defaulted(strings.TrimSpace(ch.Category), "external"),
		Severity:    severity,
		Message:     message,
		DeviceClass: deviceClass,
		DeviceID:    deviceID,
		Standing:    true,
	}); err != nil {
		return itemResult{}, err
	}
	return itemResult{Op: "create", ExternalID: externalID, Target: "alarm"}, nil
}
