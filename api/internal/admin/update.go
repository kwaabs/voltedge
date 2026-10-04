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
		// geometry_optional (Phase 26) is pure metadata like the two
		// is_diagram flags -- safe to toggle on any class, draft or
		// compiled. The physical effect (geom column nullability) only
		// happens on the next recompile, where the compiler relaxes the
		// NOT NULL; setting it here just records the intent.
		if req.GeometryOptional != nil {
			fc.GeometryOptional = *req.GeometryOptional
			cols = append(cols, "geometry_optional")
		}
		// diagram_geometry_type (migration 0053) is pure metadata too --
		// it only changes how the diagram canvas draws this class, never
		// the physical table -- so it's editable at any status. "" clears
		// it back to NULL (inherit geometry_type); a value sets the override.
		if req.DiagramGeometryType != nil {
			if *req.DiagramGeometryType == "" {
				fc.DiagramGeometryType = nil
			} else {
				v := *req.DiagramGeometryType
				fc.DiagramGeometryType = &v
			}
			cols = append(cols, "diagram_geometry_type")
		}
		// label_def (Phase 30) is pure metadata -- a map-render concern with
		// no physical footprint -- so it's editable at any status, like the
		// flags above. Raw jsonb passthrough; a JSON null (or empty) clears
		// the column to SQL NULL (no labels), a JSON object sets the config.
		if req.LabelDef != nil {
			raw := *req.LabelDef
			if len(raw) == 0 || string(raw) == "null" {
				fc.LabelDef = nil
			} else {
				fc.LabelDef = raw
			}
			cols = append(cols, "label_def")
		}
		// category (migration 0074) is pure presentation metadata -- the menu
		// grouping -- with no physical footprint, so it's editable at any
		// status like the flags above. "" clears it back to uncategorised
		// (NULL); any other string sets the grouping.
		if req.Category != nil {
			if *req.Category == "" {
				fc.Category = nil
			} else {
				v := *req.Category
				fc.Category = &v
			}
			cols = append(cols, "category")
		}
		// functional_role (migration 0091) is the class's electrical function --
		// pure metadata the compiler's DDL never reads, so editable at any status
		// (unlike network_role). "" clears it back to NULL (unclassified); any
		// taxonomy value sets it.
		if req.FunctionalRole != nil {
			if *req.FunctionalRole == "" {
				fc.FunctionalRole = nil
			} else {
				v := *req.FunctionalRole
				fc.FunctionalRole = &v
			}
			cols = append(cols, "functional_role")
		}
		// trace_tier (migration 0126) is the class's network tier -- pure metadata
		// the compiler's DDL never reads, so editable at any status. Sentinel-
		// encoded (see updateFeatureClassRequest): a negative value clears it back
		// to NULL (untiered / transparent), a non-negative value sets the rank.
		if req.TraceTier != nil {
			if *req.TraceTier < 0 {
				fc.TraceTier = nil
			} else {
				v := *req.TraceTier
				fc.TraceTier = &v
			}
			cols = append(cols, "trace_tier")
		}
		// min/max_display_zoom (migration 0090) is pure map-render metadata,
		// editable at any status like the flags above. Sentinel-encoded (see
		// updateFeatureClassRequest): a negative value clears the bound to
		// NULL, a value in 0..24 sets it. Validated as a band once both
		// resolved values are known so min <= max is enforced whichever side
		// this request touches (an untouched side reads the stored value).
		if req.MinDisplayZoom != nil || req.MaxDisplayZoom != nil {
			if req.MinDisplayZoom != nil {
				if *req.MinDisplayZoom < 0 {
					fc.MinDisplayZoom = nil
				} else {
					v := *req.MinDisplayZoom
					fc.MinDisplayZoom = &v
				}
				cols = append(cols, "min_display_zoom")
			}
			if req.MaxDisplayZoom != nil {
				if *req.MaxDisplayZoom < 0 {
					fc.MaxDisplayZoom = nil
				} else {
					v := *req.MaxDisplayZoom
					fc.MaxDisplayZoom = &v
				}
				cols = append(cols, "max_display_zoom")
			}
			if fc.MinDisplayZoom != nil && (*fc.MinDisplayZoom < 0 || *fc.MinDisplayZoom > 24) {
				return invalid("min_display_zoom must be between 0 and 24")
			}
			if fc.MaxDisplayZoom != nil && (*fc.MaxDisplayZoom < 0 || *fc.MaxDisplayZoom > 24) {
				return invalid("max_display_zoom must be between 0 and 24")
			}
			if fc.MinDisplayZoom != nil && fc.MaxDisplayZoom != nil && *fc.MinDisplayZoom > *fc.MaxDisplayZoom {
				return invalid("min_display_zoom (%d) must not exceed max_display_zoom (%d)", *fc.MinDisplayZoom, *fc.MaxDisplayZoom)
			}
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
			return invalid("request must set at least one of name, is_diagram_container, is_diagram_placeable, geometry_optional, diagram_geometry_type, category, functional_role, trace_tier, label_def, min_display_zoom/max_display_zoom, geometry_type/srid/network_role")
		}

		if _, err := tx.NewUpdate().Model(fc).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating feature class %q: %w", code, err)
		}

		resp = updateFeatureClassResponse{
			Code:                fc.Code,
			Name:                fc.Name,
			GeometryType:        fc.GeometryType,
			SRID:                fc.SRID,
			NetworkRole:         fc.NetworkRole,
			FunctionalRole:      fc.FunctionalRole,
			IsDiagramContainer:  fc.IsDiagramContainer,
			IsDiagramPlaceable:  fc.IsDiagramPlaceable,
			GeometryOptional:    fc.GeometryOptional,
			DiagramGeometryType: fc.DiagramGeometryType,
			Category:            fc.Category,
			TraceTier:           fc.TraceTier,
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
		if len(cols) == 0 && req.RequiredForSubtypes == nil {
			return invalid("request must set at least one of domain_code, group_code, nullable, visible_on_create, visible_on_update, editable_after_publish, required_for_subtypes")
		}

		if len(cols) > 0 {
			if _, err := tx.NewUpdate().Model(field).Column(cols...).Where("id = ?", field.ID).Exec(ctx); err != nil {
				return fmt.Errorf("updating field %q: %w", fieldName, err)
			}
		}

		// Reconcile the field's per-subtype required rules to exactly the
		// requested set, when provided. subtype_field_rules is read live by
		// the CRUD engine (validateRequired), so this alone makes the field
		// required -- no recompile, unlike a nullable flip. A rule row can
		// also carry a default_override, so we never blindly delete: turning
		// required off keeps a row that still has a default (just clears the
		// flag) and only deletes rows that existed solely to mark required,
		// matching addField's "a row means required" convention.
		if req.RequiredForSubtypes != nil {
			var subtypes []metadata.Subtype
			if err := tx.NewSelect().Model(&subtypes).Where("feature_class_id = ?", fc.ID).Scan(ctx); err != nil {
				return fmt.Errorf("loading subtypes for %q: %w", featureClassCode, err)
			}
			subtypeByCode := make(map[int]metadata.Subtype, len(subtypes))
			for _, s := range subtypes {
				subtypeByCode[s.Code] = s
			}
			wantRequired := make(map[string]bool, len(*req.RequiredForSubtypes))
			for _, c := range *req.RequiredForSubtypes {
				s, ok := subtypeByCode[c]
				if !ok {
					return invalid("required_for_subtypes references subtype code %d, which doesn't exist on this feature class", c)
				}
				wantRequired[s.ID] = true
			}

			var rules []metadata.SubtypeFieldRule
			if err := tx.NewSelect().Model(&rules).Where("field_id = ?", field.ID).Scan(ctx); err != nil {
				return fmt.Errorf("loading subtype_field_rules for field %q: %w", fieldName, err)
			}
			ruleBySubtype := make(map[string]metadata.SubtypeFieldRule, len(rules))
			for _, r := range rules {
				ruleBySubtype[r.SubtypeID] = r
			}

			for _, s := range subtypes {
				r, hasRule := ruleBySubtype[s.ID]
				switch {
				case wantRequired[s.ID] && !hasRule:
					rule := &metadata.SubtypeFieldRule{SubtypeID: s.ID, FieldID: field.ID, IsRequired: true}
					if _, err := tx.NewInsert().Model(rule).ExcludeColumn("id").Exec(ctx); err != nil {
						return fmt.Errorf("insert subtype_field_rule for subtype %d: %w", s.Code, err)
					}
				case wantRequired[s.ID] && hasRule && !r.IsRequired:
					r.IsRequired = true
					if _, err := tx.NewUpdate().Model(&r).Column("is_required").Where("id = ?", r.ID).Exec(ctx); err != nil {
						return fmt.Errorf("update subtype_field_rule for subtype %d: %w", s.Code, err)
					}
				case !wantRequired[s.ID] && hasRule && r.IsRequired:
					if r.DefaultOverride != nil {
						r.IsRequired = false
						if _, err := tx.NewUpdate().Model(&r).Column("is_required").Where("id = ?", r.ID).Exec(ctx); err != nil {
							return fmt.Errorf("update subtype_field_rule for subtype %d: %w", s.Code, err)
						}
					} else if _, err := tx.NewDelete().Model(&r).Where("id = ?", r.ID).Exec(ctx); err != nil {
						return fmt.Errorf("delete subtype_field_rule for subtype %d: %w", s.Code, err)
					}
				}
			}
		}

		resp = fieldResponse{
			Name: field.Name, DataType: field.DataType, Nullable: field.Nullable,
			VisibleOnCreate: field.VisibleOnCreate, VisibleOnUpdate: field.VisibleOnUpdate,
			EditableAfterPublish: field.EditableAfterPublish,
		}
		if req.RequiredForSubtypes != nil {
			// We reconciled the rules to exactly this set, so echo it back.
			resp.RequiredForSubtypes = *req.RequiredForSubtypes
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

		cols := make([]string, 0, 13)
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
		if req.CostPerKm != nil {
			m.CostPerKm = req.CostPerKm
			cols = append(cols, "cost_per_km")
		}
		if req.UnitCost != nil {
			m.UnitCost = req.UnitCost
			cols = append(cols, "unit_cost")
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
			CostPerKm: m.CostPerKm, UnitCost: m.UnitCost,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- Bill of Quantities cost catalog (migrations 0127/0128) ------------------

// updateCurrency patches a currency. Changing is_base or per_base re-checks the
// base-is-unit and single-base invariants (both also DB-enforced) so the caller
// gets a clear 400, not a constraint 500.
func updateCurrency(ctx context.Context, db *bun.DB, code string, req updateCurrencyRequest) (*currencyResponse, error) {
	if err := validateUpdateCurrency(req); err != nil {
		return nil, err
	}

	var resp currencyResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.Currency)
		if err := tx.NewSelect().Model(m).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("currency %q does not exist", code)
			}
			return fmt.Errorf("loading currency %q: %w", code, err)
		}

		cols := make([]string, 0, 3)
		if req.Name != nil {
			m.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.PerBase != nil {
			m.PerBase = *req.PerBase
			cols = append(cols, "per_base")
		}
		if req.IsBase != nil {
			m.IsBase = *req.IsBase
			cols = append(cols, "is_base")
		}

		if m.IsBase && m.PerBase != 1 {
			return invalid("the base currency's per_base must be exactly 1")
		}
		if m.IsBase {
			otherBase, err := tx.NewSelect().Model((*metadata.Currency)(nil)).
				Where("is_base").Where("code <> ?", code).Exists(ctx)
			if err != nil {
				return fmt.Errorf("checking existing base currency: %w", err)
			}
			if otherBase {
				return invalid("another currency is already the base -- only one can be the base")
			}
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating currency %q: %w", code, err)
		}
		resp = currencyResponse{Code: m.Code, Name: m.Name, PerBase: m.PerBase, IsBase: m.IsBase}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateCostRate patches a cost rate. If the patch moves the (class, subtype)
// composite key it re-checks uniqueness against every other row.
func updateCostRate(ctx context.Context, db *bun.DB, id string, req updateCostRateRequest) (*costRateResponse, error) {
	if err := validateUpdateCostRate(req); err != nil {
		return nil, err
	}

	var resp costRateResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(metadata.CostRate)
		if err := tx.NewSelect().Model(m).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("cost rate %q does not exist", id)
			}
			return fmt.Errorf("loading cost rate %q: %w", id, err)
		}

		cols := make([]string, 0, 4)
		if req.ClearSubtype {
			m.SubtypeCode = nil
			cols = append(cols, "subtype_code")
		} else if req.SubtypeCode != nil {
			m.SubtypeCode = req.SubtypeCode
			cols = append(cols, "subtype_code")
		}
		if req.UnitBasis != nil {
			m.UnitBasis = *req.UnitBasis
			cols = append(cols, "unit_basis")
		}
		if req.UnitRate != nil {
			m.UnitRate = *req.UnitRate
			cols = append(cols, "unit_rate")
		}
		if req.Notes != nil {
			m.Notes = req.Notes
			cols = append(cols, "notes")
		}

		dup, err := costRateExists(ctx, tx, m.FeatureClassCode, m.SubtypeCode, id)
		if err != nil {
			return err
		}
		if dup {
			return invalid("a cost rate for class %q and that subtype already exists", m.FeatureClassCode)
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("updating cost rate %q: %w", id, err)
		}
		resp = toCostRateResponse(m)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
