package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// createDomain inserts a domain and its values in one transaction --
// existence-checked first (see the package doc comment on why this
// package prefers a friendly 400 over a raw Postgres unique-violation
// bubbling up as a 500) rather than relying on the UNIQUE constraint
// alone to reject a duplicate.
func createDomain(ctx context.Context, db *bun.DB, req createDomainRequest) (*domainResponse, error) {
	if err := validateCreateDomain(req); err != nil {
		return nil, err
	}

	var resp domainResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*metadata.Domain)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("a domain with code %q already exists", req.Code)
		}

		d := &metadata.Domain{Code: req.Code, Name: req.Name, DomainType: req.DomainType, RangeMin: req.RangeMin, RangeMax: req.RangeMax}
		if _, err := tx.NewInsert().Model(d).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert domain: %w", err)
		}

		values := make([]domainValueResponse, 0, len(req.Values))
		for _, v := range req.Values {
			dv := &metadata.DomainValue{DomainID: d.ID, Code: v.Code, Label: v.Label, SortOrder: v.SortOrder}
			if v.MaterialSpecCode != nil {
				id, err := resolveMaterialSpecCode(ctx, tx, *v.MaterialSpecCode)
				if err != nil {
					return err
				}
				dv.MaterialSpecID = &id
			}
			if _, err := tx.NewInsert().Model(dv).ExcludeColumn("id").Exec(ctx); err != nil {
				return fmt.Errorf("insert domain value %q: %w", v.Code, err)
			}
			values = append(values, domainValueResponse{Code: v.Code, Label: v.Label, SortOrder: v.SortOrder, MaterialSpecCode: v.MaterialSpecCode})
		}

		resp = domainResponse{
			Code: req.Code, Name: req.Name, DomainType: req.DomainType, Values: values,
			RangeMin: req.RangeMin, RangeMax: req.RangeMax,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func createFieldGroup(ctx context.Context, db *bun.DB, req createFieldGroupRequest) (*fieldGroupResponse, error) {
	if err := validateCreateFieldGroup(req); err != nil {
		return nil, err
	}

	var resp fieldGroupResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*metadata.FieldGroup)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("a field group with code %q already exists", req.Code)
		}

		fg := &metadata.FieldGroup{Code: req.Code, Name: req.Name, SortOrder: req.SortOrder, Icon: req.Icon}
		if _, err := tx.NewInsert().Model(fg).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert field group: %w", err)
		}

		resp = fieldGroupResponse{Code: req.Code, Name: req.Name, SortOrder: req.SortOrder, Icon: req.Icon}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// createFeatureClass is the wizard's real work: one transaction that
// inserts feature_classes, subtypes, fields, subtype_field_rules,
// terminals, and (optionally) one default map-context symbology_rules
// row -- exactly the shape every hand-written seed/*.sql file has built
// by hand so far (see e.g. seed/protective_relay_example.sql), just
// through a real API instead of psql. Domain and field-group references
// are resolved against what already exists (this endpoint doesn't create
// either inline, see fieldRequest's own doc comment) -- an unresolvable
// code is a 400, not a silently-null foreign key.
func createFeatureClass(ctx context.Context, db *bun.DB, req createFeatureClassRequest) (*createFeatureClassResponse, error) {
	if err := validateCreateFeatureClass(req); err != nil {
		return nil, err
	}

	var resp createFeatureClassResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*metadata.FeatureClass)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("a feature class with code %q already exists", req.Code)
		}

		domainIDByCode, err := resolveDomainCodes(ctx, tx, req.Fields)
		if err != nil {
			return err
		}
		groupIDByCode, err := resolveGroupCodes(ctx, tx, req.Fields)
		if err != nil {
			return err
		}

		fc := &metadata.FeatureClass{
			Code:               req.Code,
			Name:               req.Name,
			GeometryType:       req.GeometryType,
			SRID:               req.SRID,
			NetworkRole:        req.NetworkRole,
			Status:             "draft",
			IsDiagramContainer: req.IsDiagramContainer,
		}
		if _, err := tx.NewInsert().Model(fc).ExcludeColumn("id", "physical_table", "metadata_version_id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert feature class: %w", err)
		}

		subtypeIDByCode := make(map[int]string, len(req.Subtypes))
		for _, s := range req.Subtypes {
			st := &metadata.Subtype{FeatureClassID: fc.ID, Code: s.Code, Name: s.Name}
			if _, err := tx.NewInsert().Model(st).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
				return fmt.Errorf("insert subtype %d: %w", s.Code, err)
			}
			subtypeIDByCode[s.Code] = st.ID
		}

		for _, f := range req.Fields {
			field := &metadata.Field{
				FeatureClassID:       fc.ID,
				Name:                 f.Name,
				DataType:             f.DataType,
				Nullable:             f.Nullable,
				Status:               "active",
				VisibleOnCreate:      f.VisibleOnCreate,
				VisibleOnUpdate:      f.VisibleOnUpdate,
				EditableAfterPublish: f.EditableAfterPublish,
			}
			if f.DomainCode != nil {
				id := domainIDByCode[*f.DomainCode]
				field.DomainID = &id
			}
			if f.GroupCode != nil {
				id := groupIDByCode[*f.GroupCode]
				field.GroupID = &id
			}
			if _, err := tx.NewInsert().Model(field).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
				return fmt.Errorf("insert field %q: %w", f.Name, err)
			}
			for _, sc := range f.RequiredForSubtypes {
				rule := &metadata.SubtypeFieldRule{SubtypeID: subtypeIDByCode[sc], FieldID: field.ID, IsRequired: true}
				if _, err := tx.NewInsert().Model(rule).ExcludeColumn("id").Exec(ctx); err != nil {
					return fmt.Errorf("insert subtype_field_rule for %q/subtype %d: %w", f.Name, sc, err)
				}
			}
		}

		for _, t := range req.Terminals {
			term := &metadata.Terminal{FeatureClassID: fc.ID, Code: t.Code, Role: t.Role, PhaseCapable: t.PhaseCapable}
			if t.SubtypeCode != nil {
				id := subtypeIDByCode[*t.SubtypeCode]
				term.SubtypeID = &id
			}
			if _, err := tx.NewInsert().Model(term).ExcludeColumn("id").Exec(ctx); err != nil {
				return fmt.Errorf("insert terminal %q: %w", t.Code, err)
			}
		}

		if req.Symbology != nil {
			styleDef := map[string]any{"icon_ref": req.Symbology.IconRef, "color": req.Symbology.Color}
			if req.Symbology.Width != nil {
				styleDef["width"] = *req.Symbology.Width
			}
			if req.Symbology.Dash != nil {
				styleDef["dash"] = *req.Symbology.Dash
			}
			raw, err := json.Marshal(styleDef)
			if err != nil {
				return fmt.Errorf("marshal symbology style_def: %w", err)
			}
			rule := &metadata.SymbologyRule{FeatureClassID: fc.ID, RenderContext: "map", StyleDef: raw}
			if _, err := tx.NewInsert().Model(rule).ExcludeColumn("id").Exec(ctx); err != nil {
				return fmt.Errorf("insert symbology rule: %w", err)
			}
		}

		resp = createFeatureClassResponse{
			Code:           req.Code,
			SubtypesAdded:  len(req.Subtypes),
			FieldsAdded:    len(req.Fields),
			TerminalsAdded: len(req.Terminals),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// codeExists is a tiny, model-agnostic existence check -- every code
// column in this package's scope (domains, field_groups, feature_classes)
// is named "code" and unique, so one generic query serves all three
// instead of three near-identical ones.
func codeExists(ctx context.Context, db bun.IDB, model any, code string) (bool, error) {
	exists, err := db.NewSelect().Model(model).Where("code = ?", code).Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("checking code %q: %w", code, err)
	}
	return exists, nil
}

