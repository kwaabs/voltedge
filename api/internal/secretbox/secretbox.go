// Package secretbox is the at-rest encryption used for external-system
// credentials (Phase 32 Slice 2). A single AES-256-GCM key, loaded once
// from the environment, encrypts a secret before it is written to
// metadata.external_systems.credential_enc and decrypts it only when the
// Slice 3 fetch proxy needs to build an authenticated outbound request.
// The plaintext secret never leaves the server and is never returned to
// the browser.
//
// The key is an *optional* dependency, same shape as the assistant's LLM
// provider: NewFromEnv returns (nil, nil) when the key is unset so the
// API still boots (external systems with no credentials work fine); the
// admin layer then refuses to store a secret with a clear error rather
// than silently dropping it.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvKey is the environment variable holding the base64-encoded 32-byte
// (AES-256) key.
const EnvKey = "EXTERNAL_SYSTEMS_ENCRYPTION_KEY"

// Cipher wraps an AES-256-GCM AEAD. Safe for concurrent use -- the
// underlying gcm is stateless across Seal/Open calls (each call carries
// its own nonce).
type Cipher struct {
	aead cipher.AEAD
}

// NewFromEnv builds a Cipher from EXTERNAL_SYSTEMS_ENCRYPTION_KEY. A
// missing key yields (nil, nil) -- "encryption unavailable", not an
// error. A key that IS set but malformed (bad base64, wrong length) is a
// hard error worth failing boot on, since it signals a misconfigured
// deployment that meant to enable encryption.
func NewFromEnv() (*Cipher, error) {
	raw := strings.TrimSpace(os.Getenv(EnvKey))
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %s is not valid base64: %w", EnvKey, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: %s must decode to 32 bytes (AES-256), got %d", EnvKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext, returning nonce||ciphertext so Decrypt is
// self-contained (the random nonce travels with the payload). A fresh
// nonce per call is what makes reusing one key across many secrets safe.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	// Seal appends the ciphertext to its first arg (nonce here), so the
	// result is nonce||ciphertext||tag.
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. A ciphertext produced under a different key
// (e.g. the key was rotated) fails GCM authentication and returns an
// error rather than garbage.
func (c *Cipher) Decrypt(sealed []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := sealed[:ns], sealed[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: decrypt: %w", err)
	}
	return plaintext, nil
}
