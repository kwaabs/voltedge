package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the authoring (write) side of the three rule tables the
// admin UI could previously only display through api/internal/rules:
// metadata.network_rules (legal electrical connectivity patterns, one
// row = one allowed from/to terminal pairing -- migrations/0011),
// metadata.relationship_classes (legal structural/spatial pairings, e.g.
// STRUCTURE contains METER_CHAMBER -- migrations/0010), and
// metadata.snapping_rules (digitizing snap targets, one from/to pairing
// with a tolerance and priority -- migrations/0013). All three are pure
// metadata: a new row is real and safe the moment it's written (the
// connectivity/relationship validators read these live, no compile
// step), which is why -- unlike a field -- there's no recompile to do
// after adding one. Note snapping_rules are display-only for now: the
// map's Snap interaction doesn't yet consume tolerance_m/target_type
// (docs/open-items.md), so authoring one populates the Rules display but
// doesn't change snap behavior until that wiring slice lands.

// validRelationshipKinds / validCardinalities mirror the CHECK
// constraints on metadata.relationship_classes (migrations/0010, widened by
// 0075 for the protection kinds, 0080 for metering_sensing, 0082 for
// cable_routing and 0083 for panel_bay). We validate here too so the API returns
// a clean 400 with the allowed set rather than letting Postgres reject it with a
// raw constraint error -- keep this in sync with the DB CHECK whenever a new kind
// is added, or admin authoring silently lags what the schema (and seeds) already
// allow.
var validRelationshipKinds = map[string]bool{
	"spatial_support":    true,
	"containment":        true,
	"composite":          true,
	"protection_trip":    true,
	"protection_sensing": true,
	"metering_sensing":   true,
	"cable_routing":      true,
	"panel_bay":          true,
}

var validCardinalities = map[string]bool{
	"one_to_one":   true,
	"one_to_many":  true,
	"many_to_many": true,
}

// ruleIDResponse is the minimal create result -- the UI refetches the
// class's rules list (GET /feature-classes/{code}/rules) to render the
// new row, so the id it needs for a later delete is all we return.
type ruleIDResponse struct {
	ID string `json:"id"`
}

// networkRuleRequest is the create body -- everything by human code
// (class codes, terminal codes), never raw ids, the same convention
// every other admin write uses. Terminals are optional: an edge feature
// class has no terminals, and a rule can legitimately name just the two
// classes. IsValid defaults to true when omitted (a rule is authored to
// permit a connection, not forbid one).
type networkRuleRequest struct {
	Name         string  `json:"name"`
	FromClass    string  `json:"from_class"`
	FromTerminal *string `json:"from_terminal,omitempty"`
	ToClass      string  `json:"to_class"`
	ToTerminal   *string `json:"to_terminal,omitempty"`
	PhaseRule    *string `json:"phase_rule,omitempty"`
	IsValid      *bool   `json:"is_valid,omitempty"`
}

func createNetworkRule(ctx context.Context, db *bun.DB, req networkRuleRequest) (*ruleIDResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalid("name is required")
	}
	if strings.TrimSpace(req.FromClass) == "" || strings.TrimSpace(req.ToClass) == "" {
		return nil, invalid("from_class and to_class are required")
	}

	from, err := featureClassByCode(ctx, db, strings.TrimSpace(req.FromClass))
	if err != nil {
		return nil, err
	}
	to, err := featureClassByCode(ctx, db, strings.TrimSpace(req.ToClass))
	if err != nil {
		return nil, err
	}

	row := &metadata.NetworkRule{
		Name:               name,
		FromFeatureClassID: from.ID,
		ToFeatureClassID:   to.ID,
		IsValid:            true,
	}
	if req.IsValid != nil {
		row.IsValid = *req.IsValid
	}
	if req.PhaseRule != nil {
		if pr := strings.TrimSpace(*req.PhaseRule); pr != "" {
			row.PhaseRule = &pr
		}
	}
	if req.FromTerminal != nil && strings.TrimSpace(*req.FromTerminal) != "" {
		t, err := terminalByClassAndCode(ctx, db, from, strings.TrimSpace(*req.FromTerminal))
		if err != nil {
			return nil, err
		}
		row.FromTerminalID = &t.ID
	}
	if req.ToTerminal != nil && strings.TrimSpace(*req.ToTerminal) != "" {
		t, err := terminalByClassAndCode(ctx, db, to, strings.TrimSpace(*req.ToTerminal))
		if err != nil {
			return nil, err
		}
		row.ToTerminalID = &t.ID
	}

	// ExcludeColumn("id") lets the DB's gen_random_uuid() default fill the
	// pk; Returning("id") scans it back into row.ID -- the same insert
	// shape every other admin create (create.go) uses.
	if _, err := db.NewInsert().Model(row).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
		return nil, fmt.Errorf("admin: create network rule: %w", err)
	}
	return &ruleIDResponse{ID: row.ID}, nil
}

