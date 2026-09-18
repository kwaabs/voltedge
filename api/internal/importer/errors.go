package importer

import (
	"errors"
	"fmt"
)

// This package's own client-fixable error types, mapped to HTTP status by the
// http.go writeError helper. They mirror admin/errors.go and tie's InvalidError:
// a validation problem the caller can fix is a 400, a missing profile is a 404,
// and anything else stays a 500. crud's own validation/not-found errors (raised
// deep in ValidateFeature) are mapped alongside these.

// validationError is a client-fixable request problem (a bad transform value, a
// non-numeric cell for a scale transform, a sample that is too large).
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

// invalidf builds a validationError with a formatted message.
func invalidf(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) this package's validationError.
func IsInvalid(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}

// notFoundError is a referenced row that does not exist (an unknown profile
// code).
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

// notFoundf builds a notFoundError with a formatted message.
func notFoundf(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

// IsNotFound reports whether err is (or wraps) this package's notFoundError.
func IsNotFound(err error) bool {
	var n *notFoundError
	return errors.As(err, &n)
}
