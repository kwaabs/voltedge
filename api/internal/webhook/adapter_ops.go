package webhook

// This file holds the non-feature ingestion adapters -- work orders and fault
// events -- plus the small correlation helpers they share. They mirror the
// featureAdapter shape (decode a raw change, resolve the per-endpoint external_id
// to an existing entity, create or update accordingly) but drive the wam / faults
// engines instead of crud, and correlate through ops.webhook_entity_refs (0125)
// rather than webhook_feature_refs. Both run inside the delivery's transaction
// under the endpoint's service-user context.
//
// Two deliberate differences from featureAdapter:
//   - No delete op. wam/faults have no hard-delete engine path (a work order is
//     cancelled, a fault is cleared/closed -- lifecycle transitions, not
//     removal), so a webhook delete would have nothing sound to call. Both
//     adapters accept upsert only.
//   - An explicit RBAC gate. crud enforces territory write-scope itself, so the
//     feature adapter can trust it; the wam/faults engine functions enforce no
//     permission of their own (that lives in their REST middleware, which
//     ingestion bypasses), so these adapters check the service user's write
//     permission via requirePerm before touching the engine.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/faults"
	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/uptrace/bun"
)

// --- correlation helpers (ops.webhook_entity_refs) ---

// lookupEntityRef resolves the (endpoint, external_id) correlation for a
// non-feature target, returning the mapped entity id and whether one exists. A
// miss is not an error -- it just means "first sight, create".
func lookupEntityRef(ctx context.Context, tx bun.IDB, endpointID, externalID string) (*EntityRef, bool, error) {
	ref := new(EntityRef)
	err := tx.NewSelect().Model(ref).
		Where("endpoint_id = ?", endpointID).
		Where("external_id = ?", externalID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("webhook: entity ref lookup: %w", err)
	}
	return ref, true, nil
}

// recordEntityRef writes the correlation after a create so the next delivery
// carrying the same external_id updates that entity instead of duplicating it.
func recordEntityRef(ctx context.Context, tx bun.IDB, endpointID, externalID, target, entityID string) error {
	if _, err := tx.NewInsert().Model(&EntityRef{
		EndpointID: endpointID,
		ExternalID: externalID,
		Target:     target,
		EntityID:   entityID,
	}).Exec(ctx); err != nil {
		return fmt.Errorf("webhook: record entity ref: %w", err)
	}
	return nil
}

// --- work adapter ---

// workChange is one work-order create/update a sender expresses natively when the
// endpoint targets "work". external_id is the sender's stable id -- the
// correlation key that turns a repeat delivery into an update. The fields mirror
// wam.CreateWorkOrderParams / UpdateWorkOrderParams; Status and CompletionNotes
// take effect only on a repeat (update), as a brand-new order is always a draft.
type workChange struct {
	Op               string     `json:"op"`
	ExternalID       string     `json:"external_id"`
	Reference        *string    `json:"reference"`
	WorkType         string     `json:"work_type"`
	Priority         string     `json:"priority"`
	Title            string     `json:"title"`
	Description      *string    `json:"description"`
	AssetClass       *string    `json:"asset_class"`
	AssetID          *string    `json:"asset_id"`
	Longitude        *float64   `json:"longitude"`
	Latitude         *float64   `json:"latitude"`
	DefectID         *string    `json:"defect_id"`
	AssignedTo       *string    `json:"assigned_to"`
	AssignedToUserID *string    `json:"assigned_to_user_id"`
	ScheduledFor     *time.Time `json:"scheduled_for"`
	DueBy            *time.Time `json:"due_by"`
	Status           *string    `json:"status"`
	CompletionNotes  *string    `json:"completion_notes"`
}

// workAdapter ingests work orders through wam.CreateWorkOrder / UpdateWorkOrder
// -- the exact path a hand edit takes, so work-type/priority/status validation
// and the lifecycle-timestamp side effects all apply. Identity is the
// per-endpoint external_id: a first sight creates and records the mapping; a
// repeat patches the mapped order (including its status).
type workAdapter struct{}

