package connectivity

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/uptrace/bun"
)

// audit.go is the read-only connectivity consistency audit -- the first slice
// of the "model trust" work. It never writes: it compares the logical
// connectivity graph (runtime.feature_connectivity) against the live feature
// rows and against geometry, and reports divergences as findings a human can
// act on. The logical connectivity model stays the single source of truth;
// geometry is only ever an advisory signal here, never authoritative.
//
// Two checks in this first version:
//
//   - orphaned_connectivity (ERROR): a connectivity row pointing at a feature
//     that is soft-deleted or no longer exists. These are dangling facts -- the
//     connection references something that isn't there.
//
//   - coincident_unconnected (WARNING): two features whose geometries are
//     coincident within a tolerance, for a class pair that COULD legally
//     connect, but which have no connectivity row. This is the dangerous
//     "looks connected on the map, isn't connected in the model" case -- such a
//     link is silently absent from traces and load flow.
//
// Known limits (also returned in the report so the caller sees them):
//   - Terminals carry no geometry (a junction is one point; terminals are
//     logical), so coincidence is asserted feature-to-feature, never
//     terminal-to-endpoint -- the audit cannot say which terminal a missing
//     connection should use.
//   - Coincidence uses whole-feature geometry, so a conductor running close to
//     an unrelated device can surface a reviewable (not definitive) finding.
//   - Revoked connections are hard-deleted (no soft-delete on the table), so
//     there is no revoked-row history to audit.
//
// Deliberately NOT here yet (clean follow-ups): checking stored rows against
// *current* network_rules (needs a per-row resolve + matchRuleEitherDirection),
// dangling required terminals, and per-class-pair tolerance from snapping_rules.

const (
	// auditDefaultToleranceMeters is the coincidence distance used when the
	// caller gives none. Kept tight on purpose: at ~1 m, a point essentially
	// sitting on a line (a transformer at a conductor end) flags, while routine
	// near-misses do not. Looser values trade precision for recall.
	auditDefaultToleranceMeters = 1.0
	// auditDefaultMaxFindings caps how many findings one run returns, so a
	// pathological dataset can't produce an unbounded response; the summary
	// still counts everything found and sets Truncated.
	auditDefaultMaxFindings = 500
	auditMaxMaxFindings     = 5000
)

// AuditInput bounds a run. Zero values fall back to the defaults above.
type AuditInput struct {
	// ToleranceMeters is the coincidence distance for the geometric check.
	ToleranceMeters float64
	// MaxFindings caps the findings list (not the summary counts).
	MaxFindings int
}

// AuditFinding is one reported divergence.
type AuditFinding struct {
	Kind         string `json:"kind"`     // "orphaned_connectivity" | "coincident_unconnected"
	Severity     string `json:"severity"` // "error" | "warning"
	Message      string `json:"message"`
	ConnectionID string `json:"connection_id,omitempty"` // set for orphaned_connectivity
	FromClass    string `json:"from_class,omitempty"`
	FromID       string `json:"from_id,omitempty"`
	ToClass      string `json:"to_class,omitempty"`
	ToID         string `json:"to_id,omitempty"`
	// FromStatus/ToStatus mark an endpoint that can't be located because it is
	// gone: "missing" (purged / never existed) or "deleted" (soft-deleted).
	// Empty means the endpoint is live and locatable. Set only on orphan
	// findings; a coincident finding's two features are both live.
	FromStatus     string   `json:"from_status,omitempty"`
	ToStatus       string   `json:"to_status,omitempty"`
	DistanceMeters *float64 `json:"distance_meters,omitempty"` // set for coincident_unconnected
}

// AuditSummary is the counts behind the findings list (which may be truncated).
type AuditSummary struct {
	ConnectionsTotal      int     `json:"connections_total"`
	OrphanedConnectivity  int     `json:"orphaned_connectivity"`
	CoincidentUnconnected int     `json:"coincident_unconnected"`
	RelationshipsTotal    int     `json:"relationships_total"`
	OrphanedRelationships int     `json:"orphaned_relationships"`
	ClassPairsScanned     int     `json:"class_pairs_scanned"`
	ToleranceMeters       float64 `json:"tolerance_meters"`
	Truncated             bool    `json:"truncated"`
}

// AuditReport is the whole read-only result.
type AuditReport struct {
	Summary     AuditSummary   `json:"summary"`
	Findings    []AuditFinding `json:"findings"`
	Limitations []string       `json:"limitations"`
}

