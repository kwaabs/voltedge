package servicelayers

import "fmt"

// validationError/notFoundError mirror the same small copies in
// api/internal/externalsystem and api/internal/basemap -- a client-fixable
// problem (400) versus a missing row (404), kept local rather than shared for
// the same reason those packages keep theirs local. Everything else is a 500.
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

// upstreamError is a failure talking to the third-party service the proxy
// forwards to -- unreachable, a decrypt failure, or a non-2xx it can't relay --
// mapped to a 502 Bad Gateway so the client can tell "the remote service is the
// problem" apart from a bad request (400) or a missing layer (404).
type upstreamError struct{ msg string }

func (e *upstreamError) Error() string { return e.msg }

func upstream(format string, args ...any) error {
	return &upstreamError{msg: fmt.Sprintf(format, args...)}
}

func isUpstreamError(err error) bool {
	_, ok := err.(*upstreamError)
	return ok
}
