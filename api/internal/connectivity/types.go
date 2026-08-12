// Package connectivity is the zero-trust electrical connectivity engine
// for Phase 2: the only path that turns a candidate connection into real
// topology. An establish request is only ever committed when a matching,
// active metadata.network_rules row exists for the from/to feature
// classes, subtypes, and terminals involved -- absence of a rule, or a
// matching rule with is_valid = false, rejects the write outright. See
// docs/metadata-core.md's "Suggested vs. established" section: a
// matching rule produces a candidate, not a fact.
package connectivity

import "time"

// endpointRef is one side of a connectivity request or response -- a
// specific feature, optionally at a specific named terminal.
type endpointRef struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	Terminal     *string `json:"terminal,omitempty"`
}

// establishRequest is the POST /connectivity body.
type establishRequest struct {
	From endpointRef `json:"from"`
	To   endpointRef `json:"to"`
}

// connectionResponse is the shape returned for one established
// connection, whether from establish, list, or (implicitly) before
// revoke.
type connectionResponse struct {
	ID              string      `json:"id"`
	NetworkRuleID   string      `json:"network_rule_id"`
	NetworkRuleName string      `json:"network_rule_name"`
	From            endpointRef `json:"from"`
	To              endpointRef `json:"to"`
	EstablishedAt   time.Time   `json:"established_at"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
