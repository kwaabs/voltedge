package connectivity

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// matchRuleEitherDirection tries the request's own from/to pair, then
// the swapped pair -- see establishConnection's own comment for why a
// rule authored in one direction needs to authorize a request made in
// either direction. When only the swapped pair matches, the returned
// endpoints are swapped too, so the connection is always written (and
// reported back) in the rule's own canonical direction, never whatever
// direction the caller happened to name first.
func matchRuleEitherDirection(ctx context.Context, db bun.IDB, from, to *resolvedEndpoint) (*metadata.NetworkRule, *resolvedEndpoint, *resolvedEndpoint, error) {
	rules, err := metadata.LoadNetworkRules(ctx, db, from.loaded.FeatureClass.ID, to.loaded.FeatureClass.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	rule, forwardErr := matchRule(rules, from.subtypeID, to.subtypeID, from.terminalCode, to.terminalCode, from.loaded, to.loaded)
	if forwardErr == nil {
		if err := checkPhaseCompatibility(rule, from, to); err != nil {
			return nil, nil, nil, err
		}
		return rule, from, to, nil
	}

	swappedRules, err := metadata.LoadNetworkRules(ctx, db, to.loaded.FeatureClass.ID, from.loaded.FeatureClass.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if rule, err := matchRule(swappedRules, to.subtypeID, from.subtypeID, to.terminalCode, from.terminalCode, to.loaded, from.loaded); err == nil {
		if err := checkPhaseCompatibility(rule, to, from); err != nil {
			return nil, nil, nil, err
		}
		return rule, to, from, nil
	}

	// Neither direction matched -- surface the forward attempt's error,
	// since it directly reflects what the caller actually requested.
	return nil, nil, nil, forwardErr
}

// matchRule picks the network_rules row that authorizes a specific
// from/to connection, or explains exactly why none does. Zero trust:
// the caller passes every network_rules row for this from/to
// feature-class pair (valid and invalid alike, via metadata.LoadNetworkRules)
// so this function can tell apart "no rule was ever defined for this"
// from "a rule was defined and then disabled" -- two different rejection
// reasons a user fixing their data needs to distinguish, even though
// both mean the same thing operationally: no connection.
//
// Terminal/subtype fields on a rule are wildcards when null (apply to
// any subtype/terminal) and exact matches when set. Among every
// applicable, active rule, the most specific one wins -- same
// most-specific-wins principle symbology resolution already uses.
//
// fromTerminalCode/toTerminalCode are the CODES of the terminals the
// connection actually resolved to (not their row ids); the rule's own
// terminal pins are resolved to codes via fromLoaded/toLoaded so the two
// are compared by code -- see terminalMatches for why id comparison was
// wrong.
func matchRule(rules []metadata.NetworkRule, fromSubtypeID, toSubtypeID, fromTerminalCode, toTerminalCode *string, fromLoaded, toLoaded *metadata.Loaded) (*metadata.NetworkRule, error) {
	var best *metadata.NetworkRule
	bestScore := -1
	disabledMatch := false

	for i := range rules {
		r := &rules[i]
		if !ruleApplies(r, fromSubtypeID, toSubtypeID, fromTerminalCode, toTerminalCode, fromLoaded, toLoaded) {
			continue
		}
		if !r.IsValid {
			disabledMatch = true
			continue
		}
		if score := ruleSpecificity(r); score > bestScore {
			best, bestScore = r, score
		}
	}

	if best != nil {
		return best, nil
	}
	if disabledMatch {
		return nil, invalid("a network rule exists for this connection but is disabled (is_valid = false) -- no electrical connectivity")
	}
	// Distinguish "no rule was ever authored for this class pair" from "a
	// rule exists but its subtype/terminal scope excludes this specific
	// connection" -- the second is the confusing case (the operator sees a
	// matching rule in the admin list yet the establish is rejected), so
	// name what was requested to make the mismatch diagnosable.
	if len(rules) > 0 {
		return nil, invalid(
			"no network rule permits this connection: %d rule(s) exist for this class pair but none match the requested terminals (from terminal %s, to terminal %s)",
			len(rules), codeOrNone(fromTerminalCode), codeOrNone(toTerminalCode),
		)
	}
	return nil, invalid("no network rule permits this connection -- zero trust default: absence of a rule means the connection is invalid")
}

func ruleApplies(r *metadata.NetworkRule, fromSubtypeID, toSubtypeID, fromTerminalCode, toTerminalCode *string, fromLoaded, toLoaded *metadata.Loaded) bool {
	if !idMatches(r.FromSubtypeID, fromSubtypeID) {
		return false
	}
	if !idMatches(r.ToSubtypeID, toSubtypeID) {
		return false
	}
	if !terminalMatches(r.FromTerminalID, fromTerminalCode, fromLoaded) {
		return false
	}
	if !terminalMatches(r.ToTerminalID, toTerminalCode, toLoaded) {
		return false
	}
	return true
}

// terminalMatches compares a rule's optional terminal pin against the
// connection's actual terminal BY CODE, not by row id. A rule authored
// for "terminal T1" means the logical terminal T1, but a feature class
// can carry more than one terminal row sharing that code -- a generic
// (subtype-null) row plus subtype-specific overrides -- so the specific
// row the rule pinned (admin.terminalByClassAndCode prefers the generic)
// is often a DIFFERENT uuid than the one Loaded.ResolveTerminal returns
// for the actual feature's subtype, even though both are "T1". Comparing
// ids silently rejected such connections ("no network rule permits this")
// despite a visibly-correct rule; resolving the rule's pinned id back to
// its code and comparing codes is the connection the author meant.
//
// A nil pin is a wildcard (any terminal, incl. none). A non-nil pin that
// resolves to no terminal in the loaded class (should be impossible under
// the RESTRICT FK on network_rules.*_terminal_id) stays strict: no match.
func terminalMatches(ruleTerminalID, actualCode *string, loaded *metadata.Loaded) bool {
	if ruleTerminalID == nil {
		return true
	}
	t := loaded.TerminalByID(*ruleTerminalID)
	if t == nil {
		return false
	}
	return actualCode != nil && *actualCode == t.Code
}

func codeOrNone(code *string) string {
	if code == nil {
		return "(none)"
	}
	return *code
}

// idMatches implements the wildcard rule: a nil rule value matches
// anything. A non-nil rule value requires an exact match against what
// the actual connection has -- including requiring the connection to
// have a value at all (a rule pinned to a specific terminal can't be
// satisfied by a connection that specifies no terminal).
func idMatches(ruleValue, actual *string) bool {
	if ruleValue == nil {
		return true
	}
	return actual != nil && *actual == *ruleValue
}

// ruleSpecificity counts how many of a rule's four optional fields are
// pinned rather than wildcarded -- more pins means a more specific,
// higher-priority match.
func ruleSpecificity(r *metadata.NetworkRule) int {
	score := 0
	if r.FromSubtypeID != nil {
		score++
	}
	if r.ToSubtypeID != nil {
		score++
	}
	if r.FromTerminalID != nil {
		score++
	}
	if r.ToTerminalID != nil {
		score++
	}
	return score
}
