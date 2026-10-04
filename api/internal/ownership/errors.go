package ownership

import (
	"errors"
	"fmt"
)

// InvalidError is a client-fixable problem (a bad request, an unconfigured
// class) -- the handler maps it to 400, everything else to 500, the same split
// circuit/crud draw.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, a ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, a...)}
}

// IsInvalid reports whether err is (wraps) an InvalidError.
func IsInvalid(err error) bool {
	var e *InvalidError
	return errors.As(err, &e)
}
