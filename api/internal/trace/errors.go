package trace

import "fmt"

// validationError/notFoundError mirror api/internal/connectivity's own
// copy of this pattern (itself mirroring api/internal/crud) -- kept as
// this package's own small copy rather than a shared import, same
// reasoning as connectivity's: sharing two tiny types isn't worth
// exporting either package's internals for.
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

// IsClientError reports whether err is a client-fixable trace error (an unknown
// or soft-deleted feature, an unknown class or configuration). Other packages
// that call into the trace walks (api/internal/oms) use it to answer a bad id
// with a 400 instead of letting the error surface as a 500.
func IsClientError(err error) bool {
	return isNotFoundError(err) || isValidationError(err)
}
