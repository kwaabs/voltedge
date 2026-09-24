// Package httpx holds small, dependency-free HTTP middleware shared across the
// server binaries (cmd/server, cmd/fieldsync). It exists so the same request
// hardening is applied identically everywhere rather than re-derived per binary.
package httpx

import "net/http"

// MaxBytes returns middleware that caps the request body at n bytes by wrapping
// r.Body in http.MaxBytesReader. A handler reading past the cap (e.g. a JSON
// decode) then fails, and MaxBytesReader also asks the server to close the
// connection, so an unbounded or hostile body can't make the process buffer
// arbitrary memory. Apply a generous cap globally to bound the worst case; put a
// tighter http.MaxBytesReader directly on endpoints whose bodies are known-small
// (the credential endpoints do this).
//
// GET/HEAD/DELETE and other bodyless methods are left untouched -- there is
// nothing to cap, and wrapping a nil/empty body is pointless.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.ContentLength != 0 {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
