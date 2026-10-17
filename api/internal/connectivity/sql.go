package connectivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Connectivity mirrors runtime.feature_connectivity (migrations/0016).
// Unlike api/internal/crud's dynamic, runtime-shaped feature tables,
// this table's schema is fixed and known at compile time -- same
// category as the metadata.* tables -- so it's a plain bun model rather
// than hand-built SQL.
//
// alias:feature_connectivity pins the table's SQL alias to its own bare
// name -- without it, bun derives the alias from the Go struct name
// ("connectivity", lowercased from Connectivity) instead, which breaks
// establishConnection's ON CONFLICT DO UPDATE SET clause: that clause
// references the pre-existing row's value as feature_connectivity.<col>,
// and Postgres rejects that as an unknown table once bun has aliased the
// insert target to something else ("missing FROM-clause entry for table
// \"feature_connectivity\""). Pinning the alias here means the SET
// clause's table reference is guaranteed correct rather than dependent
// on guessing what bun would derive from the struct name.
type Connectivity struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity,alias:feature_connectivity"`

	ID                 string    `bun:"id,pk,nullzero"`
	NetworkRuleID      string    `bun:"network_rule_id"`
	FromFeatureClassID string    `bun:"from_feature_class_id"`
	FromFeatureID      string    `bun:"from_feature_id"`
	FromTerminalID     *string   `bun:"from_terminal_id"`
	ToFeatureClassID   string    `bun:"to_feature_class_id"`
	ToFeatureID        string    `bun:"to_feature_id"`
	ToTerminalID       *string   `bun:"to_terminal_id"`
	EstablishedAt      time.Time `bun:"established_at,nullzero"`
	EstablishedBy      *string   `bun:"established_by"`
}

// resolvedEndpoint is one side of a connection after its feature class,
// specific feature row, and (optional) terminal have all been resolved
// and validated -- everything matchRule and the insert need, with the
// human-facing code/terminal strings kept alongside for the response.
type resolvedEndpoint struct {
	ref        endpointRef
	loaded     *metadata.Loaded
	subtypeID  *string
	terminalID *string
	// terminalCode is the resolved terminal's stable CODE (e.g. "T1"),
	// kept alongside terminalID because network-rule matching compares
	// terminals by code, not by row id -- a class can carry several
	// terminal rows sharing one code (a generic subtype-null row plus
	// subtype-specific overrides), so the specific row this feature
	// resolves to may be a different uuid than the one a rule pinned even
	// though both are "T1". nil when the endpoint names no terminal.
	terminalCode *string
	// phase is the feature's PHASE-coded field value (e.g. "L1N",
	// "L1L2L3N"), or nil when the class has no phase field or the row
	// left it unset. Phase 9 Slice 0's phase gate reads this; a nil phase
	// means the gate can't run (see checkPhaseCompatibility).
	phase *string
}

// loadFeatureClass mirrors crud's loadOrNotFound -- kept as its own copy
// here rather than exported from crud, since translating sql.ErrNoRows
// into a connectivity-local notFoundError (so this package's writeError
// never has to know about crud's error types) is the whole point.
func loadFeatureClass(ctx context.Context, db bun.IDB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return loaded, nil
}

