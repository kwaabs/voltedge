// Package issuework orchestrates turning a data-quality audit issue into an
// assigned, field-syncable WAM work order -- the "assign this fix to someone,
// office or field" action. It is a thin seam over two modules that don't (and
// shouldn't) know about each other: connectivity owns the issue queue, wam owns
// the assignable work order that already rides into field packs. Mirrors the
// streetlight package's cross-module pattern.
package issuework

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/wam"
)

// AssignInput is the issue to fix plus who to send it to. One of AssignedToUserID
// (an internal office/field user) or AssignedTo (a free-text external crew) is the
// usual case, but both empty is allowed -- that just raises the fix as an
// unassigned draft order linked to the issue. Priority defaults from the issue's
// severity when not overridden.
type AssignInput struct {
	IssueID          string
	AssignedToUserID string
	AssignedTo       string
	Priority         string
}

// AssignResult is the raised order and the now-linked issue.
type AssignResult struct {
	WorkOrder *wam.WorkOrder                `json:"work_order"`
	Issue     *connectivity.ValidationIssue `json:"issue"`
}

// AssignIssue raises a corrective WAM work order for an issue and links the two,
// in one transaction so a half-done assignment can't strand an orphan order. An
// issue that already carries a work order is rejected rather than double-raised.
// The issue moves to acknowledged (it's being handled); it still only RESOLVES
// when the audit reconcile confirms the feature is actually fixed -- completing
// the order is "work done", not "problem gone".
func AssignIssue(ctx context.Context, db *bun.DB, in AssignInput) (*AssignResult, error) {
	var result *AssignResult
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		wo, issue, err := assignOne(ctx, tx, in)
		if err != nil {
			return err
		}
		result = &AssignResult{WorkOrder: wo, Issue: issue}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// BulkAssignInput assigns many issues to one assignee in a single gesture.
type BulkAssignInput struct {
	IssueIDs         []string
	AssignedToUserID string
	AssignedTo       string
	Priority         string
}

// BulkAssignResult reports how the batch went: orders raised, plus ids skipped
// (already assigned, or no longer present) -- a skip is not a failure, so one
// stale id never sinks the whole batch.
type BulkAssignResult struct {
	Assigned   int              `json:"assigned"`
	Skipped    int              `json:"skipped"`
	WorkOrders []*wam.WorkOrder `json:"work_orders,omitempty"`
}

// AssignIssues assigns a batch of issues to one user in ONE transaction -- the
// server-side form of the queue's "Assign N to…", replacing N client round-trips.
// An id that's already assigned or no longer exists is skipped (counted, not
// fatal); any real error (a bad work-order param) rolls the whole batch back.
func AssignIssues(ctx context.Context, db *bun.DB, in BulkAssignInput) (*BulkAssignResult, error) {
	res := &BulkAssignResult{}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, id := range in.IssueIDs {
			wo, _, err := assignOne(ctx, tx, AssignInput{
				IssueID:          id,
				AssignedToUserID: in.AssignedToUserID,
				AssignedTo:       in.AssignedTo,
				Priority:         in.Priority,
			})
			if err != nil {
				// Already-assigned / missing is detected by a read before any write,
				// so the tx stays clean and we can skip the id and carry on.
				if IsInvalid(err) || connectivity.IsNotFoundError(err) {
					res.Skipped++
					continue
				}
				return err
			}
			res.Assigned++
			res.WorkOrders = append(res.WorkOrders, wo)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// assignOne does the per-issue work on an existing tx: load the issue, guard
// against a double-raise, create the linked work order, and move the issue to
// acknowledged. Returns the raised order and the locally-updated issue. Shared by
// AssignIssue (one) and AssignIssues (a batch).
func assignOne(ctx context.Context, tx bun.IDB, in AssignInput) (*wam.WorkOrder, *connectivity.ValidationIssue, error) {
	issue, err := connectivity.GetIssue(ctx, tx, in.IssueID)
	if err != nil {
		return nil, nil, err
	}
	if issue.WorkOrderID != nil && strings.TrimSpace(*issue.WorkOrderID) != "" {
		return nil, nil, invalidf("this issue is already assigned (work order %s)", *issue.WorkOrderID)
	}

	priority := strings.TrimSpace(in.Priority)
	if priority == "" {
		priority = priorityForSeverity(issue.Severity)
	}
	desc := issue.Message + "\n\nRaised from the data-quality audit (" + issue.Kind + ")."
	source := wam.SourceValidation
	params := wam.CreateWorkOrderParams{
		WorkType:    wam.WorkTypeCorrective,
		Priority:    priority,
		Title:       "Fix: " + truncate(issue.Message, 120),
		Description: &desc,
		Source:      &source,
	}
	// Link the order to the problem feature so it opens on the right asset in the
	// office and (with the asset's inherited location) on the field map.
	if issue.FromClass != "" && issue.FromID != "" {
		fc, fid := issue.FromClass, issue.FromID
		params.AssetClass, params.AssetID = &fc, &fid
	}
	if s := strings.TrimSpace(in.AssignedToUserID); s != "" {
		params.AssignedToUserID = &s
	}
	if s := strings.TrimSpace(in.AssignedTo); s != "" {
		params.AssignedTo = &s
	}

	wo, err := wam.CreateWorkOrder(ctx, tx, params)
	if err != nil {
		return nil, nil, err
	}
	if err := connectivity.SetIssueWorkOrder(ctx, tx, issue.ID, wo.ID, auth.ActorID(ctx)); err != nil {
		return nil, nil, err
	}
	issue.WorkOrderID = &wo.ID
	issue.Status = "acknowledged"
	return wo, issue, nil
}

// priorityForSeverity maps an issue's severity to a work-order priority: an error
// (a real break) is high, a warning is medium.
func priorityForSeverity(severity string) string {
	if severity == "error" {
		return wam.PriorityHigh
	}
	return wam.PriorityMedium
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-1]) + "…"
}

// InvalidError is a client-fixable problem originating here (an already-assigned
// issue). wam / connectivity raise their own, mapped the same way in writeError.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error { return &InvalidError{msg: fmt.Sprintf(format, args...)} }

// IsInvalid reports whether err is this package's InvalidError (errors.As, so a
// wrapped one still matches) -- used to treat an already-assigned issue as a
// skippable, not fatal, outcome in a bulk assign.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}
