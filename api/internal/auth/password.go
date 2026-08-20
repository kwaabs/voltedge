package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing is Argon2id -- OWASP's first-choice password KDF
// (memory-hard, GPU/ASIC-resistant). Parameters start at the OWASP
// baseline and are stored inline in each hash (PHC string format), so
// they can be raised later without invalidating existing hashes: verify
// reads the parameters out of the stored hash, it never assumes the
// current defaults. golang.org/x/crypto/argon2 is already a dependency of
// this module, so this adds no new supply-chain surface.
type argon2idParams struct {
	memory      uint32 // KiB
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

// OWASP baseline: 19 MiB, 2 iterations, 1 lane, 16-byte salt, 32-byte key.
var defaultArgon2idParams = argon2idParams{
	memory:      19 * 1024,
	iterations:  2,
	parallelism: 1,
	saltLength:  16,
	keyLength:   32,
}

var (
	errInvalidHash         = errors.New("auth: hash is not in Argon2id PHC format")
	errIncompatibleVersion = errors.New("auth: incompatible Argon2 version")
)

// b64 is the raw (unpadded) standard base64 the PHC format uses.
var b64 = base64.RawStdEncoding

// hashPassword derives a fresh salt from crypto/rand and returns the
// Argon2id PHC-encoded hash:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func hashPassword(password string) (string, error) {
	p := defaultArgon2idParams
	salt := make([]byte, p.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: reading salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, p.keyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.iterations, p.parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	), nil
}

// verifyPassword recomputes the hash of password under the parameters and
// salt recorded in encoded, and compares it to the stored digest in
// constant time (crypto/subtle). A mismatch returns (false, nil); a
// malformed stored hash returns an error.
func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, errInvalidHash
	}
	if version != argon2.Version {
		return false, errIncompatibleVersion
	}

	var p argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return false, errInvalidHash
	}

	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errInvalidHash
	}
	stored, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errInvalidHash
	}
	p.keyLength = uint32(len(stored))

	computed := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, p.keyLength)
	return subtle.ConstantTimeCompare(computed, stored) == 1, nil
}

// dummyArgon2idHash is a real Argon2id hash of a throwaway value, computed
// once at startup. loginHandler verifies against it when no local
// identity exists for a submitted email, so the failed path spends the
// same CPU as the success path -- closing the timing side channel that
// would otherwise let an attacker enumerate which emails have accounts.
var dummyArgon2idHash = mustHash("voltedge-argon2id-timing-equalizer")

func mustHash(s string) string {
	h, err := hashPassword(s)
	if err != nil {
		panic("auth: computing dummy hash: " + err.Error())
	}
	return h
}
