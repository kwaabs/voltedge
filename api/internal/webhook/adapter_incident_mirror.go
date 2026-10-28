package webhook

// adapter_incident_mirror.go ingests an AUTHORITATIVE external OMS's incidents as a
// READ-ONLY MIRROR (migration 0181, external-mirror-integration design). Unlike the
// `outage` target -- which turns a device-trip/last-gasp SIGNAL into VoltEdge's OWN
// incident -- this target reflects a FULL external incident (its own id, status and
// fields) into a read-only shadow that VoltEdge displays but never edits or
// re-emits. Push-only: the external system POSTs each incident delta; there is no
// pull-reconcile.
//
// Config (per endpoint):
//   - system_code (REQUIRED): the origin tag stamped on every mirrored incident and
//     the identity namespace for its external id. Also resolves the referenced
//     device through the external_ref reverse index (0145).
//   - status_map (optional): {external_state: voltedge_status} translating the
//     vendor's lifecycle into predicted/scheduled/confirmed/dispatched/restored/
//     cancelled. A state absent from the map but already a valid VoltEdge status is
//     passed through; anything else is a loud per-item error.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/oms"
	"github.com/uptrace/bun"
)

type incidentMirrorChange struct {
	Op                    string     `json:"op"`
	ExternalID            string     `json:"external_id"`
	Status                string     `json:"status"`
	OutageType            string     `json:"outage_type"`
	DeviceClass           string     `json:"device_class"`
	DeviceID              string     `json:"device_id"`
	DeviceExternalID      string     `json:"device_external_id"`
	CauseCode             *string    `json:"cause_code"`
	AffectedCustomers     *int       `json:"affected_customers"`
	AffectedServicePoints *int       `json:"affected_service_points"`
	StartedAt             *time.Time `json:"started_at"`
	RestoredAt            *time.Time `json:"restored_at"`
	ETR                   *time.Time `json:"etr"`
	Notes                 *string    `json:"notes"`
}

type incidentMirrorAdapter struct{}

func (incidentMirrorAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch incidentMirrorChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid incident change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every mirrored incident needs an external_id (the external OMS's own incident id)")
	}
	if err := requirePerm(ctx, auth.PermOMSWrite); err != nil {
		return itemResult{}, err
	}
	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("mirrored incident %q: unsupported op %q (the external OMS signals a close as an upsert carrying status=restored/cancelled; a hard delete is not supported)", externalID, ch.Op)
	}

	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))
	if systemCode == "" {
		return itemResult{}, invalid("the incident mirror needs config.system_code (the external OMS's code, used as the origin and the external-id namespace)")
	}

	status, err := mapIncidentStatus(ep, ch.Status)
	if err != nil {
		return itemResult{}, err
	}

	// Resolve the referenced device, when the delta names one. A mirror may carry no
	// device (an unattributed external incident); only resolve when a ref is present.
	var deviceClass, deviceID *string
	if strings.TrimSpace(ch.DeviceClass) != "" || strings.TrimSpace(ch.DeviceID) != "" || strings.TrimSpace(ch.DeviceExternalID) != "" {
		dc, did, err := resolveFeatureRef(ctx, tx, systemCode, ch.DeviceClass, ch.DeviceID, ch.DeviceExternalID, "device")
		if err != nil {
			return itemResult{}, err
		}
		deviceClass, deviceID = &dc, &did
	}

	inc, err := oms.MirrorIncident(ctx, tx, oms.MirrorIncidentParams{
		OriginSystem:          systemCode,
		ExternalID:            externalID,
		Status:                status,
		OutageType:            strings.TrimSpace(ch.OutageType),
		DeviceClass:           deviceClass,
		DeviceID:              deviceID,
		CauseCode:             ch.CauseCode,
		AffectedCustomers:     ch.AffectedCustomers,
		AffectedServicePoints: ch.AffectedServicePoints,
		StartedAt:             ch.StartedAt,
		RestoredAt:            ch.RestoredAt,
		ETR:                   ch.ETR,
		Notes:                 ch.Notes,
	})
	if err != nil {
		return itemResult{}, mapOMSError(err)
	}
	return itemResult{Op: "upsert", ExternalID: externalID, Target: "incident", EntityID: inc.ID}, nil
}

// mapIncidentStatus translates the vendor's lifecycle state to a VoltEdge incident
// status via the endpoint's config.status_map, falling back to a pass-through when
// the raw value is already a valid VoltEdge status, else a loud error so a
// misconfigured map is caught at ingest rather than silently storing a bad state.
func mapIncidentStatus(ep *Endpoint, external string) (string, error) {
	ext := strings.TrimSpace(external)
	if ext == "" {
		return "", invalid("a mirrored incident needs a status")
	}
	if mapped := configMapString(ep.Config, "status_map", ext); mapped != "" {
		return mapped, nil
	}
	// No explicit mapping: accept it only if it is already a VoltEdge status.
	switch strings.ToLower(ext) {
	case "predicted", "scheduled", "confirmed", "dispatched", "restored", "cancelled":
		return strings.ToLower(ext), nil
	}
	return "", invalid("mirrored incident status %q is not in this endpoint's config.status_map and is not a VoltEdge status (map it to predicted/scheduled/confirmed/dispatched/restored/cancelled)", external)
}
