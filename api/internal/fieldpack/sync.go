package fieldpack

// POST /sync + GET /sync/status -- durable edit capture (Phase 23 Slice 1,
// docs/phase-23-sync-inbox.md; originally Phase 21 Slice 3e inline replay).
//
// A device pushes its outbox: the queue of edits made offline, oldest first. An op
// is a feature create, an attribute/geometry update, a delete, or a graph edit --
// establishing a connection or a relationship. The feature ops arrive in
// crud.CreateFeatureInput / crud.UpdateFeatureInput shape; the graph ops in the
// establish payload shapes below.
//
// RECEIVE / APPLY SPLIT (Phase 23). POST /sync no longer applies ops inline. Its
// only synchronous job is a cheap, durable capture: shape-validate each op and bulk
// INSERT the batch into ops.sync_inbox (inbox.go), then ack 'received'. The ack now
// means "durably captured," not "applied to production" -- so the no-loss guarantee
// stops riding on apply-time metadata/geometry/topology work. A worker (syncworker.go)
// drains the inbox moments later and commits each op through applySyncOp (still in
// this file, unchanged), the same per-op transaction + shared crud/connectivity/
// relationship engines the old inline endpoint used.
//
// The capture is idempotent by construction: op_id is the device idempotency key and
// the inbox PK (ON CONFLICT DO NOTHING), and each apply is itself idempotent (a
// create's ON CONFLICT (id) DO NOTHING, an establish's no-op re-establish). So a
// device retries a partial/failed push safely -- it marks synced exactly the ops the
// server acked as received/duplicate, and re-sends the rest. Because apply is now
// asynchronous, the device reads back outcomes (applied / rejected / conflict) via a
// status pull: piggy-backed on the next POST /sync (a `poll` list of op_ids) or a
// dedicated GET /sync/status -- both owner-scoped through getInboxStatuses.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// syncOp is one queued edit from a device's outbox (mobile EditDatabase.outbox).
// Payload is the op's crud input verbatim; FeatureID is the target (the create's
// client UUID or the edited feature's id). OpID is the idempotency key the device
// dedupes acks on -- the server just echoes it back per result.
type syncOp struct {
	OpID         string          `json:"op_id"`
	FeatureClass string          `json:"feature_class"`
	Kind         string          `json:"kind"` // "create" | "update_attrs" | "delete" | "establish_connection" | "establish_relationship"
	FeatureID    string          `json:"feature_id"`
	Payload      json.RawMessage `json:"payload"`
	BaseVersion  string          `json:"base_version,omitempty"`
}

// syncEndpoint / establishConnPayload are the wire shape of an establish_connection
// op's payload -- one side of a connection (a feature, optionally at a named
// terminal). Kept as sync-local snake_case structs rather than reusing
// connectivity.EstablishConnectionInput directly so the mobile client writes clean
// JSON (that exported type's From/To fields carry no json tags, so they'd marshal
// as capitalized keys); applySyncOp maps these into the exported input.
type syncEndpoint struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	Terminal     *string `json:"terminal,omitempty"`
}

type establishConnPayload struct {
	From syncEndpoint `json:"from"`
	To   syncEndpoint `json:"to"`
}

// syncFeatureRef / establishRelPayload are the wire shape of an
// establish_relationship op's payload. Kind is optional -- only needed to
// disambiguate when more than one relationship_classes row exists for the same
// origin/destination class pairing.
type syncFeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

type establishRelPayload struct {
	Origin      syncFeatureRef `json:"origin"`
	Destination syncFeatureRef `json:"destination"`
	Kind        *string        `json:"kind,omitempty"`
}

// syncRequest is a device push. Ops is the outbox batch (oldest-first). DeviceID /
// SubmissionID are optional grouping hints (the S1 client sends neither; the inbox
// tolerates null). Poll is a list of op_ids the device is still awaiting an outcome
// for -- the server piggy-backs their current statuses on the response.
type syncRequest struct {
	Ops          []syncOp `json:"ops"`
	DeviceID     string   `json:"device_id,omitempty"`
	SubmissionID string   `json:"submission_id,omitempty"`
	Poll         []string `json:"poll,omitempty"`
}

// syncOpResult is one op's capture outcome (POST /sync no longer applies inline, so
// this is a RECEIVE result). Status is "received" (newly captured), "duplicate"
// (already captured -- a safe re-push), or "error" with a human message for an op
// rejected at capture (missing op_id / unknown kind). The device marks synced on
// received/duplicate; the eventual applied/rejected outcome comes via the status pull.
type syncOpResult struct {
	OpID    string         `json:"op_id"`
	Status  string         `json:"status"`
	Error   string         `json:"error,omitempty"`
	Feature map[string]any `json:"feature,omitempty"`
}

