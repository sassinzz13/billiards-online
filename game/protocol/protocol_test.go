package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/sassinzz13/billiards-online/game/protocol"
)

func TestEnvelopeRoundTrips(t *testing.T) {
	payload, err := protocol.EncodePayload(map[string]int{"power": 6})
	if err != nil {
		t.Fatalf("EncodePayload() = %v", err)
	}
	original := protocol.Envelope{
		V: protocol.Version, Type: "shot.request", Seq: 7,
		RequestID: "r1", MatchID: "m1", TS: 12345, Payload: payload,
	}

	data, err := protocol.Encode(original)
	if err != nil {
		t.Fatalf("Encode() = %v", err)
	}

	decoded, err := protocol.Decode(data)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	// Payload is json.RawMessage ([]byte), so the struct is compared field by field rather than
	// with == / !=, which would not compile against a slice-bearing struct.
	if decoded.V != original.V || decoded.Type != original.Type || decoded.Seq != original.Seq ||
		decoded.RequestID != original.RequestID || decoded.MatchID != original.MatchID || decoded.TS != original.TS {
		t.Errorf("round trip mismatch:\n  got  %+v\n  want %+v", decoded, original)
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("payload mismatch:\n  got  %s\n  want %s", decoded.Payload, original.Payload)
	}
}

// The wire format is a public contract shared with the Angular client (rooms.service.ts and its
// siblings expect exactly this shape). A field rename here is a breaking change the JSON tag,
// not the Go field name, controls.
func TestEnvelopeFieldNames(t *testing.T) {
	env := protocol.Envelope{V: 1, Type: "auth.success", Seq: 1}
	data, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode() = %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("re-decode into map: %v", err)
	}
	for _, key := range []string{"v", "type", "seq"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("encoded envelope missing key %q: %s", key, data)
		}
	}
	// Empty optional fields must be omitted, not sent as "" / 0 / null — a smaller wire format and
	// what the omitempty tags promise.
	for _, key := range []string{"requestId", "matchId", "ts", "payload"} {
		if _, present := generic[key]; present {
			t.Errorf("encoded envelope unexpectedly includes empty field %q: %s", key, data)
		}
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	if _, err := protocol.Decode([]byte("not json")); err == nil {
		t.Error("Decode(invalid json) = nil error, want a rejection")
	}
	if _, err := protocol.Decode([]byte("")); err == nil {
		t.Error("Decode(empty) = nil error, want a rejection")
	}
}

func TestDecodeAcceptsUnknownExtraFields(t *testing.T) {
	// Forward compatibility: a future client field this server does not know about yet must not
	// break decoding of the fields it does know about.
	env, err := protocol.Decode([]byte(`{"v":1,"type":"shot.request","futureField":"whatever"}`))
	if err != nil {
		t.Fatalf("Decode() = %v, want it to tolerate unknown fields", err)
	}
	if env.Type != "shot.request" {
		t.Errorf("Type = %q, want shot.request", env.Type)
	}
}

func TestNewErrorEchoesRequestID(t *testing.T) {
	env, err := protocol.NewError(5, protocol.ErrCodeUnknownType, "unknown message type", "req-1")
	if err != nil {
		t.Fatalf("NewError() = %v", err)
	}
	if env.Type != protocol.TypeError || env.Seq != 5 {
		t.Errorf("envelope = %+v, want type=error seq=5", env)
	}

	var p protocol.ErrorPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Code != protocol.ErrCodeUnknownType || p.RequestID != "req-1" {
		t.Errorf("payload = %+v, want code=%s requestId=req-1", p, protocol.ErrCodeUnknownType)
	}
}

func TestNewAuthSuccessPayload(t *testing.T) {
	env, err := protocol.NewAuthSuccess(1, "user-1", "conn-1")
	if err != nil {
		t.Fatalf("NewAuthSuccess() = %v", err)
	}

	var p protocol.AuthSuccessPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.UserID != "user-1" || p.ConnectionID != "conn-1" {
		t.Errorf("payload = %+v, want userId=user-1 connectionId=conn-1", p)
	}
}
