// Package rules answers "what applies to this feature class" for the
// three kinds of rule metadata this project has always enforced only at
// write time, never let anyone browse: network_rules
// (api/internal/connectivity), relationship_classes
// (api/internal/relationship), and snapping_rules (never read from Go
// at all before this package -- see metadata.SnappingRule's own doc
// comment). Each of those packages loads rules scoped to one specific
// from/to pair, right before validating a specific proposed write; this
// package instead loads every rule where a given feature class appears
// on either side, purely for display -- there is no write path here.
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

type networkRuleSchema struct {
	Name         string  `json:"name"`
	Direction    string  `json:"direction"` // "outgoing" if this class is the from-side, "incoming" if the to-side
	FromClass    string  `json:"from_class"`
	FromSubtype  *string `json:"from_subtype,omitempty"`
	FromTerminal *string `json:"from_terminal,omitempty"`
	ToClass      string  `json:"to_class"`
	ToSubtype    *string `json:"to_subtype,omitempty"`
	ToTerminal   *string `json:"to_terminal,omitempty"`
	PhaseRule    *string `json:"phase_rule,omitempty"`
	IsValid      bool    `json:"is_valid"`
}

type relationshipRuleSchema struct {
	Name             string `json:"name"`
	Direction        string `json:"direction"` // "origin" or "destination"
	OriginClass      string `json:"origin_class"`
	DestinationClass string `json:"destination_class"`
	Kind             string `json:"kind"`
	Cardinality      string `json:"cardinality"`
}

type snappingRuleSchema struct {
	Direction   string  `json:"direction"` // "from" (this class is what's being digitized) or "to" (this class is an eligible snap target)
	FromClass   string  `json:"from_class"`
	FromSubtype *string `json:"from_subtype,omitempty"`
	ToClass     string  `json:"to_class"`
	ToSubtype   *string `json:"to_subtype,omitempty"`
	ToTerminal  *string `json:"to_terminal,omitempty"`
	TargetType  string  `json:"target_type"`
	ToleranceM  float64 `json:"tolerance_m"`
	Priority    int     `json:"priority"`
	IsRequired  bool    `json:"is_required"`
}

type featureClassRulesResponse struct {
	Code              string                   `json:"code"`
	NetworkRules      []networkRuleSchema      `json:"network_rules"`
	RelationshipRules []relationshipRuleSchema `json:"relationship_rules"`
	SnappingRules     []snappingRuleSchema     `json:"snapping_rules"`
}

// resolver batch-loads every feature class/subtype/terminal/domain
// value referenced across a set of rules, once, rather than a query per
// row -- the same countByFeatureClass-style trick api/internal/admin's
// own list.go already uses, just across four different lookup tables
// instead of one.
type resolver struct {
	classByID       map[string]metadata.FeatureClass
	subtypeByID     map[string]metadata.Subtype
	terminalByID    map[string]metadata.Terminal
	domainValueByID map[string]metadata.DomainValue
}

func newResolver(ctx context.Context, db *bun.DB, classIDs, subtypeIDs, terminalIDs, domainValueIDs []string) (*resolver, error) {
	r := &resolver{
		classByID:       map[string]metadata.FeatureClass{},
		subtypeByID:     map[string]metadata.Subtype{},
		terminalByID:    map[string]metadata.Terminal{},
		domainValueByID: map[string]metadata.DomainValue{},
	}

	if len(classIDs) > 0 {
		var classes []metadata.FeatureClass
		if err := db.NewSelect().Model(&classes).Where("id IN (?)", bun.In(classIDs)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("rules: loading referenced feature classes: %w", err)
		}
		for _, c := range classes {
			r.classByID[c.ID] = c
		}
	}
	if len(subtypeIDs) > 0 {
		var subtypes []metadata.Subtype
		if err := db.NewSelect().Model(&subtypes).Where("id IN (?)", bun.In(subtypeIDs)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("rules: loading referenced subtypes: %w", err)
		}
		for _, s := range subtypes {
			r.subtypeByID[s.ID] = s
		}
	}
	if len(terminalIDs) > 0 {
		var terminals []metadata.Terminal
		if err := db.NewSelect().Model(&terminals).Where("id IN (?)", bun.In(terminalIDs)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("rules: loading referenced terminals: %w", err)
		}
		for _, t := range terminals {
			r.terminalByID[t.ID] = t
		}
	}
	if len(domainValueIDs) > 0 {
		var values []metadata.DomainValue
		if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(domainValueIDs)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("rules: loading referenced domain values: %w", err)
		}
		for _, v := range values {
			r.domainValueByID[v.ID] = v
		}
	}
	return r, nil
}

func (r *resolver) className(id string) string {
	if c, ok := r.classByID[id]; ok {
		return c.Code
	}
	return id
}

func (r *resolver) subtypeName(id *string) *string {
	if id == nil {
		return nil
	}
	if s, ok := r.subtypeByID[*id]; ok {
		return &s.Name
	}
	return nil
}

func (r *resolver) terminalCode(id *string) *string {
	if id == nil {
		return nil
	}
	if t, ok := r.terminalByID[*id]; ok {
		return &t.Code
	}
	return nil
}

