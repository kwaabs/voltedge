package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the write path for the two tables that together drive
// every rendered symbol: metadata.symbology_icons (the artwork behind an
// icon_ref, migrations/0023) and metadata.symbology_rules (which icon/
// color satisfies which subtype/domain-value, migrations/0012). Both were
// authored only by hand (seed/symbology_icons.sql,
// seed/symbology_example.sql) or via raw SQL until now -- the admin UI
// (web/src/routes/admin-symbology-icons.tsx, the rule editor on
// admin-feature-class-detail.tsx) writes through these so a user never has
// to reach for psql, though the DB stays directly editable for anyone who
// prefers to. Read-side counterparts live in api/cmd/server/main.go
// (GET /symbology/icons, the schema endpoint's pre-resolved symbology
// array); this is deliberately the mirror image of that resolution.

// ---- symbology icons ------------------------------------------------------

func listSymbologyIcons(ctx context.Context, db *bun.DB) ([]symbologyIconResponse, error) {
	icons, err := metadata.ListSymbologyIcons(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]symbologyIconResponse, len(icons))
	for i, ic := range icons {
		out[i] = symbologyIconResponse{IconRef: ic.IconRef, SVG: ic.SVG}
	}
	return out, nil
}

// createSymbologyIcon inserts one icon_ref -> svg row -- existence-checked
// first on the icon_ref primary key, the same friendly-400-over-raw-
// unique-violation discipline createDomain/createMaterialSpec already
// apply (the pk here is icon_ref itself, not a surrogate id, so there's no
// ExcludeColumn("id") as elsewhere).
func createSymbologyIcon(ctx context.Context, db *bun.DB, req createSymbologyIconRequest) (*symbologyIconResponse, error) {
	if err := validateSymbologyIcon(req.IconRef, req.SVG); err != nil {
		return nil, err
	}

	var resp symbologyIconResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := tx.NewSelect().Model((*metadata.SymbologyIcon)(nil)).Where("icon_ref = ?", req.IconRef).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking icon_ref %q: %w", req.IconRef, err)
		}
		if exists {
			return invalid("an icon with icon_ref %q already exists", req.IconRef)
		}

		ic := &metadata.SymbologyIcon{IconRef: req.IconRef, SVG: req.SVG}
		if _, err := tx.NewInsert().Model(ic).Exec(ctx); err != nil {
			return fmt.Errorf("insert symbology icon: %w", err)
		}

		resp = symbologyIconResponse{IconRef: ic.IconRef, SVG: ic.SVG}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateSymbologyIcon replaces one icon's markup in place -- the icon_ref
// is the identity a rule references and never changes here (renaming is a
// delete-and-recreate, see updateSymbologyIconRequest's own doc comment).
// Editing markup on an already-referenced icon is deliberately safe: it's
// exactly the "change what the symbol looks like without touching which
// rule uses it" case the icon-table split (docs/metadata-core.md) exists
// to allow.
func updateSymbologyIcon(ctx context.Context, db *bun.DB, iconRef string, req updateSymbologyIconRequest) (*symbologyIconResponse, error) {
	if req.SVG == nil {
		return nil, invalid("request must set svg")
	}
	if err := validateSymbologyIcon(iconRef, *req.SVG); err != nil {
		return nil, err
	}

	var resp symbologyIconResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ic := new(metadata.SymbologyIcon)
		if err := tx.NewSelect().Model(ic).Where("icon_ref = ?", iconRef).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("icon %q does not exist", iconRef)
			}
			return fmt.Errorf("loading icon %q: %w", iconRef, err)
		}
		ic.SVG = *req.SVG
		if _, err := tx.NewUpdate().Model(ic).Column("svg").Where("icon_ref = ?", iconRef).Exec(ctx); err != nil {
			return fmt.Errorf("updating icon %q: %w", iconRef, err)
		}
		resp = symbologyIconResponse{IconRef: ic.IconRef, SVG: ic.SVG}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteSymbologyIcon removes one icon, but only when no symbology rule
