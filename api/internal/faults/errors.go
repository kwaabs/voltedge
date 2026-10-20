package faults

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// InvalidError is a client-fixable problem (a fault with no anchor, an unknown
// incident, an illegal status transition) -- distinct from a plain error so the
// HTTP layer can map it to a 400 rather than a 500, the same split
// api/internal/oms and api/internal/wam draw. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// ForbiddenError marks an authorization failure (the caller lacks a permission an
// in-handler escalation requires, e.g. metadata:write to edit the fault-mode
// catalogue) -- mapped to 403 by writeError.
type ForbiddenError struct{ msg string }

func (e *ForbiddenError) Error() string { return e.msg }

func forbiddenf(format string, args ...any) error {
	return &ForbiddenError{msg: fmt.Sprintf(format, args...)}
}

// IsForbidden reports whether err is (or wraps) a ForbiddenError.
func IsForbidden(err error) bool {
	var fe *ForbiddenError
	return errors.As(err, &fe)
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else to
// 500 -- the same split oms and wam draw.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case IsForbidden(err):
		status = http.StatusForbidden
	case IsInvalid(err):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
