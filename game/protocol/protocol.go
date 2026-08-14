// Package protocol defines the realtime wire contract between the Angular client and the Go
// server: the envelope every message travels in, the message catalogue, and the JSON codec.
//
// Stdlib only — this is a game/* package (MEMORY.md §6). It has no idea Gin, pgx, or a WebSocket
// library exist; internal/realtime is the only thing that imports both this and a transport.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the current protocol version. It is present on every envelope from message #1, so a
// breaking change never needs a flag day — see ADR 0006 and docs/protocol.md §8.
const Version = 1

// Envelope wraps every message in both directions.
//
//	{ "v": 1, "type": "shot.request", "seq": 123, "requestId": "...", "matchId": "...",
//	  "ts": 0, "payload": {} }
//
// Payload is left as raw JSON rather than decoded here: this package does not know the shape of
// any particular message type, and decoding it into a concrete struct is the dispatching code's
// job once it knows Type.
type Envelope struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	Seq       uint64 `json:"seq,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	MatchID   string `json:"matchId,omitempty"`
	// TS is informational only — unix milliseconds, set by whichever side sends the message. No
	// gameplay decision may depend on a client-supplied TS (docs/protocol.md §2): the server's own
	// clock is authoritative for every deadline.
	TS      int64           `json:"ts,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Message types Phase 5 actually sends. Gameplay types (shot.request, room.joined, ...) are added
// as their owning phase lands — see docs/protocol.md's full catalogue — not built ahead of the
// feature that needs them (§72).
const (
	TypeAuthSuccess = "auth.success"
	TypeError       = "error"
)

// Error codes. Stable and dotted so a client can switch on them; messages are human-readable and
// never carry internal detail (§42, §51). Extend this list as new failure modes need distinct
// handling — see docs/protocol.md §7 for the full, documented catalogue this draws from.
const (
	ErrCodeVersion     = "protocol.version"
	ErrCodeUnknownType = "protocol.unknown_type"
	ErrCodeTooLarge    = "protocol.too_large"
	ErrCodeMalformed   = "protocol.malformed"
	ErrCodeInternal    = "internal"
)

// ErrorPayload is the payload of a `type: "error"` envelope.
type ErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

// AuthSuccessPayload is the payload of the first message a connection receives.
type AuthSuccessPayload struct {
	UserID       string `json:"userId"`
	ConnectionID string `json:"connectionId"`
}

var ErrUnsupportedVersion = errors.New("unsupported protocol version")

// Decode parses a raw client frame into an Envelope.
//
// It does not validate Version — callers that care (the realtime gateway does) check it
// explicitly, because "wrong version" and "not JSON at all" want different error codes
// (ErrCodeVersion vs ErrCodeMalformed) even though both arrive here as the same input.
func Decode(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	return env, nil
}

// Encode serializes an Envelope for the wire.
func Encode(env Envelope) ([]byte, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return data, nil
}

// EncodePayload marshals a typed payload into the RawMessage Envelope.Payload expects. A helper
// rather than requiring every call site to remember json.Marshal-then-wrap.
func EncodePayload(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return data, nil
}

// NewError builds a `type: "error"` envelope with the given seq. requestID is echoed back when the
// failure can be attributed to a specific client message, and omitted otherwise (a malformed frame,
// for instance, may not have parsed far enough to have one).
func NewError(seq uint64, code, message, requestID string) (Envelope, error) {
	payload, err := EncodePayload(ErrorPayload{Code: code, Message: message, RequestID: requestID})
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{V: Version, Type: TypeError, Seq: seq, Payload: payload}, nil
}

// NewAuthSuccess builds the envelope sent immediately after a successful upgrade.
func NewAuthSuccess(seq uint64, userID, connectionID string) (Envelope, error) {
	payload, err := EncodePayload(AuthSuccessPayload{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{V: Version, Type: TypeAuthSuccess, Seq: seq, Payload: payload}, nil
}