// resolveEndpoint validates one side of an establish request: the
// feature class must exist and be compiled, the specific feature row
// must exist and not be soft-deleted (connecting a deleted feature makes
// no sense), and a requested terminal code must actually be defined for
// that feature's feature class/subtype.
func resolveEndpoint(ctx context.Context, db bun.IDB, ref endpointRef) (*resolvedEndpoint, error) {
	loaded, err := loadFeatureClass(ctx, db, ref.FeatureClass)
	if err != nil {
		return nil, err
	}

	row, err := crud.GetFeature(ctx, db, loaded, ref.FeatureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", ref.FeatureID, ref.FeatureClass)
		}
		return nil, err
	}
	if row["deleted_at"] != nil {
		return nil, invalid("feature %s (%s) is deleted -- restore it before connecting", ref.FeatureID, ref.FeatureClass)
	}

	var subtypeID *string
	if rawSubtype, ok := row["subtype"]; ok && rawSubtype != nil {
		code, ok := toInt(rawSubtype)
		if !ok {
			return nil, fmt.Errorf("connectivity: %s: subtype value %v not an integer", ref.FeatureClass, rawSubtype)
		}
		if st := loaded.SubtypeByCode(code); st != nil {
			subtypeID = &st.ID
		}
	}

	var terminalID *string
	var terminalCode *string
	if ref.Terminal != nil {
		t := loaded.ResolveTerminal(subtypeID, *ref.Terminal)
		if t == nil {
			return nil, invalid("terminal %q is not defined for %s", *ref.Terminal, ref.FeatureClass)
		}
		terminalID = &t.ID
		code := t.Code
		terminalCode = &code
	}

	// phase is optional: a class may have no phase field at all (Phase 9
	// Slice 0 only added it to phase-bearing classes, as a nullable
	// field), so a missing/empty value is normal and simply leaves the
	// phase gate inert for this endpoint rather than being an error here.
	// crud.GetFeature nests every custom field under row["attributes"]
	// (only system columns -- id/subtype/status/deleted_at/timestamps --
	// sit at the top level, see crud.formatRow), so phase must be read
	// from there, not from row["phase"] directly.
	var phase *string
	if attrs, ok := row["attributes"].(map[string]any); ok {
		if raw, ok := attrs["phase"]; ok && raw != nil {
			if s, ok := raw.(string); ok && s != "" {
				phase = &s
			}
		}
	}

	return &resolvedEndpoint{ref: ref, loaded: loaded, subtypeID: subtypeID, terminalID: terminalID, terminalCode: terminalCode, phase: phase}, nil
}

// toInt mirrors crud's own helper -- pgdriver/bun can hand back a scanned
// integer as any of these Go types depending on path taken, and this
// package needs the same normalization crud's completeness gate does.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// establishConnection is the only path that turns a candidate connection
// into real topology: resolve both endpoints, require a matching active
// network_rules row (zero trust -- matchRule rejects if none exists or
// the only match is disabled), then write. The insert is an idempotent
// upsert on the table's (from_feature_id, from_terminal_id,
// to_feature_id, to_terminal_id) unique constraint -- establishing the
// same connection twice is a safe no-op, not a duplicate row, matching
// this codebase's existing idempotency pattern for publish/unpublish and
// delete/restore.
func establishConnection(ctx context.Context, db bun.IDB, req establishRequest) (*connectionResponse, error) {
	from, err := resolveEndpoint(ctx, db, req.From)
	if err != nil {
		return nil, err
	}
	to, err := resolveEndpoint(ctx, db, req.To)
	if err != nil {
		return nil, err
	}

	// network_rules.from/to is a metadata-authoring convention (e.g. an
	// edge like MV_CONDUCTOR is always authored as "from", a
	// terminal-bearing junction like DIST_XFMR always "to"), not a
	// statement about real electrical current direction -- every other
	// consumer (the Terminals tab, the trace engine's "connected"
	// direction) treats an established connection as undirected. Which
	// endpoint the client calls "from" only reflects which feature the
	// UI's connect workflow happened to start from, so a rule authored
	// for one direction must also authorize the request made in the
	// other: try the request's own from/to pair first, then the swapped
	// pair, and if only the swap matches, write (and report) the
	// connection in the rule's own canonical direction rather than the
	// direction the request happened to name it in -- so a connection
	// between the same two features is recorded identically regardless
	// of which endpoint the user started the workflow from.
	rule, canonicalFrom, canonicalTo, err := matchRuleEitherDirection(ctx, db, from, to)
	if err != nil {
		return nil, err
	}
	from, to = canonicalFrom, canonicalTo

	// Terminal-cardinality guard: a terminal flagged single_connection (e.g. a
	// cable joint's T1/T2, which splice exactly ONE cable each) may hold only one
	// connection. A busbar terminal is the opposite -- a common node where many
	// ways meet -- and is left unflagged, so it's never guarded. Checked for each
	// endpoint so a second distinct feature on an already-occupied single terminal
	// is refused up front, rather than surfacing later as a mysterious trace gap (a
	// split that re-wired both halves onto one joint terminal is the motivating
	// case). Re-establishing the SAME connection is still a no-op (the occupant is
	// the same feature).
	if err := enforceSingleConnectionTerminal(ctx, db, from, to.ref.FeatureID); err != nil {
		return nil, err
	}
	if err := enforceSingleConnectionTerminal(ctx, db, to, from.ref.FeatureID); err != nil {
		return nil, err
	}

	// Stamp the acting user (Slice E): established_by is text and now
	// holds the actor's uuid rather than the old fixed "api" literal.
	// Empty only on a session-less code path -- leave it NULL then rather
	// than invent an identity.
	conn := &Connectivity{
		NetworkRuleID:      rule.ID,
		FromFeatureClassID: from.loaded.FeatureClass.ID,
		FromFeatureID:      from.ref.FeatureID,
		FromTerminalID:     from.terminalID,
		ToFeatureClassID:   to.loaded.FeatureClass.ID,
		ToFeatureID:        to.ref.FeatureID,
	}
	if actor := auth.ActorID(ctx); actor != "" {
		conn.EstablishedBy = &actor
	}

	_, err = db.NewInsert().Model(conn).
		On("CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO UPDATE").
		Set("established_at = feature_connectivity.established_at").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectivity: establish: %w", err)
	}

	return formatConnection(conn, rule.Name, from, to), nil
}

