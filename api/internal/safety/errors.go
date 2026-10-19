package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// InvalidError is a client-fixable problem (an unknown tag_type, a missing ref, an
// illegal clearance transition) -- distinct from a plain error so the HTTP layer
// maps it to 400 not 500, the same split faults/oms/ops/wam draw. Test with IsInvalid.
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else to 500.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if IsInvalid(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