// still points at it. Unlike a foreign key, style_def.icon_ref is a plain
// string inside a jsonb blob (migrations/0012 never modeled it as a real
// FK -- an icon_ref can name a frontend fallback shape that has no DB row
// at all), so nothing at the database level would stop this delete; the
// precheck exists to turn a silently-broken rule (one whose icon vanished
// out from under it, falling back to a generic shape at render time) into
// a clear 400 naming the actual reason, the same discipline deleteDomain/
// deleteMaterialSpec apply for their real FKs.
func deleteSymbologyIcon(ctx context.Context, db *bun.DB, iconRef string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ic := new(metadata.SymbologyIcon)
		if err := tx.NewSelect().Model(ic).Where("icon_ref = ?", iconRef).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("icon %q does not exist", iconRef)
			}
			return fmt.Errorf("loading icon %q: %w", iconRef, err)
		}
		referenced, err := tx.NewSelect().Model((*metadata.SymbologyRule)(nil)).
			Where("style_def->>'icon_ref' = ?", iconRef).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking icon %q references: %w", iconRef, err)
		}
		if referenced {
			return invalid("icon %q is referenced by at least one symbology rule -- change or remove that rule first", iconRef)
		}
		if _, err := tx.NewDelete().Model(ic).Where("icon_ref = ?", iconRef).Exec(ctx); err != nil {
			return fmt.Errorf("deleting icon %q: %w", iconRef, err)
		}
		return nil
	})
}

// ---- symbology rules ------------------------------------------------------

// listSymbologyRules returns every rule for one feature class, each with
// its id and the same code-shaped targeting the rule editor edits by --
// the exact inverse of api/cmd/server/main.go's resolveSymbologySchema
// (which is id-less and pre-resolved for the read-only map matcher). Both
// walk the same subtype_id->code and domain_value_id->(field,value)
// resolution; this one keeps the id because editing a rule needs it.
func listSymbologyRules(ctx context.Context, db *bun.DB, code string) ([]symbologyRuleResponse, error) {
	fc := new(metadata.FeatureClass)
	if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, fmt.Errorf("loading feature class %q: %w", code, err)
	}

	rules, err := metadata.LoadSymbologyRules(ctx, db, fc.ID)
	if err != nil {
		return nil, err
	}

	subtypeCodeByID, fieldNameByDomainID, err := ruleLookups(ctx, db, fc.ID)
	if err != nil {
		return nil, err
	}

	// Resolve every referenced domain_value_id -> (domain_id, code) in one
	// query, same as resolveSymbologySchema.
	valueIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.DomainValueID != nil {
			valueIDs = append(valueIDs, *r.DomainValueID)
		}
	}
	valueByID := make(map[string]metadata.DomainValue)
	if len(valueIDs) > 0 {
		var values []metadata.DomainValue
		if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
			return nil, err
		}
		for _, v := range values {
			valueByID[v.ID] = v
		}
	}

	out := make([]symbologyRuleResponse, 0, len(rules))
	for _, r := range rules {
		resp := symbologyRuleResponse{
			ID: r.ID, RenderContext: r.RenderContext, StyleDef: r.StyleDef,
			MinZoom: r.MinZoom, MaxZoom: r.MaxZoom,
		}
		if r.SubtypeID != nil {
			if c, ok := subtypeCodeByID[*r.SubtypeID]; ok {
				resp.SubtypeCode = &c
			}
		}
		if r.DomainValueID != nil {
			if v, ok := valueByID[*r.DomainValueID]; ok {
				if name, ok := fieldNameByDomainID[v.DomainID]; ok {
					resp.DomainField = &name
				}
				vc := v.Code
				resp.DomainValueCode = &vc
			}
		}
		out = append(out, resp)
	}
	return out, nil
}

// ruleLookups builds the two reverse maps both the list resolver and the
// (subtype code -> id) resolution need: subtype_id -> code, and domain_id
// -> the name of whichever field on this class uses that domain. Pulled
// out so listSymbologyRules and the create/update path stay in step with
// each other and with resolveSymbologySchema.
func ruleLookups(ctx context.Context, db bun.IDB, featureClassID string) (map[string]int, map[string]string, error) {
	var subtypes []metadata.Subtype
	if err := db.NewSelect().Model(&subtypes).Where("feature_class_id = ?", featureClassID).Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("loading subtypes: %w", err)
	}
	subtypeCodeByID := make(map[string]int, len(subtypes))
	for _, s := range subtypes {
		subtypeCodeByID[s.ID] = s.Code
	}

	var fields []metadata.Field
	if err := db.NewSelect().Model(&fields).Where("feature_class_id = ?", featureClassID).Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("loading fields: %w", err)
	}
	fieldNameByDomainID := make(map[string]string, len(fields))
	for _, f := range fields {
		if f.DomainID != nil {
			fieldNameByDomainID[*f.DomainID] = f.Name
		}
	}
	return subtypeCodeByID, fieldNameByDomainID, nil
}

