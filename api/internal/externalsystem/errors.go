package externalsystem

import "fmt"

// validationError/notFoundError mirror the same small copies in
// api/internal/relationship and api/internal/externallink -- kept local
// rather than shared for the same reason.
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