func (r *resolver) domainValueCode(id string) string {
	if v, ok := r.domainValueByID[id]; ok {
		return v.Code
	}
	return id
}

// Handler serves GET /feature-classes/{code}/rules -- every network,
// relationship, and snapping rule where the named class appears on
// either side, resolved to readable codes/names instead of raw ids.
// Read-only: nothing here writes, unlike every other consumer of these
// three tables (api/internal/connectivity, api/internal/relationship,
// the digitizing client's own Snap interaction).
func Handler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		code := chi.URLParam(req, "code")
		ctx := req.Context()

		fc := new(metadata.FeatureClass)
		if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code))
			return
		}

		networkRules, err := metadata.LoadNetworkRulesForClass(ctx, db, fc.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		relationshipClasses, err := metadata.LoadRelationshipClassesForClass(ctx, db, fc.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		snappingRules, err := metadata.LoadSnappingRulesForClass(ctx, db, fc.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		var classIDs, subtypeIDs, terminalIDs, domainValueIDs []string
		for _, nr := range networkRules {
			classIDs = append(classIDs, nr.FromFeatureClassID, nr.ToFeatureClassID)
			if nr.FromSubtypeID != nil {
				subtypeIDs = append(subtypeIDs, *nr.FromSubtypeID)
			}
			if nr.ToSubtypeID != nil {
				subtypeIDs = append(subtypeIDs, *nr.ToSubtypeID)
			}
			if nr.FromTerminalID != nil {
				terminalIDs = append(terminalIDs, *nr.FromTerminalID)
			}
			if nr.ToTerminalID != nil {
				terminalIDs = append(terminalIDs, *nr.ToTerminalID)
			}
		}
		for _, rc := range relationshipClasses {
			classIDs = append(classIDs, rc.OriginFeatureClassID, rc.DestinationFeatureClassID)
		}
		for _, sr := range snappingRules {
			classIDs = append(classIDs, sr.FromFeatureClassID, sr.ToFeatureClassID)
			if sr.FromSubtypeID != nil {
				subtypeIDs = append(subtypeIDs, *sr.FromSubtypeID)
			}
			if sr.ToSubtypeID != nil {
				subtypeIDs = append(subtypeIDs, *sr.ToSubtypeID)
			}
			if sr.ToTerminalID != nil {
				terminalIDs = append(terminalIDs, *sr.ToTerminalID)
			}
			domainValueIDs = append(domainValueIDs, sr.TargetTypeValueID)
		}

		res, err := newResolver(ctx, db, classIDs, subtypeIDs, terminalIDs, domainValueIDs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := featureClassRulesResponse{
			Code:              fc.Code,
			NetworkRules:      make([]networkRuleSchema, len(networkRules)),
			RelationshipRules: make([]relationshipRuleSchema, len(relationshipClasses)),
			SnappingRules:     make([]snappingRuleSchema, len(snappingRules)),
		}

		for i, nr := range networkRules {
			direction := "incoming"
			if nr.FromFeatureClassID == fc.ID {
				direction = "outgoing"
			}
			resp.NetworkRules[i] = networkRuleSchema{
				Name:         nr.Name,
				Direction:    direction,
				FromClass:    res.className(nr.FromFeatureClassID),
				FromSubtype:  res.subtypeName(nr.FromSubtypeID),
				FromTerminal: res.terminalCode(nr.FromTerminalID),
				ToClass:      res.className(nr.ToFeatureClassID),
				ToSubtype:    res.subtypeName(nr.ToSubtypeID),
				ToTerminal:   res.terminalCode(nr.ToTerminalID),
				PhaseRule:    nr.PhaseRule,
				IsValid:      nr.IsValid,
			}
		}

		for i, rc := range relationshipClasses {
			direction := "destination"
			if rc.OriginFeatureClassID == fc.ID {
				direction = "origin"
			}
			resp.RelationshipRules[i] = relationshipRuleSchema{
				Name:             rc.Name,
				Direction:        direction,
				OriginClass:      res.className(rc.OriginFeatureClassID),
				DestinationClass: res.className(rc.DestinationFeatureClassID),
				Kind:             rc.Kind,
				Cardinality:      rc.Cardinality,
			}
		}

		for i, sr := range snappingRules {
			direction := "to"
			if sr.FromFeatureClassID == fc.ID {
				direction = "from"
			}
			resp.SnappingRules[i] = snappingRuleSchema{
				Direction:   direction,
				FromClass:   res.className(sr.FromFeatureClassID),
				FromSubtype: res.subtypeName(sr.FromSubtypeID),
				ToClass:     res.className(sr.ToFeatureClassID),
				ToSubtype:   res.subtypeName(sr.ToSubtypeID),
				ToTerminal:  res.terminalCode(sr.ToTerminalID),
				TargetType:  res.domainValueCode(sr.TargetTypeValueID),
				ToleranceM:  sr.ToleranceM,
				Priority:    sr.Priority,
				IsRequired:  sr.IsRequired,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
