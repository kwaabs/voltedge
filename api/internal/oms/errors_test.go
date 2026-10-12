package oms

import (
	"errors"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A trace error that is the caller's fault (an unknown service point) must
// surface as a 400, not a 500. trace.IsClientError is exercised against the
// real trace error types through the exported helper in the trace package.
func TestMapTraceErrorPassesThroughOtherErrors(t *testing.T) {
	boom := errors.New("database is down")
	if got := mapTraceError("service point x", boom); got != boom {
		t.Fatalf("a non-client error must pass through unchanged, got %v", got)
	}
}

func TestWriteErrorStatusSplit(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, invalidf("service point %s: feature not found", "abc"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "feature not found") {
		t.Fatalf("invalid error => %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeError(rec, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("plain error => %d", rec.Code)
	}
}

func TestAttributeNeedsDevice(t *testing.T) {
	_, err := AttributeIncidentDevice(nil, nil, "inc", " ", "")
	if !IsInvalid(err) {
		t.Fatalf("a missing device must be an InvalidError, got %v", err)
	}
}

func ref(class, id string) trace.FeatureRef {
	return trace.FeatureRef{FeatureClass: class, FeatureID: id}
}

func path(refs ...trace.FeatureRef) []trace.FeatureRef { return refs }

func call(id, sp string) CallLocation { return CallLocation{CallID: id, ServicePointID: sp} }

func devices(gs []*cluster) []string {
	var out []string
	for _, g := range gs {
		if g.device == nil {
			out = append(out, "none")
		} else {
			out = append(out, g.device.FeatureID)
		}
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The Feeder 7 outage: REC-7 is locked out, so the calls from SP-220 (below the
// fused FSW-12) and SP-230 (below the fused RMU tee way WT) both arrive. Each
// call's nearest protective device is a fuse; two fused devices sharing one
// upstream device means that device operated.
func TestFusedGroupsEscalateToTheSharedUpstreamDevice(t *testing.T) {
	rec := ref("MV_RECLOSER", "REC7")
	cb := ref("MV_BREAKER", "CB7")
	items := []callPath{
		{call("c1", "SP220"), path(ref("MV_SWITCH", "FSW12"), rec, cb)},
		{call("c2", "SP230"), path(ref("MV_SWITCH", "WT"), rec, cb)},
	}
	got := clusterCalls(items)
	if !same(devices(got), []string{"REC7"}) {
		t.Fatalf("devices = %v, want the two fused groups merged into REC7", devices(got))
	}
	if len(got[0].callIDs) != 2 || len(got[0].spOrder) != 2 {
		t.Fatalf("merged cluster = %+v", got[0])
	}
}

// One blown fuse: a single call under one fused device keeps the blame on the fuse.
func TestASingleFusedGroupKeepsTheBlame(t *testing.T) {
	items := []callPath{{call("c1", "SP220"), path(ref("MV_SWITCH", "FSW12"), ref("MV_RECLOSER", "REC7"))}}
	if got := clusterCalls(items); !same(devices(got), []string{"FSW12"}) {
		t.Fatalf("devices = %v, want FSW12", devices(got))
	}
}

// Calls from a fused device and from the device directly above it fold together.
func TestFusedGroupMergesWithADirectGroupOnItsAncestor(t *testing.T) {
	rec := ref("MV_RECLOSER", "REC7")
	items := []callPath{
		{call("c1", "SP220"), path(ref("MV_SWITCH", "FSW12"), rec)},
		{call("c2", "SPX"), path(rec)}, // a customer between REC-7 and the tee
	}
	got := clusterCalls(items)
	if !same(devices(got), []string{"REC7"}) || len(got[0].callIDs) != 2 {
		t.Fatalf("devices = %v clusters=%d", devices(got), len(got))
	}
}

// Non-fused behaviour is unchanged: nearest device per call, no-device cluster kept.
func TestNonFusedClusteringIsUnchanged(t *testing.T) {
	items := []callPath{
		{call("c1", "SP8"), path(ref("MV_BREAKER", "CB8"))},
		{call("c2", "SP9"), path(ref("MV_BREAKER", "CB9"))},
		{call("c3", "SP-nowhere"), nil},
		{call("c4", "SP8b"), path(ref("MV_BREAKER", "CB8"))},
	}
	got := clusterCalls(items)
	if !same(devices(got), []string{"CB8", "CB9", "none"}) {
		t.Fatalf("devices = %v", devices(got))
	}
	if len(got[0].callIDs) != 2 {
		t.Fatalf("CB8 should hold two calls: %+v", got[0])
	}
}
