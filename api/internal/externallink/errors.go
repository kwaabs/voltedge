package externallink

import "fmt"

// validationError/notFoundError mirror api/internal/relationship's
// identical pattern -- kept as this package's own small copy for the
// same reason: sharing would mean exporting internals across packages
// just for two tiny types.
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

// upstreamError marks a failure that came from the external system the
// fetch proxy called (unreachable, non-2xx, unparseable body) rather than
// from the caller's request. It maps to 502 Bad Gateway so a broken
// downstream system is never misreported as our own 500 or the caller's
// 400.
type upstreamError struct{ msg string }

func (e *upstreamError) Error() string { return e.msg }

func upstream(format string, args ...any) error {
	return &upstreamError{msg: fmt.Sprintf(format, args...)}
}

func isUpstreamError(err error) bool {
	_, ok := err.(*upstreamError)
	return ok
}
