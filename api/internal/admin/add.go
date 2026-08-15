package admin

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// addSubtype appends one new subtype to an already-existing feature
// class, regardless of status (draft or already compiled) -- on its
// own this is purely a metadata insert, always safe. Recompiling
// afterward (POST .../compile, unchanged) is what actually widens the
// physical subtype CHECK constraint to allow it -- see
// api/internal/compiler's syncCheckConstraints. Until then, the new
// subtype exists in metadata.* but the physical table would still
// reject a row using it, same as any other metadata/physical drift
// this admin package already leaves to an explicit recompile step
// (e.g. a newly added field).
func addSubtype(ctx context.Context, db *bun.DB, featureClassCode string, req subtypeRequest) (*subtypeResponse, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, invalid("subtype name is required")
	}

	var resp subtypeResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", featureClassCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", featureClassCode)
			}
			return fmt.Errorf("loading feature class %q: %w", featureClassCode, err)
		}

		exists, err := tx.NewSelect().Model((*metadata.Subtype)(nil)).
			Where("feature_class_id = ? AND code = ?", fc.ID, req.Code).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking subtype code %d: %w", req.Code, err)
		}
		if exists {
			return invalid("feature class %q already has a subtype with code %d", featureClassCode, req.Code)
		}

		st := &metadata.Subtype{FeatureClassID: fc.ID, Code: req.Code, Name: req.Name}
		if _, err := tx.NewInsert().Model(st).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert subtype: %w", err)
		}

		resp = subtypeResponse{Code: req.Code, Name: req.Name}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// addDomainValue mirrors addSubtype -- appends one new coded value to
// an already-existing domain, regardless of whether any compiled
// field already uses it. Same "insert now, recompile any affected
// class to actually enforce it" split.
func addDomainValue(ctx context.Context, db *bun.DB, domainCode string, req domainValueRequest) (*domainValueResponse, error) {
	if strings.TrimSpace(req.Code) == "" {
		return nil, invalid("domain value code is required")
	}
	if strings.TrimSpace(req.Label) == "" {
		return nil, invalid("domain value label is required")
	}

	var resp domainValueResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		d := new(metadata.Domain)
		if err := tx.NewSelect().Model(d).Where("code = ?", domainCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("domain %q does not exist", domainCode)
			}
			return fmt.Errorf("loading domain %q: %w", domainCode, err)
		}
		if d.DomainType != "coded" {
			return invalid("domain %q is not a coded domain -- values only apply to coded domains", domainCode)
		}

		exists, err := tx.NewSelect().Model((*metadata.DomainValue)(nil)).
			Where("domain_id = ? AND code = ?", d.ID, req.Code).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking domain value code %q: %w", req.Code, err)
		}
		if exists {
			return invalid("domain %q already has a value with code %q", domainCode, req.Code)
		}

		dv := &metadata.DomainValue{DomainID: d.ID, Code: req.Code, Label: req.Label, SortOrder: req.SortOrder}
		if _, err := tx.NewInsert().Model(dv).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert domain value: %w", err)
		}

		resp = domainValueResponse{Code: req.Code, Label: req.Label, SortOrder: req.SortOrder}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
