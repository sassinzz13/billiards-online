package rooms

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// pageCursor is keyset pagination position, matching the (created_at DESC, id DESC) ordering
// rooms_public_open_idx is built for (§54, §55). Kept opaque to callers as an encoded string, so
// the encoding can change without becoming a public contract.
type pageCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

var errInvalidCursor = errors.New("invalid pagination cursor")

// EncodeCursor produces the opaque token a client echoes back to request the next page.
func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a client-supplied cursor. A malformed value is a client error (400), never a
// server error — nothing about it can fail for reasons the client couldn't have caused, since it
// only ever contains what EncodeCursor put there.
func decodeCursor(s string) (*pageCursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errInvalidCursor
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, errInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, errInvalidCursor
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, errInvalidCursor
	}
	return &pageCursor{CreatedAt: t, ID: id}, nil
}
