package relationship

import (
	"errors"
	"fmt"
)

// validationError/notFoundError mirror api/internal/crud and
// api/internal/connectivity's identical pattern -- kept as this
// package's own small copy for the same reason connectivity's is:
// sharing would mean exporting internals across three packages just for
// two tiny types.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

func isValidationError(err error) bool {
	_, ok := err.(*validationError)
	return ok
}

// IsValidationError lets another package (api/internal/panel's composite create)
// map a relationship validation failure -- no matching relationship class for the
// pairing, or a cardinality already exhausted -- to a 400 without exporting the
// error-type hierarchy. errors.As so a %w-wrapped error still classifies.
func IsValidationError(err error) bool {
	var ve *validationError
	return errors.As(err, &ve)
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
