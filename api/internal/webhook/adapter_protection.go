package webhook

// adapter_protection.go ingests PROTECTION SETTINGS (as-built relay / overcurrent
// settings) from an authoritative external system -- a relay-setting database or
// SCADA -- into the protection-settings register (ops.protection_settings,
// migration 0185) through protection.Upsert, the same path the register UI takes.
// It is the external feed for the protection register.
//
// Like the operation adapter it correlates the DEVICE by its external id through
// the external_ref reverse index (migration 0145) using the endpoint's
// config.system_code, so the external side never needs VoltEdge uuids (direct
// device_class + device_id are accepted too, and win when both are given). Unlike
// an operation it is an upsertable ENTITY keyed on the device, so there is no
// event-dedup: a repeat for the same device REPLACES its settings (last-writer-
// wins), and a delete removes them. The ingested write is stamped source =
// system_code, so the register distinguishes it from a hand entry.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/protection"
)

// protectionSettingsChange is one device's settings a sender expresses natively
// when the endpoint targets "protection". The device is named directly
// (device_class + device_id) or by its external id (device_external_id, resolved
// through the external_ref index). external_id is an optional label for the
// result (the device is the upsert key, so no separate correlation id is needed).
// Every setting field is optional -- a partially-known relay is normal.
type protectionSettingsChange struct {
	Op               string `json:"op"`
	ExternalID       string `json:"external_id"`
	DeviceClass      string `json:"device_class"`
	DeviceID         string `json:"device_id"`
	DeviceExternalID string `json:"device_external_id"`

	RelayModel        *string  `json:"relay_model"`
	CTRatio           *string  `json:"ct_ratio"`
	PhasePickupA      *float64 `json:"phase_pickup_a"`
	PhaseTimeDial     *float64 `json:"phase_time_dial"`
	PhaseCurve        *string  `json:"phase_curve"`
	PhaseInstA        *float64 `json:"phase_instantaneous_a"`
	GroundPickupA     *float64 `json:"ground_pickup_a"`
	GroundTimeDial    *float64 `json:"ground_time_dial"`
	GroundCurve       *string  `json:"ground_curve"`
	GroundInstA       *float64 `json:"ground_instantaneous_a"`
	RecloseOperations *int     `json:"reclose_operations"`
	RecloseIntervals  *string  `json:"reclose_intervals"`
	Notes             *string  `json:"notes"`
}

type protectionSettingsAdapter struct{}

func (protectionSettingsAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch protectionSettingsChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid protection-settings change: %v", err)
	}
	// Editing the register is a feature write (same gate as the UI's PUT).
	if err := requirePerm(ctx, auth.PermFeaturesWrite); err != nil {
		return itemResult{}, err
	}
	op := defaulted(strings.ToLower(ch.Op), "upsert")

	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))
	// Correlate the device: direct refs win; else its external id via the reverse
	// index. The resolved class must be a protective/switching one -- Upsert
	// enforces that, so a ref pointing at the wrong kind of feature is refused.
	class, id, err := resolveFeatureRef(ctx, tx, systemCode, ch.DeviceClass, ch.DeviceID, ch.DeviceExternalID, "protection device")
	if err != nil {
		return itemResult{}, err
	}

	label := strings.TrimSpace(ch.ExternalID)
	if label == "" {
		label = strings.TrimSpace(ch.DeviceExternalID)
	}

	switch op {
	case "delete":
		if err := protection.DeleteByFeature(ctx, tx, class, id); err != nil {
			return itemResult{}, err
		}
		return itemResult{Op: "delete", ExternalID: label, Target: "protection", EntityID: id}, nil

	case "upsert":
		// Stamp provenance: the external system's code, falling back to the endpoint
		// id so the source is never null for an ingested row.
		source := systemCode
		if source == "" {
			source = ep.ID
		}
		s, err := protection.Upsert(ctx, tx, protection.UpsertParams{
			FeatureClassCode:  class,
			FeatureID:         id,
			Source:            &source,
			RelayModel:        ch.RelayModel,
			CTRatio:           ch.CTRatio,
			PhasePickupA:      ch.PhasePickupA,
			PhaseTimeDial:     ch.PhaseTimeDial,
			PhaseCurve:        ch.PhaseCurve,
			PhaseInstA:        ch.PhaseInstA,
			GroundPickupA:     ch.GroundPickupA,
			GroundTimeDial:    ch.GroundTimeDial,
			GroundCurve:       ch.GroundCurve,
			GroundInstA:       ch.GroundInstA,
			RecloseOperations: ch.RecloseOperations,
			RecloseIntervals:  ch.RecloseIntervals,
			Notes:             ch.Notes,
		})
		if err != nil {
			return itemResult{}, err
		}
		return itemResult{Op: "update", ExternalID: label, Target: "protection", EntityID: s.ID}, nil

	default:
		return itemResult{}, invalid("protection-settings change: unsupported op %q (upsert or delete)", ch.Op)
	}
}
