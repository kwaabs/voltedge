package connectivity

// validation_rules.go is the admin-editable COMPLETENESS / business-rule catalog
// behind the attribute audit (migration 0155, ops.validation_rules). Each rule
// says a field is EXPECTED to be populated on a class (optionally only for one
// subtype) -- softer than the schema-enforced required/domain/range, and
// deployment-specific, so it lives as policy an admin tunes rather than schema the
// compiler reads. The attribute audit (attribute_audit.go) reads the active rules
// and scans existing rows for the fields left unset; violations flow into
// ops.validation_issues (kind attr_completeness), auto-resolving when filled.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// ValidationRule is one row of ops.validation_rules.
type ValidationRule struct {
	bun.BaseModel `bun:"table:ops.validation_rules"`

	ID               string  `bun:"id,pk" json:"id"`
	FeatureClassCode string  `bun:"feature_class_code" json:"feature_class_code"`
	FieldName        string  `bun:"field_name" json:"field_name"`
	SubtypeCode      *int    `bun:"subtype_code" json:"subtype_code"`
	Severity         string  `bun:"severity" json:"severity"`
	Message          *string `bun:"message" json:"message"`
	SortOrder        int     `bun:"sort_order" json:"sort_order"`
	Active           bool    `bun:"active" json:"active"`
}

// ListValidationRules returns a class's rules in display order. includeInactive
// adds retired rules (the admin editor); the audit calls ListActiveValidationRules.
func ListValidationRules(ctx context.Context, db bun.IDB, featureClass string, includeInactive bool) ([]ValidationRule, error) {
	featureClass = strings.TrimSpace(featureClass)
	if featureClass == "" {
		return []ValidationRule{}, nil
	}
	rules := []ValidationRule{}
	q := db.NewSelect().Model(&rules).Where("feature_class_code = ?", featureClass).OrderExpr("sort_order, field_name")
	if !includeInactive {
		q = q.Where("active")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: listing validation rules for %q: %w", featureClass, err)
	}
	return rules, nil
}

// ListActiveValidationRules returns every active rule, for the attribute audit to
// group by class. A small, admin-sized set, fetched whole.
func ListActiveValidationRules(ctx context.Context, db bun.IDB) ([]ValidationRule, error) {
	rules := []ValidationRule{}
	if err := db.NewSelect().Model(&rules).Where("active").OrderExpr("feature_class_code, sort_order, field_name").Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: listing active validation rules: %w", err)
	}
	return rules, nil
}

// ValidationRuleParams is the create/update input.
type ValidationRuleParams struct {
	FeatureClassCode string
	FieldName        string
	SubtypeCode      *int
	Severity         string
	Message          *string
	SortOrder        int
	Active           *bool
}

func validateRuleSeverity(v string) error {
	if v != "error" && v != "warning" {
		return invalid("severity must be 'error' or 'warning'")
	}
	return nil
}

// CreateValidationRule adds a rule. The (class, field, subtype) triple is unique.
func CreateValidationRule(ctx context.Context, db bun.IDB, p ValidationRuleParams) (*ValidationRule, error) {
	class, field := strings.TrimSpace(p.FeatureClassCode), strings.TrimSpace(p.FieldName)
	if class == "" || field == "" {
		return nil, invalid("feature_class_code and field_name are required")
	}
	sev := p.Severity
	if sev == "" {
		sev = "warning"
	}
	if err := validateRuleSeverity(sev); err != nil {
		return nil, err
	}
	rule := &ValidationRule{
		FeatureClassCode: class,
		FieldName:        field,
		SubtypeCode:      p.SubtypeCode,
		Severity:         sev,
		Message:          p.Message,
		SortOrder:        p.SortOrder,
		Active:           p.Active == nil || *p.Active,
	}
	if _, err := db.NewInsert().Model(rule).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: creating validation rule: %w", err)
	}
	return rule, nil
}

// UpdateValidationRule replaces the mutable fields of a rule (the class/field/
// subtype identity is fixed). Not-found for an unknown id.
func UpdateValidationRule(ctx context.Context, db bun.IDB, id string, p ValidationRuleParams) (*ValidationRule, error) {
	sev := p.Severity
	if sev == "" {
		sev = "warning"
	}
	if err := validateRuleSeverity(sev); err != nil {
		return nil, err
	}
	rule := &ValidationRule{
		ID:        id,
		Severity:  sev,
		Message:   p.Message,
		SortOrder: p.SortOrder,
		Active:    p.Active == nil || *p.Active,
	}
	res, err := db.NewUpdate().Model(rule).
		Column("severity", "message", "sort_order", "active").
		WherePK().Returning("*").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectivity: updating validation rule %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("validation rule %q not found", id)
	}
	return rule, nil
}

// DeleteValidationRule removes a rule by id.
func DeleteValidationRule(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*ValidationRule)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("connectivity: deleting validation rule %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("validation rule %q not found", id)
	}
	return nil
}

// --- HTTP ------------------------------------------------------------------

// requireMetadataWrite gates a rule-catalog write on metadata:write (editing the
// quality policy is an admin action, like the cost/curve/fault-mode catalogs),
// on top of whatever the /connectivity mount already enforced for the method.
func requireMetadataWrite(w http.ResponseWriter, r *http.Request) bool {
	user, ok := auth.UserFrom(r.Context())
	if !ok || !user.Has(auth.PermMetadataWrite) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "editing validation rules needs the metadata:write permission"})
		return false
	}
	return true
}

func rulesListHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := r.URL.Query().Get("class")
		includeInactive := r.URL.Query().Get("include_inactive") == "true"
		rules, err := ListValidationRules(r.Context(), db, class, includeInactive)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
	}
}

type validationRuleRequest struct {
	FeatureClassCode string  `json:"feature_class_code"`
	FieldName        string  `json:"field_name"`
	SubtypeCode      *int    `json:"subtype_code"`
	Severity         string  `json:"severity"`
	Message          *string `json:"message"`
	SortOrder        int     `json:"sort_order"`
	Active           *bool   `json:"active"`
}

func (req validationRuleRequest) params() ValidationRuleParams {
	return ValidationRuleParams{
		FeatureClassCode: req.FeatureClassCode,
		FieldName:        req.FieldName,
		SubtypeCode:      req.SubtypeCode,
		Severity:         req.Severity,
		Message:          req.Message,
		SortOrder:        req.SortOrder,
		Active:           req.Active,
	}
}

func ruleCreateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataWrite(w, r) {
			return
		}
		var req validationRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("invalid request body: %v", err))
			return
		}
		rule, err := CreateValidationRule(r.Context(), db, req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	}
}

func ruleUpdateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataWrite(w, r) {
			return
		}
		var req validationRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("invalid request body: %v", err))
			return
		}
		rule, err := UpdateValidationRule(r.Context(), db, chi.URLParam(r, "id"), req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rule)
	}
}

func ruleDeleteHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataWrite(w, r) {
			return
		}
		if err := DeleteValidationRule(r.Context(), db, chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
