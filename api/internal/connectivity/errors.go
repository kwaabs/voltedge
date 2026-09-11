package connectivity

import (
	"errors"
	"fmt"
)

// validationError/notFoundError mirror api/internal/crud's pattern --
// kept as this package's own small copy rather than an import, since
// sharing them would mean exporting crud's internals just for two tiny
// types. Distinct types (not a plain error) so writeError never has to
// guess a status code from a message string.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

func isValidationError(err error) bool {
	_, ok := err.(*validationError)
	return ok
}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFound(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

func isNotFoundError(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

// IsValidationError / IsNotFoundError are the EXPORTED counterparts of the two
// checkers above, for composite callers that commit connections through the
// exported EstablishConnection and map their own HTTP status (e.g. the coupler
// placement endpoint, api/internal/coupler, whose two bus endpoints are
// user-picked -- so a "no authorizing rule" / "bad terminal" / "phase mismatch"
// rejection is a client-fixable 400, and a missing bus feature a 404). Unlike
// the unexported pair they use errors.As, so a wrapped error (fmt.Errorf("...:
// %w", err)) still matches -- the composite wraps for context.
func IsValidationError(err error) bool {
	var ve *validationError
	return errors.As(err, &ve)
}

func IsNotFoundError(err error) bool {
	var nfe *notFoundError
	return errors.As(err, &nfe)
}
