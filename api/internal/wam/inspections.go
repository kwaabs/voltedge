package wam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// This file is Phase 59 Slice 2: inspection templates and their execution. It
// sits on top of the Slice 1 core (defects + work orders) and closes the loop
// from the finding end -- a failing checklist item auto-raises a defect, which
// then flows through the same RaiseWorkOrderFromDefect corrective path.

func validateResponseType(v string) error {
	return oneOf("response_type", v, ResponseTypePassFail, ResponseTypeRating, ResponseTypeNumeric, ResponseTypeText)
}

// argOf turns a *T into a driver arg: the dereferenced value, or nil for a SQL
// NULL. Used for the fixed-column inspection inserts (the defect/work-order
// inserts instead omit the column entirely, but a checklist row has a stable
// shape, so NULLing individual columns reads cleaner).
func strArg(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func boolArg(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func floatArg(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func strp(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Inspection templates
// ---------------------------------------------------------------------------

// TemplateItemInput is one checklist question on a new template. Position
// defaults to the 1-based slice index when zero; ResponseType defaults to
// pass_fail. FailSeverity (when non-empty) arms the auto-defect on failure.
type TemplateItemInput struct {
	Position     int
	Prompt       string
	ResponseType string
	FailSeverity *string
	Guidance     *string
}

// CreateTemplateParams is the input to CreateTemplate. Code, Name and at least
// one item are required.
type CreateTemplateParams struct {
	Code        string
	Name        string
	Description *string
	AssetClass  *string
	Items       []TemplateItemInput
}

// CreateTemplate inserts a checklist definition and its ordered items in one
// transaction. Item response types and fail severities are validated up front.
func CreateTemplate(ctx context.Context, db *bun.DB, p CreateTemplateParams) (*InspectionTemplate, error) {
	code := strings.TrimSpace(p.Code)
	name := strings.TrimSpace(p.Name)
	if code == "" || name == "" {
		return nil, invalidf("an inspection template needs a code and a name")
	}
	if len(p.Items) == 0 {
		return nil, invalidf("an inspection template needs at least one checklist item")
	}
	for i := range p.Items {
		if err := normalizeTemplateItem(&p.Items[i], i); err != nil {
			return nil, err
		}
	}

	var tmpl *InspectionTemplate
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		cols := []string{"code", "name"}
		vals := []string{"?", "?"}
		args := []any{code, name}
		if p.Description != nil {
			cols, vals, args = append(cols, "description"), append(vals, "?"), append(args, *p.Description)
		}
		if p.AssetClass != nil && strings.TrimSpace(*p.AssetClass) != "" {
			cols, vals, args = append(cols, "asset_class"), append(vals, "?"), append(args, strings.TrimSpace(*p.AssetClass))
		}
		query := fmt.Sprintf(
			"INSERT INTO wam.inspection_templates (%s) VALUES (%s) RETURNING id",
			strings.Join(cols, ", "), strings.Join(vals, ", "),
		)
		var id string
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			return fmt.Errorf("wam: inserting inspection template: %w", err)
		}
		for i := range p.Items {
			it := p.Items[i]
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO wam.inspection_template_items
				 (template_id, position, prompt, response_type, fail_severity, guidance)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				id, it.Position, strings.TrimSpace(it.Prompt), it.ResponseType, strArg(it.FailSeverity), strArg(it.Guidance),
			); err != nil {
				return fmt.Errorf("wam: inserting template item: %w", err)
			}
		}
		var err error
		tmpl, err = GetTemplate(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

// normalizeTemplateItem validates and defaults one item in place (position ->
// 1-based index when zero, response type -> pass_fail, fail severity trimmed to
// nil when blank).
func normalizeTemplateItem(it *TemplateItemInput, idx int) error {
	if strings.TrimSpace(it.Prompt) == "" {
		return invalidf("checklist item %d needs a prompt", idx+1)
	}
	if it.Position == 0 {
		it.Position = idx + 1
	}
	rt := strings.TrimSpace(it.ResponseType)
	if rt == "" {
		rt = ResponseTypePassFail
	}
	if err := validateResponseType(rt); err != nil {
		return err
	}
	it.ResponseType = rt
	if it.FailSeverity != nil && strings.TrimSpace(*it.FailSeverity) != "" {
		sv := strings.TrimSpace(*it.FailSeverity)
		if err := validateSeverity(sv); err != nil {
			return err
		}
		it.FailSeverity = &sv
	} else {
		it.FailSeverity = nil
	}
	if it.Guidance != nil && strings.TrimSpace(*it.Guidance) == "" {
		it.Guidance = nil
	}
	return nil
}

// GetTemplate loads one template and its items (ordered by position).
func GetTemplate(ctx context.Context, db bun.IDB, id string) (*InspectionTemplate, error) {
	t := new(InspectionTemplate)
	if err := db.NewSelect().Model(t).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("inspection template %q does not exist", id)
		}
		return nil, fmt.Errorf("wam: loading inspection template %s: %w", id, err)
	}
	var items []InspectionTemplateItem
	if err := db.NewSelect().Model(&items).Where("template_id = ?", id).Order("position").Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: loading template items %s: %w", id, err)
	}
	t.Items = items
	return t, nil
}

// ListTemplates returns templates ordered by name; activeOnly hides retired ones.
func ListTemplates(ctx context.Context, db bun.IDB, activeOnly bool) ([]InspectionTemplate, error) {
	var tmpls []InspectionTemplate
	q := db.NewSelect().Model(&tmpls).Order("name")
	if activeOnly {
		q = q.Where("active = true")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: listing inspection templates: %w", err)
	}
	return tmpls, nil
}

// SetTemplateActive retires or restores a template (kept for history, hidden
// from the record picker when inactive).
func SetTemplateActive(ctx context.Context, db bun.IDB, id string, active bool) (*InspectionTemplate, error) {
	res, err := db.ExecContext(ctx, "UPDATE wam.inspection_templates SET active = ?, updated_at = now() WHERE id = ?", active, id)
	if err != nil {
		return nil, fmt.Errorf("wam: updating inspection template %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("inspection template %q does not exist", id)
	}
	return GetTemplate(ctx, db, id)
}

// ---------------------------------------------------------------------------
// Inspections (execution)
// ---------------------------------------------------------------------------

const inspectionColumns = "id, work_order_id, template_id, asset_class, asset_id, overall_result, inspected_by, inspected_at, notes, created_at, updated_at"

func selectInspections(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr(inspectionColumns).
		ColumnExpr("ST_X(location) AS longitude").
		ColumnExpr("ST_Y(location) AS latitude")
}

// RecordItemInput is one answered checklist question. Passed is the pass/fail
// outcome (nil = not answered / N/A). FailSeverity carries the template item's
// arming severity through so a failing answer can raise a defect without a
// re-lookup; the value fields hold a numeric reading or a free note.
type RecordItemInput struct {
	TemplateItemID *string
	Position       int
	Prompt         string
	ResponseType   string
	Passed         *bool
	ValueText      *string
	ValueNumeric   *float64
	FailSeverity   *string
	Notes          *string
}

// RecordInspectionParams is the input to RecordInspection. Asset (class + id)
// and at least one item are required. OverallResult, when unset, is rolled up
// from the items (fail if any item failed, else pass). CompleteWorkOrder closes
// out the linked inspection work order in the same transaction.
type RecordInspectionParams struct {
	// ID, when set, is the device-minted primary key for an offline inspection
	// (Phase 59 offline WAM). Because recording an inspection has side effects
	// (it raises defects and can complete a work order), replay idempotency is an
	// existence guard rather than an ON CONFLICT: if a row with this id already
	// exists, the whole record is a no-op and the existing inspection is returned,
	// so a re-pushed sync op never double-raises defects. nil = DB-defaulted id.
	ID                *string
	WorkOrderID       *string
	TemplateID        *string
	AssetClass        string
	AssetID           string
	InspectedBy       *string
	InspectedAt       *time.Time
	Notes             *string
	Longitude         *float64
	Latitude          *float64
	OverallResult     *string
	CompleteWorkOrder bool
	Items             []RecordItemInput
}

// RecordInspection writes an inspection and its item results, atomically, and
// auto-raises a defect (source='inspection') for every failing item that carries
// a fail severity -- linking each raised defect back onto its item result. When
// CompleteWorkOrder is set and a work order is named, that order is marked
// completed in the same transaction. This is the inspection-side analogue of
// RaiseWorkOrderFromDefect: one action turns findings into tracked defects.
func RecordInspection(ctx context.Context, db *bun.DB, p RecordInspectionParams) (*Inspection, error) {
	assetClass := strings.TrimSpace(p.AssetClass)
	assetID := strings.TrimSpace(p.AssetID)
	if assetClass == "" || assetID == "" {
		return nil, invalidf("an inspection needs an asset (asset_class and asset_id)")
	}
	if len(p.Items) == 0 {
		return nil, invalidf("an inspection needs at least one recorded item")
	}
	hasLng := p.Longitude != nil
	hasLat := p.Latitude != nil
	if hasLng != hasLat {
		return nil, invalidf("an inspection location needs both longitude and latitude, or neither")
	}
	for i := range p.Items {
		if err := normalizeRecordItem(&p.Items[i], i); err != nil {
			return nil, err
		}
	}

	// Roll up the outcome: fail if any item explicitly failed. A valid caller
	// override (e.g. 'conditional') wins.
	overall := InspectionResultPass
	for i := range p.Items {
		if p.Items[i].Passed != nil && !*p.Items[i].Passed {
			overall = InspectionResultFail
			break
		}
	}
	if p.OverallResult != nil && strings.TrimSpace(*p.OverallResult) != "" {
		ov := strings.TrimSpace(*p.OverallResult)
		if err := oneOf("overall_result", ov, InspectionResultPass, InspectionResultFail, InspectionResultConditional); err != nil {
			return nil, err
		}
		overall = ov
	}

	var inspection *Inspection
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Idempotent replay guard: a re-pushed offline inspection carries the same
		// device-minted id, and re-running the body would double-raise its defects.
		// If the row already exists, return it unchanged.
		if p.ID != nil && strings.TrimSpace(*p.ID) != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM wam.inspections WHERE id = ?)", strings.TrimSpace(*p.ID),
			).Scan(&exists); err != nil {
				return fmt.Errorf("wam: checking inspection existence: %w", err)
			}
			if exists {
				var err error
				inspection, err = GetInspection(ctx, tx, strings.TrimSpace(*p.ID))
				return err
			}
		}

		cols := []string{"asset_class", "asset_id", "overall_result"}
		vals := []string{"?", "?", "?"}
		args := []any{assetClass, assetID, overall}
		add := func(col, val string, arg any) {
			cols, vals, args = append(cols, col), append(vals, val), append(args, arg)
		}
		if p.WorkOrderID != nil && strings.TrimSpace(*p.WorkOrderID) != "" {
			add("work_order_id", "?", strings.TrimSpace(*p.WorkOrderID))
		}
		if p.TemplateID != nil && strings.TrimSpace(*p.TemplateID) != "" {
			add("template_id", "?", strings.TrimSpace(*p.TemplateID))
		}
		if p.InspectedBy != nil && strings.TrimSpace(*p.InspectedBy) != "" {
			add("inspected_by", "?", strings.TrimSpace(*p.InspectedBy))
		}
		if p.InspectedAt != nil {
			add("inspected_at", "?", *p.InspectedAt)
		}
		if p.Notes != nil {
			add("notes", "?", *p.Notes)
		}
		if hasLng && hasLat {
			cols = append(cols, "location")
			vals = append(vals, "ST_SetSRID(ST_MakePoint(?, ?), 4326)")
			args = append(args, *p.Longitude, *p.Latitude)
		}
		if p.ID != nil && strings.TrimSpace(*p.ID) != "" {
			add("id", "?", strings.TrimSpace(*p.ID))
		}
		query := fmt.Sprintf(
			"INSERT INTO wam.inspections (%s) VALUES (%s) RETURNING id",
			strings.Join(cols, ", "), strings.Join(vals, ", "),
		)
		var inspectionID string
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&inspectionID); err != nil {
			return fmt.Errorf("wam: inserting inspection: %w", err)
		}

		for i := range p.Items {
			it := p.Items[i]
			var defectID *string
			// A failing item that carries a severity becomes a tracked defect.
			if it.Passed != nil && !*it.Passed && it.FailSeverity != nil {
				d, err := CreateDefect(ctx, tx, CreateDefectParams{
					AssetClass:  assetClass,
					AssetID:     assetID,
					Severity:    *it.FailSeverity,
					Source:      SourceInspection,
					Title:       "Inspection: " + strings.TrimSpace(it.Prompt),
					Description: it.Notes,
					Longitude:   p.Longitude,
					Latitude:    p.Latitude,
					ReportedBy:  p.InspectedBy,
				})
				if err != nil {
					return err
				}
				defectID = &d.ID
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO wam.inspection_item_results
				 (inspection_id, template_item_id, position, prompt, response_type, passed, value_text, value_numeric, defect_id, notes)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				inspectionID, strArg(it.TemplateItemID), it.Position, strings.TrimSpace(it.Prompt), it.ResponseType,
				boolArg(it.Passed), strArg(it.ValueText), floatArg(it.ValueNumeric), strArg(defectID), strArg(it.Notes),
			); err != nil {
				return fmt.Errorf("wam: inserting inspection item result: %w", err)
			}
		}

		if p.CompleteWorkOrder && p.WorkOrderID != nil && strings.TrimSpace(*p.WorkOrderID) != "" {
			note := "Inspection recorded: " + overall
			if _, err := UpdateWorkOrder(ctx, tx, strings.TrimSpace(*p.WorkOrderID), UpdateWorkOrderParams{
				Status:          strp(WorkStatusCompleted),
				CompletionNotes: &note,
			}); err != nil {
				return err
			}
		}

		var err error
		inspection, err = GetInspection(ctx, tx, inspectionID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return inspection, nil
}

