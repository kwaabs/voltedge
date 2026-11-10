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
	"sync"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/faults"
	"github.com/kwaabs/voltedge/api/internal/fieldtrack"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/kwaabs/voltedge/api/internal/wam"
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
	// Policy rides every sync/status response so an already-provisioned device
	// refreshes the org-wide tracking policy WITHOUT re-logging in. The field token
	// is non-expiring (login is effectively one-time), so a policy change an admin
	// makes later can't wait for the next login -- it has to reach the device on its
	// regular heartbeat, and sync is that heartbeat.
	Policy *fieldTrackingPolicyWire `json:"policy,omitempty"`
}

// fieldTrackingPolicyWire mirrors the login response + GET /field-tracking/policy
// keys so the device parses it the same way wherever it arrives.
type fieldTrackingPolicyWire struct {
	Enabled           bool `json:"enabled"`
	DisclosureEnabled bool `json:"disclosure_enabled"`
}

// currentFieldTrackingPolicy reads both org-wide flags (default on, fail open) for a
// sync response. A read error is non-fatal -- the capture/status pull already
// succeeded -- so it degrades to the fail-open defaults rather than failing the call.
func currentFieldTrackingPolicy(ctx context.Context, db *bun.DB) *fieldTrackingPolicyWire {
	enabled, _ := fieldtrack.TrackingEnabled(ctx, db)
	disclosure, _ := fieldtrack.DisclosureEnabled(ctx, db)
	return &fieldTrackingPolicyWire{Enabled: enabled, DisclosureEnabled: disclosure}
}

// trackingPolicyHandler serves GET /tracking-policy: the org-wide field-tracking
// policy for the device to pull on demand (on pack open, before it decides whether to
// record and whether to disclose). The field token is non-expiring, so login is a
// one-time event -- without this the device could only learn a policy change at the
// next login. A read of the policy governing the caller's own device, so it's mounted
// behind the ordinary features:read field gate, not the supervisor field:tracks:read.
func trackingPolicyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentFieldTrackingPolicy(r.Context(), db))
	}
}

// syncCacheTTL bounds how long the drain worker's resolveCache serves a cached
// class metadata or session user before reloading it. Short so an admin recompile
// (new field/subtype) or a role/territory change is picked up within a minute,
// long enough to collapse the per-op reloads across a device's burst of ops.
const syncCacheTTL = 60 * time.Second

// resolveCache is the sync drain worker's cross-op cache for the two per-op costs
// that otherwise dominate the apply query load at fleet scale: a class's metadata
// (metadata.Load, ~6 queries) and the submitting field user's session (RBAC +
// territory, needed so each replayed write enforces Phase 58 write-scope and
// stamps the actor). Both are reused heavily across a device's ops but change
// rarely, so caching them at the WORKER level with a short TTL ([syncCacheTTL])
// kills the N+1 that Slice 11's per-batch cache couldn't (the async drain applies
// one op per claim, so a per-op cache never reuses anything), while bounding
// staleness after a recompile / role change. ONE instance is shared across the
// worker's concurrent op goroutines, so it is mutex-guarded. Only SUCCESSFUL loads
// are cached (a transient load error retries on the next op rather than being
// pinned for the whole TTL). Both loads use the base db -- reference data read
// outside any op's transaction, exactly as the old per-op cache did.
type resolveCache struct {
	db  bun.IDB
	ttl time.Duration

	mu    sync.Mutex
	meta  map[string]metaEntry
	users map[string]userEntry
}

type metaEntry struct {
	loaded *metadata.Loaded
	at     time.Time
}

type userEntry struct {
	user *auth.SessionUser
	at   time.Time
}

func newResolveCache(db bun.IDB, ttl time.Duration) *resolveCache {
	return &resolveCache{
		db:    db,
		ttl:   ttl,
		meta:  map[string]metaEntry{},
		users: map[string]userEntry{},
	}
}

// get returns a class's loaded metadata, from the cache when fresh (within the
// TTL) else reloaded. The lock is not held across the metadata.Load I/O.
func (c *resolveCache) get(ctx context.Context, code string) (*metadata.Loaded, error) {
	c.mu.Lock()
	if e, ok := c.meta[code]; ok && time.Since(e.at) < c.ttl {
		l := e.loaded
		c.mu.Unlock()
		return l, nil
	}
	c.mu.Unlock()

	loaded, err := metadata.Load(ctx, c.db, code)
	if err != nil {
		return nil, err // don't pin a transient failure for the whole TTL
	}
	c.mu.Lock()
	c.meta[code] = metaEntry{loaded: loaded, at: time.Now()}
	c.mu.Unlock()
	return loaded, nil
}

