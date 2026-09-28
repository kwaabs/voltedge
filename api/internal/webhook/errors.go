package webhook

import "fmt"

// validationError/notFoundError/authError are the same small local copies
// api/internal/externalsystem and its siblings keep -- local rather than shared
// so the package draws no cross-package dependency for one tiny type. writeError
// (handler.go) maps them to 400/404/401 respectively.
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

// authError is a rejected sender credential on the public receiver -- a bad or
// missing signature/bearer. It maps to 401 and its message is deliberately
// vague to a caller (never "wrong signature vs wrong timestamp"), so it can't be
// used as an oracle.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func unauthorized(format string, args ...any) error {
	return &authError{msg: fmt.Sprintf(format, args...)}
}

func isAuthError(err error) bool {
	_, ok := err.(*authError)
	return ok
}
