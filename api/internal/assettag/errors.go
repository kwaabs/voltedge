package assettag

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// InvalidError is a client-fixable problem (a malformed code, a legacy code that
// collides with an existing one, an operation on the wrong feature) -- distinct
// from a plain error so the HTTP layer maps it to 400 rather than 500, the same
// split oms/wam/faults draw. Callers test with IsInvalid.
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

// NotFoundError is "no such tag" (or a tag that resolves to a feature the caller
// may not see -- hidden as not-found so a code's existence never leaks). Mapped
// to 404. Callers test with IsNotFound.
type NotFoundError struct{ msg string }

func (e *NotFoundError) Error() string { return e.msg }

func notFoundf(format string, args ...any) error {
	return &NotFoundError{msg: fmt.Sprintf(format, args...)}
}

// IsNotFound reports whether err is (or wraps) a NotFoundError.
func IsNotFound(err error) bool {
	var ne *NotFoundError
	return errors.As(err, &ne)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and a NotFoundError to
// 404; everything else is a 500.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case IsInvalid(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case IsNotFound(err):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}