func (workAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch workChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid work change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every work change needs an external_id (the correlation key)")
	}
	if err := requirePerm(ctx, auth.PermWAMWrite); err != nil {
		return itemResult{}, err
	}

	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("work change %q: unsupported op %q (work supports upsert only -- a work order is cancelled via status, never webhook-deleted)", externalID, ch.Op)
	}

	ref, found, err := lookupEntityRef(ctx, tx, ep.ID, externalID)
	if err != nil {
		return itemResult{}, err
	}
	if found {
		// last-writer-wins patch; the prior values live in the work order's own
		// audit trail. Only fields the sender set are written (nil = leave alone).
		wo, err := wam.UpdateWorkOrder(ctx, tx, ref.EntityID, wam.UpdateWorkOrderParams{
			Status:           ch.Status,
			Priority:         nilIfEmpty(ch.Priority),
			Title:            nilIfEmpty(ch.Title),
			Description:      ch.Description,
			AssignedTo:       ch.AssignedTo,
			AssignedToUserID: ch.AssignedToUserID,
			ScheduledFor:     ch.ScheduledFor,
			DueBy:            ch.DueBy,
			CompletionNotes:  ch.CompletionNotes,
		})
		if err != nil {
			return itemResult{}, err
		}
		return itemResult{Op: "update", ExternalID: externalID, Target: "work", EntityID: wo.ID}, nil
	}

	wo, err := wam.CreateWorkOrder(ctx, tx, wam.CreateWorkOrderParams{
		Reference:        ch.Reference,
		WorkType:         ch.WorkType,
		Priority:         ch.Priority,
		Title:            ch.Title,
		Description:      ch.Description,
		AssetClass:       ch.AssetClass,
		AssetID:          ch.AssetID,
		Longitude:        ch.Longitude,
		Latitude:         ch.Latitude,
		DefectID:         ch.DefectID,
		AssignedTo:       ch.AssignedTo,
		AssignedToUserID: ch.AssignedToUserID,
		ScheduledFor:     ch.ScheduledFor,
		DueBy:            ch.DueBy,
	})
	if err != nil {
		return itemResult{}, err
	}
	if err := recordEntityRef(ctx, tx, ep.ID, externalID, "work", wo.ID); err != nil {
		return itemResult{}, err
	}
	return itemResult{Op: "create", ExternalID: externalID, Target: "work", EntityID: wo.ID}, nil
}

// --- fault adapter ---

// faultChange is one fault-event create/update a sender expresses natively when
// the endpoint targets "fault". Fields mirror faults.CreateFaultParams /
// UpdateFaultParams. Status drives a lifecycle transition (SetFaultStatus), which
// the engine keeps separate from a findings patch, so it is applied after the
// create/update below.
type faultChange struct {
	Op                    string     `json:"op"`
	ExternalID            string     `json:"external_id"`
	Reference             *string    `json:"reference"`
	AssetClass            *string    `json:"asset_class"`
	AssetID               *string    `json:"asset_id"`
	Longitude             *float64   `json:"longitude"`
	Latitude              *float64   `json:"latitude"`
	FaultType             *string    `json:"fault_type"`
	Cause                 *string    `json:"cause"`
	FaultCurrentA         *float64   `json:"fault_current_a"`
	ProtectiveDeviceClass *string    `json:"protective_device_class"`
	ProtectiveDeviceID    *string    `json:"protective_device_id"`
	ClearingTimeMS        *float64   `json:"clearing_time_ms"`
	DetectedBy            *string    `json:"detected_by"`
	DetectedAt            *time.Time `json:"detected_at"`
	IncidentID            *string    `json:"incident_id"`
	DefectID              *string    `json:"defect_id"`
	Title                 string     `json:"title"`
	Description           *string    `json:"description"`
	Notes                 *string    `json:"notes"`
	Status                *string    `json:"status"`
}