// user returns the submitting user's session, from the cache when fresh else
// reloaded via auth.LoadSessionUser. Same lock-off-IO shape as get.
func (c *resolveCache) user(ctx context.Context, id string) (*auth.SessionUser, error) {
	c.mu.Lock()
	if e, ok := c.users[id]; ok && time.Since(e.at) < c.ttl {
		u := e.user
		c.mu.Unlock()
		return u, nil
	}
	c.mu.Unlock()

	u, err := auth.LoadSessionUser(ctx, c.db, id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.users[id] = userEntry{user: u, at: time.Now()}
	c.mu.Unlock()
	return u, nil
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
		// Refresh the device's org-wide tracking policy on the way out (see Policy).
		resp.Policy = currentFieldTrackingPolicy(r.Context(), db)

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
		json.NewEncoder(w).Encode(syncResponse{
			Statuses: statuses,
			// A status-only pull (nothing to push) still refreshes the policy, so a
			// device that only reconciles in-flight ops picks up a change too.
			Policy: currentFieldTrackingPolicy(r.Context(), db),
		})
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
	case "wam_defect_create", "wam_defect_status", "wam_workorder_update", "wam_inspection_record":
		// Offline work-&-asset-management capture (Phase 59 offline WAM): a field
		// technician logs defects, advances their assigned work orders, and runs
		// inspection checklists offline; each is replayed through the shared wam
		// engine in applyWAMOp.
		return true
	case "fault_create", "fault_status", "fault_book_defect":
		// Offline faults capture (offline faults module): a fault-response crew
		// raises fault events, marks them cleared, and books a WAM defect from one
		// offline; each is replayed through the shared faults engine in
		// applyFaultsOp.
		return true
	}
	return false
}

// WAM offline-capture op payloads (Phase 59 offline WAM). A field technician's
// device pushes these alongside the feature/graph ops; they're sync-local
// snake_case mirrors of the wam engine's param structs (kept separate so the
// mobile client writes clean JSON, exactly as the establish payloads are). The
// record-creating ops (defect_create, inspection_record) carry their new row's
// device-minted UUID in the op's feature_id so replay is idempotent; the
// mutating ops (defect_status, workorder_update) target an existing row's id.
type wamDefectCreatePayload struct {
	AssetClass  string     `json:"asset_class"`
	AssetID     string     `json:"asset_id"`
	DefectType  *string    `json:"defect_type,omitempty"`
	Severity    string     `json:"severity,omitempty"`
	Source      string     `json:"source,omitempty"`
	Title       string     `json:"title"`
	Description *string    `json:"description,omitempty"`
	Longitude   *float64   `json:"longitude,omitempty"`
	Latitude    *float64   `json:"latitude,omitempty"`
	ReportedAt  *time.Time `json:"reported_at,omitempty"`
	Notes       *string    `json:"notes,omitempty"`
}

type wamDefectStatusPayload struct {
	Status string `json:"status"`
}

type wamWorkOrderUpdatePayload struct {
	Status          *string    `json:"status,omitempty"`
	Priority        *string    `json:"priority,omitempty"`
	CompletionNotes *string    `json:"completion_notes,omitempty"`
	ScheduledFor    *time.Time `json:"scheduled_for,omitempty"`
	DueBy           *time.Time `json:"due_by,omitempty"`
}

type wamInspectionItemPayload struct {
	TemplateItemID *string  `json:"template_item_id,omitempty"`
	Position       int      `json:"position,omitempty"`
	Prompt         string   `json:"prompt"`
	ResponseType   string   `json:"response_type,omitempty"`
	Passed         *bool    `json:"passed,omitempty"`
	ValueText      *string  `json:"value_text,omitempty"`
	ValueNumeric   *float64 `json:"value_numeric,omitempty"`
	FailSeverity   *string  `json:"fail_severity,omitempty"`
	Notes          *string  `json:"notes,omitempty"`
}

