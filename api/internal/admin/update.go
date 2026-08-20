package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// updateFeatureClass is the safe subset of "edit a feature class after
// creation" -- Name and IsDiagramContainer are pure metadata with no
// physical-table footprint, so they're always changeable. GeometryType/
// SRID/NetworkRole are exactly the three columns compiler/ddl.go reads
// to build the physical table's own shape (the geom column, its type,
// its SRID); changing any of them on a class that's already been
// compiled would leave the physical table and the metadata describing
// it disagreeing with each other, the same class of problem that kept
// editing an already-compiled class's subtypes/domain values out of
// this admin package's original scope. So: those three are only
// accepted while the class is still draft (PhysicalTable is nil) --
// nothing physical exists yet to disagree with.
func updateFeatureClass(ctx context.Context, db *bun.DB, code string, req updateFeatureClassRequest) (*updateFeatureClassResponse, error) {
	if err := validateUpdateFeatureClass(req); err != nil {
		return nil, err
	}

	var resp updateFeatureClassResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}

		cols := make([]string, 0, 5)
		if req.Name != nil {
			fc.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.IsDiagramContainer != nil {
			fc.IsDiagramContainer = *req.IsDiagramContainer
			cols = append(cols, "is_diagram_container")
		}
		if req.IsDiagramPlaceable != nil {
			fc.IsDiagramPlaceable = *req.IsDiagramPlaceable
			cols = append(cols, "is_diagram_placeable")
		}
		if req.GeometryType != nil {
			if fc.Status != "draft" {
				return invalid("geometry_type/srid/network_role can only be changed while %q is still draft (not yet compiled) -- it's %s", code, fc.Status)
			}
			fc.GeometryType = *req.GeometryType
			fc.NetworkRole = *req.NetworkRole
			fc.SRID = req.SRID
			cols = append(cols, "geometry_type", "srid", "network_role")
		}
		if len(cols) == 0 {
			return invalid("request must set at least one of name, is_diagram_container, is_diagram_placeable, geometry_type/srid/network_role")
		}

		if _, err := tx.NewUpdate().Model(fc).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating feature class %q: %w", code, err)
		}

		resp = updateFeatureClassResponse{
			Code:               fc.Code,
			Name:               fc.Name,
			GeometryType:       fc.GeometryType,
			SRID:               fc.SRID,
			NetworkRole:        fc.NetworkRole,
			IsDiagramContainer: fc.IsDiagramContainer,
			IsDiagramPlaceable: fc.IsDiagramPlaceable,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateDomain edits a domain's own name -- always safe, pure metadata
// with no physical footprint -- and, for a range domain, its
// range_min/range_max bounds. Editing the bounds is safe the same way
// widening a coded domain's values already is (see addDomainValue and
// compiler/ddl.go's syncCheckConstraints doc comment): a recompile of
// any class using this domain rebuilds the field's own BETWEEN CHECK
// from whatever's here now, and Postgres's own ADD CONSTRAINT rejects
// it outright if that narrows past real data, rather than silently
// applying a narrower rule.
func updateDomain(ctx context.Context, db *bun.DB, code string, req updateDomainRequest) (*domainResponse, error) {
	if err := validateUpdateDomain(req); err != nil {
		return nil, err
	}

	var resp domainResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		d := new(metadata.Domain)
		if err := tx.NewSelect().Model(d).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("domain %q does not exist", code)
			}
			return fmt.Errorf("loading domain %q: %w", code, err)
		}

		cols := make([]string, 0, 3)
		if req.Name != nil {
			d.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.RangeMin != nil {
			if d.DomainType != "range" {
				return invalid("domain %q is not a range domain -- range_min/range_max only apply to a range domain", code)
			}
			d.RangeMin = req.RangeMin
			d.RangeMax = req.RangeMax
			cols = append(cols, "range_min", "range_max")
		}

		if _, err := tx.NewUpdate().Model(d).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating domain %q: %w", code, err)
		}

		var values []metadata.DomainValue
		if err := tx.NewSelect().Model(&values).Where("domain_id = ?", d.ID).Order("sort_order").Scan(ctx); err != nil {
			return fmt.Errorf("loading values for domain %q: %w", code, err)
		}
		valueResponses := make([]domainValueResponse, len(values))
		for i, v := range values {
			var materialSpecCode *string
			if v.MaterialSpecID != nil {
				var m metadata.MaterialSpec
				if err := tx.NewSelect().Model(&m).Where("id = ?", *v.MaterialSpecID).Scan(ctx); err != nil {
					return fmt.Errorf("loading material spec for domain value %q: %w", v.Code, err)
				}
				materialSpecCode = &m.Code
			}
			valueResponses[i] = domainValueResponse{Code: v.Code, Label: v.Label, SortOrder: v.SortOrder, MaterialSpecCode: materialSpecCode}
		}

		resp = domainResponse{
			Code: d.Code, Name: d.Name, DomainType: d.DomainType, Values: valueResponses,
			RangeMin: d.RangeMin, RangeMax: d.RangeMax,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateDomainValue edits an existing coded domain value's label and/or
// sort_order -- both pure display metadata, never referenced by a CHECK
// constraint (that's built from codes, not labels -- see
// syncCheckConstraints), so this needs no recompile-safety story at all,
// unlike updateDomain's range bounds above.
func updateDomainValue(ctx context.Context, db *bun.DB, domainCode, valueCode string, req updateDomainValueRequest) (*domainValueResponse, error) {
	if err := validateUpdateDomainValue(req); err != nil {
		return nil, err
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

		dv := new(metadata.DomainValue)
		if err := tx.NewSelect().Model(dv).Where("domain_id = ? AND code = ?", d.ID, valueCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("domain %q has no value with code %q", domainCode, valueCode)
			}
			return fmt.Errorf("loading domain value %q: %w", valueCode, err)
		}

		cols := make([]string, 0, 3)
		if req.Label != nil {
			dv.Label = *req.Label
			cols = append(cols, "label")
		}
		if req.SortOrder != nil {
			dv.SortOrder = *req.SortOrder
			cols = append(cols, "sort_order")
		}
		if req.MaterialSpecCode != nil {
			if *req.MaterialSpecCode == "" {
				dv.MaterialSpecID = nil
			} else {
				id, err := resolveMaterialSpecCode(ctx, tx, *req.MaterialSpecCode)
				if err != nil {
					return err
				}
				dv.MaterialSpecID = &id
			}
			cols = append(cols, "material_spec_id")
		}

		if _, err := tx.NewUpdate().Model(dv).Column(cols...).Where("id = ?", dv.ID).Exec(ctx); err != nil {
			return fmt.Errorf("updating domain value %q: %w", valueCode, err)
		}

		var materialSpecCode *string
		if dv.MaterialSpecID != nil {
			var m metadata.MaterialSpec
			if err := tx.NewSelect().Model(&m).Where("id = ?", *dv.MaterialSpecID).Scan(ctx); err != nil {
				return fmt.Errorf("loading material spec for domain value %q: %w", valueCode, err)
			}
			materialSpecCode = &m.Code
		}

		resp = domainValueResponse{Code: dv.Code, Label: dv.Label, SortOrder: dv.SortOrder, MaterialSpecCode: materialSpecCode}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateField edits the subset of an existing field's metadata that the
// rest of the system reads live rather than bakes into the physical
// column at ADD COLUMN time -- domain_id/group_id (a following recompile
// rebuilds the CHECK constraint from whichever domain is assigned when
// it's called, same "insert now, recompile to enforce" split as addField),
// and visible_on_create/visible_on_update/editable_after_publish/nullable,
// all four of which the CRUD engine (api/internal/crud) reads straight
// from metadata.fields on every request, never from anything physical.
// Nullable is included even though flipping it false->true doesn't
// retroactively loosen an already-compiled column's own NOT NULL --
// compiler.go's ADD COLUMN path sets that once, and nothing revisits it,
// so the two can only disagree if someone then writes an actual null
// against a field the physical table still enforces NOT NULL on. That
// fails safe: crud's own validateValue would now permit the null through
// its Go-side check, so it reaches Postgres and gets rejected there
// instead -- a raw constraint-violation 500 rather than a clean 400, but
// never silent data loss. Name and data_type are absent from
// updateFieldRequest entirely -- see its own doc comment.
func updateField(ctx context.Context, db *bun.DB, featureClassCode, fieldName string, req updateFieldRequest) (*fieldResponse, error) {
	var resp fieldResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", featureClassCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", featureClassCode)
			}
			return fmt.Errorf("loading feature class %q: %w", featureClassCode, err)
		}

		field := new(metadata.Field)
		if err := tx.NewSelect().Model(field).Where("feature_class_id = ? AND name = ?", fc.ID, fieldName).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q has no field named %q", featureClassCode, fieldName)
			}
			return fmt.Errorf("loading field %q: %w", fieldName, err)
		}

		cols := make([]string, 0, 6)
		if req.DomainCode != nil {
			if *req.DomainCode == "" {
				field.DomainID = nil
			} else {
				var d metadata.Domain
				if err := tx.NewSelect().Model(&d).Where("code = ?", *req.DomainCode).Scan(ctx); err != nil {
					if err == sql.ErrNoRows {
						return invalid("domain_code %q does not exist -- create it first", *req.DomainCode)
					}
					return fmt.Errorf("resolving domain %q: %w", *req.DomainCode, err)
				}
				field.DomainID = &d.ID
			}
			cols = append(cols, "domain_id")
		}
		if req.GroupCode != nil {
			if *req.GroupCode == "" {
				field.GroupID = nil
			} else {
				var g metadata.FieldGroup
				if err := tx.NewSelect().Model(&g).Where("code = ?", *req.GroupCode).Scan(ctx); err != nil {
					if err == sql.ErrNoRows {
						return invalid("group_code %q does not exist -- create it first", *req.GroupCode)
					}
					return fmt.Errorf("resolving field group %q: %w", *req.GroupCode, err)
				}
				field.GroupID = &g.ID
			}
			cols = append(cols, "group_id")
		}
		if req.Nullable != nil {
			field.Nullable = *req.Nullable
			cols = append(cols, "nullable")
		}
		if req.VisibleOnCreate != nil {
			field.VisibleOnCreate = *req.VisibleOnCreate
			cols = append(cols, "visible_on_create")
		}
		if req.VisibleOnUpdate != nil {
			field.VisibleOnUpdate = *req.VisibleOnUpdate
			cols = append(cols, "visible_on_update")
		}
		if req.EditableAfterPublish != nil {
			field.EditableAfterPublish = *req.EditableAfterPublish
			cols = append(cols, "editable_after_publish")
		}
		if len(cols) == 0 {
			return invalid("request must set at least one of domain_code, group_code, nullable, visible_on_create, visible_on_update, editable_after_publish")
		}

		if _, err := tx.NewUpdate().Model(field).Column(cols...).Where("id = ?", field.ID).Exec(ctx); err != nil {
			return fmt.Errorf("updating field %q: %w", fieldName, err)
		}

		resp = fieldResponse{
			Name: field.Name, DataType: field.DataType, Nullable: field.Nullable,
			VisibleOnCreate: field.VisibleOnCreate, VisibleOnUpdate: field.VisibleOnUpdate,
			EditableAfterPublish: field.EditableAfterPublish,
		}
		if field.DomainID != nil {
			var d metadata.Domain
			if err := tx.NewSelect().Model(&d).Where("id = ?", *field.DomainID).Scan(ctx); err != nil {
				return fmt.Errorf("loading domain for response: %w", err)
			}
			resp.DomainCode = &d.Code
		}
		if field.GroupID != nil {
			var g metadata.FieldGroup
			if err := tx.NewSelect().Model(&g).Where("id = ?", *field.GroupID).Scan(ctx); err != nil {
				return fmt.Errorf("loading group for response: %w", err)
			}
			resp.GroupCode = &g.Code
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateMaterialSpec edits an existing material spec's own name/category/
// engineering numbers. Always safe: nothing physical or compiled reads
// these columns directly (see MaterialSpec's own doc comment) -- a
// domain value just carries an id pointer to whichever row is here now,
// so changing the numbers on that row updates what every linked value
// resolves to, the same way editing a domain value's own label already
// does.
func updateMaterialSpec(ctx context.Context, db *bun.DB, code string, req updateMaterialSpecRequest) (*materialSpecResponse, error) {
	if err := validateUpdateMaterialSpec(req); err != nil {
		return nil, err
	}

	var resp materialSpecResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.MaterialSpec)
		if err := tx.NewSelect().Model(m).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("material spec %q does not exist", code)
			}
			return fmt.Errorf("loading material spec %q: %w", code, err)
		}

		cols := make([]string, 0, 11)
		if req.Name != nil {
			m.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.Category != nil {
			m.Category = *req.Category
			cols = append(cols, "category")
		}
		if req.ResistanceOhmPerKm != nil {
			m.ResistanceOhmPerKm = req.ResistanceOhmPerKm
			cols = append(cols, "resistance_ohm_per_km")
		}
		if req.ReactanceOhmPerKm != nil {
			m.ReactanceOhmPerKm = req.ReactanceOhmPerKm
			cols = append(cols, "reactance_ohm_per_km")
		}
		if req.AmpacityAmps != nil {
			m.AmpacityAmps = req.AmpacityAmps
			cols = append(cols, "ampacity_amps")
		}
		if req.DiameterMm != nil {
			m.DiameterMm = req.DiameterMm
			cols = append(cols, "diameter_mm")
		}
		if req.WeightKgPerKm != nil {
			m.WeightKgPerKm = req.WeightKgPerKm
			cols = append(cols, "weight_kg_per_km")
		}
		if req.MaxOperatingTempC != nil {
			m.MaxOperatingTempC = req.MaxOperatingTempC
			cols = append(cols, "max_operating_temp_c")
		}
		if req.Emissivity != nil {
			m.Emissivity = req.Emissivity
			cols = append(cols, "emissivity")
		}
		if req.Absorptivity != nil {
			m.Absorptivity = req.Absorptivity
			cols = append(cols, "absorptivity")
		}
		if req.ResistanceTempCoefficientPerC != nil {
			m.ResistanceTempCoefficientPerC = req.ResistanceTempCoefficientPerC
			cols = append(cols, "resistance_temp_coefficient_per_c")
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating material spec %q: %w", code, err)
		}

		resp = materialSpecResponse{
			Code: m.Code, Name: m.Name, Category: m.Category,
			ResistanceOhmPerKm: m.ResistanceOhmPerKm, ReactanceOhmPerKm: m.ReactanceOhmPerKm,
			AmpacityAmps: m.AmpacityAmps, DiameterMm: m.DiameterMm, WeightKgPerKm: m.WeightKgPerKm,
			MaxOperatingTempC: m.MaxOperatingTempC, Emissivity: m.Emissivity,
			Absorptivity: m.Absorptivity, ResistanceTempCoefficientPerC: m.ResistanceTempCoefficientPerC,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
