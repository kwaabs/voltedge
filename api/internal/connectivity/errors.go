package connectivity

import "fmt"

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