type wamInspectionRecordPayload struct {
	WorkOrderID       *string                    `json:"work_order_id,omitempty"`
	TemplateID        *string                    `json:"template_id,omitempty"`
	AssetClass        string                     `json:"asset_class"`
	AssetID           string                     `json:"asset_id"`
	InspectedAt       *time.Time                 `json:"inspected_at,omitempty"`
	Notes             *string                    `json:"notes,omitempty"`
	Longitude         *float64                   `json:"longitude,omitempty"`
	Latitude          *float64                   `json:"latitude,omitempty"`
	OverallResult     *string                    `json:"overall_result,omitempty"`
	CompleteWorkOrder bool                       `json:"complete_work_order,omitempty"`
	Items             []wamInspectionItemPayload `json:"items"`
}

// Faults offline-capture op payloads (offline faults module). A fault-response
// crew's device pushes these alongside the feature/WAM ops; they're sync-local
// snake_case mirrors of the faults engine's param structs (kept separate for the
// same clean-JSON reason as the WAM payloads). fault_create carries its new
// event's device-minted UUID in the op's feature_id so replay is idempotent;
// fault_status / fault_book_defect target an existing fault's id. detected_by /
// reported_by are not on the wire -- the submitting user is stamped as the actor,
// exactly as the WAM ops do.
type faultCreatePayload struct {
	Reference             *string    `json:"reference,omitempty"`
	AssetClass            *string    `json:"asset_class,omitempty"`
	AssetID               *string    `json:"asset_id,omitempty"`
	Longitude             *float64   `json:"longitude,omitempty"`
	Latitude              *float64   `json:"latitude,omitempty"`
	FaultType             *string    `json:"fault_type,omitempty"`
	Cause                 *string    `json:"cause,omitempty"`
	FaultCurrentA         *float64   `json:"fault_current_a,omitempty"`
	ProtectiveDeviceClass *string    `json:"protective_device_class,omitempty"`
	ProtectiveDeviceID    *string    `json:"protective_device_id,omitempty"`
	ClearingTimeMS        *float64   `json:"clearing_time_ms,omitempty"`
	DetectedAt            *time.Time `json:"detected_at,omitempty"`
	IncidentID            *string    `json:"incident_id,omitempty"`
	Title                 string     `json:"title"`
	Description           *string    `json:"description,omitempty"`
	Notes                 *string    `json:"notes,omitempty"`
}

type faultStatusPayload struct {
	Status string `json:"status"`
}