// normalizeRecordItem validates/defaults one recorded item in place.
func normalizeRecordItem(it *RecordItemInput, idx int) error {
	if strings.TrimSpace(it.Prompt) == "" {
		return invalidf("recorded item %d needs a prompt", idx+1)
	}
	if it.Position == 0 {
		it.Position = idx + 1
	}
	rt := strings.TrimSpace(it.ResponseType)
	if rt == "" {
		rt = ResponseTypePassFail
	}
	if err := validateResponseType(rt); err != nil {
		return err
	}
	it.ResponseType = rt
	if it.FailSeverity != nil && strings.TrimSpace(*it.FailSeverity) != "" {
		sv := strings.TrimSpace(*it.FailSeverity)
		if err := validateSeverity(sv); err != nil {
			return err
		}
		it.FailSeverity = &sv
	} else {
		it.FailSeverity = nil
	}
	return nil
}

// GetInspection loads one inspection and its item results (ordered by position).
func GetInspection(ctx context.Context, db bun.IDB, id string) (*Inspection, error) {
	insp := new(Inspection)
	if err := selectInspections(db, insp).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("inspection %q does not exist", id)
		}
		return nil, fmt.Errorf("wam: loading inspection %s: %w", id, err)
	}
	var items []InspectionItemResult
	if err := db.NewSelect().Model(&items).Where("inspection_id = ?", id).Order("position").Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: loading inspection items %s: %w", id, err)
	}
	insp.Items = items
	return insp, nil
}

// InspectionFilter narrows ListInspections; empty fields are ignored.
type InspectionFilter struct {
	AssetClass  string
	AssetID     string
	WorkOrderID string
	TemplateID  string
}

// ListInspections returns inspections newest-first (without their items).
func ListInspections(ctx context.Context, db bun.IDB, f InspectionFilter) ([]Inspection, error) {
	var inspections []Inspection
	q := selectInspections(db, &inspections).Order("inspected_at DESC")
	if f.AssetClass != "" {
		q = q.Where("asset_class = ?", f.AssetClass)
	}
	if f.AssetID != "" {
		q = q.Where("asset_id = ?", f.AssetID)
	}
	if f.WorkOrderID != "" {
		q = q.Where("work_order_id = ?", f.WorkOrderID)
	}
	if f.TemplateID != "" {
		q = q.Where("template_id = ?", f.TemplateID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: listing inspections: %w", err)
	}
	return inspections, nil
}
