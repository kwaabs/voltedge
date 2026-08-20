package auth

import "fmt"

// validationError/unauthorizedError/forbiddenError mirror the small
// per-package error-classification pattern the rest of this codebase uses
// (api/internal/powerflow/errors.go and its siblings) -- an error's Go
// type is what writeError maps to an HTTP status, so a handler never
// hand-writes a status code. Kept as this package's own copy rather than
// a shared import, same reasoning as every other package here.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

func isValidationError(err error) bool {
	_, ok := err.(*validationError)
	return ok
}

// unauthorizedError -> 401. Deliberately carries a fixed, generic message
// at the call sites that authenticate a credential, so a caller can never
// distinguish "no such user" from "wrong password" (user-enumeration
// defense). Its Error text is what the client sees.
type unauthorizedError struct{ msg string }

func (e *unauthorizedError) Error() string { return e.msg }

func unauthorized(msg string) error { return &unauthorizedError{msg: msg} }

func isUnauthorizedError(err error) bool {
	_, ok := err.(*unauthorizedError)
	return ok
}

// forbiddenError -> 403. Used when the credential is valid but policy
// refuses the action -- e.g. local auth is switched off and the account
// is not break-glass, or (Slice C) a permission is missing.
type forbiddenError struct{ msg string }

func (e *forbiddenError) Error() string { return e.msg }

func forbidden(msg string) error { return &forbiddenError{msg: msg} }

func isForbiddenError(err error) bool {
	_, ok := err.(*forbiddenError)
	return ok
}
