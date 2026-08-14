package admin

import "fmt"

// Same validationError/notFoundError split as api/internal/crud's own
// errors.go -- handlers return these as 400/404 respectively, everything
// else is a 500. Kept as this package's own copy rather than a shared
// export, matching how crud/connectivity/relationship/diagram each keep
// theirs -- these packages don't share code across their own boundaries
// anywhere else either.
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

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
