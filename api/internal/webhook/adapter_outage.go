package webhook

// adapter_outage.go ingests FIELD GROUND-TRUTH outage signals from an external
// system -- SCADA/DMS trips and AMI "last gasp" frames -- into the OMS, so an
// outage can appear on the board from telemetry instead of waiting for a customer
// to ring in. It is the field counterpart to call-driven prediction.
//
// One target, two `kind`s:
//   - "device_trip": a protective device reported tripped/locked out. Raises (or
//     returns the already-open) incident blamed on that device, freezing its
//     downstream supplies as interruptions -- oms.RaiseIncidentFromDevice, which
//     is idempotent per open-incident-per-device, so a re-delivered trip never
//     duplicates the outage.
//   - "last_gasp": a meter's dying power-down frame. Logs a machine trouble call
//     for its service point (oms.RecordLastGasp) -- ground truth that THIS supply
//     is dark, feeding the same intake pipeline a human call does. Deduped on an
//     open AMI call per SP so retransmits don't pile up.
//
// Device / service point correlation mirrors the operation adapter: direct refs
// (device_class+device_id / service_point_id) win; otherwise an external id
// (device_external_id / service_point_external_id) is resolved through the
// external_ref reverse index (migration 0145) using the endpoint's
// config.system_code, so the field side never needs VoltEdge uuids.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/oms"
	"github.com/uptrace/bun"
)

// outageChange is one field signal a sender expresses when the endpoint targets
// "outage". external_id is the sender's event id (echoed back for correlation).
// kind selects the handler; the device fields apply to a device_trip, the service
// point fields to a last_gasp.
type outageChange struct {
	Op         string     `json:"op"`
	ExternalID string     `json:"external_id"`
	Kind       string     `json:"kind"`
	OccurredAt *time.Time `json:"occurred_at"`
	Cause      *string    `json:"cause"`

	// device_trip: the operated device, direct or by external id.
	DeviceClass      string `json:"device_class"`
	DeviceID         string `json:"device_id"`
	DeviceExternalID string `json:"device_external_id"`

	// last_gasp: the dark service point, direct or by external id.
	ServicePointID         string `json:"service_point_id"`
	ServicePointExternalID string `json:"service_point_external_id"`
}

type outageAdapter struct{}

func (outageAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch outageChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid outage change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every outage change needs an external_id (the event id)")
	}
	if err := requirePerm(ctx, auth.PermOMSWrite); err != nil {
		return itemResult{}, err
	}
	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("outage change %q: unsupported op %q (outage supports upsert only -- a field signal is never webhook-deleted)", externalID, ch.Op)
	}

	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))

	switch strings.ToLower(strings.TrimSpace(ch.Kind)) {
	case "device_trip", "trip", "lockout":
		deviceClass, deviceID, err := resolveFeatureRef(ctx, tx, systemCode, ch.DeviceClass, ch.DeviceID, ch.DeviceExternalID, "device")
		if err != nil {
			return itemResult{}, err
		}
		inc, err := oms.RaiseIncidentFromDevice(ctx, tx, oms.RaiseFromDeviceParams{
			DeviceClass: deviceClass,
			DeviceID:    deviceID,
			Cause:       ch.Cause,
			OccurredAt:  ch.OccurredAt,
		})
		if err != nil {
			return itemResult{}, mapOMSError(err)
		}
		return itemResult{Op: "create", ExternalID: externalID, Target: "outage", EntityID: inc.ID}, nil

	case "last_gasp", "lastgasp", "power_down", "power_fail":
		_, spID, err := resolveFeatureRef(ctx, tx, systemCode, "", ch.ServicePointID, ch.ServicePointExternalID, "service point")
		if err != nil {
			return itemResult{}, err
		}
		call, err := oms.RecordLastGasp(ctx, tx, spID, ch.OccurredAt)
		if err != nil {
			return itemResult{}, mapOMSError(err)
		}
		return itemResult{Op: "create", ExternalID: externalID, Target: "outage", EntityID: call.ID}, nil

	default:
		return itemResult{}, invalid("outage change %q: unknown kind %q (want device_trip or last_gasp)", externalID, ch.Kind)
	}
}

// resolveFeatureRef resolves a feature either from a direct class+id pair or,
// failing that, from an external id through the external_ref reverse index
// (migration 0145) scoped to the endpoint's system_code. directClass is unused
// for a service point (its class is fixed), so callers pass "" there and the
// external lookup carries the only path. label names the thing for error text.
func resolveFeatureRef(ctx context.Context, tx bun.IDB, systemCode, directClass, directID, externalID, label string) (string, string, error) {
	class := strings.TrimSpace(directClass)
	id := strings.TrimSpace(directID)
	// A service point has a fixed class; accept a bare id as the direct ref.
	if id != "" && (class != "" || label == "service point") {
		if class == "" {
			class = oms.ServicePointClass
		}
		return class, id, nil
	}

	ext := strings.TrimSpace(externalID)
	if ext == "" || systemCode == "" {
		return "", "", invalid("a %s is required -- give its id (and class), or its external id with the endpoint's config.system_code set", label)
	}
	var resolved struct {
		FeatureClassCode string `bun:"feature_class_code"`
		FeatureID        string `bun:"feature_id"`
	}
	err := tx.NewSelect().
		TableExpr("runtime.external_feature_refs").
		Column("feature_class_code", "feature_id").
		Where("system_code = ? AND external_id = ?", systemCode, ext).
		Scan(ctx, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", invalid("no VoltEdge feature claims external %s id %q in system %q (stamp its external_ref first)", label, ext, systemCode)
	}
	if err != nil {
		return "", "", fmt.Errorf("webhook: %s external-ref lookup: %w", label, err)
	}
	return resolved.FeatureClassCode, resolved.FeatureID, nil
}

// mapOMSError surfaces an oms client-fixable error (e.g. an unknown device or
// service point) as the webhook package's own validation error, so the delivery
// reports it as a per-item 4xx rather than a 500. Any other error (a real
// failure) passes through untouched.
func mapOMSError(err error) error {
	if oms.IsInvalid(err) {
		return invalid("%s", err.Error())
	}
	return err
}
