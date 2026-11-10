package deviceops

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/alarms"
)

// alarms.go feeds the operator alarm annunciator (api/internal/alarms) from the two
// device-side sources: an alarm-worthy device OPERATION (event alarm) and the standing
// OFF-NORMAL configuration (standing alarm). deviceops imports alarms, never the
// reverse; main.go injects ReconcileAbnormalAlarms as the annunciator's standing
// reconciler (alarms.SetStandingReconciler) so /alarms/reconcile can trigger it.

// alarmFor maps an operation type to an alarm spec; ok=false for the op types that are
// routine switching (reclose/open/close/other), not a condition needing acknowledgement.
func alarmFor(opType string) (severity, category, label string, ok bool) {
	switch opType {
	case "lockout":
		return alarms.SeverityHigh, "protection", "Protection lockout", true
	case "earth":
		return alarms.SeverityHigh, "safety", "Earth / ground applied", true
	case "alarm":
		return alarms.SeverityMedium, "device", "Device alarm", true
	case "trip":
		return alarms.SeverityLow, "protection", "Protection trip", true
	default:
		return "", "", "", false
	}
}

// SyncFromOperation raises an EVENT alarm for an alarm-worthy device operation so it
// appears on the annunciator until an operator acknowledges it. Called from Record for
// every new operation (any source -- crew, SCADA webhook, telemetry, switching), in the
// op's own transaction, so an alarm and its operation commit or roll back together.
// Routine switching (reclose/open/close) raises nothing. The fingerprint is per
// (op_type, device), so a repeat operation re-alarms and bumps the count rather than
// piling up duplicates.
func SyncFromOperation(ctx context.Context, db bun.IDB, op *Operation) error {
	if op == nil {
		return nil
	}
	severity, category, label, ok := alarmFor(op.OpType)
	if !ok {
		return nil
	}
	msg := fmt.Sprintf("%s on %s %s", label, op.DeviceClass, shortID(op.DeviceID))
	if op.Cause != nil && *op.Cause != "" {
		msg += " (" + *op.Cause + ")"
	}
	return alarms.Raise(ctx, db, alarms.RaiseInput{
		Fingerprint: fmt.Sprintf("deviceop:%s:%s:%s", op.OpType, op.DeviceClass, op.DeviceID),
		Source:      "device_op",
		Category:    category,
		Severity:    severity,
		Message:     msg,
		DeviceClass: op.DeviceClass,
		DeviceID:    op.DeviceID,
		Standing:    false,
	})
}

// ReconcileAbnormalAlarms makes the STANDING off-normal-configuration alarms match the
// live set: every switching device currently off its normal position (Abnormal) is a
// standing alarm; one that returns to normal auto-clears. Reuses the abnormal-config
// scan; wired in main.go as alarms.SetStandingReconciler, and called after any operation.
func ReconcileAbnormalAlarms(ctx context.Context, db *bun.DB) error {
	devices, err := Abnormal(ctx, db, AbnormalFilter{})
	if err != nil {
		return err
	}
	items := make([]alarms.RaiseInput, 0, len(devices))
	for _, d := range devices {
		items = append(items, alarms.RaiseInput{
			Fingerprint: fmt.Sprintf("abnormal_config:%s:%s", d.DeviceClass, d.DeviceID),
			Source:      "abnormal_config",
			Category:    "configuration",
			Severity:    alarms.SeverityMedium,
			Message:     fmt.Sprintf("%s %s is %s (normally %s)", d.DeviceClass, shortID(d.DeviceID), d.DeviceState, normalWord(d.NormalState)),
			DeviceClass: d.DeviceClass,
			DeviceID:    d.DeviceID,
			Standing:    true,
		})
	}
	return alarms.ReconcileStanding(ctx, db, "abnormal_config", items)
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func normalWord(s string) string {
	switch s {
	case "NORMALLY_OPEN":
		return "open"
	case "NORMALLY_CLOSED":
		return "closed"
	default:
		return s
	}
}
