package oms

// causes.go is the incident cause/detection attribution (migration 0168): the
// coded outage-cause catalog (oms.outage_causes) and the action that refines an
// incident's WHY (cause_code + cause_status: unknown -> suspected -> confirmed)
// and corrects its HOW (detection_source + detection_detail). Cause lives on the
// incident so it is always present -- even an unattributed, unfaulted incident --
// while the detailed per-asset FMEA stays on the fault (faults.fault_modes).

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/webhookout"
	"github.com/uptrace/bun"
)

// OutageCause is one coded entry from the catalog -- the coarse, outage-level
// "why" (a family + a code), e.g. environmental / LIGHTNING.
type OutageCause struct {
	bun.BaseModel `bun:"table:oms.outage_causes,alias:oc"`

	Code      string `bun:"code,pk" json:"code"`
	Label     string `bun:"label" json:"label"`
	Family    string `bun:"family" json:"family"`
	SortOrder int    `bun:"sort_order" json:"sort_order"`
	Active    bool   `bun:"active" json:"active"`
}

// ListOutageCauses returns the active cause catalog, grouped by sort_order then
// label -- the order the picker renders.
func ListOutageCauses(ctx context.Context, db bun.IDB) ([]OutageCause, error) {
	var causes []OutageCause
	if err := db.NewSelect().Model(&causes).
		Where("active").
		Order("sort_order", "label").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing outage causes: %w", err)
	}
	return causes, nil
}

// causeExists reports whether a code is in the catalog (active or not -- a code
// that was deactivated after being assigned is still a valid stored value).
func causeExists(ctx context.Context, db bun.IDB, code string) (bool, error) {
	ok, err := db.NewSelect().Model((*OutageCause)(nil)).Where("code = ?", code).Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("oms: checking outage cause %q: %w", code, err)
	}
	return ok, nil
}

// detectionSources is the recognised set for detection_source (mirrors the CHECK
// in migration 0168). customer = phoned/typed in; ami = a meter last-gasp; scada =
// a device alarm/trip; crew = a field observation; planned = utility-initiated;
// upstream = a transmission/neighbour notification; analytics = an inferred event.
var detectionSources = map[string]bool{
	"customer": true, "ami": true, "scada": true, "crew": true,
	"planned": true, "upstream": true, "analytics": true,
}

var causeStatuses = map[string]bool{"unknown": true, "suspected": true, "confirmed": true}

// SetAttributionParams is a partial update of an incident's cause/detection: only
// the non-nil fields are applied. A CauseCode of "" clears the cause (and resets
// the status to unknown unless a status is given); a DetectionDetail of "" clears
// the detail.
type SetAttributionParams struct {
	CauseCode       *string `json:"cause_code"`
	CauseStatus     *string `json:"cause_status"`
	DetectionSource *string `json:"detection_source"`
	DetectionDetail *string `json:"detection_detail"`
}

// SetIncidentAttribution refines an incident's WHY (cause_code + cause_status) and
// corrects its HOW (detection_source + detection_detail). It is the editable
// counterpart of the stamps the raising paths set (0168): a crew finds the dig-in
// and sets cause EQUIPMENT? no -- THIRD_PARTY_DIGIN, status confirmed; or an
// operator corrects a mis-stamped source. Stamps the actor, logs an
// incident.cause event, and fires oms.incident.updated.
func SetIncidentAttribution(ctx context.Context, db *bun.DB, id string, p SetAttributionParams) (*Incident, error) {
	if p.CauseStatus != nil && !causeStatuses[*p.CauseStatus] {
		return nil, invalidf("cause_status must be one of unknown, suspected, confirmed")
	}
	if p.DetectionSource != nil && !detectionSources[strings.TrimSpace(*p.DetectionSource)] {
		return nil, invalidf("detection_source %q is not a recognised value", *p.DetectionSource)
	}
	if p.CauseCode == nil && p.CauseStatus == nil && p.DetectionSource == nil && p.DetectionDetail == nil {
		return nil, invalidf("nothing to update: give a cause_code, cause_status, detection_source or detection_detail")
	}

	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := GetIncident(ctx, tx, id); err != nil {
			return err
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).Set("updated_at = now()")
		statusSetByCause := false
		if p.CauseCode != nil {
			code := strings.TrimSpace(*p.CauseCode)
			if code == "" {
				upd = upd.Set("cause_code = NULL")
				// Clearing the cause resets certainty unless one is given explicitly.
				if p.CauseStatus == nil {
					upd = upd.Set("cause_status = ?", "unknown")
					statusSetByCause = true
				}
			} else {
				ok, err := causeExists(ctx, tx, code)
				if err != nil {
					return err
				}
				if !ok {
					return invalidf("unknown cause code %q", code)
				}
				upd = upd.Set("cause_code = ?", code)
				// Assigning a cause with no explicit certainty implies at least a guess.
				if p.CauseStatus == nil {
					upd = upd.Set("cause_status = ?", "suspected")
					statusSetByCause = true
				}
			}
		}
		if p.CauseStatus != nil && !statusSetByCause {
			upd = upd.Set("cause_status = ?", *p.CauseStatus)
		}
		if p.DetectionSource != nil {
			upd = upd.Set("detection_source = ?", strings.TrimSpace(*p.DetectionSource))
		}
		if p.DetectionDetail != nil {
			if d := strings.TrimSpace(*p.DetectionDetail); d == "" {
				upd = upd.Set("detection_detail = NULL")
			} else {
				upd = upd.Set("detection_detail = ?", d)
			}
		}
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating incident %s attribution: %w", id, err)
		}

		o, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		out = o
		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentCause, attributionNote(out)); err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// attributionNote summarises the current cause/source for the timeline entry.
func attributionNote(inc *Incident) *string {
	cause := "unknown"
	if inc.CauseCode != nil && strings.TrimSpace(*inc.CauseCode) != "" {
		cause = *inc.CauseCode
	}
	src := "—"
	if inc.DetectionSource != nil {
		src = *inc.DetectionSource
	}
	return noteOf(fmt.Sprintf("cause %s (%s) · source %s", cause, inc.CauseStatus, src))
}
