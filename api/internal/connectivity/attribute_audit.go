package connectivity

// attribute_audit.go is the data-quality half of the validation framework.
// Alongside the connectivity checks (audit.go) it scans existing feature rows for
// values that no longer conform to the class's CURRENT metadata:
//
//   - attr_required_missing -- a non-nullable field holding NULL (a legacy row, or
//     a NOT NULL that wasn't applied on an ALTER);
//   - attr_out_of_domain    -- a coded value no longer in its domain (domain drift);
//   - attr_out_of_range     -- a number outside a since-tightened range.
//
// These are "drift" problems the write-time validation can't catch -- they were
// valid when written and the metadata changed since. They flow into the SAME
// ops.validation_issues queue (via ReconcileIssues, keyed per feature+field) and
// auto-resolve when the field is fixed, exactly like the connectivity findings.

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// AuditAll runs the connectivity audit then the attribute (data-quality) audit and
// returns one merged report. The reconcile endpoint and the incremental sweep use
// it so BOTH kinds of finding share the queue and its auto-resolve -- reconciling
// them separately would wrongly auto-resolve one kind while validating the other.
func AuditAll(ctx context.Context, db bun.IDB, in AuditInput) (*AuditReport, error) {
	report, err := AuditConnectivity(ctx, db, in)
	if err != nil {
		return nil, err
	}
	maxFindings := in.MaxFindings
	if maxFindings <= 0 {
		maxFindings = auditDefaultMaxFindings
	}
	remaining := maxFindings - len(report.Findings)
	if remaining <= 0 {
		report.Summary.Truncated = true
		return report, nil
	}
	attrs, truncated, err := AuditAttributes(ctx, db, in.FeatureIDs, remaining)
	if err != nil {
		return nil, err
	}
	report.Findings = append(report.Findings, attrs...)
	report.Summary.AttributeIssues = len(attrs)
	if truncated {
		report.Summary.Truncated = true
	}

	// Topology loops (switch-state-respecting) ride the same report so they share
	// the queue + auto-resolve. Only run while there's headroom in the findings cap.
	if remaining = maxFindings - len(report.Findings); remaining > 0 {
		loops, loopTrunc, lerr := auditTopologyLoops(ctx, db, in.FeatureIDs, remaining)
		if lerr != nil {
			return nil, lerr
		}
		report.Findings = append(report.Findings, loops...)
		report.Summary.TopologyLoops = len(loops)
		if loopTrunc {
			report.Summary.Truncated = true
		}
	}
	return report, nil
}

// AuditAttributes scans the scoped features (or every compiled class when the
// scope is empty) for data-quality divergences from the current metadata, capped
// at maxFindings. Read-only. Returns the findings and whether the cap was hit.
func AuditAttributes(ctx context.Context, db bun.IDB, scope []string, maxFindings int) ([]AuditFinding, bool, error) {
	if maxFindings <= 0 {
		maxFindings = auditDefaultMaxFindings
	}
	codes, err := attributeScopeClasses(ctx, db, scope)
	if err != nil {
		return nil, false, err
	}

	// The admin-defined completeness rules (rules.go), grouped by class, run
	// alongside the metadata-derived checks below.
	allRules, err := ListActiveValidationRules(ctx, db)
	if err != nil {
		return nil, false, err
	}
	rulesByClass := map[string][]ValidationRule{}
	for _, rule := range allRules {
		rulesByClass[rule.FeatureClassCode] = append(rulesByClass[rule.FeatureClassCode], rule)
	}

	out := make([]AuditFinding, 0, 16)
	for _, code := range codes {
		if len(out) >= maxFindings {
			return out, true, nil
		}
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil || loaded.FeatureClass.PhysicalTable == nil {
			continue // unknown / not compiled -- nothing to scan
		}
		table := *loaded.FeatureClass.PhysicalTable

		// The class's real field names + whether it has subtypes -- used to skip a
		// stale completeness rule (one whose field no longer exists) rather than
		// error on a missing column, and to only apply a subtype-conditioned rule
		// where there is a subtype column.
		fieldSet := make(map[string]bool, len(loaded.Fields))
		for i := range loaded.Fields {
			fieldSet[loaded.Fields[i].Name] = true
		}
		hasSubtypes := len(loaded.Subtypes) > 0

		for i := range loaded.Fields {
			f := loaded.Fields[i]
			if f.IsComputed {
				continue
			}
			col := attrQuoteIdent(f.Name)

			if !f.Nullable {
				if err := appendAttrFindings(ctx, db, &out, maxFindings, attrQuery{
					table: table, valueExpr: "NULL::text", predicate: col + " IS NULL", scope: scope,
					kind: "attr_required_missing", severity: "error", class: code, field: f.Name,
					message: fmt.Sprintf("Required field %q is not set", f.Name),
				}); err != nil {
					return nil, false, err
				}
			}

			if f.DomainID != nil {
				dom := loaded.Domains[*f.DomainID]
				if dom == nil {
					continue
				}
				switch dom.DomainType {
				case "coded":
					vals := loaded.DomainValues[*f.DomainID]
					if len(vals) == 0 {
						continue
					}
					if err := appendAttrFindings(ctx, db, &out, maxFindings, attrQuery{
						table: table, valueExpr: col + "::text",
						predicate: col + " IS NOT NULL AND " + col + "::text NOT IN (" + attrQuoteList(vals) + ")",
						scope: scope, kind: "attr_out_of_domain", severity: "warning", class: code, field: f.Name,
						message: fmt.Sprintf("Field %q holds a value no longer in its domain", f.Name),
					}); err != nil {
						return nil, false, err
					}
				case "range":
					if dom.RangeMin == nil || dom.RangeMax == nil {
						continue
					}
					lo, hi := attrNum(*dom.RangeMin), attrNum(*dom.RangeMax)
					if err := appendAttrFindings(ctx, db, &out, maxFindings, attrQuery{
						table: table, valueExpr: col + "::text",
						predicate: fmt.Sprintf("%s IS NOT NULL AND (%s < %s OR %s > %s)", col, col, lo, col, hi),
						scope: scope, kind: "attr_out_of_range", severity: "warning", class: code, field: f.Name,
						message: fmt.Sprintf("Field %q is outside its allowed range [%s, %s]", f.Name, lo, hi),
					}); err != nil {
						return nil, false, err
					}
				}
			}

			if len(out) >= maxFindings {
				return out, true, nil
			}
		}

		// Completeness / business rules (rules.go): a field EXPECTED to be
		// populated (optionally only for one subtype), beyond the schema's own
		// required/domain/range.
		for _, rule := range rulesByClass[code] {
			if len(out) >= maxFindings {
				return out, true, nil
			}
			if !fieldSet[rule.FieldName] {
				continue // stale rule -- its field is no longer on the class
			}
			pred := attrQuoteIdent(rule.FieldName) + " IS NULL"
			if rule.SubtypeCode != nil && hasSubtypes {
				pred += fmt.Sprintf(" AND subtype = %d", *rule.SubtypeCode)
			}
			msg := fmt.Sprintf("Field %q is expected but not set", rule.FieldName)
			if rule.Message != nil && strings.TrimSpace(*rule.Message) != "" {
				msg = strings.TrimSpace(*rule.Message)
			}
			if err := appendAttrFindings(ctx, db, &out, maxFindings, attrQuery{
				table: table, valueExpr: "NULL::text", predicate: pred, scope: scope,
				kind: "attr_completeness", severity: rule.Severity, class: code, field: rule.FieldName,
				message: msg,
			}); err != nil {
				return nil, false, err
			}
		}
	}
	return out, false, nil
}

