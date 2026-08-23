// Package assistant is the in-app AI assistant's backend (Phase 19,
// docs/phase-19-assistant.md) -- the second front door onto the same tools the
// Phase 18 MCP server exposes (docs/phase-18-mcp.md), but for the VoltEdge web
// frontend rather than an external agent. Where MCP authenticates a separate
// process with a bearer token and speaks JSON-RPC, this runs in-process behind
// the ordinary cookie session and calls the same exported engine functions
// directly -- so a suggestion or (later) a tool call is attributed to the
// logged-in user and bounded by exactly their RBAC, with no second auth path.
//
// Slice 1 is the deterministic half of the assistant, with no LLM in the loop
// at all: given one feature the user just edited (typically snapped to another
// feature on the map), it asks the connectivity and relationship suggest
// engines for the ranked, rule-legal candidates that feature could connect or
// relate to, and returns each as a ready-to-execute *proposed action* -- the
// establish tool to call plus the exact request body the existing
// POST /connectivity / POST /relationships routes already accept. The frontend
// surfaces these as confirmation chips; clicking one just POSTs the prebuilt
// args to the establish route it already uses (web/src/components/map/
// connect-confirm-panel.tsx's own establish path), so nothing auto-writes --
// the human's click is the commit, mirroring the MCP write-confirm contract.
//
// The suggest engines only read, but the actions they produce are writes the
// user would then commit, so this endpoint is deliberately gated on the
// *commit* permissions (connectivity:commit / relationships:commit), not the
// read ones: there is no point proposing an action the user could never carry
// out. A user holding only :read sees no chips (they can browse candidates
// through the MCP suggest tools instead). The two capabilities are checked
// independently against the session user's own permission set, so a user with
// one but not the other gets exactly the half they can act on.
//
// Slice 2 makes the "second front door onto the same tools" literal: the tool
// contract (name, schema, required permission, handler, and the Execute choke
// point) is extracted into internal/agenttools, which both this package and the
// MCP server consume, so the two surfaces can't drift. This package's GET /tools
// serves that shared registry, scoped to the session user's permissions (see
// tools.go). The suggestions endpoint below still calls the connectivity and
// relationship suggest engines directly rather than through agenttools.Execute,
// because it needs the engines' typed candidate structs to prebuild each
// establish body; the LLM chat surface (Slice 3) is what will route freeform
// requests through Execute.
//
// Slice 3 adds that chat surface: a small local language model (behind the
// llm.Provider seam) as a single tool-router. POST /chat (chat.go) hands the
// model the session-scoped tool catalog, executes any read tool it calls inline
// through agenttools.Execute, and returns any write tool call as a proposed
// action the user confirms -- committed through POST /execute (execute.go), the
// same Execute choke point, so a chat-driven write is bounded by exactly the
// same RBAC as everything else. GET /status reports whether a model is
// configured at all. Chat is optional: with no model wired up the provider is
// nil, /chat answers 503, and only the deterministic half above runs.
package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/assistant/llm"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// suggestLimit caps how many candidate *features* (grouped by target -- see
// groupAndCap) one call returns after merging the connectivity and relationship
// candidates -- a small list is an assist; a wall of chips is noise. The
// per-engine limits (connectivity / relationship suggestMaxLimit) still bound
// each side before the merge.
const suggestLimit = 8

// suggestMaxDistanceMeters drops geometric candidates farther than this from the
// edited feature before they ever reach a chip. The suggest surface is a
// snap-and-connect assist: a candidate a couple of hundred metres away is not
// something the user "just snapped to", it's the nearest-N fallback the engines
// return when nothing is genuinely close, and surfacing it reads as noise. Non-
// geometric candidates (nil distance -- a class with no geometry) are never
// distance-filtered, since there's no distance to judge them by. Tunable: raise
// it if a deployment's features are legitimately sparser than this.
const suggestMaxDistanceMeters = 200.0

