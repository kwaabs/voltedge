package alarms

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// InvalidError is a client-fixable problem (a bad status/severity filter) -- mapped
// to 400 by writeError; NotFoundError -> 404. Same split faults/oms/wam/deviceops use.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error { return &InvalidError{msg: fmt.Sprintf(format, args...)} }

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// NotFoundError marks an unknown id -> 404.
type NotFoundError struct{ msg string }

func (e *NotFoundError) Error() string { return e.msg }

func notFound(format string, args ...any) error { return &NotFoundError{msg: fmt.Sprintf(format, args...)} }

// IsNotFound reports whether err is (or wraps) a NotFoundError.
func IsNotFound(err error) bool {
	var ne *NotFoundError
	return errors.As(err, &ne)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case IsInvalid(err):
		status = http.StatusBadRequest
	case IsNotFound(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// nullUUID returns nil for an empty id so it writes SQL NULL into a uuid column.
func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
