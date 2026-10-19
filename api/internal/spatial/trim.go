package spatial

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/uptrace/bun"
)

// TrimOrderInput raises a WAM trim work order for one vegetation-clearance breach.
// The measurements come from a scan result (VegetationEncroachments); Priority is
// derived from the breach unless set.
type TrimOrderInput struct {
	ConductorClass  string
	ConductorID     string
	VegetationClass string
	VegetationID    string
	DistanceM       float64
	ClearanceM      float64
	Species         string
	Priority        string
}

// RaiseTrimOrder creates a preventive WAM work order to trim a vegetation zone
// breaching clearance -- closing the detect-to-dispatch loop (Slice C2). The
// order's asset is the vegetation zone (what gets cut); the conductor and the
// measured breach ride in the title/description. The caller must have enforced
// wam:write (the HTTP handler and MCP tool do).
func RaiseTrimOrder(ctx context.Context, db bun.IDB, in TrimOrderInput) (*wam.WorkOrder, error) {
	if strings.TrimSpace(in.VegetationClass) == "" || strings.TrimSpace(in.VegetationID) == "" {
		return nil, invalidf("vegetation_class and vegetation_id are required")
	}

	condLabel := strings.TrimSpace(in.ConductorClass)
	if in.ConductorID != "" {
		condLabel = fmt.Sprintf("%s %s", in.ConductorClass, shortID(in.ConductorID))
	}
	subject := "vegetation"
	if s := strings.TrimSpace(in.Species); s != "" {
		subject = s
	}
	title := "Vegetation trim — clearance breach"
	if condLabel != "" {
		title = "Vegetation trim — clearance breach on " + condLabel
	}
	desc := fmt.Sprintf("%s is %.1f m from %s; clearance requires %.1f m. Trim to restore clearance.",
		subject, in.DistanceM, orDefault(condLabel, "the conductor"), in.ClearanceM)

	priority := strings.TrimSpace(in.Priority)
	if priority == "" {
		priority = trimPriority(in.ClearanceM, in.DistanceM)
	}

	vegClass, vegID := in.VegetationClass, in.VegetationID
	return wam.CreateWorkOrder(ctx, db, wam.CreateWorkOrderParams{
		WorkType:    wam.WorkTypePreventive,
		Priority:    priority,
		Title:       title,
		Description: &desc,
		AssetClass:  &vegClass,
		AssetID:     &vegID,
	})
}

// trimPriority derives a work-order priority from how badly clearance is breached:
// near-contact is urgent, a big breach (or well under 40 % of the required gap) is
// high, otherwise medium.
func trimPriority(clearanceM, distanceM float64) string {
	if distanceM <= 0.5 {
		return wam.PriorityUrgent
	}
	if (clearanceM-distanceM) >= 1.0 || (clearanceM > 0 && distanceM < clearanceM*0.4) {
		return wam.PriorityHigh
	}
	return wam.PriorityMedium
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