func deleteNetworkRule(ctx context.Context, db *bun.DB, id string) error {
	res, err := db.NewDelete().Model((*metadata.NetworkRule)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("admin: delete network rule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin: delete network rule: %w", err)
	}
	if n == 0 {
		return notFound("network rule %s not found", id)
	}
	return nil
}

// relationshipClassRequest is the create body -- classes by code, kind
// and cardinality by their metadata code. name is UNIQUE in the table;
// we pre-check it for a friendly message instead of a raw 500.
type relationshipClassRequest struct {
	Name             string `json:"name"`
	OriginClass      string `json:"origin_class"`
	DestinationClass string `json:"destination_class"`
	Kind             string `json:"kind"`
	Cardinality      string `json:"cardinality"`
}

func createRelationshipClass(ctx context.Context, db *bun.DB, req relationshipClassRequest) (*ruleIDResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalid("name is required")
	}
	if !validRelationshipKinds[req.Kind] {
		return nil, invalid("kind must be one of spatial_support, containment, composite, protection_trip, protection_sensing, metering_sensing, cable_routing, panel_bay")
	}
	if !validCardinalities[req.Cardinality] {
		return nil, invalid("cardinality must be one of one_to_one, one_to_many, many_to_many")
	}
	if strings.TrimSpace(req.OriginClass) == "" || strings.TrimSpace(req.DestinationClass) == "" {
		return nil, invalid("origin_class and destination_class are required")
	}

	origin, err := featureClassByCode(ctx, db, strings.TrimSpace(req.OriginClass))
	if err != nil {
		return nil, err
	}
	dest, err := featureClassByCode(ctx, db, strings.TrimSpace(req.DestinationClass))
	if err != nil {
		return nil, err
	}

	exists, err := db.NewSelect().Model((*metadata.RelationshipClass)(nil)).Where("name = ?", name).Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin: create relationship class: %w", err)
	}
	if exists {
		return nil, invalid("a relationship class named %q already exists", name)
	}

	row := &metadata.RelationshipClass{
		Name:                      name,
		OriginFeatureClassID:      origin.ID,
		DestinationFeatureClassID: dest.ID,
		Kind:                      req.Kind,
		Cardinality:               req.Cardinality,
	}
	if _, err := db.NewInsert().Model(row).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
		return nil, fmt.Errorf("admin: create relationship class: %w", err)
	}
	return &ruleIDResponse{ID: row.ID}, nil
}

func deleteRelationshipClass(ctx context.Context, db *bun.DB, id string) error {
	res, err := db.NewDelete().Model((*metadata.RelationshipClass)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("admin: delete relationship class: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin: delete relationship class: %w", err)
	}
	if n == 0 {
		return notFound("relationship class %s not found", id)
	}
	return nil
}

// snapTargetTypeDomain is the domain metadata.snapping_rules'
// target_type_value_id points at (seeded by
// seed/snapping_rules_example.sql; values point_feature / endpoint /
// edge / vertex). We resolve the caller's target_type code against it,
// same as diagram_rules.go resolves its diagram-type domain.
const snapTargetTypeDomain = "SNAP_TARGET_TYPE"