// createSymbologyRule inserts one rule for a feature class, resolving its
// code-shaped targeting to the real ids symbology_rules stores. A rule's
// targeting axes are how it wins (or loses) the most-specific match
// (migrations/0012); this endpoint never enforces the "exactly one
// mandatory default rule" invariant, deliberately -- and note that
// nothing else enforces it either (the compiler has no symbology check; a
// class without a default just falls back to plain geometry styling in the
// web matcher), the same reason deleteSymbologyRule below has no guard.
func createSymbologyRule(ctx context.Context, db *bun.DB, code string, req symbologyRuleRequest) (*symbologyRuleResponse, error) {
	if err := validateSymbologyRule(req); err != nil {
		return nil, err
	}

	var resp symbologyRuleResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}

		subtypeID, domainValueID, err := resolveRuleTargeting(ctx, tx, fc.ID, req)
		if err != nil {
			return err
		}
		styleDef, err := buildStyleDef(req.Style)
		if err != nil {
			return err
		}

		rule := &metadata.SymbologyRule{
			FeatureClassID: fc.ID,
			SubtypeID:      subtypeID,
			DomainValueID:  domainValueID,
			RenderContext:  req.RenderContext,
			MinZoom:        req.MinZoom,
			MaxZoom:        req.MaxZoom,
			StyleDef:       styleDef,
		}
		if _, err := tx.NewInsert().Model(rule).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert symbology rule: %w", err)
		}

		resp = symbologyRuleResponse{
			ID: rule.ID, RenderContext: req.RenderContext, StyleDef: styleDef,
			SubtypeCode: req.SubtypeCode, DomainField: req.DomainField, DomainValueCode: req.DomainValueCode,
			MinZoom: req.MinZoom, MaxZoom: req.MaxZoom,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateSymbologyRule replaces a rule wholesale (see symbologyRuleRequest's
// doc comment on why a rule is replaced, not patched). Keyed by the rule's
// own id rather than by feature-class code + some natural key, since a
// rule has no stable natural key -- two rules for the same class can share
// every targeting axis and differ only in render_context or style. The
// class the rule belongs to is loaded from the rule itself so its
// targeting still resolves against the right subtypes/fields.
func updateSymbologyRule(ctx context.Context, db *bun.DB, id string, req symbologyRuleRequest) (*symbologyRuleResponse, error) {
	if err := validateSymbologyRule(req); err != nil {
		return nil, err
	}

	var resp symbologyRuleResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		rule := new(metadata.SymbologyRule)
		if err := tx.NewSelect().Model(rule).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("symbology rule %q does not exist", id)
			}
			return fmt.Errorf("loading symbology rule %q: %w", id, err)
		}

		subtypeID, domainValueID, err := resolveRuleTargeting(ctx, tx, rule.FeatureClassID, req)
		if err != nil {
			return err
		}
		styleDef, err := buildStyleDef(req.Style)
		if err != nil {
			return err
		}

		rule.SubtypeID = subtypeID
		rule.DomainValueID = domainValueID
		rule.RenderContext = req.RenderContext
		rule.MinZoom = req.MinZoom
		rule.MaxZoom = req.MaxZoom
		rule.StyleDef = styleDef
		if _, err := tx.NewUpdate().Model(rule).
			Column("subtype_id", "domain_value_id", "render_context", "min_zoom", "max_zoom", "style_def").
			Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("updating symbology rule %q: %w", id, err)
		}

		resp = symbologyRuleResponse{
			ID: id, RenderContext: req.RenderContext, StyleDef: styleDef,
			SubtypeCode: req.SubtypeCode, DomainField: req.DomainField, DomainValueCode: req.DomainValueCode,
			MinZoom: req.MinZoom, MaxZoom: req.MaxZoom,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteSymbologyRule removes one rule by id, with no referential guard --
// deliberately, matching deleteDomainValue's philosophy. Deleting a rule
// can only ever remove specificity; the one invariant that matters (a
// class keeps exactly one mandatory feature-class-only default rule) is
// not enforced here or by the compiler (it has no symbology check). A class
// left with no default falls back to plain geometry styling in the web
// matcher.
func deleteSymbologyRule(ctx context.Context, db *bun.DB, id string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*metadata.SymbologyRule)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting symbology rule %q: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("symbology rule %q does not exist", id)
		}
		return nil
	})
}

