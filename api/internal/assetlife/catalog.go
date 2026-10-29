// Package assetlife is the asset-lifecycle aging engine: the expected-useful-life
// catalog (ops.asset_life_expectancy, migration 0157) and the report that joins
// it to each feature's in-service age to derive remaining useful life and flag
// replacement candidates. It reads the lifecycle_status / commissioned_at system
// columns the compiler puts on every feature table (api/internal/crud/lifecycle.go).
//
// The catalog is admin policy (how long a class/subtype is expected to last),
// edited through the metadata:write side of the /asset-life mount; the report is
// a features:read read. Mirrors the shape of ops.validation_rules and the cost
// catalog.
package assetlife

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// LifeExpectancy is one row of ops.asset_life_expectancy: how many years a class
// (or one subtype of it) is expected to last in service.
type LifeExpectancy struct {
	bun.BaseModel `bun:"table:ops.asset_life_expectancy"`

	ID               string    `bun:"id,pk" json:"id"`
	FeatureClassCode string    `bun:"feature_class_code" json:"feature_class_code"`
	SubtypeCode      *int      `bun:"subtype_code" json:"subtype_code"`
	ExpectedYears    int       `bun:"expected_years" json:"expected_years"`
	Notes            *string   `bun:"notes" json:"notes"`
	CreatedAt        time.Time `bun:"created_at,nullzero" json:"created_at"`
	UpdatedAt        time.Time `bun:"updated_at,nullzero" json:"updated_at"`
}

// ListExpectancies returns a class's expectancy rows (or every row when class is
// empty), the class default (subtype NULL) first, then by subtype.
func ListExpectancies(ctx context.Context, db bun.IDB, class string) ([]LifeExpectancy, error) {
	rows := []LifeExpectancy{}
	q := db.NewSelect().Model(&rows).OrderExpr("feature_class_code, subtype_code NULLS FIRST")
	if class = strings.TrimSpace(class); class != "" {
		q = q.Where("feature_class_code = ?", class)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("assetlife: listing expectancies: %w", err)
	}
	return rows, nil
}

// ExpectancyParams is the create/update input.
type ExpectancyParams struct {
	FeatureClassCode string
	SubtypeCode      *int
	ExpectedYears    int
	Notes            *string
}

func (p ExpectancyParams) validate() error {
	if strings.TrimSpace(p.FeatureClassCode) == "" {
		return invalid("feature_class_code is required")
	}
	if p.ExpectedYears <= 0 {
		return invalid("expected_years must be a positive whole number of years")
	}
	return nil
}

// CreateExpectancy adds a catalog row. The (class, subtype) pair is unique.
func CreateExpectancy(ctx context.Context, db bun.IDB, p ExpectancyParams) (*LifeExpectancy, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	row := &LifeExpectancy{
		FeatureClassCode: strings.TrimSpace(p.FeatureClassCode),
		SubtypeCode:      p.SubtypeCode,
		ExpectedYears:    p.ExpectedYears,
		Notes:            p.Notes,
	}
	// ExcludeColumn("id") so the zero-value pk isn't sent as "" to the uuid column
	// (invalid input syntax 22P02); the DB default gen_random_uuid() fills it.
	if _, err := db.NewInsert().Model(row).ExcludeColumn("id").Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("assetlife: creating expectancy: %w", err)
	}
	return row, nil
}

// UpdateExpectancy replaces the mutable fields (the class/subtype identity is
// fixed). Not-found for an unknown id.
func UpdateExpectancy(ctx context.Context, db bun.IDB, id string, p ExpectancyParams) (*LifeExpectancy, error) {
	if p.ExpectedYears <= 0 {
		return nil, invalid("expected_years must be a positive whole number of years")
	}
	row := &LifeExpectancy{ID: id, ExpectedYears: p.ExpectedYears, Notes: p.Notes}
	res, err := db.NewUpdate().Model(row).
		Column("expected_years", "notes").
		Set("updated_at = now()").
		WherePK().Returning("*").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("assetlife: updating expectancy %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("expectancy %q not found", id)
	}
	return row, nil
}

// DeleteExpectancy removes a catalog row by id.
func DeleteExpectancy(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*LifeExpectancy)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("assetlife: deleting expectancy %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("expectancy %q not found", id)
	}
	return nil
}