// snappingRuleRequest is the create body. metadata.snapping_rules has no
// name column (a snap rule is identified by its from/to pairing, not a
// label -- see the read side in api/internal/rules), so unlike the other
// two rule kinds there's no Name here. Terminals: only the to-side can
// carry one (you snap a whole from-feature to a specific terminal of the
// target); it's optional. IsRequired defaults false (a snap you may make,
// not one the digitizer is forced to complete).
type snappingRuleRequest struct {
	FromClass  string   `json:"from_class"`
	ToClass    string   `json:"to_class"`
	ToTerminal *string  `json:"to_terminal,omitempty"`
	TargetType string   `json:"target_type"`
	ToleranceM *float64 `json:"tolerance_m,omitempty"`
	Priority   *int     `json:"priority,omitempty"`
	IsRequired *bool    `json:"is_required,omitempty"`
}

func createSnappingRule(ctx context.Context, db *bun.DB, req snappingRuleRequest) (*ruleIDResponse, error) {
	if strings.TrimSpace(req.FromClass) == "" || strings.TrimSpace(req.ToClass) == "" {
		return nil, invalid("from_class and to_class are required")
	}
	if strings.TrimSpace(req.TargetType) == "" {
		return nil, invalid("target_type is required (one of point_feature, endpoint, edge, vertex)")
	}
	if req.ToleranceM == nil || *req.ToleranceM <= 0 {
		return nil, invalid("tolerance_m is required and must be greater than 0")
	}

	from, err := featureClassByCode(ctx, db, strings.TrimSpace(req.FromClass))
	if err != nil {
		return nil, err
	}
	to, err := featureClassByCode(ctx, db, strings.TrimSpace(req.ToClass))
	if err != nil {
		return nil, err
	}

	tt, err := metadata.ResolveDomainValueByCode(ctx, db, snapTargetTypeDomain, strings.TrimSpace(req.TargetType))
	if err != nil {
		return nil, invalid("target_type %q is not a value of the %s domain (expected one of point_feature, endpoint, edge, vertex)", req.TargetType, snapTargetTypeDomain)
	}

	// priority: lower wins when several targets are in tolerance at once
	// (migrations/0013). Default 1 -- a lone rule is the top choice by
	// definition; the caller only needs to set it when ranking against
	// other rules on the same from-class.
	priority := 1
	if req.Priority != nil {
		priority = *req.Priority
	}

	row := &metadata.SnappingRule{
		FromFeatureClassID: from.ID,
		ToFeatureClassID:   to.ID,
		TargetTypeValueID:  tt.ID,
		ToleranceM:         *req.ToleranceM,
		Priority:           priority,
		IsRequired:         false,
	}
	if req.IsRequired != nil {
		row.IsRequired = *req.IsRequired
	}
	if req.ToTerminal != nil && strings.TrimSpace(*req.ToTerminal) != "" {
		t, err := terminalByClassAndCode(ctx, db, to, strings.TrimSpace(*req.ToTerminal))
		if err != nil {
			return nil, err
		}
		row.ToTerminalID = &t.ID
	}

	if _, err := db.NewInsert().Model(row).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
		return nil, fmt.Errorf("admin: create snapping rule: %w", err)
	}
	return &ruleIDResponse{ID: row.ID}, nil
}

func deleteSnappingRule(ctx context.Context, db *bun.DB, id string) error {
	res, err := db.NewDelete().Model((*metadata.SnappingRule)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("admin: delete snapping rule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin: delete snapping rule: %w", err)
	}
	if n == 0 {
		return notFound("snapping rule %s not found", id)
	}
	return nil
}

// terminalByClassAndCode resolves a terminal code (e.g. "T1") to its id
// on a given feature class. A class can carry several terminals sharing a
// code across subtypes (subtype_id scopes them); a network rule authored
// without a subtype targets the generic terminal, so we prefer the
// subtype-unscoped row and fall back to the first match otherwise.
func terminalByClassAndCode(ctx context.Context, db bun.IDB, fc *metadata.FeatureClass, code string) (*metadata.Terminal, error) {
	var terms []metadata.Terminal
	if err := db.NewSelect().Model(&terms).
		Where("feature_class_id = ? AND code = ?", fc.ID, code).Scan(ctx); err != nil {
		return nil, fmt.Errorf("admin: resolving terminal %q on %s: %w", code, fc.Code, err)
	}
	if len(terms) == 0 {
		return nil, invalid("feature class %q has no terminal %q", fc.Code, code)
	}
	for i := range terms {
		if terms[i].SubtypeID == nil {
			return &terms[i], nil
		}
	}
	return &terms[0], nil
}