// enforceSingleConnectionTerminal refuses a new connection that would put a SECOND
// distinct feature on a terminal flagged metadata.terminals.single_connection -- a
// one-cable-per-port terminal like a cable joint's T1/T2. Returns nil (no guard)
// when the endpoint names no terminal, the terminal isn't flagged single, or the
// single_connection column isn't present yet (the seed that adds it is owed), so a
// not-yet-seeded deployment connects exactly as before. otherFeatureID is the
// feature this endpoint is being connected TO, excluded from the occupancy check so
// re-establishing the SAME connection is never blocked; a soft-deleted occupant
// (feature_registry.deleted_at set) is ignored, so a deleted cable never squats a
// terminal.
func enforceSingleConnectionTerminal(ctx context.Context, db bun.IDB, ep *resolvedEndpoint, otherFeatureID string) error {
	if ep.terminalID == nil {
		return nil
	}
	var single bool
	if err := db.NewSelect().
		ColumnExpr("single_connection").
		TableExpr("metadata.terminals").
		Where("id = ?", *ep.terminalID).
		Scan(ctx, &single); err != nil {
		// No such terminal row, or the single_connection column doesn't exist yet
		// (seed not applied): degrade to no guard rather than break connect.
		return nil
	}
	if !single {
		return nil
	}
	occupied, err := db.NewSelect().
		TableExpr("runtime.feature_connectivity AS fc").
		Where(
			`(fc.from_feature_id = ? AND fc.from_terminal_id IS NOT DISTINCT FROM ? AND fc.to_feature_id <> ? `+
				`AND EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = fc.to_feature_id AND r.deleted_at IS NULL)) `+
				`OR (fc.to_feature_id = ? AND fc.to_terminal_id IS NOT DISTINCT FROM ? AND fc.from_feature_id <> ? `+
				`AND EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = fc.from_feature_id AND r.deleted_at IS NULL))`,
			ep.ref.FeatureID, ep.terminalID, otherFeatureID,
			ep.ref.FeatureID, ep.terminalID, otherFeatureID,
		).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("connectivity: checking single-connection terminal: %w", err)
	}
	if occupied {
		code := ""
		if ep.terminalCode != nil {
			code = *ep.terminalCode
		}
		return invalid("terminal %s on %s is a single-connection terminal and is already connected to another feature -- a cable joint splices one cable per terminal. Disconnect the existing connection first, or use a different terminal.", code, ep.ref.FeatureClass)
	}
	return nil
}

// listConnections returns every connection involving one feature, on
// either side -- a junction like DIST_XFMR is typically the "to" side of
// several incoming conductors, but nothing stops a feature from
// appearing as "from" elsewhere too.
func listConnections(ctx context.Context, db bun.IDB, featureClassCode, featureID string) ([]connectionResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClassCode)
	if err != nil {
		return nil, err
	}
	fcID := loaded.FeatureClass.ID

	var rows []Connectivity
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			featureID, fcID, featureID, fcID).
		// A soft-deleted feature must not exist anywhere in the system: an edge to
		// a deleted peer is hidden from this list the moment either endpoint is
		// deleted, so a deleted feature stops showing on its former neighbours'
		// Terminals tab (the edge row survives for lossless restore -- see
		// crud.softDeleteFeature -- but it's invisible until purge/cleanup removes
		// it). "Live" == a feature_registry row with deleted_at IS NULL, the same
		// cross-class liveness definition the connectivity audit/cleanup use.
		Where("from_feature_id IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)").
		Where("to_feature_id IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)").
		Order("established_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: list: %w", err)
	}

	return formatConnections(ctx, db, rows)
}

