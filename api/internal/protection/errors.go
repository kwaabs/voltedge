package protection

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error { return &validationError{msg: fmt.Sprintf(format, args...)} }

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFound(format string, args ...any) error { return &notFoundError{msg: fmt.Sprintf(format, args...)} }

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	var ve *validationError
	var nfe *notFoundError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, apiError{Error: ve.msg})
	case errors.As(err, &nfe):
		writeJSON(w, http.StatusNotFound, apiError{Error: nfe.msg})
	default:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
	}
}
