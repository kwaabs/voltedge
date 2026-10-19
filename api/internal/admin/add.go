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

// addField appends one new field to an already-existing feature class,
// regardless of status -- purely a metadata insert, like addSubtype.
// Unlike addSubtype, the physical column this eventually produces needs
// no compiler change to apply: compiler.go's existing-table path
// already walks every field in metadata looking for one missing from
// the physical table and ALTER TABLE ADD COLUMNs it -- that path
// predates this admin package entirely (it's what lets a hand-edited
// migration add a field to an already-compiled class today). Recompile
// still has to be called to actually add the column; this endpoint
// only ever touches metadata.
func addField(ctx context.Context, db *bun.DB, featureClassCode string, req fieldRequest) (*fieldResponse, error) {
	if err := validateAddField(req); err != nil {
		return nil, err
	}

	var resp fieldResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", featureClassCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", featureClassCode)
			}
			return fmt.Errorf("loading feature class %q: %w", featureClassCode, err)
		}

		exists, err := tx.NewSelect().Model((*metadata.Field)(nil)).
			Where("feature_class_id = ? AND name = ?", fc.ID, req.Name).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking field name %q: %w", req.Name, err)
		}
		if exists {
			return invalid("feature class %q already has a field named %q", featureClassCode, req.Name)
		}

		domainIDByCode, err := resolveDomainCodes(ctx, tx, []fieldRequest{req})
		if err != nil {
			return err
		}
		groupIDByCode, err := resolveGroupCodes(ctx, tx, []fieldRequest{req})
		if err != nil {
			return err
		}

		// Append a new field at the END of the class's form order (migration
		// 0146) rather than letting it default to 0 and jump to the top.
		var maxOrder int
		if err := tx.NewSelect().Model((*metadata.Field)(nil)).
			ColumnExpr("COALESCE(MAX(sort_order), 0)").
			Where("feature_class_id = ?", fc.ID).Scan(ctx, &maxOrder); err != nil {
			return fmt.Errorf("computing next field order: %w", err)
		}

		field := &metadata.Field{
			FeatureClassID:       fc.ID,
			Name:                 req.Name,
			DataType:             req.DataType,
			Nullable:             req.Nullable,
			Status:               "active",
			VisibleOnCreate:      req.VisibleOnCreate,
			VisibleOnUpdate:      req.VisibleOnUpdate,
			EditableAfterPublish: req.EditableAfterPublish,
			SortOrder:            maxOrder + 1,
		}
		if req.DomainCode != nil {
			id := domainIDByCode[*req.DomainCode]
			field.DomainID = &id
		}
		if req.GroupCode != nil {
			id := groupIDByCode[*req.GroupCode]
			field.GroupID = &id
		}
		// Optional field-level default (migration 0150); for a domain-tied field
		// it's a domain value code, validated at create time by the CRUD engine.
		if req.DefaultValue != nil {
			if v := strings.TrimSpace(*req.DefaultValue); v != "" {
				field.DefaultValue = &v
			}
		}
		if _, err := tx.NewInsert().Model(field).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert field: %w", err)
		}

		if len(req.RequiredForSubtypes) > 0 {
			subtypeIDByCode, err := resolveSubtypeCodes(ctx, tx, fc.ID, req.RequiredForSubtypes)
			if err != nil {
				return err
			}
			for _, sc := range req.RequiredForSubtypes {
				rule := &metadata.SubtypeFieldRule{SubtypeID: subtypeIDByCode[sc], FieldID: field.ID, IsRequired: true}
				if _, err := tx.NewInsert().Model(rule).ExcludeColumn("id").Exec(ctx); err != nil {
					return fmt.Errorf("insert subtype_field_rule for subtype %d: %w", sc, err)
				}
			}
		}

		resp = fieldResponse{
			Name: req.Name, DataType: req.DataType, DomainCode: req.DomainCode, GroupCode: req.GroupCode,
			Nullable: req.Nullable, VisibleOnCreate: req.VisibleOnCreate, VisibleOnUpdate: req.VisibleOnUpdate,
			EditableAfterPublish: req.EditableAfterPublish, RequiredForSubtypes: req.RequiredForSubtypes,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// resolveSubtypeCodes looks up subtype ids for a set of subtype codes
// scoped to one feature class -- an unresolvable code is a 400 naming
// it, same discipline resolveDomainCodes/resolveGroupCodes already
// apply for domain_code/group_code.
func resolveSubtypeCodes(ctx context.Context, tx bun.Tx, featureClassID string, codes []int) (map[int]string, error) {
	var subtypes []metadata.Subtype
	if err := tx.NewSelect().Model(&subtypes).Where("feature_class_id = ?", featureClassID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading subtypes: %w", err)
	}
	byCode := make(map[int]string, len(subtypes))
	for _, s := range subtypes {
		byCode[s.Code] = s.ID
	}
	for _, c := range codes {
		if _, ok := byCode[c]; !ok {
			return nil, invalid("required_for_subtypes references subtype code %d, which doesn't exist on this feature class", c)
		}
	}
	return byCode, nil
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
		if req.MaterialSpecCode != nil {
			id, err := resolveMaterialSpecCode(ctx, tx, *req.MaterialSpecCode)
			if err != nil {
				return err
			}
			dv.MaterialSpecID = &id
		}
		if _, err := tx.NewInsert().Model(dv).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert domain value: %w", err)
		}

		resp = domainValueResponse{Code: req.Code, Label: req.Label, SortOrder: req.SortOrder, MaterialSpecCode: req.MaterialSpecCode}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
