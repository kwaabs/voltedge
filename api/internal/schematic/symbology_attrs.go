package schematic

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

// enrichSymbologyAttrs attaches, to each node/connector, the values of the columns
// its class's symbology rules actually key off -- so the client resolves the SAME
// rule the map does. The one-line normally projects only subtype/circuit_ref, which
// can't match an attribute-driven rule (e.g. a conductor coloured by voltage), so
// those features fall back to the class default (looks like "no symbology" / a wrong
// colour). This runs on the operations path only (best-effort: an error here must not
// fail the whole one-line) and reads only the referenced columns, keyed by id.
func enrichSymbologyAttrs(ctx context.Context, db bun.IDB, one *OneLine) error {
	// Which attribute columns does each class's symbology reference?
	cols, err := symbologyAttrColumns(ctx, db)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}

	classes, err := allClasses(ctx, db)
	if err != nil {
		return err
	}
	byCode := make(map[string]*classMeta, len(classes))
	for i := range classes {
		byCode[classes[i].code] = &classes[i]
	}

	// Ids per class across both nodes and conductors (a conductor is a feature too).
	idsByCode := make(map[string]map[string]bool)
	addRef := func(code, id string) {
		if code == "" || id == "" || len(cols[code]) == 0 {
			return
		}
		if idsByCode[code] == nil {
			idsByCode[code] = map[string]bool{}
		}
		idsByCode[code][id] = true
	}
	for i := range one.Nodes {
		addRef(one.Nodes[i].FeatureClass, one.Nodes[i].FeatureID)
	}
	for i := range one.Connectors {
		addRef(one.Connectors[i].FeatureClass, one.Connectors[i].FeatureID)
	}
	if len(idsByCode) == 0 {
		return nil
	}

	// id -> attributes map, read per class (only the referenced columns).
	attrByID := make(map[string]map[string]any)
	for code, idSet := range idsByCode {
		cm := byCode[code]
		if cm == nil {
			continue
		}
		fields := cols[code]
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}

		sel := []string{"id::text AS id"}
		for _, f := range fields {
			sel = append(sel, quoteIdent(f))
		}
		args := make([]any, 0, len(ids))
		placeholders := make([]string, len(ids))
		for i := range ids {
			placeholders[i] = "?"
			args = append(args, ids[i])
		}
		query := fmt.Sprintf(
			"SELECT %s FROM %s WHERE deleted_at IS NULL AND id IN (%s)",
			strings.Join(sel, ", "), cm.table, strings.Join(placeholders, ", "),
		)
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("schematic: symbology attrs for %s: %w", code, err)
		}
		colNames, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			vals := make([]any, len(colNames))
			ptrs := make([]any, len(colNames))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return err
			}
			row := map[string]any{}
			for i, c := range colNames {
				row[c] = normalizeAttr(vals[i])
			}
			id := toString(row["id"])
			delete(row, "id")
			attrByID[id] = row
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	for i := range one.Nodes {
		if a, ok := attrByID[one.Nodes[i].FeatureID]; ok {
			one.Nodes[i].Attributes = a
		}
	}
	for i := range one.Connectors {
		if a, ok := attrByID[one.Connectors[i].FeatureID]; ok {
			one.Connectors[i].Attributes = a
		}
	}
	return nil
}

// symbologyAttrColumns returns, per feature-class CODE, the distinct attribute
// column names that class's symbology rules compare against -- a domain-coded
// field (via a rule's domain_value -> domain -> field) or a raw match_field
// (migration 0193). Only these columns need carrying for the client to resolve the
// same rule the map does.
func symbologyAttrColumns(ctx context.Context, db bun.IDB) (map[string][]string, error) {
	var rows []struct {
		ClassCode string `bun:"class_code"`
		FieldName string `bun:"field_name"`
	}
	const q = `
SELECT fc.code AS class_code, f.name AS field_name
FROM metadata.symbology_rules sr
JOIN metadata.feature_classes fc ON fc.id = sr.feature_class_id
JOIN metadata.domain_values dv ON dv.id = sr.domain_value_id
JOIN metadata.fields f ON f.feature_class_id = fc.id AND f.domain_id = dv.domain_id
WHERE sr.domain_value_id IS NOT NULL
UNION
SELECT fc.code AS class_code, sr.match_field AS field_name
FROM metadata.symbology_rules sr
JOIN metadata.feature_classes fc ON fc.id = sr.feature_class_id
WHERE sr.match_field IS NOT NULL AND sr.match_field <> ''`
	if err := db.NewRaw(q).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("schematic: symbology attr columns: %w", err)
	}
	out := make(map[string][]string)
	seen := make(map[string]bool)
	for _, r := range rows {
		if r.FieldName == "" {
			continue
		}
		k := r.ClassCode + "\x00" + r.FieldName
		if seen[k] {
			continue
		}
		seen[k] = true
		out[r.ClassCode] = append(out[r.ClassCode], r.FieldName)
	}
	return out, nil
}

// normalizeAttr renders a scanned column value as a client-friendly scalar. The
// symbology matcher compares against the domain value's CODE (a string), so coerce
// []byte (text/numeric come back as bytes over database/sql) to string; leave other
// scalars as-is.
func normalizeAttr(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	default:
		return v
	}
}
