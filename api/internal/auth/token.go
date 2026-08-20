package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// sessionTokenBytes is 256 bits of entropy -- far beyond guessable, so
// the token itself needs no slow hashing (unlike a password). It is
// stored only as its SHA-256 (hashToken), never in the clear, so a
// database leak yields no usable cookie.
const sessionTokenBytes = 32

// csrfTokenBytes is a fresh 256-bit random per session for the
// double-submit CSRF defense.
const csrfTokenBytes = 32

// newRandomToken returns a URL-safe, unpadded base64 string of n random
// bytes from crypto/rand -- used for both the session token and the CSRF
// token. Never math/rand.
func newRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: reading random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the SHA-256 stored/compared for a session token. A fast
// hash is correct here precisely because the input is high-entropy
// random, not a low-entropy secret a human chose.
func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