// revokeConnection permanently removes an established connection --
// undoing a mistaken establish, or reflecting a real physical
// disconnection. No soft-delete/restore pair like features have:
// unlike a feature, a connection carries no draft/published life cycle
// of its own to preserve -- it's a binary fact, established or not.
// ListConnectionsForFeature returns every connection touching the given feature,
// in the same shape the REST GET /connectivity?feature_class=&feature_id= handler
// returns -- the exported entry point the shared agent-tool surface calls. It
// returns the rows as `any` (the concrete element type is package-private, but its
// fields are exported so it serializes verbatim) plus the count, so a caller can
// present the list -- including each connection's id, which the revoke tool needs
// -- without this package exposing its response type.
func ListConnectionsForFeature(ctx context.Context, db bun.IDB, featureClassCode, featureID string) (any, int, error) {
	conns, err := listConnections(ctx, db, featureClassCode, featureID)
	if err != nil {
		return nil, 0, err
	}
	return conns, len(conns), nil
}

// RevokeConnection permanently removes an established connection by id. It is the
// exported entry point the shared agent-tool surface (internal/agenttools) calls,
// delegating to the exact path the REST DELETE /connectivity/{id} handler uses so
// both front doors behave identically (and both are gated on connectivity:commit
// by their callers).
func RevokeConnection(ctx context.Context, db bun.IDB, id string) error {
	return revokeConnection(ctx, db, id)
}

func revokeConnection(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*Connectivity)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("connectivity: revoke: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("connectivity: revoke: %w", err)
	}
	if n == 0 {
		return notFound("connection %s not found", id)
	}
	return nil
}

// RepointFeature moves every connectivity endpoint that references
// (classID, oldFeatureID) onto newFeatureID, staying within the same feature
// class -- the row-level half of the diagram engine's "replace this stub with
// the real map feature" swap (api/internal/diagram/replace.go). Only the
// feature id changes; the class and any terminal stay put, so a cable stub's
// MV_SWITCH.T1 link simply re-anchors to the real conductor.
//
// The endpoint quadruple (from_feature_id, from_terminal_id, to_feature_id,
// to_terminal_id) is UNIQUE NULLS NOT DISTINCT, so a rewrite that would
// duplicate an edge the replacement already carries is dropped instead of
// updated -- the transfer stays idempotent and never trips the constraint.
// Returns the number of rows moved (rewrites + dropped duplicates). It is the
// caller's job to run this inside the same transaction as the rest of the swap.
func RepointFeature(ctx context.Context, db bun.IDB, classID, oldFeatureID, newFeatureID string) (int, error) {
	if oldFeatureID == newFeatureID {
		return 0, nil
	}

	var rows []Connectivity
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_class_id = ? AND from_feature_id = ?) OR (to_feature_class_id = ? AND to_feature_id = ?)",
			classID, oldFeatureID, classID, oldFeatureID).
		Scan(ctx); err != nil {
		return 0, fmt.Errorf("connectivity: repoint: %w", err)
	}

	moved := 0
	for i := range rows {
		r := rows[i]
		// The quadruple this row would hold once its stub side(s) point at the
		// replacement -- a row can reference the stub on both sides (a self-loop),
		// so rewrite whichever match.
		fromID, toID := r.FromFeatureID, r.ToFeatureID
		if r.FromFeatureClassID == classID && r.FromFeatureID == oldFeatureID {
			fromID = newFeatureID
		}
		if r.ToFeatureClassID == classID && r.ToFeatureID == oldFeatureID {
			toID = newFeatureID
		}

		exists, err := db.NewSelect().Model((*Connectivity)(nil)).
			Where("id <> ?", r.ID).
			Where("from_feature_id = ?", fromID).
			Where("from_terminal_id IS NOT DISTINCT FROM ?", r.FromTerminalID).
			Where("to_feature_id = ?", toID).
			Where("to_terminal_id IS NOT DISTINCT FROM ?", r.ToTerminalID).
			Exists(ctx)
		if err != nil {
			return moved, fmt.Errorf("connectivity: repoint check: %w", err)
		}
		if exists {
			// The replacement already carries this exact edge -- drop the stub's
			// duplicate rather than collide with the unique constraint.
			if _, err := db.NewDelete().Model((*Connectivity)(nil)).Where("id = ?", r.ID).Exec(ctx); err != nil {
				return moved, fmt.Errorf("connectivity: repoint drop dup: %w", err)
			}
			moved++
			continue
		}

		if _, err := db.NewUpdate().Model((*Connectivity)(nil)).
			Set("from_feature_id = ?", fromID).
			Set("to_feature_id = ?", toID).
			Where("id = ?", r.ID).Exec(ctx); err != nil {
			return moved, fmt.Errorf("connectivity: repoint update: %w", err)
		}
		moved++
	}
	return moved, nil
}

