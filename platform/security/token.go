package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// TokenBytes is the entropy of a session token. 256 bits — brute force is not a consideration, and
// the value is short enough to sit comfortably in a cookie.
const TokenBytes = 32

// Token is a bearer secret. It exists in plaintext only in the response that creates it and in the
// client's cookie; the database stores only its hash.
type Token string

// NewToken returns a cryptographically random session token, URL-safe so it can be used in a cookie
// value or a query parameter without escaping.
func NewToken() (Token, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

// HashToken returns the SHA-256 of a token, which is what gets stored and queried.
//
// A plain hash is correct here, unlike for passwords: the token already has 256 bits of entropy, so
// there is nothing to brute force and no need for a slow KDF. Using Argon2 would instead make every
// authenticated request cost 64 MiB.
func HashToken(t Token) []byte {
	sum := sha256.Sum256([]byte(t))
	return sum[:]
}

// EqualTokenHash compares two token hashes in constant time.
//
// The database lookup is by exact hash so this is rarely needed, but any in-process comparison of
// secret-derived bytes must not short-circuit on the first differing byte.
func EqualTokenHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
