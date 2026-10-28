package oms

// mirror.go is the READ-ONLY OMS MIRROR (migration 0181, external-mirror-integration
// design): an authoritative external OMS that OWNS its incidents pushes them to
// VoltEdge, which reflects them beside its own locally-raised incidents. A mirrored
// incident is display-only here -- its lifecycle is theirs -- so:
//
//   - MirrorIncident upserts the shadow by (origin_system, external_id). It freezes
//     NO interruptions (mirrored incidents never enter VoltEdge's reliability indices;
//     the external system owns reliability) and emits NO outbound webhook (echo
//     suppression -- a mirrored change must never loop back to its source).
//   - assertLocalIncident gates every VoltEdge-side mutation: a mirrored incident
//     can't be confirmed / dispatched / restored / cancelled / re-attributed / crewed
//     / switched here. The device status translation (their state -> our status) is
//     done by the ingest adapter via its per-endpoint status_map; this engine takes
//     an already-resolved VoltEdge status.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// MirrorIncidentParams is one authoritative external incident to reflect. Status is
// an ALREADY-TRANSLATED VoltEdge status (the adapter maps the vendor's state).
// DeviceClass/DeviceID are the VoltEdge feature the external device resolved to via
// external_ref (optional -- a mirror may carry no device). Affected counts are the
// EXTERNAL system's own numbers, shown as-is.
type MirrorIncidentParams struct {
	OriginSystem          string
	ExternalID            string
	Status                string
	OutageType            string
	DeviceClass           *string
	DeviceID              *string
	CauseCode             *string
	AffectedCustomers     *int
	AffectedServicePoints *int
	StartedAt             *time.Time
	RestoredAt            *time.Time
	ETR                   *time.Time
	Notes                 *string
}

// MirrorIncident upserts a read-only mirrored incident keyed by (origin_system,
// external_id). It does NOT freeze interruptions and does NOT emit a webhook, so a
// mirrored incident stays out of reliability and never echoes back to its source.
// Returns the shadow incident.
func MirrorIncident(ctx context.Context, db bun.IDB, p MirrorIncidentParams) (*Incident, error) {
	origin := strings.TrimSpace(p.OriginSystem)
	ext := strings.TrimSpace(p.ExternalID)
	if origin == "" || ext == "" {
		return nil, invalidf("a mirrored incident needs origin_system and external_id")
	}
	status := strings.TrimSpace(p.Status)
	if !validIncidentStatus(status) {
		return nil, invalidf("mirrored incident %q: unknown status %q (map the external state to one of predicted/scheduled/confirmed/dispatched/restored/cancelled)", ext, status)
	}
	outageType := strings.TrimSpace(p.OutageType)
	if outageType == "" {
		outageType = OutageTypeUnplanned
	}
	started := time.Now()
	if p.StartedAt != nil {
		started = *p.StartedAt
	}

	// Upsert on the mirror key. bun's On() carries the partial-index predicate so the
	// conflict target matches the unique index (0181). A repeat delta updates in place.
	inc := &Incident{
		OriginSystem:          &origin,
		ExternalID:            &ext,
		Status:                status,
		OutageType:            outageType,
		PredictedDeviceClass:  p.DeviceClass,
		PredictedDeviceID:     p.DeviceID,
		CauseCode:             p.CauseCode,
		AffectedCustomers:     p.AffectedCustomers,
		AffectedServicePoints: p.AffectedServicePoints,
		StartedAt:             started,
		RestoredAt:            p.RestoredAt,
		ETR:                   p.ETR,
		Notes:                 p.Notes,
	}
	_, err := db.NewInsert().Model(inc).
		Column("origin_system", "external_id", "status", "outage_type",
			"predicted_device_class", "predicted_device_id", "cause_code",
			"affected_customers", "affected_service_points", "started_at",
			"restored_at", "etr", "notes").
		On("CONFLICT (origin_system, external_id) WHERE origin_system IS NOT NULL AND external_id IS NOT NULL DO UPDATE").
		Set("status = EXCLUDED.status").
		Set("outage_type = EXCLUDED.outage_type").
		Set("predicted_device_class = EXCLUDED.predicted_device_class").
		Set("predicted_device_id = EXCLUDED.predicted_device_id").
		Set("cause_code = EXCLUDED.cause_code").
		Set("affected_customers = EXCLUDED.affected_customers").
		Set("affected_service_points = EXCLUDED.affected_service_points").
		Set("restored_at = EXCLUDED.restored_at").
		Set("etr = EXCLUDED.etr").
		Set("notes = EXCLUDED.notes").
		Set("updated_at = now()").
		Returning("id").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: mirroring incident %s/%s: %w", origin, ext, err)
	}
	return GetIncident(ctx, db, inc.ID)
}

// assertLocalIncident refuses a VoltEdge-side mutation on a mirrored (read-only)
// incident. Called at the top of every incident lifecycle action.
func assertLocalIncident(inc *Incident) error {
	if inc.IsMirrored() {
		return invalidf("incident %q is mirrored from %q and is read-only in VoltEdge (its lifecycle is owned by that system)", inc.ID, *inc.OriginSystem)
	}
	return nil
}

func validIncidentStatus(s string) bool {
	switch s {
	case StatusPredicted, StatusScheduled, StatusConfirmed, StatusDispatched, StatusRestored, StatusCancelled:
		return true
	}
	return false
}
