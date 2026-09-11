package mapannotation

import "fmt"

// validationError/notFoundError mirror api/internal/diagram (and
// connectivity/relationship before it) -- kept as this package's own small
// copy for the same reason theirs are: sharing would mean exporting internals
// across packages just for two tiny types. writeError (handler.go) maps them
// to 400/404, everything else to 500.
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
