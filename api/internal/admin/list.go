package admin

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// listFeatureClasses backs the admin catalog view -- every class
// regardless of status (unlike GET /feature-classes, which the map/
// layers panel use and deliberately only shows active ones), plus a
// subtype/field count so the table gives a real sense of each class's
// size without a second round trip per row.
func listFeatureClasses(ctx context.Context, db *bun.DB) ([]featureClassSummary, error) {
	classes, err := metadata.ListAllFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}

	subtypeCounts, err := countByFeatureClass(ctx, db, (*metadata.Subtype)(nil))
	if err != nil {
		return nil, fmt.Errorf("counting subtypes: %w", err)
	}
	fieldCounts, err := countByFeatureClass(ctx, db, (*metadata.Field)(nil))
	if err != nil {
		return nil, fmt.Errorf("counting fields: %w", err)
	}

	out := make([]featureClassSummary, len(classes))
	for i, fc := range classes {
		out[i] = featureClassSummary{
			Code:               fc.Code,
			Name:               fc.Name,
			GeometryType:       fc.GeometryType,
			SRID:               fc.SRID,
			NetworkRole:        fc.NetworkRole,
			Status:             fc.Status,
			IsDiagramContainer: fc.IsDiagramContainer,
			PhysicalTable:      fc.PhysicalTable,
			SubtypeCount:       subtypeCounts[fc.ID],
			FieldCount:         fieldCounts[fc.ID],
		}
	}
	return out, nil
}

// countByFeatureClass groups either metadata.subtypes or metadata.fields
// by feature_class_id in one query -- the same generic-model trick
// codeExists uses, so listFeatureClasses doesn't run one COUNT query per
// row.
func countByFeatureClass(ctx context.Context, db bun.IDB, model any) (map[string]int, error) {
	var rows []struct {
		FeatureClassID string `bun:"feature_class_id"`
		Count          int    `bun:"count"`
	}
	if err := db.NewSelect().Model(model).
		ColumnExpr("feature_class_id").
		ColumnExpr("count(*) AS count").
		GroupExpr("feature_class_id").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.FeatureClassID] = r.Count
	}
	return out, nil
}

// listDomains returns every domain with its values attached -- the
// admin domains view and the feature-class wizard's own domain picker
// both want the full set with values inline, not a second fetch per
// domain.
func listDomains(ctx context.Context, db *bun.DB) ([]domainResponse, error) {
	domains, err := metadata.ListDomains(ctx, db)
	if err != nil {
		return nil, err
	}
	values, err := metadata.ListDomainValues(ctx, db)
	if err != nil {
		return nil, err
	}
	materialSpecs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return nil, err
	}
	materialSpecCodeByID := make(map[string]string, len(materialSpecs))
	for _, m := range materialSpecs {
		materialSpecCodeByID[m.ID] = m.Code
	}

	valuesByDomain := make(map[string][]domainValueResponse, len(domains))
	for _, v := range values {
		var materialSpecCode *string
		if v.MaterialSpecID != nil {
			if code, ok := materialSpecCodeByID[*v.MaterialSpecID]; ok {
				materialSpecCode = &code
			}
		}
		valuesByDomain[v.DomainID] = append(valuesByDomain[v.DomainID], domainValueResponse{
			Code: v.Code, Label: v.Label, SortOrder: v.SortOrder, MaterialSpecCode: materialSpecCode,
		})
	}

	out := make([]domainResponse, len(domains))
	for i, d := range domains {
		out[i] = domainResponse{
			Code: d.Code, Name: d.Name, DomainType: d.DomainType, Values: valuesByDomain[d.ID],
			RangeMin: d.RangeMin, RangeMax: d.RangeMax,
		}
	}
	return out, nil
}

// listMaterialSpecs returns every material spec -- the materials admin
// page's own list view, and the domains page's own "attach a material
// spec" picker.
func listMaterialSpecs(ctx context.Context, db *bun.DB) ([]materialSpecResponse, error) {
	specs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]materialSpecResponse, len(specs))
	for i, m := range specs {
		out[i] = materialSpecResponse{
			Code: m.Code, Name: m.Name, Category: m.Category,
			ResistanceOhmPerKm: m.ResistanceOhmPerKm, ReactanceOhmPerKm: m.ReactanceOhmPerKm,
			AmpacityAmps: m.AmpacityAmps, DiameterMm: m.DiameterMm, WeightKgPerKm: m.WeightKgPerKm,
			MaxOperatingTempC: m.MaxOperatingTempC, Emissivity: m.Emissivity,
			Absorptivity: m.Absorptivity, ResistanceTempCoefficientPerC: m.ResistanceTempCoefficientPerC,
		}
	}
	return out, nil
}

func listFieldGroups(ctx context.Context, db *bun.DB) ([]fieldGroupResponse, error) {
	groups, err := metadata.ListFieldGroups(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]fieldGroupResponse, len(groups))
	for i, g := range groups {
		out[i] = fieldGroupResponse{Code: g.Code, Name: g.Name, SortOrder: g.SortOrder, Icon: g.Icon}
	}
	return out, nil
}