// Routes returns the assistant router, meant to be mounted at /assistant behind
// the session-auth trust boundary (see api/cmd/server/main.go):
//
//	POST /suggestions   ranked, executable action proposals for one feature
//	GET  /tools         the session-scoped shared tool catalog (agenttools)
//	GET  /status        whether freeform chat is available (needs a model)
//	POST /chat          the Slice 3 chat orchestrator (tool-routing over an LLM)
//	POST /execute       commit one confirmed tool call (the chat write path)
//
// It takes the *bun.DB the engines read/write through and the llm.Provider the
// chat orchestrator routes over; provider may be nil, in which case /chat reports
// itself unconfigured (503) and /status reports chat disabled, while the
// deterministic suggestions and the commit path keep working. The state-changing
// POST /chat and POST /execute carry RequireCSRF on top of the mount's
// RequireAuth; GET and the read-only /suggestions match the Slice 1 posture.
func Routes(db *bun.DB, provider llm.Provider) chi.Router {
	r := chi.NewRouter()
	r.Post("/suggestions", suggestionsHandler(db))
	r.Get("/tools", toolsHandler())
	r.Get("/status", statusHandler(provider))
	r.With(auth.RequireCSRF).Post("/chat", chatHandler(db, provider))
	r.With(auth.RequireCSRF).Post("/execute", executeHandler(db))
	return r
}

// featureRef is one feature over the wire: the class code plus the row id,
// the same shape the establish routes and the map frontend already speak.
type featureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// suggestRequest is the body of POST /suggestions: the feature the user just
// touched, plus optional narrowing. Kinds filters which engines run ("connection"
// and/or "relationship"); empty means both. Target, when set, restricts the
// results to candidates that are that one feature -- the precise "I just snapped
// to *this* pole, what can I do with it" case -- rather than every nearby
// candidate. Limit/RadiusMeters pass through to the engines' own bounds.
type suggestRequest struct {
	FeatureClass string      `json:"feature_class"`
	FeatureID    string      `json:"feature_id"`
	Kinds        []string    `json:"kinds,omitempty"`
	Target       *featureRef `json:"target,omitempty"`
	Limit        int         `json:"limit,omitempty"`
	RadiusMeters float64     `json:"radius_meters,omitempty"`
}

// suggestedFeature is the candidate a proposed action points at, carried
// through from the engines' own SuggestedFeature so the frontend can label the
// chip without a second lookup.
type suggestedFeature struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	Name         string `json:"name,omitempty"`
	Subtype      *int   `json:"subtype,omitempty"`
}

// action is one proposed, executable action: a human-readable title/detail for
// the chip, the target it acts on, the ranking distance (nil when a class is
// non-geometric), and -- the point of the whole endpoint -- Tool + Args, the
// exact establish route and prebuilt request body the frontend re-POSTs verbatim
// on confirm. Args is raw JSON so the frontend never has to reshape it.
type action struct {
	Kind           string           `json:"kind"`
	Tool           string           `json:"tool"`
	Title          string           `json:"title"`
	Detail         string           `json:"detail,omitempty"`
	Target         suggestedFeature `json:"target"`
	DistanceMeters *float64         `json:"distance_meters,omitempty"`
	Args           json.RawMessage  `json:"args"`
}

// suggestionGroup collapses every proposed action that targets the *same*
// candidate feature into one entry. The engines legitimately return several
// distinct-but-adjacent actions against one target -- an electrical connection
// plus one or more relationship kinds (e.g. a support and a composite) -- which,
// listed flat, read as duplicate rows cluttering the surface. Grouping gives the
// frontend one row per nearby feature (Label + the nearest DistanceMeters across
// its actions) that expands to the handful of things you can actually do with
// it, so no legal action is hidden, just tidied. Actions within a group are
// ordered connection-first then nearest-first.
type suggestionGroup struct {
	Target         suggestedFeature `json:"target"`
	Label          string           `json:"label"`
	DistanceMeters *float64         `json:"distance_meters,omitempty"`
	Actions        []action         `json:"actions"`
}

type suggestResponse struct {
	Suggestions []suggestionGroup `json:"suggestions"`
}

func suggestionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			// The /assistant mount sits behind RequireAuth, so this is
			// belt-and-suspenders -- never reached in normal wiring.
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req suggestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, http.StatusBadRequest, "feature_class and feature_id are required")
			return
		}

		wantConnections := kindWanted(req.Kinds, "connection") && user.Has(auth.PermConnectivityCommit)
		wantRelationships := kindWanted(req.Kinds, "relationship") && user.Has(auth.PermRelationshipsCommit)

		actions := make([]action, 0, suggestLimit)

		if wantConnections {
			acts, err := connectionActions(r.Context(), db, req)
			if err != nil {
				// A suggestion failure is almost always a bad feature ref
				// (unknown class/id) -- a client condition, not a server
				// fault -- so it maps to 400 with the engine's own message
				// rather than a 500 that would read as "the assistant broke".
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			actions = append(actions, acts...)
		}
		if wantRelationships {
			acts, err := relationshipActions(r.Context(), db, req)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			actions = append(actions, acts...)
		}

		groups := groupAndCap(actions, suggestMaxDistanceMeters, suggestLimit)
		writeJSON(w, http.StatusOK, suggestResponse{Suggestions: groups})
	}
}

