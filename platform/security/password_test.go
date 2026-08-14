package security

import (
	"strings"
	"testing"
)

const validPassword = "correct-horse-battery-staple"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v, want nil", err)
	}

	ok, err := VerifyPassword(validPassword, hash)
	if err != nil {
		t.Fatalf("VerifyPassword() = %v, want nil", err)
	}
	if !ok {
		t.Error("VerifyPassword() = false for the correct password")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}

	for _, wrong := range []string{
		"correct-horse-battery-stapl",   // one character short
		"correct-horse-battery-staplex", // one character extra
		"Correct-Horse-Battery-Staple",  // different case
		"",
	} {
		ok, err := VerifyPassword(wrong, hash)
		if err != nil {
			t.Fatalf("VerifyPassword(%q) errored: %v", wrong, err)
		}
		if ok {
			t.Errorf("VerifyPassword(%q) = true, want false", wrong)
		}
	}
}

// A per-hash random salt is what stops an attacker who steals the table from seeing which accounts
// share a password, and makes a single rainbow table useless against all of them.
func TestHashIsSaltedPerCall(t *testing.T) {
	a, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	b, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}

	if a == b {
		t.Error("identical passwords produced identical hashes — the salt is not random")
	}

	// Both must still verify.
	for i, h := range []string{a, b} {
		ok, err := VerifyPassword(validPassword, h)
		if err != nil || !ok {
			t.Errorf("hash %d does not verify: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestHashFormatIsPHC(t *testing.T) {
	hash, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}

	// The database CHECK constraint requires this prefix, so the format is not merely cosmetic.
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("hash = %q, want the current PHC parameter prefix", hash)
	}
	if n := len(strings.Split(hash, "$")); n != 6 {
		t.Errorf("hash has %d $-separated fields, want 6", n)
	}
}

func TestHashRejectsOutOfRangeLengths(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MinPasswordLen-1)); err != ErrPasswordTooShort {
		t.Errorf("short password: err = %v, want ErrPasswordTooShort", err)
	}
	// The upper bound matters: Argon2 has no length limit of its own, so without this a
	// multi-megabyte "password" would be hashed at 64 MiB cost.
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordLen+1)); err != ErrPasswordTooLong {
		t.Errorf("long password: err = %v, want ErrPasswordTooLong", err)
	}
}

// A malformed hash must be an error, never a silent "true". A bug that returned true here would
// let anyone log in as an account with a corrupted credential row.
func TestVerifyRejectsMalformedHashes(t *testing.T) {
	malformed := []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",  // wrong variant
		"$argon2id$v=18$m=65536,t=3,p=4$c2FsdA$aGFzaA", // wrong version
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",     // zero parameters
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA",        // missing hash segment
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",    // salt is not base64
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$",       // empty hash
	}

	for _, h := range malformed {
		ok, err := VerifyPassword(validPassword, h)
		if err == nil {
			t.Errorf("VerifyPassword(_, %q) = nil error, want a rejection", h)
		}
		if ok {
			t.Errorf("VerifyPassword(_, %q) = true — a malformed hash must never verify", h)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	if NeedsRehash(current) {
		t.Error("NeedsRehash(current policy) = true, want false")
	}

	// Weaker than policy: memory below the current setting.
	weak := "$argon2id$v=19$m=4096,t=1,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGE"
	if !NeedsRehash(weak) {
		t.Error("NeedsRehash(weak parameters) = false, want true")
	}
	// Unreadable hashes should be replaced rather than trusted.
	if !NeedsRehash("garbage") {
		t.Error("NeedsRehash(garbage) = false, want true")
	}
}

func BenchmarkHashPassword(b *testing.B) {
	for b.Loop() {
		if _, err := HashPassword(validPassword); err != nil {
			b.Fatal(err)
		}
	}
}