// hasFindings reports whether a repeat delivery carries any field UpdateFault can
// patch -- so an update that only moves the status skips the empty findings patch.
func (ch faultChange) hasFindings() bool {
	return ch.Reference != nil || ch.FaultType != nil || ch.Cause != nil ||
		ch.FaultCurrentA != nil || ch.ProtectiveDeviceClass != nil ||
		ch.ProtectiveDeviceID != nil || ch.ClearingTimeMS != nil ||
		ch.DetectedBy != nil || ch.IncidentID != nil ||
		strings.TrimSpace(ch.Title) != "" || ch.Description != nil || ch.Notes != nil
}

// faultAdapter ingests fault events through faults.CreateFault / UpdateFault /
// SetFaultStatus -- the same path a hand edit takes, so the anchor rule (asset,
// location, or incident), fault-type validation, and the cleared_at side effects
// all apply. Identity is the per-endpoint external_id: a first sight creates and
// records the mapping; a repeat patches findings and/or transitions status.
type faultAdapter struct{}

func (faultAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch faultChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid fault change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every fault change needs an external_id (the correlation key)")
	}
	if err := requirePerm(ctx, auth.PermFaultsWrite); err != nil {
		return itemResult{}, err
	}

	op := defaulted(strings.ToLower(ch.Op), "upsert")
	if op != "upsert" {
		return itemResult{}, invalid("fault change %q: unsupported op %q (fault supports upsert only -- a fault is cleared/closed via status, never webhook-deleted)", externalID, ch.Op)
	}

	ref, found, err := lookupEntityRef(ctx, tx, ep.ID, externalID)
	if err != nil {
		return itemResult{}, err
	}

	resultOp := "update"
	var entityID string
	if found {
		entityID = ref.EntityID
		if ch.hasFindings() {
			if _, err := faults.UpdateFault(ctx, tx, entityID, faults.UpdateFaultParams{
				Reference:             ch.Reference,
				FaultType:             ch.FaultType,
				Cause:                 ch.Cause,
				FaultCurrentA:         ch.FaultCurrentA,
				ProtectiveDeviceClass: ch.ProtectiveDeviceClass,
				ProtectiveDeviceID:    ch.ProtectiveDeviceID,
				ClearingTimeMS:        ch.ClearingTimeMS,
				DetectedBy:            ch.DetectedBy,
				IncidentID:            ch.IncidentID,
				Title:                 nilIfEmpty(ch.Title),
				Description:           ch.Description,
				Notes:                 ch.Notes,
			}); err != nil {
				return itemResult{}, err
			}
		}
	} else {
		f, err := faults.CreateFault(ctx, tx, faults.CreateFaultParams{
			Reference:             ch.Reference,
			AssetClass:            ch.AssetClass,
			AssetID:               ch.AssetID,
			Longitude:             ch.Longitude,
			Latitude:              ch.Latitude,
			FaultType:             ch.FaultType,
			Cause:                 ch.Cause,
			FaultCurrentA:         ch.FaultCurrentA,
			ProtectiveDeviceClass: ch.ProtectiveDeviceClass,
			ProtectiveDeviceID:    ch.ProtectiveDeviceID,
			ClearingTimeMS:        ch.ClearingTimeMS,
			DetectedBy:            ch.DetectedBy,
			DetectedAt:            ch.DetectedAt,
			IncidentID:            ch.IncidentID,
			DefectID:              ch.DefectID,
			Title:                 ch.Title,
			Description:           ch.Description,
			Notes:                 ch.Notes,
		})
		if err != nil {
			return itemResult{}, err
		}
		entityID = f.ID
		if err := recordEntityRef(ctx, tx, ep.ID, externalID, "fault", entityID); err != nil {
			return itemResult{}, err
		}
		resultOp = "create"
	}

	// A status transition is a distinct engine call (SetFaultStatus keeps
	// cleared_at consistent), applied after the create/findings patch when set.
	if ch.Status != nil {
		if _, err := faults.SetFaultStatus(ctx, tx, entityID, strings.TrimSpace(*ch.Status)); err != nil {
			return itemResult{}, err
		}
	}
	return itemResult{Op: resultOp, ExternalID: externalID, Target: "fault", EntityID: entityID}, nil
}