// auditClass is one compiled feature class in the catalog used by the audit.
type auditClass struct {
	id            string
	code          string
	physicalTable string
	geometryType  string
}

// AuditConnectivity runs the read-only consistency audit. It writes nothing.
func AuditConnectivity(ctx context.Context, db bun.IDB, in AuditInput) (*AuditReport, error) {
	tol := in.ToleranceMeters
	if tol <= 0 {
		tol = auditDefaultToleranceMeters
	}
	max := in.MaxFindings
	if max <= 0 {
		max = auditDefaultMaxFindings
	}
	if max > auditMaxMaxFindings {
		max = auditMaxMaxFindings
	}

	report := &AuditReport{
		Findings: []AuditFinding{},
		Limitations: []string{
			"Terminals are non-positional, so geometric coincidence is reported feature-to-feature; the specific terminal a missing connection should use is not asserted.",
			"Coincidence uses whole-feature geometry within the tolerance, so a conductor passing close to an unrelated device can surface a reviewable (not definitive) finding.",
			"Coincidence tolerance is per class pair where a snapping rule authors one (snapping_rules.tolerance_m); pairs without a rule use the default tolerance shown.",
			"Revoked connections are hard-deleted and leave no trace, so they are not audited.",
		},
	}
	report.Summary.ToleranceMeters = tol

	total, err := db.NewSelect().Model((*Connectivity)(nil)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectivity: audit: counting connections: %w", err)
	}
	report.Summary.ConnectionsTotal = total

	relTotal, err := db.NewSelect().Table("runtime.feature_relationships").Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectivity: audit: counting relationships: %w", err)
	}
	report.Summary.RelationshipsTotal = relTotal

	byID, err := loadAuditClasses(ctx, db)
	if err != nil {
		return nil, err
	}

	if err := auditOrphans(ctx, db, byID, report, max); err != nil {
		return nil, err
	}
	if err := auditCoincidence(ctx, db, byID, tol, report, max); err != nil {
		return nil, err
	}
	if err := auditRelationshipOrphans(ctx, db, byID, report, max); err != nil {
		return nil, err
	}

	sortFindings(report.Findings)
	return report, nil
}

// loadAuditClasses returns every compiled feature class keyed by its metadata
// id, with the code, physical table and geometry type the checks need.
func loadAuditClasses(ctx context.Context, db bun.IDB) (map[string]auditClass, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, code, physical_table, geometry_type
		FROM metadata.feature_classes
		WHERE physical_table IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("connectivity: audit: loading class catalog: %w", err)
	}
	defer rows.Close()

	byID := map[string]auditClass{}
	for rows.Next() {
		var c auditClass
		var table sql.NullString
		if err := rows.Scan(&c.id, &c.code, &table, &c.geometryType); err != nil {
			return nil, fmt.Errorf("connectivity: audit: scanning class catalog: %w", err)
		}
		if !table.Valid || table.String == "" {
			continue
		}
		c.physicalTable = table.String
		byID[c.id] = c
	}
	return byID, rows.Err()
}

