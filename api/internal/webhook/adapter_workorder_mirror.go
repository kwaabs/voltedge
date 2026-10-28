package webhook

// adapter_workorder_mirror.go ingests an AUTHORITATIVE external WAM's work orders as
// a READ-ONLY MIRROR (migration 0182, the WAM sibling of the incident mirror). Unlike
// the `work` target -- which creates VoltEdge-OWNED work orders from an external feed
// -- this `workorder` target reflects a FULL external order (its own id, status and
// fields) into a read-only shadow VoltEdge displays but never edits or re-emits.
// Push-only: the external WAM POSTs each order delta; no pull-reconcile.
//
// Config (per endpoint):
//   - system_code (REQUIRED): the origin tag + the external-id namespace, also used to
//     resolve the referenced asset through the external_ref reverse index (0145).
//   - status_map (optional): {external_state: voltedge_status} translating the vendor
//     lifecycle into draft/scheduled/assigned/in_progress/completed/cancelled. A state
//     absent from the map but already a valid VoltEdge status passes through; anything
//     else is a loud per-item error.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/uptrace/bun"
)

type workOrderMirrorChange struct {
	Op              string     `json:"op"`
	ExternalID      string     `json:"external_id"`
	Reference       *string    `json:"reference"`
	WorkType        string     `json:"work_type"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	Title           string     `json:"title"`
	Description     *string    `json:"description"`
	AssetClass      string     `json:"asset_class"`
	AssetID         string     `json:"asset_id"`
	AssetExternalID string     `json:"asset_external_id"`
	ScheduledFor    *time.Time `json:"scheduled_for"`
	DueBy           *time.Time `json:"due_by"`
}

type workOrderMirrorAdapter struct{}

func (workOrderMirrorAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch workOrderMirrorChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid work-order change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every mirrored work order needs an external_id (the external WAM's own order id)")
	}
	if err := requirePerm(ctx, auth.PermWAMWrite); err != nil {
		return itemResult{}, err
	}
	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("mirrored work order %q: unsupported op %q (a close is an upsert carrying status=completed/cancelled; a hard delete is not supported)", externalID, ch.Op)
	}

	systemCode := strings.TrimSpace(configString(ep.Config, "system_code"))
	if systemCode == "" {
		return itemResult{}, invalid("the work-order mirror needs config.system_code (the external WAM's code, used as the origin and the external-id namespace)")
	}

	status, err := mapWorkOrderStatus(ep, ch.Status)
	if err != nil {
		return itemResult{}, err
	}

	// Resolve the referenced asset when the delta names one (direct class+id, or an
	// external id via external_ref). A work order may be asset-less (a survey/task).
	var assetClass, assetID *string
	if strings.TrimSpace(ch.AssetClass) != "" || strings.TrimSpace(ch.AssetID) != "" || strings.TrimSpace(ch.AssetExternalID) != "" {
		ac, aid, err := resolveFeatureRef(ctx, tx, systemCode, ch.AssetClass, ch.AssetID, ch.AssetExternalID, "asset")
		if err != nil {
			return itemResult{}, err
		}
		assetClass, assetID = &ac, &aid
	}

	wo, err := wam.MirrorWorkOrder(ctx, tx, wam.MirrorWorkOrderParams{
		OriginSystem: systemCode,
		ExternalID:   externalID,
		Reference:    ch.Reference,
		WorkType:     strings.TrimSpace(ch.WorkType),
		Status:       status,
		Priority:     strings.TrimSpace(ch.Priority),
		Title:        ch.Title,
		Description:  ch.Description,
		AssetClass:   assetClass,
		AssetID:      assetID,
		ScheduledFor: ch.ScheduledFor,
		DueBy:        ch.DueBy,
	})
	if err != nil {
		return itemResult{}, mapWAMError(err)
	}
	return itemResult{Op: "upsert", ExternalID: externalID, Target: "workorder", EntityID: wo.ID}, nil
}

// mapWorkOrderStatus translates the vendor's state to a VoltEdge work status via the
// endpoint's config.status_map, falling back to a pass-through when the raw value is
// already a valid VoltEdge status, else a loud error.
func mapWorkOrderStatus(ep *Endpoint, external string) (string, error) {
	ext := strings.TrimSpace(external)
	if ext == "" {
		return "", invalid("a mirrored work order needs a status")
	}
	if mapped := configMapString(ep.Config, "status_map", ext); mapped != "" {
		return mapped, nil
	}
	switch strings.ToLower(ext) {
	case "draft", "scheduled", "assigned", "in_progress", "completed", "cancelled":
		return strings.ToLower(ext), nil
	}
	return "", invalid("mirrored work-order status %q is not in this endpoint's config.status_map and is not a VoltEdge status (map it to draft/scheduled/assigned/in_progress/completed/cancelled)", external)
}

// mapWAMError surfaces a wam client-fixable error as the webhook package's own
// validation error, so a bad mirror item is a per-item 4xx rather than a 500.
func mapWAMError(err error) error {
	if wam.IsInvalid(err) {
		return invalid("%s", err.Error())
	}
	return err
}