// syncOpStatus is one op's current apply status, returned by the status pull (the
// POST /sync `statuses` piggy-back or GET /sync/status). Status is the inbox
// lifecycle value (received | applying | applied | rejected | conflict | ...); Error
// carries the quarantine message on rejected; Feature is the server row on applied.
type syncOpStatus struct {
	OpID    string          `json:"op_id"`
	Status  string          `json:"status"`
	Error   string          `json:"error,omitempty"`
	Feature json.RawMessage `json:"feature,omitempty"`
}

type syncResponse struct {
	Results  []syncOpResult `json:"results,omitempty"`
	Statuses []syncOpStatus `json:"statuses,omitempty"`
}

// metaCache memoizes metadata.Load across the ops in one sync batch. A push is
// almost always many ops spanning only a handful of feature classes, and Load
// runs ~6 queries, so loading each class once instead of once-per-op removes an
// N+1 that dominated a large batch's cost (Phase 21 Slice 11 scale hardening).
// Metadata is read-only reference data, loaded on the base db outside any op's
// transaction, so sharing one copy across ops is safe. Both hits AND load errors
// are cached, so a bad class name doesn't re-run the failing queries for every op
// that references it.
type metaCache struct {
	db     bun.IDB
	loaded map[string]*metadata.Loaded
	errs   map[string]error
}

func newMetaCache(db bun.IDB) *metaCache {
	return &metaCache{
		db:     db,
		loaded: map[string]*metadata.Loaded{},
		errs:   map[string]error{},
	}
}

func (c *metaCache) get(ctx context.Context, code string) (*metadata.Loaded, error) {
	if l, ok := c.loaded[code]; ok {
		return l, nil
	}
	if e, ok := c.errs[code]; ok {
		return nil, e
	}
	l, err := metadata.Load(ctx, c.db, code)
	if err != nil {
		c.errs[code] = err
		return nil, err
	}
	c.loaded[code] = l
	return l, nil
}

// syncHandler durably captures a device's outbox into ops.sync_inbox and acks. It
// no longer applies inline (a worker drains the inbox -- syncworker.go); its whole
// synchronous cost is a shape check + one bulk idempotent insert, so the ack is fast
// and the no-loss guarantee doesn't ride on apply-time logic. The batch always
// answers 200 -- per-op capture outcome lives in the body, so the device marks
// exactly what was captured and keeps the rest queued. It also piggy-backs current
// statuses for any op_ids the device listed in `poll`. The device is expected to
// chunk a large outbox into bounded batches (Phase 21 Slice 11).
func syncHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}

		var req syncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("request body is not valid JSON: %v", err))
			return
		}

		// Shape-validate up front: an op missing its op_id or carrying an unknown
		// kind could never apply, so it's rejected at capture rather than stored.
		// Everything else is captured verbatim -- metadata/geometry/engine checks
		// happen later, in the worker.
		results := make([]syncOpResult, 0, len(req.Ops))
		valid := make([]syncOp, 0, len(req.Ops))
		for _, op := range req.Ops {
			if op.OpID == "" {
				results = append(results, syncOpResult{Status: "error", Error: "op is missing op_id"})
				continue
			}
			if !validOpKind(op.Kind) {
				results = append(results, syncOpResult{OpID: op.OpID, Status: "error", Error: fmt.Sprintf("unknown op kind %q", op.Kind)})
				continue
			}
			valid = append(valid, op)
		}

		var deviceID, submissionID *string
		if req.DeviceID != "" {
			deviceID = &req.DeviceID
		}
		if req.SubmissionID != "" {
			submissionID = &req.SubmissionID
		}

		// Resolve the apply policy once for the whole batch (Phase 23 Slice 3): it
		// decides, per feature class, whether each op lands 'received' (worker
		// auto-applies) or 'pending_review' (held for a supervisor to post). An empty
		// ops.sync_policies table resolves to auto for everything -- Slice 1 behaviour.
		policy, err := loadSyncPolicy(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not load sync policy: "+err.Error())
			return
		}

		newly, err := receiveInboxOps(r.Context(), db, user.ID, deviceID, submissionID, valid, policy)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not capture ops: "+err.Error())
			return
		}
		for _, op := range valid {
			status := "duplicate"
			if newly[op.OpID] {
				status = "received"
			}
			results = append(results, syncOpResult{OpID: op.OpID, Status: status})
		}

		resp := syncResponse{Results: results}
		if len(req.Poll) > 0 {
			// A status pull failure must not fail the capture (the important work is
			// already durable) -- omit the statuses and let the device poll again.
			if statuses, err := loadStatuses(r.Context(), db, user.ID, req.Poll); err == nil {
				resp.Statuses = statuses
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// statusHandler serves the standalone status pull: GET /sync/status?op_ids=a,b,c
// returns the current apply status of the named ops, scoped to the caller. It's the
// path a device with nothing new to push uses to reconcile its in-flight ops (the
// piggy-back on POST /sync covers the push-and-check case).
func statusHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		var ids []string
		for _, s := range strings.Split(r.URL.Query().Get("op_ids"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				ids = append(ids, s)
			}
		}
		if len(ids) == 0 {
			writeErr(w, http.StatusBadRequest, "op_ids query parameter is required (comma-separated op ids)")
			return
		}
		if len(ids) > 500 {
			writeErr(w, http.StatusBadRequest, "too many op_ids (max 500 per poll)")
			return
		}
		statuses, err := loadStatuses(r.Context(), db, user.ID, ids)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(syncResponse{Statuses: statuses})
	}
}