// resolveDomainCodes looks up every distinct domain_code referenced by
// req.Fields against real, already-existing domains -- an unresolvable
// code is a 400 naming exactly which field and code, not a foreign-key
// violation surfacing from the insert three steps later.
func resolveDomainCodes(ctx context.Context, tx bun.Tx, fields []fieldRequest) (map[string]string, error) {
	codes := make(map[string]bool)
	for _, f := range fields {
		if f.DomainCode != nil {
			codes[*f.DomainCode] = true
		}
	}
	out := make(map[string]string, len(codes))
	for code := range codes {
		var d metadata.Domain
		if err := tx.NewSelect().Model(&d).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil, invalid("domain_code %q does not exist -- create it first", code)
			}
			return nil, fmt.Errorf("resolving domain %q: %w", code, err)
		}
		out[code] = d.ID
	}
	return out, nil
}

func resolveGroupCodes(ctx context.Context, tx bun.Tx, fields []fieldRequest) (map[string]string, error) {
	codes := make(map[string]bool)
	for _, f := range fields {
		if f.GroupCode != nil {
			codes[*f.GroupCode] = true
		}
	}
	out := make(map[string]string, len(codes))
	for code := range codes {
		var g metadata.FieldGroup
		if err := tx.NewSelect().Model(&g).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil, invalid("group_code %q does not exist -- create it first", code)
			}
			return nil, fmt.Errorf("resolving field group %q: %w", code, err)
		}
		out[code] = g.ID
	}
	return out, nil
}

// resolveMaterialSpecCode looks up one material_spec_code against real,
// already-existing metadata.material_specs rows -- same reasoning as
// resolveDomainCodes/resolveGroupCodes above, just for a single code at
// a time since callers (addDomainValue, createDomain's inline values,
// updateDomainValue) each only ever have one to resolve, not a batch
// across many fields.
func resolveMaterialSpecCode(ctx context.Context, tx bun.Tx, code string) (string, error) {
	var m metadata.MaterialSpec
	if err := tx.NewSelect().Model(&m).Where("code = ?", code).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", invalid("material_spec_code %q does not exist -- create it first", code)
		}
		return "", fmt.Errorf("resolving material spec %q: %w", code, err)
	}
	return m.ID, nil
}

// createMaterialSpec inserts a new material_specs catalog row --
// existence-checked first, same discipline codeExists/createDomain
// already apply.
func createMaterialSpec(ctx context.Context, db *bun.DB, req createMaterialSpecRequest) (*materialSpecResponse, error) {
	if err := validateCreateMaterialSpec(req); err != nil {
		return nil, err
	}

	var resp materialSpecResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*metadata.MaterialSpec)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("a material spec with code %q already exists", req.Code)
		}

		m := &metadata.MaterialSpec{
			Code: req.Code, Name: req.Name, Category: req.Category,
			ResistanceOhmPerKm: req.ResistanceOhmPerKm, ReactanceOhmPerKm: req.ReactanceOhmPerKm,
			AmpacityAmps: req.AmpacityAmps, DiameterMm: req.DiameterMm, WeightKgPerKm: req.WeightKgPerKm,
			MaxOperatingTempC: req.MaxOperatingTempC, Emissivity: req.Emissivity,
			Absorptivity: req.Absorptivity, ResistanceTempCoefficientPerC: req.ResistanceTempCoefficientPerC,
		}
		if _, err := tx.NewInsert().Model(m).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert material spec: %w", err)
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
