package relationship

import "fmt"

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

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFound(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

func isNotFoundError(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}