// auditOrphans flags connectivity rows whose from- or to-feature is missing or
// soft-deleted, resolved via the cross-class runtime.feature_registry so we
// never have to know each polymorphic target table.
func auditOrphans(ctx context.Context, db bun.IDB, byID map[string]auditClass, report *AuditReport, max int) error {
	rows, err := db.QueryContext(ctx, `
		SELECT c.id::text,
		       c.from_feature_class_id::text, c.from_feature_id::text,
		       c.to_feature_class_id::text,   c.to_feature_id::text,
		       (rf.feature_id IS NULL)       AS from_missing,
		       (rf.deleted_at IS NOT NULL)   AS from_deleted,
		       (rt.feature_id IS NULL)       AS to_missing,
		       (rt.deleted_at IS NOT NULL)   AS to_deleted
		FROM runtime.feature_connectivity c
		LEFT JOIN runtime.feature_registry rf ON rf.feature_id = c.from_feature_id
		LEFT JOIN runtime.feature_registry rt ON rt.feature_id = c.to_feature_id
		WHERE rf.feature_id IS NULL OR rf.deleted_at IS NOT NULL
		   OR rt.feature_id IS NULL OR rt.deleted_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("connectivity: audit: orphan scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, fromClassID, fromID, toClassID, toID       string
			fromMissing, fromDeleted, toMissing, toDeleted bool
		)
		if err := rows.Scan(&id, &fromClassID, &fromID, &toClassID, &toID,
			&fromMissing, &fromDeleted, &toMissing, &toDeleted); err != nil {
			return fmt.Errorf("connectivity: audit: scanning orphan: %w", err)
		}
		report.Summary.OrphanedConnectivity++
		if len(report.Findings) >= max {
			report.Summary.Truncated = true
			continue
		}
		// A reference to a MISSING (purged / never-existed) feature is a hard
		// error -- it can never resolve. A reference to a merely SOFT-DELETED
		// feature is a warning: it's recoverable (the feature can be restored,
		// re-validating the connection) and analysis already ignores deleted
		// endpoints, so it's dead weight, not a wrong answer.
		severity := "warning"
		if fromMissing || toMissing {
			severity = "error"
		}
		fromCode := classCode(byID, fromClassID)
		toCode := classCode(byID, toClassID)
		report.Findings = append(report.Findings, AuditFinding{
			Kind:         "orphaned_connectivity",
			Severity:     severity,
			Message:      orphanMessage("connection", fromCode, fromID, fromMissing, fromDeleted, toCode, toID, toMissing, toDeleted),
			ConnectionID: id,
			FromClass:    fromCode,
			FromID:       fromID,
			ToClass:      toCode,
			ToID:         toID,
			FromStatus:   endpointStatus(fromMissing, fromDeleted),
			ToStatus:     endpointStatus(toMissing, toDeleted),
		})
	}
	return rows.Err()
}

// endpointStatus names why an endpoint is unavailable -- "missing" (gone) or
// "deleted" (soft-deleted) -- or "" when it is live. Missing wins when both are
// set, matching orphanMessage's priority.
func endpointStatus(missing, deleted bool) string {
	switch {
	case missing:
		return "missing"
	case deleted:
		return "deleted"
	default:
		return ""
	}
}

// orphanMessage phrases an orphaned-reference finding. subject is the kind of
// reference ("connection" or "relationship") so the one helper serves both the
// connectivity and relationship orphan scans.
func orphanMessage(subject, fromCode, fromID string, fromMissing, fromDeleted bool, toCode, toID string, toMissing, toDeleted bool) string {
	state := func(code, id string, missing, deleted bool) string {
		switch {
		case missing:
			return fmt.Sprintf("%s %s no longer exists", code, id)
		case deleted:
			return fmt.Sprintf("%s %s is soft-deleted", code, id)
		default:
			return ""
		}
	}
	from := state(fromCode, fromID, fromMissing, fromDeleted)
	to := state(toCode, toID, toMissing, toDeleted)
	switch {
	case from != "" && to != "":
		return subject + " references two unavailable features: " + from + "; " + to
	case from != "":
		return subject + " references an unavailable feature: " + from
	default:
		return subject + " references an unavailable feature: " + to
	}
}

// auditRelationshipOrphans flags relationship rows whose origin- or
// destination-feature is missing or soft-deleted -- the relationship-graph
// counterpart of auditOrphans (runtime.feature_relationships parallels
// runtime.feature_connectivity). Same registry-based resolution and
// severity rule: a missing endpoint is an error, a soft-deleted one a
// recoverable warning. Relationships have no geometry, so there is no
// coincidence counterpart.
func auditRelationshipOrphans(ctx context.Context, db bun.IDB, byID map[string]auditClass, report *AuditReport, max int) error {
	rows, err := db.QueryContext(ctx, `
		SELECT r.id::text,
		       r.origin_feature_class_id::text,      r.origin_feature_id::text,
		       r.destination_feature_class_id::text, r.destination_feature_id::text,
		       (ro.feature_id IS NULL)     AS origin_missing,
		       (ro.deleted_at IS NOT NULL) AS origin_deleted,
		       (rd.feature_id IS NULL)     AS dest_missing,
		       (rd.deleted_at IS NOT NULL) AS dest_deleted
		FROM runtime.feature_relationships r
		LEFT JOIN runtime.feature_registry ro ON ro.feature_id = r.origin_feature_id
		LEFT JOIN runtime.feature_registry rd ON rd.feature_id = r.destination_feature_id
		WHERE ro.feature_id IS NULL OR ro.deleted_at IS NOT NULL
		   OR rd.feature_id IS NULL OR rd.deleted_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("connectivity: audit: relationship orphan scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, originClassID, originID, destClassID, destID       string
			originMissing, originDeleted, destMissing, destDeleted bool
		)
		if err := rows.Scan(&id, &originClassID, &originID, &destClassID, &destID,
			&originMissing, &originDeleted, &destMissing, &destDeleted); err != nil {
			return fmt.Errorf("connectivity: audit: scanning relationship orphan: %w", err)
		}
		report.Summary.OrphanedRelationships++
		if len(report.Findings) >= max {
			report.Summary.Truncated = true
			continue
		}
		severity := "warning"
		if originMissing || destMissing {
			severity = "error"
		}
		originCode := classCode(byID, originClassID)
		destCode := classCode(byID, destClassID)
		report.Findings = append(report.Findings, AuditFinding{
			Kind:         "orphaned_relationship",
			Severity:     severity,
			Message:      orphanMessage("relationship", originCode, originID, originMissing, originDeleted, destCode, destID, destMissing, destDeleted),
			ConnectionID: id,
			FromClass:    originCode,
			FromID:       originID,
			ToClass:      destCode,
			ToID:         destID,
			FromStatus:   endpointStatus(originMissing, originDeleted),
			ToStatus:     endpointStatus(destMissing, destDeleted),
		})
	}
	return rows.Err()
}