// loadStatuses maps the inbox rows for opIDs (owner-scoped) into the wire shape.
func loadStatuses(ctx context.Context, db *bun.DB, submittedBy string, opIDs []string) ([]syncOpStatus, error) {
	rows, err := getInboxStatuses(ctx, db, submittedBy, opIDs)
	if err != nil {
		return nil, err
	}
	out := make([]syncOpStatus, 0, len(rows))
	for i := range rows {
		s := syncOpStatus{OpID: rows[i].OpID, Status: rows[i].Status}
		if rows[i].Error != nil {
			s.Error = *rows[i].Error
		}
		if len(rows[i].ResultFeature) > 0 {
			s.Feature = rows[i].ResultFeature
		}
		out = append(out, s)
	}
	return out, nil
}

// validOpKind is the capture-time allow-list of op kinds -- the same set applySyncOp
// dispatches on (its default case is the safety net for anything that slips past).
func validOpKind(kind string) bool {
	switch kind {
	case "create", "update_attrs", "delete", "establish_connection", "establish_relationship":
		return true
	}
	return false
}

// applySyncOp replays one op in its own transaction, so a failure rolls back just
// that op (and the create's territory assign stays atomic with its insert) while
// the rest of the batch proceeds. Errors are returned in the result, never
// propagated -- one bad op must not strand the ops queued behind it.
//
// The feature CRUD kinds (create / update_attrs) load their class metadata via the
// per-batch cache; the establish kinds don't touch metadata at all -- they replay
// through the shared connectivity/relationship engines, which authorize the write
// against network_rules / relationship_classes themselves (zero trust, exactly as
// the REST + MCP establish surfaces do). Re-pushing an already-established edge is a
// no-op (ON CONFLICT keeps the original), so an establish op is retry-safe on the
// same terms as an idempotent create.
func applySyncOp(ctx context.Context, db *bun.DB, cache *metaCache, op syncOp, submittedBy string) syncOpResult {
	res := syncOpResult{OpID: op.OpID}
	if op.OpID == "" {
		res.Status = "error"
		res.Error = "op is missing op_id"
		return res
	}

	// A GPS-sourced placement/move (field tool "place at my location") nests the
	// GNSS quality snapshot under a "gps_capture" key alongside the crud input
	// (Phase gnss-quality Slice 3). It's absent on map-drawn/REST/MCP ops, on the
	// delete/establish kinds, and on a delete's empty payload -- so unmarshal
	// best-effort and only persist it (below) when a create/update actually carries
	// one. crud.CreateFeatureInput/UpdateFeatureInput ignore the extra key.
	var capEnv struct {
		GpsCapture *crud.GpsCaptureInput `json:"gps_capture"`
	}
	if len(op.Payload) > 0 {
		_ = json.Unmarshal(op.Payload, &capEnv)
	}

	var feature map[string]any
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		switch op.Kind {
		case "create":
			loaded, e := cache.get(ctx, op.FeatureClass)
			if e != nil {
				return fmt.Errorf("feature class %q: %w", op.FeatureClass, e)
			}
			var in crud.CreateFeatureInput
			if e := json.Unmarshal(op.Payload, &in); e != nil {
				return fmt.Errorf("create payload: %w", e)
			}
			// Honour the device's client-minted id as the row's primary key so
			// the replay is idempotent. Prefer the payload's own id; fall back to
			// the outbox row's feature_id if the payload omitted it.
			if in.ID == nil && op.FeatureID != "" {
				fid := op.FeatureID
				in.ID = &fid
			}
			f, e := crud.CreateFeature(ctx, tx, loaded, in)
			if e != nil {
				return e
			}
			feature = f
			if capEnv.GpsCapture != nil {
				fid, _ := f["id"].(string)
				if e := crud.RecordGpsCapture(ctx, tx, op.FeatureClass, fid, submittedBy, *capEnv.GpsCapture); e != nil {
					return e
				}
			}
			return nil
		case "update_attrs":
			loaded, e := cache.get(ctx, op.FeatureClass)
			if e != nil {
				return fmt.Errorf("feature class %q: %w", op.FeatureClass, e)
			}
			if op.FeatureID == "" {
				return fmt.Errorf("update op is missing feature_id")
			}
			var in crud.UpdateFeatureInput
			if e := json.Unmarshal(op.Payload, &in); e != nil {
				return fmt.Errorf("update payload: %w", e)
			}
			f, e := crud.UpdateFeature(ctx, tx, loaded, op.FeatureID, in)
			if e != nil {
				return e
			}
			feature = f
			if capEnv.GpsCapture != nil {
				if e := crud.RecordGpsCapture(ctx, tx, op.FeatureClass, op.FeatureID, submittedBy, *capEnv.GpsCapture); e != nil {
					return e
				}
			}
			return nil
		case "delete":
			// Soft delete (deleted_at = now()). Idempotent by construction --
			// re-deleting an already-deleted row returns its current state -- so a
			// re-pushed delete op is safe, same as create's ON CONFLICT DO NOTHING.
			// No payload: a delete is fully described by (feature_class, feature_id).
			loaded, e := cache.get(ctx, op.FeatureClass)
			if e != nil {
				return fmt.Errorf("feature class %q: %w", op.FeatureClass, e)
			}
			if op.FeatureID == "" {
				return fmt.Errorf("delete op is missing feature_id")
			}
			f, e := crud.DeleteFeature(ctx, tx, loaded, op.FeatureID)
			feature = f
			return e
		case "establish_connection":
			var p establishConnPayload
			if e := json.Unmarshal(op.Payload, &p); e != nil {
				return fmt.Errorf("establish_connection payload: %w", e)
			}
			cr, e := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
				From: connectivity.Endpoint{FeatureClass: p.From.FeatureClass, FeatureID: p.From.FeatureID, Terminal: p.From.Terminal},
				To:   connectivity.Endpoint{FeatureClass: p.To.FeatureClass, FeatureID: p.To.FeatureID, Terminal: p.To.Terminal},
			})
			if e != nil {
				return e
			}
			feature = map[string]any{
				"connection_id":     cr.ID,
				"network_rule_id":   cr.NetworkRuleID,
				"network_rule_name": cr.NetworkRuleName,
			}
			return nil
		case "establish_relationship":
			var p establishRelPayload
			if e := json.Unmarshal(op.Payload, &p); e != nil {
				return fmt.Errorf("establish_relationship payload: %w", e)
			}
			rr, e := relationship.EstablishRelationship(ctx, tx, relationship.EstablishRelationshipInput{
				Origin:      relationship.FeatureRef{FeatureClass: p.Origin.FeatureClass, FeatureID: p.Origin.FeatureID},
				Destination: relationship.FeatureRef{FeatureClass: p.Destination.FeatureClass, FeatureID: p.Destination.FeatureID},
				Kind:        p.Kind,
			})
			if e != nil {
				return e
			}
			feature = map[string]any{
				"relationship_id":         rr.ID,
				"relationship_class_id":   rr.RelationshipClassID,
				"relationship_class_name": rr.RelationshipClassName,
				"cardinality":             rr.Cardinality,
			}
			if rr.PlausibilityWarning != nil {
				feature["plausibility_warning"] = *rr.PlausibilityWarning
			}
			return nil
		default:
			return fmt.Errorf("unknown op kind %q", op.Kind)
		}
	})
	if err != nil {
		res.Status = "error"
		res.Error = err.Error()
		return res
	}

	res.Status = "applied"
	res.Feature = feature
	return res
}
