package webhook

// adapter_operation.go ingests DEVICE OPERATIONS (trips, lockouts, recloses,
// open/close) from an external system -- a SCADA/relay/DMS feed -- into the
// device-operation log (ops.operations, migration 0147) through deviceops.Record,
// the same path a crew log takes. It is the external feed for the operation log.
//
// Three things make it distinct from the work/fault adapters:
//   - No entity-ref correlation. An operation is an immutable EVENT, not an
//     upsertable entity, so there is no "find the mapped row and update it". It
//     dedupes instead on ops.operations' own (source_system, source_event_id)
//     partial unique index: a re-delivered SCADA event records nothing the second
//     time (Record returns the stored row). source_event_id is the change's
//     external_id (the sender's event id).
//   - Device correlation by external id. The sender names the operated device by
//     ITS id; device_external_id is resolved to a VoltEdge feature through the
//     external_ref reverse index (migration 0145) using the endpoint's
//     config.system_code -- so the SCADA side never needs VoltEdge uuids. Direct
//     device_class + device_id are accepted too (and win when both are given).
//   - Upsert only. An operation is never webhook-deleted (the record of a trip
//     stands); a delete op is refused.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/uptrace/bun"
)

// operationChange is one device operation a sender expresses natively when the
// endpoint targets "operation". external_id is the sender's event id -- the
// dedup key (becomes source_event_id). The device is named either directly
// (device_class + device_id) or by its external id (device_external_id, resolved
// through the external_ref index).
type operationChange struct {
	Op         string     `json:"op"`
	ExternalID string     `json:"external_id"`
	OpType     string     `json:"op_type"`
	Source     string     `json:"source"`
	OccurredAt *time.Time `json:"occurred_at"`
	Cause      *string    `json:"cause"`
	Notes      *string    `json:"notes"`

	DeviceClass      string `json:"device_class"`
	DeviceID         string `json:"device_id"`
	DeviceExternalID string `json:"device_external_id"`

	FaultID    *string `json:"fault_id"`
	IncidentID *string `json:"incident_id"`
}

type operationAdapter struct{}

func (operationAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch operationChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid operation change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every operation change needs an external_id (the event id / dedup key)")
	}
	if err := requirePerm(ctx, auth.PermOperationsWrite); err != nil {
		return itemResult{}, err
	}
	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("operation change %q: unsupported op %q (operation supports upsert only -- a logged operation is never deleted)", externalID, ch.Op)
	}

	// The external system this endpoint's ids belong to -- used both to resolve a
	// device by external id and as the operation's source_system (so dedup is
	// scoped per system). Fall back to the endpoint id when unconfigured, so dedup
	// still has a non-null system (a null source_system defeats the partial unique).
	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))
	sourceSystem := systemCode
	if sourceSystem == "" {
		sourceSystem = ep.ID
	}

	// Resolve the operated device: direct refs win; otherwise correlate the
	// external device id through the external_ref reverse index (migration 0145).
	deviceClass := strings.TrimSpace(ch.DeviceClass)
	deviceID := strings.TrimSpace(ch.DeviceID)
	if deviceClass == "" || deviceID == "" {
		devExt := strings.TrimSpace(ch.DeviceExternalID)
		if devExt == "" || systemCode == "" {
			return itemResult{}, invalid("operation change %q: a device is required -- give device_class+device_id, or device_external_id with the endpoint's config.system_code set", externalID)
		}
		var resolved struct {
			FeatureClassCode string `bun:"feature_class_code"`
			FeatureID        string `bun:"feature_id"`
		}
		err := tx.NewSelect().
			TableExpr("runtime.external_feature_refs").
			Column("feature_class_code", "feature_id").
			Where("system_code = ? AND external_id = ?", systemCode, devExt).
			Scan(ctx, &resolved)
		if errors.Is(err, sql.ErrNoRows) {
			return itemResult{}, invalid("operation change %q: no VoltEdge feature claims external device id %q in system %q (stamp its external_ref first)", externalID, devExt, systemCode)
		}
		if err != nil {
			return itemResult{}, fmt.Errorf("webhook: device external-ref lookup: %w", err)
		}
		deviceClass, deviceID = resolved.FeatureClassCode, resolved.FeatureID
	}

	occurred := time.Now()
	if ch.OccurredAt != nil {
		occurred = *ch.OccurredAt
	}

	recorded, err := deviceops.Record(ctx, tx, deviceops.RecordInput{
		DeviceClass:   deviceClass,
		DeviceID:      deviceID,
		OpType:        ch.OpType,
		Source:        defaulted(strings.ToLower(ch.Source), "scada"),
		OccurredAt:    occurred,
		Cause:         ch.Cause,
		Notes:         ch.Notes,
		FaultID:       ch.FaultID,
		IncidentID:    ch.IncidentID,
		SourceSystem:  &sourceSystem,
		SourceEventID: &externalID,
	})
	if err != nil {
		return itemResult{}, err
	}
	return itemResult{Op: "create", ExternalID: externalID, Target: "operation", EntityID: recorded.ID}, nil
}