// auditCoincidence flags feature pairs that are geometrically coincident within
// the tolerance, for a class pair that a valid network rule permits, but which
// have no connectivity row. Only connectable, compiled, geometric class pairs
// are scanned -- so a mid-span near-miss between unrelated classes is never
// considered.
func auditCoincidence(ctx context.Context, db bun.IDB, byID map[string]auditClass, defaultTol float64, report *AuditReport, max int) error {
	pairs, err := loadConnectableClassPairs(ctx, db)
	if err != nil {
		return err
	}
	// The metadata bridge (Component C): a class pair that has an authored
	// snapping rule uses that rule's tolerance_m as its coincidence distance;
	// everything else uses the run's default. So an admin tunes how close counts
	// as "coincident" per class pair just by authoring a snapping rule.
	tolByPair, err := loadSnappingTolerances(ctx, db)
	if err != nil {
		return err
	}

	seen := map[string]bool{}
	for _, p := range pairs {
		a, okA := byID[p[0]]
		b, okB := byID[p[1]]
		if !okA || !okB {
			continue
		}
		// Geometry is required on both sides; a geometry_type='none' class has
		// no geom column at all, so skip it rather than error on the query.
		if a.geometryType == "none" || b.geometryType == "none" {
			continue
		}
		// Undirected: scan each unordered pair once.
		key := undirectedKey(a.id, b.id)
		if seen[key] {
			continue
		}
		seen[key] = true
		report.Summary.ClassPairsScanned++

		tol := defaultTol
		if t, ok := tolByPair[key]; ok && t > 0 {
			tol = t
		}
		if err := scanCoincidentPair(ctx, db, a, b, tol, report, max); err != nil {
			return err
		}
	}
	return nil
}

// undirectedKey is the stable key for an unordered class pair -- the two ids
// sorted and joined -- so (A,B) and (B,A) map to the same bucket. Shared by the
// coincidence scan's dedupe and the snapping-tolerance lookup so both agree.
func undirectedKey(a, b string) string {
	if a <= b {
		return a + "|" + b
	}
	return b + "|" + a
}

// loadSnappingTolerances returns the authored coincidence tolerance per
// undirected class pair, from metadata.snapping_rules.tolerance_m. A pair may
// carry several rules (subtype/terminal-scoped); the audit works at class
// granularity, so it takes the largest tolerance for the pair (most inclusive).
// Keyed to match undirectedKey.
func loadSnappingTolerances(ctx context.Context, db bun.IDB) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT from_feature_class_id::text, to_feature_class_id::text, MAX(tolerance_m)
		FROM metadata.snapping_rules
		GROUP BY from_feature_class_id, to_feature_class_id`)
	if err != nil {
		return nil, fmt.Errorf("connectivity: audit: loading snapping tolerances: %w", err)
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var from, to string
		var tol float64
		if err := rows.Scan(&from, &to, &tol); err != nil {
			return nil, fmt.Errorf("connectivity: audit: scanning snapping tolerance: %w", err)
		}
		key := undirectedKey(from, to)
		if existing, ok := out[key]; !ok || tol > existing {
			out[key] = tol
		}
	}
	return out, rows.Err()
}

// loadConnectableClassPairs returns the distinct (from, to) class-id pairs that
// at least one valid network rule permits -- the only pairs worth auditing for
// a missing connection.
func loadConnectableClassPairs(ctx context.Context, db bun.IDB) ([][2]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT from_feature_class_id::text, to_feature_class_id::text
		FROM metadata.network_rules
		WHERE is_valid = true`)
	if err != nil {
		return nil, fmt.Errorf("connectivity: audit: loading connectable pairs: %w", err)
	}
	defer rows.Close()

	var out [][2]string
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("connectivity: audit: scanning pair: %w", err)
		}
		out = append(out, [2]string{from, to})
	}
	return out, rows.Err()
}

