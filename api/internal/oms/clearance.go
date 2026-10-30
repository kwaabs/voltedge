package oms

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/safety"
	"github.com/uptrace/bun"
)

// IssueClearanceFromOrder issues a permit-to-work from a switching order and
// auto-applies its safety protections (Slice 3): a DANGER tag on every device the
// order OPENS (barring its operation) and a GROUND at every point it EARTHS, all
// bound to the new clearance. This turns the isolate half of a switching order
// into the safety document that protects the crew -- open and earth the section,
// then issue, and those devices can no longer be operated (the Slice-2 interlock)
// until the clearance is SURRENDERED, which releases every tag and ground it owns.
//
// It writes a clearance plus its tags/grounds in one transaction, so a failure
// leaves nothing half-applied. Correlated to the order and its incident. A
// cancelled order can't back a clearance.
func IssueClearanceFromOrder(ctx context.Context, db *bun.DB, orderID string, workDescription, issuedTo *string) (*safety.Clearance, error) {
	order, err := GetSwitchingOrder(ctx, db, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status == SwitchingStatusCancelled {
		return nil, invalidf("switching order %q is cancelled; cannot issue a clearance from it", orderID)
	}

	reference := "PTW"
	if order.Reference != nil && *order.Reference != "" {
		reference = "PTW — " + *order.Reference
	}
	reason := fmt.Sprintf("isolated under switching order %s", orderID)

	var out *safety.Clearance
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		c, err := safety.CreateClearance(ctx, tx, safety.CreateClearanceInput{
			Reference:        reference,
			// nil for a standalone program (no incident); the clearance stands on its
			// own then, backed only by the switching order.
			IncidentID:       order.IncidentID,
			SwitchingOrderID: &orderID,
			WorkDescription:  workDescription,
			IssuedTo:         issuedTo,
		})
		if err != nil {
			return err
		}
		cid := c.ID

		for i := range order.Steps {
			s := order.Steps[i]
			if s.DeviceClass == nil || s.DeviceID == nil {
				continue
			}
			switch s.Action {
			case StepActionOpen:
				if _, err := safety.PlaceTag(ctx, tx, safety.PlaceTagInput{
					DeviceClass: *s.DeviceClass,
					DeviceID:    *s.DeviceID,
					TagType:     "danger",
					Reason:      &reason,
					ClearanceID: &cid,
				}); err != nil {
					return err
				}
			case StepActionEarth:
				if _, err := safety.ApplyGround(ctx, tx, safety.ApplyGroundInput{
					FeatureClass: *s.DeviceClass,
					FeatureID:    *s.DeviceID,
					Reason:       &reason,
					ClearanceID:  &cid,
				}); err != nil {
					return err
				}
			}
		}

		out, err = safety.GetClearance(ctx, tx, c.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
