package webhookout

import "fmt"

// Per-package error types, deliberately local (the inbound webhook package keeps
// its own twin). permanent() keys off these so the delivery worker can tell a
// retryable failure (a 5xx, a network blip) from one that will never succeed on
// retry (a bad target URL, a missing signing secret, a 4xx rejection) and should
// dead-letter immediately instead of burning the whole attempt budget.

// permanentError marks a failure that retrying cannot fix.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// permanentf builds a permanent (non-retryable) delivery error.
func permanentf(format string, a ...any) error {
	return &permanentError{msg: fmt.Sprintf(format, a...)}
}

// isPermanent reports whether err is a permanentError.
func isPermanent(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*permanentError)
	return ok
}