// connectionActions runs the connectivity suggest engine for the request's
// feature and maps every candidate into a proposed establish_connection action,
// prebuilding the POST /connectivity body (from = the source feature at the
// rule's implied source terminal, to = the candidate at its implied terminal).
func connectionActions(ctx context.Context, db bun.IDB, req suggestRequest) ([]action, error) {
	suggestions, err := connectivity.SuggestConnections(ctx, db, connectivity.SuggestInput{
		FeatureClass: req.FeatureClass,
		FeatureID:    req.FeatureID,
		Limit:        req.Limit,
		RadiusMeters: req.RadiusMeters,
	})
	if err != nil {
		return nil, err
	}

	out := make([]action, 0, len(suggestions))
	for _, s := range suggestions {
		if !matchesTarget(req.Target, s.Feature.FeatureClass, s.Feature.FeatureID) {
			continue
		}
		args, err := json.Marshal(map[string]any{
			"from": endpointBody(req.FeatureClass, req.FeatureID, s.SourceTerminal),
			"to":   endpointBody(s.Feature.FeatureClass, s.Feature.FeatureID, s.Terminal),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, action{
			Kind:           "connection",
			Tool:           "establish_connection",
			Title:          "Connect to " + candidateLabel(s.Feature.Name, s.Feature.FeatureClass),
			Detail:         "Rule: " + s.NetworkRuleName,
			Target:         suggestedFeature{FeatureClass: s.Feature.FeatureClass, FeatureID: s.Feature.FeatureID, Name: s.Feature.Name, Subtype: s.Feature.Subtype},
			DistanceMeters: s.DistanceMeters,
			Args:           args,
		})
	}
	return out, nil
}

// relationshipActions runs the relationship suggest engine and maps every
// candidate into a proposed establish_relationship action. The engine reports
// the *candidate's* role, so origin/destination are assigned to put the source
// and candidate on the sides the relationship class expects; kind is passed
// through so an otherwise-ambiguous pairing establishes without a second prompt.
func relationshipActions(ctx context.Context, db bun.IDB, req suggestRequest) ([]action, error) {
	suggestions, err := relationship.SuggestRelationships(ctx, db, relationship.SuggestInput{
		FeatureClass: req.FeatureClass,
		FeatureID:    req.FeatureID,
		Limit:        req.Limit,
		RadiusMeters: req.RadiusMeters,
	})
	if err != nil {
		return nil, err
	}

	source := featureRef{FeatureClass: req.FeatureClass, FeatureID: req.FeatureID}
	out := make([]action, 0, len(suggestions))
	for _, s := range suggestions {
		if !matchesTarget(req.Target, s.Feature.FeatureClass, s.Feature.FeatureID) {
			continue
		}
		candidate := featureRef{FeatureClass: s.Feature.FeatureClass, FeatureID: s.Feature.FeatureID}
		origin, destination := source, candidate
		if s.Role == "origin" {
			// The candidate is the origin side, so the source is the
			// destination -- flip them for the establish body.
			origin, destination = candidate, source
		}
		body := map[string]any{
			"origin":      origin,
			"destination": destination,
		}
		if s.Kind != "" {
			body["kind"] = s.Kind
		}
		args, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		out = append(out, action{
			Kind:           "relationship",
			Tool:           "establish_relationship",
			Title:          relationshipTitle(s) + candidateLabel(s.Feature.Name, s.Feature.FeatureClass),
			Detail:         s.RelationshipClassName,
			Target:         suggestedFeature{FeatureClass: s.Feature.FeatureClass, FeatureID: s.Feature.FeatureID, Name: s.Feature.Name, Subtype: s.Feature.Subtype},
			DistanceMeters: s.DistanceMeters,
			Args:           args,
		})
	}
	return out, nil
}

// endpointBody builds one side of a POST /connectivity body, matching the
// establish route's from/to shape and omitting a nil terminal entirely (the
// engine wildcarded it) rather than sending a null the route would reject.
func endpointBody(featureClass, featureID string, terminal *string) map[string]any {
	body := map[string]any{"feature_class": featureClass, "feature_id": featureID}
	if terminal != nil {
		body["terminal"] = *terminal
	}
	return body
}

// relationshipTitle renders the leading verb of a relationship chip from the
// class's kind ("attached_to Pole 12"), falling back to a generic verb when the
// class carries no kind, so the chip always reads as an action.
func relationshipTitle(s relationship.RelationshipSuggestion) string {
	if s.Kind != "" {
		return s.Kind + " "
	}
	return "Relate to "
}

// candidateLabel picks the most human-identifiable label for a candidate --
// its per-feature name when set, else the class code -- taking the two fields
// directly so it serves both engines' SuggestedFeature types, which differ only
// by package.
func candidateLabel(name, featureClass string) string {
	if name != "" {
		return name
	}
	return featureClass
}

// kindWanted reports whether an engine should run for this request: an empty
// Kinds list means "all engines", otherwise the kind must be named explicitly.
func kindWanted(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// matchesTarget reports whether a candidate passes the optional Target filter:
// no target set means every candidate passes; a target set keeps only the one
// feature it names (the "act on this specific snapped feature" case).
func matchesTarget(target *featureRef, featureClass, featureID string) bool {
	if target == nil {
		return true
	}
	return target.FeatureClass == featureClass && target.FeatureID == featureID
}

// groupAndCap turns the flat merged actions into the surface the frontend shows:
// it drops geometric candidates past maxDistance, collapses every remaining
// action onto its target feature (one suggestionGroup per candidate), orders the
// actions within each group connection-first then nearest-first, ranks the
// groups nearest-first, and trims to limit. A geometric group (has a distance)
// always outranks a non-geometric one (nil distance); the closer of two
// geometric groups wins. Grouping preserves first-seen order for equal-distance
// ties (stable), so the per-engine ranking the suggest engines already produced
// carries through.
func groupAndCap(actions []action, maxDistance float64, limit int) []suggestionGroup {
	order := make([]string, 0, len(actions))
	byTarget := make(map[string]*suggestionGroup, len(actions))
	for _, a := range actions {
		// A candidate too far away to be a plausible snap target is noise,
		// not an assist -- drop it before it can seed a group. Nil-distance
		// (non-geometric) candidates have no distance to judge and pass.
		if a.DistanceMeters != nil && *a.DistanceMeters > maxDistance {
			continue
		}
		key := a.Target.FeatureClass + "\x00" + a.Target.FeatureID
		g, ok := byTarget[key]
		if !ok {
			g = &suggestionGroup{
				Target: a.Target,
				Label:  candidateLabel(a.Target.Name, a.Target.FeatureClass),
			}
			byTarget[key] = g
			order = append(order, key)
		}
		g.Actions = append(g.Actions, a)
		if a.DistanceMeters != nil && (g.DistanceMeters == nil || *a.DistanceMeters < *g.DistanceMeters) {
			d := *a.DistanceMeters
			g.DistanceMeters = &d
		}
	}

	groups := make([]suggestionGroup, 0, len(order))
	for _, key := range order {
		g := byTarget[key]
		sort.SliceStable(g.Actions, func(i, j int) bool {
			ai, aj := g.Actions[i], g.Actions[j]
			if ai.Kind != aj.Kind {
				// Electrical connections lead a feature's action list; the
				// structural relationships follow.
				return ai.Kind == "connection"
			}
			return lessDistance(ai.DistanceMeters, aj.DistanceMeters)
		})
		groups = append(groups, *g)
	}

	sort.SliceStable(groups, func(i, j int) bool {
		return lessDistance(groups[i].DistanceMeters, groups[j].DistanceMeters)
	})
	if len(groups) > limit {
		groups = groups[:limit]
	}
	return groups
}

// lessDistance orders by distance nearest-first, treating a nil distance
// (non-geometric candidate) as farther than any real distance so geometric
// candidates always sort ahead of it.
func lessDistance(di, dj *float64) bool {
	switch {
	case di == nil && dj == nil:
		return false
	case di == nil:
		return false
	case dj == nil:
		return true
	default:
		return *di < *dj
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
