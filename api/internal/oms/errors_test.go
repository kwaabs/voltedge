package oms

import (
	"errors"
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