// attributeScopeClasses resolves which classes to scan: every compiled class for a
// whole-DB run, or just the classes of the scoped features (via the cross-class
// registry) for the incremental path.
func attributeScopeClasses(ctx context.Context, db bun.IDB, scope []string) ([]string, error) {
	if len(scope) == 0 {
		fcs, err := metadata.ListFeatureClasses(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("connectivity: attribute audit: listing classes: %w", err)
		}
		codes := make([]string, 0, len(fcs))
		for _, fc := range fcs {
			codes = append(codes, fc.Code)
		}
		return codes, nil
	}
	var codes []string
	if err := db.NewSelect().
		ColumnExpr("DISTINCT feature_class_code").
		TableExpr("runtime.feature_registry").
		Where("feature_id::text IN (?)", bun.In(scope)).
		Where("deleted_at IS NULL").
		Scan(ctx, &codes); err != nil {
		return nil, fmt.Errorf("connectivity: attribute audit: scope classes: %w", err)
	}
	return codes, nil
}

// attrQuery is one data-quality scan: a WHERE predicate over a class's physical
// table, plus how to label the findings it yields.
type attrQuery struct {
	table, valueExpr, predicate         string
	scope                               []string
	kind, severity, class, field, message string
}

// appendAttrFindings runs one scan and appends an AuditFinding per violating row
// (up to the remaining cap), carrying the offending value into the message. The
// table/column/value expressions come from trusted metadata (physical_table + the
// admin-authored field name and domain values), interpolated the same way the
// connectivity audit interpolates physical_table; the scope ids are bound.
func appendAttrFindings(ctx context.Context, db bun.IDB, out *[]AuditFinding, maxFindings int, q attrQuery) error {
	limit := maxFindings - len(*out)
	if limit <= 0 {
		return nil
	}
	sqlStr := "SELECT id::text, " + q.valueExpr + " FROM " + q.table + " WHERE deleted_at IS NULL AND (" + q.predicate + ")"
	var args []any
	if len(q.scope) > 0 {
		sqlStr += " AND id::text IN (?)"
		args = append(args, bun.In(q.scope))
	}
	sqlStr += " LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("connectivity: attribute audit %s on %s: %w", q.kind, q.table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var val sql.NullString
		if err := rows.Scan(&id, &val); err != nil {
			return fmt.Errorf("connectivity: attribute audit scan: %w", err)
		}
		msg := q.message
		if val.Valid && val.String != "" {
			msg += " — \"" + val.String + "\""
		}
		*out = append(*out, AuditFinding{
			Kind: q.kind, Severity: q.severity, Message: msg,
			FromClass: q.class, FromID: id, Field: q.field,
		})
	}
	return rows.Err()
}

func attrQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func attrQuoteList(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return strings.Join(q, ", ")
}

func attrNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
