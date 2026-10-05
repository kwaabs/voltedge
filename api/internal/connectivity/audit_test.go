package connectivity

import "testing"

// TestOrphanMessage covers the human phrasing of an orphaned-connectivity
// finding: which endpoint(s) are unavailable, and whether each is missing
// (hard-purged) or soft-deleted. The SQL only ever surfaces a row when at
// least one side is bad, so the all-good case is not exercised.
func TestOrphanMessage(t *testing.T) {
	cases := []struct {
		name                                           string
		subject                                        string
		fromMissing, fromDeleted, toMissing, toDeleted bool
		want                                           string
	}{
		{
			name:        "from missing",
			subject:     "connection",
			fromMissing: true,
			want:        "connection references an unavailable feature: POLE p1 no longer exists",
		},
		{
			name:        "from soft-deleted",
			subject:     "connection",
			fromDeleted: true,
			want:        "connection references an unavailable feature: POLE p1 is soft-deleted",
		},
		{
			name:      "to missing (from ok)",
			subject:   "connection",
			toMissing: true,
			want:      "connection references an unavailable feature: MV_CONDUCTOR c1 no longer exists",
		},
		{
			name:        "both unavailable",
			subject:     "connection",
			fromDeleted: true,
			toMissing:   true,
			want:        "connection references two unavailable features: POLE p1 is soft-deleted; MV_CONDUCTOR c1 no longer exists",
		},
		{
			name:        "missing takes priority over deleted on the same side",
			subject:     "connection",
			fromMissing: true,
			fromDeleted: true,
			want:        "connection references an unavailable feature: POLE p1 no longer exists",
		},
		{
			name:        "relationship subject",
			subject:     "relationship",
			toMissing:   true,
			want:        "relationship references an unavailable feature: MV_CONDUCTOR c1 no longer exists",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := orphanMessage(c.subject, "POLE", "p1", c.fromMissing, c.fromDeleted, "MV_CONDUCTOR", "c1", c.toMissing, c.toDeleted)
			if got != c.want {
				t.Fatalf("orphanMessage = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSortFindings pins the report ordering: errors before warnings, then by
// kind, then by the from-feature's class and id -- so a report reads worst-first
// and is stable run to run.
func TestSortFindings(t *testing.T) {
	findings := []AuditFinding{
		{Kind: "coincident_unconnected", Severity: "warning", FromClass: "MV_CONDUCTOR", FromID: "b"},
		{Kind: "orphaned_connectivity", Severity: "error", FromClass: "POLE", FromID: "z"},
		{Kind: "orphaned_connectivity", Severity: "error", FromClass: "POLE", FromID: "a"},
		{Kind: "coincident_unconnected", Severity: "warning", FromClass: "LV_CONDUCTOR", FromID: "a"},
	}
	sortFindings(findings)

	type key struct{ sev, class, id string }
	want := []key{
		{"error", "POLE", "a"},   // errors first, then id a < z
		{"error", "POLE", "z"},
		{"warning", "LV_CONDUCTOR", "a"}, // then warnings, LV < MV by class
		{"warning", "MV_CONDUCTOR", "b"},
	}
	if len(findings) != len(want) {
		t.Fatalf("len = %d, want %d", len(findings), len(want))
	}
	for i, w := range want {
		got := key{findings[i].Severity, findings[i].FromClass, findings[i].FromID}
		if got != w {
			t.Fatalf("findings[%d] = %+v, want %+v", i, got, w)
		}
	}
}

// TestUndirectedKey confirms the pair key is order-independent, so the
// coincidence-scan dedupe and the per-pair snapping-tolerance lookup agree on
// (A,B) == (B,A).
func TestUndirectedKey(t *testing.T) {
	if undirectedKey("a", "b") != undirectedKey("b", "a") {
		t.Fatal("undirectedKey must be order-independent")
	}
	if got := undirectedKey("b", "a"); got != "a|b" {
		t.Fatalf("undirectedKey(b, a) = %q, want a|b", got)
	}
}

// TestClassCode resolves a class id to its code, falling back to the raw id for
// a class not in the compiled catalog (e.g. a dropped class a stale connectivity
// row still references).
func TestClassCode(t *testing.T) {
	byID := map[string]auditClass{
		"id-1": {id: "id-1", code: "POLE"},
	}
	if got := classCode(byID, "id-1"); got != "POLE" {
		t.Fatalf("classCode(known) = %q, want POLE", got)
	}
	if got := classCode(byID, "id-unknown"); got != "id-unknown" {
		t.Fatalf("classCode(unknown) = %q, want id-unknown (raw id fallback)", got)
	}
}
