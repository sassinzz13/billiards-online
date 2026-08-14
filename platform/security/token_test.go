package security

import (
	"encoding/base64"
	"testing"
)

func TestNewTokenIsUnpredictable(t *testing.T) {
	const n = 500
	seen := make(map[Token]bool, n)

	for range n {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken() = %v", err)
		}
		if seen[tok] {
			t.Fatalf("NewToken() repeated a value: %q", tok)
		}
		seen[tok] = true

		raw, err := base64.RawURLEncoding.DecodeString(string(tok))
		if err != nil {
			t.Fatalf("token %q is not URL-safe base64: %v", tok, err)
		}
		if len(raw) != TokenBytes {
			t.Fatalf("token decodes to %d bytes, want %d", len(raw), TokenBytes)
		}
	}
}

func TestHashTokenIsStableAndSized(t *testing.T) {
	tok := Token("a-token")

	h1, h2 := HashToken(tok), HashToken(tok)
	if !EqualTokenHash(h1, h2) {
		t.Error("HashToken is not deterministic")
	}
	// The sessions table has CHECK (octet_length(token_hash) = 32); a change here breaks inserts.
	if len(h1) != 32 {
		t.Errorf("HashToken returned %d bytes, want 32 to satisfy the sessions CHECK", len(h1))
	}

	if EqualTokenHash(h1, HashToken(Token("a-token "))) {
		t.Error("hashes of different tokens compared equal")
	}
}

// The stored hash must not reveal the token — that is the whole point of hashing it (ADR 0009).
func TestHashTokenDoesNotContainTheToken(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() = %v", err)
	}
	if string(HashToken(tok)) == string(tok) {
		t.Error("HashToken returned the token itself")
	}
}

func TestEqualTokenHashLengthMismatch(t *testing.T) {
	if EqualTokenHash([]byte{1, 2, 3}, []byte{1, 2, 3, 4}) {
		t.Error("hashes of different lengths compared equal")
	}
	if EqualTokenHash(nil, []byte{1}) {
		t.Error("nil compared equal to a non-empty hash")
	}
}