type faultBookDefectPayload struct {
	Title       *string `json:"title,omitempty"`
	DefectType  *string `json:"defect_type,omitempty"`
	Severity    *string `json:"severity,omitempty"`
	Description *string `json:"description,omitempty"`
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
func applySyncOp(ctx context.Context, db *bun.DB, cache *resolveCache, op syncOp, submittedBy string) syncOpResult {
	res := syncOpResult{OpID: op.OpID}
	if op.OpID == "" {
		res.Status = "error"
		res.Error = "op is missing op_id"
		return res
	}

	// WAM capture ops (Phase 59 offline WAM) replay through the shared wam engine,
	// not the feature CRUD path. Each wam write is already atomic on its own (a
	// single statement, or RecordInspection's own transaction) and carries the
	// device-minted record id for idempotent replay, so they run on db directly --
	// wrapping them in the feature-op transaction below would nest RecordInspection's
	// RunInTx inside another and buy nothing.
	switch op.Kind {
	case "wam_defect_create", "wam_defect_status", "wam_workorder_update", "wam_inspection_record":
		r := applyWAMOp(ctx, db, op, submittedBy)
		recordActionWaypointBestEffort(ctx, db, op, submittedBy, r.Status)
		return r
	case "fault_create", "fault_status", "fault_book_defect":
		// Offline faults ops replay through the shared faults engine for the same
		// reason as the WAM ops: each is atomic on its own (a single statement, or
		// BookDefectFromFault's own transaction) and carries the device-minted id
		// for idempotent replay, so they run on db directly.
		r := applyFaultsOp(ctx, db, op, submittedBy)
		recordActionWaypointBestEffort(ctx, db, op, submittedBy, r.Status)
		return r
	}

	// Replay the feature/graph ops under the SUBMITTING field user's identity, so
	// every downstream write enforces that user's Phase 58 territory write-scope
	// and stamps them as created_by/updated_by (and established_by for a graph
	// edit) -- exactly as the REST path does, and as the webhook ingestion engine
	// assumes its endpoint's service user (apply.go). The drain worker is headless,
	// so without this the context carries no user: enforceWriteScope silently
	// no-ops (a scoped user could write outside their territories via sync) and the
	// audit-actor columns land NULL. A submitting user that can no longer be loaded
	// (deactivated since the push) quarantines the op rather than applying it
	// unscoped and unattributed -- fail-closed, consistent with Phase 58. (The
	// wam_*/fault_* kinds returned above already thread submittedBy as an explicit
	// actor and are not territory-scoped, so they don't need this.)
	su, err := cache.user(ctx, submittedBy)
	if err != nil || su == nil {
		res.Status = "error"
		res.Error = fmt.Sprintf("cannot apply op: submitting user %q could not be loaded: %v", submittedBy, err)
		return res
	}
	ctx = auth.WithUser(ctx, su)

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
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
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
	recordActionWaypointBestEffort(ctx, db, op, submittedBy, res.Status)
	return res
}

// recordActionWaypointBestEffort stamps where the field worker physically was when
// a mutating op was made (field breadcrumb audit trail, migration 0178), from the
// op payload's nested "actor_location" -- exactly the way a GNSS gps_capture rides a
// create/update. The device nests it on EVERY mutating op (including a delete, whose
// payload is otherwise empty). It is best-effort: an absent location or a write
// failure is ignored, so the located-action audit can never fail or strand the op it
// describes. Keyed by op_id, so a replayed op re-records the same waypoint harmlessly.
func recordActionWaypointBestEffort(ctx context.Context, db *bun.DB, op syncOp, submittedBy, status string) {
	if status != "applied" || len(op.Payload) == 0 || strings.TrimSpace(submittedBy) == "" {
		return
	}
	// Org-wide tracking off: don't stamp a located waypoint (server backstop; the
	// edit itself still applies). Fails open on a read error.
	if enabled, _ := fieldtrack.TrackingEnabled(ctx, db); !enabled {
		return
	}
	var env struct {
		ActorLocation *fieldtrack.ActionLocationInput `json:"actor_location"`
	}
	if err := json.Unmarshal(op.Payload, &env); err != nil || env.ActorLocation == nil {
		return
	}
	_ = fieldtrack.RecordActionWaypoint(ctx, db, op.OpID, submittedBy, op.Kind, op.FeatureClass, op.FeatureID, *env.ActorLocation)
}

// applyWAMOp replays one work-&-asset-management capture op (Phase 59 offline
// WAM) through the shared wam engine. It's called from applySyncOp for the wam_*
// kinds. Each write is idempotent on replay: defect/inspection creates carry the
// device-minted row id (op.FeatureID) so a re-applied op is an ON CONFLICT / row-
// exists no-op; status/update ops are deterministic writes on an existing id.
// The submitting user is recorded as the reporter/inspector when the payload
// carries none. Like every apply path, errors are returned in the result, never
// propagated -- one bad op must not strand the ops queued behind it.
//
// WAM ops are not subject to the feature optimistic-concurrency check
// (detectSyncConflict only guards feature update_attrs/delete), so they follow
// today's last-write-wins semantics -- acceptable because the field flow is
// capture-oriented (new defects, own work orders, own inspections) rather than
// contended edits of shared rows.
func applyWAMOp(ctx context.Context, db *bun.DB, op syncOp, submittedBy string) syncOpResult {
	res := syncOpResult{OpID: op.OpID}
	var actor *string
	if strings.TrimSpace(submittedBy) != "" {
		a := strings.TrimSpace(submittedBy)
		actor = &a
	}

	switch op.Kind {
	case "wam_defect_create":
		var p wamDefectCreatePayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return wamErr(res, fmt.Errorf("wam_defect_create payload: %w", err))
		}
		d, err := wam.CreateDefect(ctx, db, wam.CreateDefectParams{
			ID:          nilIfBlank(op.FeatureID),
			AssetClass:  p.AssetClass,
			AssetID:     p.AssetID,
			DefectType:  p.DefectType,
			Severity:    p.Severity,
			Source:      p.Source,
			Title:       p.Title,
			Description: p.Description,
			Longitude:   p.Longitude,
			Latitude:    p.Latitude,
			ReportedBy:  actor,
			ReportedAt:  p.ReportedAt,
			Notes:       p.Notes,
		})
		if err != nil {
			return wamErr(res, err)
		}
		return wamOK(res, d)

	case "wam_defect_status":
		if strings.TrimSpace(op.FeatureID) == "" {
			return wamErr(res, fmt.Errorf("wam_defect_status op is missing feature_id"))
		}
		var p wamDefectStatusPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return wamErr(res, fmt.Errorf("wam_defect_status payload: %w", err))
		}
		d, err := wam.SetDefectStatus(ctx, db, strings.TrimSpace(op.FeatureID), p.Status)
		if err != nil {
			return wamErr(res, err)
		}
		return wamOK(res, d)

	case "wam_workorder_update":
		if strings.TrimSpace(op.FeatureID) == "" {
			return wamErr(res, fmt.Errorf("wam_workorder_update op is missing feature_id"))
		}
		var p wamWorkOrderUpdatePayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return wamErr(res, fmt.Errorf("wam_workorder_update payload: %w", err))
		}
		o, err := wam.UpdateWorkOrder(ctx, db, strings.TrimSpace(op.FeatureID), wam.UpdateWorkOrderParams{
			Status:          p.Status,
			Priority:        p.Priority,
			CompletionNotes: p.CompletionNotes,
			ScheduledFor:    p.ScheduledFor,
			DueBy:           p.DueBy,
		})
		if err != nil {
			return wamErr(res, err)
		}
		return wamOK(res, o)

	case "wam_inspection_record":
		var p wamInspectionRecordPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return wamErr(res, fmt.Errorf("wam_inspection_record payload: %w", err))
		}
		items := make([]wam.RecordItemInput, len(p.Items))
		for i, it := range p.Items {
			items[i] = wam.RecordItemInput{
				TemplateItemID: it.TemplateItemID,
				Position:       it.Position,
				Prompt:         it.Prompt,
				ResponseType:   it.ResponseType,
				Passed:         it.Passed,
				ValueText:      it.ValueText,
				ValueNumeric:   it.ValueNumeric,
				FailSeverity:   it.FailSeverity,
				Notes:          it.Notes,
			}
		}
		insp, err := wam.RecordInspection(ctx, db, wam.RecordInspectionParams{
			ID:                nilIfBlank(op.FeatureID),
			WorkOrderID:       p.WorkOrderID,
			TemplateID:        p.TemplateID,
			AssetClass:        p.AssetClass,
			AssetID:           p.AssetID,
			InspectedBy:       actor,
			InspectedAt:       p.InspectedAt,
			Notes:             p.Notes,
			Longitude:         p.Longitude,
			Latitude:          p.Latitude,
			OverallResult:     p.OverallResult,
			CompleteWorkOrder: p.CompleteWorkOrder,
			Items:             items,
		})
		if err != nil {
			return wamErr(res, err)
		}
		return wamOK(res, insp)

	default:
		return wamErr(res, fmt.Errorf("unknown wam op kind %q", op.Kind))
	}
}