// resolveRuleTargeting turns a rule request's code-shaped targeting into
// the ids symbology_rules stores -- the inverse of resolveSymbologySchema.
// SubtypeCode resolves against this class's own subtypes; (DomainField,
// DomainValueCode) resolves by finding the named field on this class,
// then the value with that code in the field's domain. An unresolvable
// code is a 400 naming exactly what didn't match, not a null foreign key
// or a Postgres FK-violation 500 three steps later (the same discipline
// resolveDomainCodes/resolveGroupCodes apply). A nil axis stays nil --
// "unconstrained", the most-specific-wins default.
func resolveRuleTargeting(ctx context.Context, tx bun.Tx, featureClassID string, req symbologyRuleRequest) (subtypeID *string, domainValueID *string, err error) {
	if req.SubtypeCode != nil {
		st := new(metadata.Subtype)
		if err := tx.NewSelect().Model(st).
			Where("feature_class_id = ? AND code = ?", featureClassID, *req.SubtypeCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil, invalid("subtype_code %d is not defined on this feature class", *req.SubtypeCode)
			}
			return nil, nil, fmt.Errorf("resolving subtype_code %d: %w", *req.SubtypeCode, err)
		}
		subtypeID = &st.ID
	}

	if req.DomainField != nil {
		// validateSymbologyRule already guaranteed DomainValueCode is set
		// whenever DomainField is.
		field := new(metadata.Field)
		if err := tx.NewSelect().Model(field).
			Where("feature_class_id = ? AND name = ?", featureClassID, *req.DomainField).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil, invalid("domain_field %q is not a field on this feature class", *req.DomainField)
			}
			return nil, nil, fmt.Errorf("resolving domain_field %q: %w", *req.DomainField, err)
		}
		if field.DomainID == nil {
			return nil, nil, invalid("domain_field %q has no domain -- it can't target a domain value", *req.DomainField)
		}
		dv := new(metadata.DomainValue)
		if err := tx.NewSelect().Model(dv).
			Where("domain_id = ? AND code = ?", *field.DomainID, *req.DomainValueCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil, invalid("domain_value_code %q is not a value of field %q's domain", *req.DomainValueCode, *req.DomainField)
			}
			return nil, nil, fmt.Errorf("resolving domain_value_code %q: %w", *req.DomainValueCode, err)
		}
		domainValueID = &dv.ID
	}

	return subtypeID, domainValueID, nil
}

// buildStyleDef marshals only the style fields actually set into the
// style_def jsonb -- the same shape createFeatureClass builds inline and
// web/src/lib/symbology.ts's StyleDef reads. Writing only present keys (a
// nil FillColor never becomes "fill_color": null) keeps a point rule's
// blob free of line/polygon keys it doesn't use, so a consumer never has
// to distinguish "unset" from "explicitly null".
func buildStyleDef(style styleDefRequest) (json.RawMessage, error) {
	m := make(map[string]any, 6)
	if style.IconRef != nil {
		m["icon_ref"] = *style.IconRef
	}
	if style.Color != nil {
		m["color"] = *style.Color
	}
	if style.Scale != nil {
		m["scale"] = *style.Scale
	}
	if style.Width != nil {
		m["width"] = *style.Width
	}
	if style.Dash != nil {
		m["dash"] = *style.Dash
	}
	if style.FillColor != nil {
		m["fill_color"] = *style.FillColor
	}
	if style.StrokeColor != nil {
		m["stroke_color"] = *style.StrokeColor
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal style_def: %w", err)
	}
	return raw, nil
}
