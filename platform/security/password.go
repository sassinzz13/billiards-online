// Package security provides password hashing, token generation, and rate limiting.
//
// It holds cryptographic and abuse-prevention mechanism only — never authorization policy. Whether
// a given user may act on a given match is a question for the feature that owns that match, not
// for this package (§7, §36).
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Stored inside every hash, so these can be raised later without invalidating
// existing passwords — VerifyPassword reads the parameters from the hash it is checking.
//
// m=64MiB is the meaningful cost. It is what makes GPU and ASIC attacks expensive, and also why
// login and signup must be rate limited: each attempt allocates 64 MiB, so unbounded concurrent
// attempts are a memory exhaustion vector on their own.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonSaltLen        = 16
	argonKeyLen  uint32 = 32

	// Rejects absurd inputs before spending 64 MiB on them. The upper bound matters because Argon2
	// has no length limit of its own, so a multi-megabyte "password" would otherwise be hashed.
	MinPasswordLen = 10
	MaxPasswordLen = 1024
)

var (
	ErrPasswordTooShort  = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrPasswordTooLong   = fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	ErrInvalidHashFormat = errors.New("password hash is not a valid argon2id PHC string")
)

// HashPassword returns a PHC-format Argon2id hash:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt-b64>$<hash-b64>
//
// The salt is random per call, so identical passwords produce different hashes.
func HashPassword(plain string) (string, error) {
	if len(plain) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	if len(plain) > MaxPasswordLen {
		return "", ErrPasswordTooLong
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether plain matches encoded.
//
// Comparison is constant-time. A byte-by-byte comparison would leak how much of the hash matched,
// which is enough to reconstruct it one byte at a time.
func VerifyPassword(plain, encoded string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(plain), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyVerify performs a hash with the same cost as a real verification and discards the result.
//
// Login calls this when the email does not exist. Without it, a missing account returns in
// microseconds while a real one takes ~50ms, and that difference is a reliable oracle for
// enumerating which email addresses are registered.
func DummyVerify(plain string) {
	salt := make([]byte, argonSaltLen)
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	// Keep the compiler from eliminating the call as dead code.
	runtime.KeepAlive(key)
}

// NeedsRehash reports whether a stored hash was produced with weaker parameters than current
// policy. Callers can transparently upgrade a password on the next successful login.
func NeedsRehash(encoded string) bool {
	params, _, _, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return params.memory < argonMemory || params.time < argonTime || params.threads < argonThreads
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHashFormat, version)
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return argonParams{}, nil, nil, ErrInvalidHashFormat
	}

	return p, salt, key, nil
}