// wamOK marks a WAM op applied, projecting the engine's returned row into the
// generic feature map the result carries (reusing the struct's own JSON tags, so
// the device reads back the same shape the REST /wam surface returns).
func wamOK(res syncOpResult, row any) syncOpResult {
	res.Status = "applied"
	res.Feature = toFeatureMap(row)
	return res
}

// wamErr marks a WAM op errored (quarantined by the worker, same as any apply
// failure).
func wamErr(res syncOpResult, err error) syncOpResult {
	res.Status = "error"
	res.Error = err.Error()
	return res
}

// applyFaultsOp replays one offline faults op through the shared faults engine --
// the exact twin of applyWAMOp for the fault_* kinds. fault_create carries the
// new event's device-minted id (op.FeatureID) so a re-applied op is an ON CONFLICT
// no-op; fault_status is a deterministic write on an existing id; fault_book_defect
// dedupes on the fault's own defect_id (it mints a DB-defaulted defect, so it can't
// dedupe on a device UUID). The submitting user is stamped as the detector/reporter.
// Faults ops are not subject to the feature optimistic-concurrency check
// (detectSyncConflict only guards feature update_attrs/delete), so they follow
// today's last-write-wins semantics -- acceptable because the field flow is
// capture-oriented (new faults, clearing own faults) rather than contended edits.
func applyFaultsOp(ctx context.Context, db *bun.DB, op syncOp, submittedBy string) syncOpResult {
	res := syncOpResult{OpID: op.OpID}
	var actor *string
	if strings.TrimSpace(submittedBy) != "" {
		a := strings.TrimSpace(submittedBy)
		actor = &a
	}

	switch op.Kind {
	case "fault_create":
		var p faultCreatePayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return faultErr(res, fmt.Errorf("fault_create payload: %w", err))
		}
		f, err := faults.CreateFault(ctx, db, faults.CreateFaultParams{
			ID:                    nilIfBlank(op.FeatureID),
			Reference:             p.Reference,
			AssetClass:            p.AssetClass,
			AssetID:               p.AssetID,
			Longitude:             p.Longitude,
			Latitude:              p.Latitude,
			FaultType:             p.FaultType,
			Cause:                 p.Cause,
			FaultCurrentA:         p.FaultCurrentA,
			ProtectiveDeviceClass: p.ProtectiveDeviceClass,
			ProtectiveDeviceID:    p.ProtectiveDeviceID,
			ClearingTimeMS:        p.ClearingTimeMS,
			DetectedBy:            actor,
			DetectedAt:            p.DetectedAt,
			IncidentID:            p.IncidentID,
			Title:                 p.Title,
			Description:           p.Description,
			Notes:                 p.Notes,
		})
		if err != nil {
			return faultErr(res, err)
		}
		return faultOK(res, f)

	case "fault_status":
		if strings.TrimSpace(op.FeatureID) == "" {
			return faultErr(res, fmt.Errorf("fault_status op is missing feature_id"))
		}
		var p faultStatusPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return faultErr(res, fmt.Errorf("fault_status payload: %w", err))
		}
		f, err := faults.SetFaultStatus(ctx, db, strings.TrimSpace(op.FeatureID), p.Status)
		if err != nil {
			return faultErr(res, err)
		}
		return faultOK(res, f)

	case "fault_book_defect":
		if strings.TrimSpace(op.FeatureID) == "" {
			return faultErr(res, fmt.Errorf("fault_book_defect op is missing feature_id"))
		}
		faultID := strings.TrimSpace(op.FeatureID)
		// Idempotent replay: BookDefectFromFault mints a DB-defaulted defect id, so
		// it can't dedupe on a device UUID the way create/status do. The fault's own
		// defect_id is the dedupe key -- if it's already set (an earlier apply booked
		// the defect), this replay is a no-op returning the fault as-is, rather than
		// tripping the engine's "already has a booked defect" guard into an error.
		existing, err := faults.GetFault(ctx, db, faultID)
		if err != nil {
			return faultErr(res, err)
		}
		if existing.DefectID != nil && strings.TrimSpace(*existing.DefectID) != "" {
			return faultOK(res, existing)
		}
		var p faultBookDefectPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return faultErr(res, fmt.Errorf("fault_book_defect payload: %w", err))
		}
		f, err := faults.BookDefectFromFault(ctx, db, faultID, faults.BookDefectFromFaultParams{
			Title:       p.Title,
			DefectType:  p.DefectType,
			Severity:    p.Severity,
			Description: p.Description,
			ReportedBy:  actor,
		})
		if err != nil {
			return faultErr(res, err)
		}
		return faultOK(res, f)

	default:
		return faultErr(res, fmt.Errorf("unknown faults op kind %q", op.Kind))
	}
}

// faultOK marks a faults op applied, projecting the engine's returned row into the
// generic feature map the result carries (reusing the FaultEvent JSON tags, so the
// device reads back the same shape the REST /faults surface returns).
func faultOK(res syncOpResult, row any) syncOpResult {
	res.Status = "applied"
	res.Feature = toFeatureMap(row)
	return res
}

// faultErr marks a faults op errored (quarantined by the worker, same as any apply
// failure).
func faultErr(res syncOpResult, err error) syncOpResult {
	res.Status = "error"
	res.Error = err.Error()
	return res
}

// toFeatureMap round-trips a typed row through JSON into the map[string]any the
// sync result carries. A marshal error yields nil (the op still counts applied --
// the row is committed; only the echoed-back feature is dropped).
func toFeatureMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// nilIfBlank returns a trimmed *string, or nil when the input is empty -- so an
// absent device-minted id lets the wam engine default it.
func nilIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	t := strings.TrimSpace(s)
	return &t
}
