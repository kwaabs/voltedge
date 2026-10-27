package wam

// subcontractors.go is the SUBCONTRACTOR registry (migration 0177): external
// firms the utility tasks field work to but which do NOT operate in this system.
// A subcontractor is a rolodex entity -- a name and a contact -- deliberately
// distinct from a crew (oms.crews): no members, no status lifecycle, no field
// pack, no dispatch. Work is tasked to one via wam.work_orders.subcontractor_id;
// the utility's own coordinator tracks that order's status on their behalf.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Subcontractor mirrors wam.subcontractors. OpenWorkOrderCount is scan-only,
// filled on list so the picker/report can show a firm's current load.
type Subcontractor struct {
	bun.BaseModel `bun:"table:wam.subcontractors,alias:sc"`

	ID          string    `bun:"id,pk" json:"id"`
	Name        string    `bun:"name" json:"name"`
	ContactName *string   `bun:"contact_name" json:"contact_name"`
	Phone       *string   `bun:"phone" json:"phone"`
	Email       *string   `bun:"email" json:"email"`
	Notes       *string   `bun:"notes" json:"notes"`
	Active      bool      `bun:"active" json:"active"`
	CreatedAt   time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at" json:"updated_at"`

	OpenWorkOrderCount int `bun:"open_work_order_count,scanonly" json:"open_work_order_count"`
}

// ListSubcontractors returns subcontractors (active unless includeInactive),
// each with its open-work-order load, name-ordered.
func ListSubcontractors(ctx context.Context, db bun.IDB, includeInactive bool) ([]Subcontractor, error) {
	var out []Subcontractor
	q := db.NewSelect().Model(&out).
		ColumnExpr("sc.*").
		ColumnExpr("(SELECT count(*) FROM wam.work_orders wo WHERE wo.subcontractor_id = sc.id AND wo.status NOT IN ('completed', 'cancelled')) AS open_work_order_count").
		Order("sc.name")
	if !includeInactive {
		q = q.Where("sc.active")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: listing subcontractors: %w", err)
	}
	return out, nil
}

// SubcontractorParams is the create/update shape. Empty optional strings clear
// the corresponding field on update.
type SubcontractorParams struct {
	Name        string
	ContactName *string
	Phone       *string
	Email       *string
	Notes       *string
}

// CreateSubcontractor registers a firm (active).
func CreateSubcontractor(ctx context.Context, db bun.IDB, p SubcontractorParams) (*Subcontractor, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalidf("a subcontractor needs a name")
	}
	sc := &Subcontractor{
		Name:        strings.TrimSpace(p.Name),
		ContactName: trimPtr(p.ContactName),
		Phone:       trimPtr(p.Phone),
		Email:       trimPtr(p.Email),
		Notes:       trimPtr(p.Notes),
		Active:      true,
	}
	if _, err := db.NewInsert().Model(sc).
		Column("name", "contact_name", "phone", "email", "notes", "active").
		Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("wam: creating subcontractor: %w", err)
	}
	return sc, nil
}

// UpdateSubcontractor edits a firm's descriptive fields. It does not touch active
// (that is SetSubcontractorActive).
func UpdateSubcontractor(ctx context.Context, db bun.IDB, id string, p SubcontractorParams) (*Subcontractor, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalidf("a subcontractor needs a name")
	}
	res, err := db.NewUpdate().Model((*Subcontractor)(nil)).
		Set("name = ?", strings.TrimSpace(p.Name)).
		Set("contact_name = ?", trimPtr(p.ContactName)).
		Set("phone = ?", trimPtr(p.Phone)).
		Set("email = ?", trimPtr(p.Email)).
		Set("notes = ?", trimPtr(p.Notes)).
		Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("wam: updating subcontractor %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("subcontractor %q does not exist", id)
	}
	return GetSubcontractor(ctx, db, id)
}

// SetSubcontractorActive retires (active=false) or restores a subcontractor.
// Retiring keeps its historical tasking; it just drops out of the task picker.
func SetSubcontractorActive(ctx context.Context, db bun.IDB, id string, active bool) (*Subcontractor, error) {
	res, err := db.NewUpdate().Model((*Subcontractor)(nil)).
		Set("active = ?", active).Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("wam: setting subcontractor %s active: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("subcontractor %q does not exist", id)
	}
	return GetSubcontractor(ctx, db, id)
}

// GetSubcontractor fetches one subcontractor by id (with its load).
func GetSubcontractor(ctx context.Context, db bun.IDB, id string) (*Subcontractor, error) {
	sc := new(Subcontractor)
	if err := db.NewSelect().Model(sc).
		ColumnExpr("sc.*").
		ColumnExpr("(SELECT count(*) FROM wam.work_orders wo WHERE wo.subcontractor_id = sc.id AND wo.status NOT IN ('completed', 'cancelled')) AS open_work_order_count").
		Where("sc.id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("subcontractor %q does not exist", id)
		}
		return nil, fmt.Errorf("wam: loading subcontractor %s: %w", id, err)
	}
	return sc, nil
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	if s == "" {
		return nil
	}
	return &s
}