// formatConnection builds the response for a connection just
// established, reusing the already-validated request-side endpointRefs
// verbatim (feature class code, feature id, terminal code) rather than
// re-deriving them from the ids just written.
func formatConnection(conn *Connectivity, ruleName string, from, to *resolvedEndpoint) *connectionResponse {
	return &connectionResponse{
		ID:              conn.ID,
		NetworkRuleID:   conn.NetworkRuleID,
		NetworkRuleName: ruleName,
		From:            from.ref,
		To:              to.ref,
		EstablishedAt:   conn.EstablishedAt,
	}
}

// formatConnections turns raw Connectivity rows (feature-class ids,
// terminal ids, a rule id -- whatever came back from the database) into
// the same human-readable shape formatConnection produces for a fresh
// establish, via a handful of batched lookups rather than one query per
// row per row's referenced feature class/terminal/rule.
func formatConnections(ctx context.Context, db bun.IDB, rows []Connectivity) ([]connectionResponse, error) {
	if len(rows) == 0 {
		return []connectionResponse{}, nil
	}

	fcIDs := map[string]struct{}{}
	terminalIDs := map[string]struct{}{}
	ruleIDs := map[string]struct{}{}
	for _, r := range rows {
		fcIDs[r.FromFeatureClassID] = struct{}{}
		fcIDs[r.ToFeatureClassID] = struct{}{}
		if r.FromTerminalID != nil {
			terminalIDs[*r.FromTerminalID] = struct{}{}
		}
		if r.ToTerminalID != nil {
			terminalIDs[*r.ToTerminalID] = struct{}{}
		}
		ruleIDs[r.NetworkRuleID] = struct{}{}
	}

	fcCodes, err := loadFeatureClassCodes(ctx, db, keys(fcIDs))
	if err != nil {
		return nil, err
	}
	terminalCodes, err := loadTerminalCodes(ctx, db, keys(terminalIDs))
	if err != nil {
		return nil, err
	}
	ruleNames, err := loadRuleNames(ctx, db, keys(ruleIDs))
	if err != nil {
		return nil, err
	}

	out := make([]connectionResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, connectionResponse{
			ID:              r.ID,
			NetworkRuleID:   r.NetworkRuleID,
			NetworkRuleName: ruleNames[r.NetworkRuleID],
			From: endpointRef{
				FeatureClass: fcCodes[r.FromFeatureClassID],
				FeatureID:    r.FromFeatureID,
				Terminal:     terminalCodePtr(r.FromTerminalID, terminalCodes),
			},
			To: endpointRef{
				FeatureClass: fcCodes[r.ToFeatureClassID],
				FeatureID:    r.ToFeatureID,
				Terminal:     terminalCodePtr(r.ToTerminalID, terminalCodes),
			},
			EstablishedAt: r.EstablishedAt,
		})
	}
	return out, nil
}

func terminalCodePtr(id *string, codes map[string]string) *string {
	if id == nil {
		return nil
	}
	code := codes[*id]
	return &code
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func loadFeatureClassCodes(ctx context.Context, db bun.IDB, ids []string) (map[string]string, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}

func loadTerminalCodes(ctx context.Context, db bun.IDB, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	var terminals []metadata.Terminal
	if err := db.NewSelect().Model(&terminals).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: loading terminals: %w", err)
	}
	out := make(map[string]string, len(terminals))
	for _, t := range terminals {
		out[t.ID] = t.Code
	}
	return out, nil
}

func loadRuleNames(ctx context.Context, db bun.IDB, ids []string) (map[string]string, error) {
	var rules []metadata.NetworkRule
	if err := db.NewSelect().Model(&rules).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: loading network rules: %w", err)
	}
	out := make(map[string]string, len(rules))
	for _, r := range rules {
		out[r.ID] = r.Name
	}
	return out, nil
}
