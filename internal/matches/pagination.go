package matches

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// pageCursor is keyset pagination position, the same (created_at DESC, id DESC) shape
// internal/rooms uses — duplicated rather than shared, since matches cannot import rooms (L4 is
// above L3) and this is the second occurrence, not the third that would justify a shared package
// (CLAUDE.md, MEMORY.md §5).
type pageCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

var errInvalidCursor = errors.New("invalid pagination cursor")

func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

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
