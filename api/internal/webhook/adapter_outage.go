package webhook

// adapter_outage.go ingests FIELD GROUND-TRUTH outage signals from an external
// system -- SCADA/DMS trips and AMI "last gasp" frames -- into the OMS, so an
// outage can appear on the board from telemetry instead of waiting for a customer
// to ring in. It is the field counterpart to call-driven prediction.
//
// One target, three `kind`s:
//   - "device_trip": a protective device reported tripped/locked out. Raises (or
//     returns the already-open) incident blamed on that device, freezing its
//     downstream supplies as interruptions -- oms.RaiseIncidentFromDevice, which
//     is idempotent per open-incident-per-device, so a re-delivered trip never
//     duplicates the outage.
//   - "last_gasp": a meter's dying power-down frame. Logs a machine trouble call
//     for its service point (oms.RecordLastGasp) -- ground truth that THIS supply
//     is dark, feeding the same intake pipeline a human call does. Deduped on an
//     open AMI call per SP so retransmits don't pile up.
//   - "customer_report" (IVR/CIS, blocker #5): a customer outage report filed
//     through an external automated phone system / customer-information system.
//     Logs a trouble call (oms.RecordCustomerReport) carrying the caller's premise
//     id and callback number, so the restoration callback queue can reach them.
//     Idempotent on the CIS report id (caller_ref), defaulting to the event's
//     external_id. The human-report counterpart to last_gasp (AMI).
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

	// customer_report (IVR/CIS): a customer outage report. Locator is a service
	// point (direct/external) OR an address OR a lat/lng. caller_ref is the CIS's
	// own report id (idempotency key); account_ref / phone carry the premise id and
	// callback number; call_type is no_supply (default) / partial / flicker.
	CallerRef  string   `json:"caller_ref"`
	AccountRef string   `json:"account_ref"`
	Phone      string   `json:"phone"`
	Address    string   `json:"address"`
	Longitude  *float64 `json:"longitude"`
	Latitude   *float64 `json:"latitude"`
	CallType   string   `json:"call_type"`
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

	case "customer_report", "ivr", "cis", "customer_call":
		// A customer outage report filed through an external IVR/CIS. Resolve the
		// service point when one is named (direct or by external id); otherwise the
		// address / lat-lng locator carries it (RecordCustomerReport enforces that at
		// least one locator is present). caller_ref defaults to the event's
		// external_id so a sender that doesn't repeat it still gets idempotency.
		var spRef *string
		if strings.TrimSpace(ch.ServicePointID) != "" || strings.TrimSpace(ch.ServicePointExternalID) != "" {
			_, spID, err := resolveFeatureRef(ctx, tx, systemCode, "", ch.ServicePointID, ch.ServicePointExternalID, "service point")
			if err != nil {
				return itemResult{}, err
			}
			spRef = &spID
		}
		callerRef := defaulted(strings.TrimSpace(ch.CallerRef), externalID)
		call, err := oms.RecordCustomerReport(ctx, tx, oms.CustomerReportParams{
			ServicePointID: spRef,
			Address:        trimToPtr(ch.Address),
			Longitude:      ch.Longitude,
			Latitude:       ch.Latitude,
			CallerRef:      callerRef,
			AccountRef:     trimToPtr(ch.AccountRef),
			Phone:          trimToPtr(ch.Phone),
			CallType:       strings.TrimSpace(ch.CallType),
			ReportedAt:     ch.OccurredAt,
		})
		if err != nil {
			return itemResult{}, mapOMSError(err)
		}
		return itemResult{Op: "create", ExternalID: externalID, Target: "outage", EntityID: call.ID}, nil

	default:
		return itemResult{}, invalid("outage change %q: unknown kind %q (want device_trip, last_gasp or customer_report)", externalID, ch.Kind)
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

// trimToPtr returns a trimmed *string, or nil when the input is blank -- so an
// absent optional field (address / account_ref / phone) is passed as nil, not an
// empty string the intake would treat as present.
func trimToPtr(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	return &t
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