// scanCoincidentPair runs the geometric-coincidence query for one class pair.
// Table names come from trusted metadata (physical_table), interpolated the
// same way suggest.go does; the tolerance and limit are bound parameters.
func scanCoincidentPair(ctx context.Context, db bun.IDB, a, b auditClass, tol float64, report *AuditReport, max int) error {
	// Distinct-pair guard: a different id for a cross-class pair, a.id < b.id
	// for a self-pair so a pair isn't reported twice (A-B and B-A).
	distinct := "a.id <> b.id"
	if a.id == b.id {
		distinct = "a.id < b.id"
	}
	query := fmt.Sprintf(`
		SELECT a.id::text, b.id::text,
		       ST_Distance(ST_Transform(a.geom, 4326)::geography, ST_Transform(b.geom, 4326)::geography) AS dist
		FROM %s a
		JOIN %s b ON %s
		 AND ST_DWithin(ST_Transform(a.geom, 4326)::geography, ST_Transform(b.geom, 4326)::geography, ?)
		WHERE a.deleted_at IS NULL AND b.deleted_at IS NULL
		  AND a.geom IS NOT NULL AND b.geom IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM runtime.feature_connectivity c
		    WHERE (c.from_feature_id = a.id AND c.to_feature_id = b.id)
		       OR (c.from_feature_id = b.id AND c.to_feature_id = a.id)
		  )
		LIMIT ?`, a.physicalTable, b.physicalTable, distinct)

	rows, err := db.QueryContext(ctx, query, tol, max)
	if err != nil {
		return fmt.Errorf("connectivity: audit: coincidence scan %s<->%s: %w", a.code, b.code, err)
	}
	defer rows.Close()

	for rows.Next() {
		var aID, bID string
		var dist sql.NullFloat64
		if err := rows.Scan(&aID, &bID, &dist); err != nil {
			return fmt.Errorf("connectivity: audit: scanning coincidence: %w", err)
		}
		report.Summary.CoincidentUnconnected++
		if len(report.Findings) >= max {
			report.Summary.Truncated = true
			continue
		}
		f := AuditFinding{
			Kind:      "coincident_unconnected",
			Severity:  "warning",
			Message:   fmt.Sprintf("%s %s and %s %s are geometrically coincident (within %.2f m) but are not connected -- the map suggests a connection the model does not have", a.code, aID, b.code, bID, tol),
			FromClass: a.code,
			FromID:    aID,
			ToClass:   b.code,
			ToID:      bID,
		}
		if dist.Valid {
			d := dist.Float64
			f.DistanceMeters = &d
		}
		report.Findings = append(report.Findings, f)
	}
	return rows.Err()
}

// classCode resolves a class id to its code for messages, falling back to the
// raw id when the class isn't in the compiled catalog (e.g. a dropped class a
// stale connectivity row still points at).
func classCode(byID map[string]auditClass, id string) string {
	if c, ok := byID[id]; ok {
		return c.code
	}
	return id
}

// sortFindings orders errors before warnings, then by kind, then by the
// features involved, for a stable, readable report.
func sortFindings(f []AuditFinding) {
	severityRank := func(s string) int {
		if s == "error" {
			return 0
		}
		return 1
	}
	sort.SliceStable(f, func(i, j int) bool {
		if ri, rj := severityRank(f[i].Severity), severityRank(f[j].Severity); ri != rj {
			return ri < rj
		}
		if f[i].Kind != f[j].Kind {
			return f[i].Kind < f[j].Kind
		}
		if f[i].FromClass != f[j].FromClass {
			return f[i].FromClass < f[j].FromClass
		}
		return f[i].FromID < f[j].FromID
	})
}
