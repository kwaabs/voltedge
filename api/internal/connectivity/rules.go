package connectivity

import "github.com/kwaabs/voltedge/api/internal/metadata"

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
func matchRule(rules []metadata.NetworkRule, fromSubtypeID, toSubtypeID, fromTerminalID, toTerminalID *string) (*metadata.NetworkRule, error) {
	var best *metadata.NetworkRule
	bestScore := -1
	disabledMatch := false

	for i := range rules {
		r := &rules[i]
		if !ruleApplies(r, fromSubtypeID, toSubtypeID, fromTerminalID, toTerminalID) {
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
	return nil, invalid("no network rule permits this connection -- zero trust default: absence of a rule means the connection is invalid")
}

func ruleApplies(r *metadata.NetworkRule, fromSubtypeID, toSubtypeID, fromTerminalID, toTerminalID *string) bool {
	if !idMatches(r.FromSubtypeID, fromSubtypeID) {
		return false
	}
	if !idMatches(r.ToSubtypeID, toSubtypeID) {
		return false
	}
	if !idMatches(r.FromTerminalID, fromTerminalID) {
		return false
	}
	if !idMatches(r.ToTerminalID, toTerminalID) {
		return false
	}
	return true
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
