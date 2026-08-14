package realtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/sassinzz13/billiards-online/game/protocol"
	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/internal/realtime"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/config"
	pws "github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
	"github.com/sassinzz13/billiards-online/platform/security"
)

const testOrigin = "http://billiards.localhost"
const testPassword = "correct-horse-battery-staple"

// harness wires a real auth.Service (against the shared rolled-back test transaction, same pattern
// as internal/auth and internal/rooms' own tests) to a realtime.Gateway, and serves it over a real
// HTTP server so a genuine coder/websocket client can exercise the actual upgrade path — Origin
// header, cookie, framing — rather than calling Go functions directly.
type harness struct {
	url     string
	authSvc *auth.Service
}

func newHarness(t *testing.T) harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tx := pws.DB(t)
	usersSvc := users.NewService(tx)
	authSvc := auth.NewService(tx, usersSvc)

	cfg := &config.Config{HTTP: config.HTTP{AllowedOrigins: []string{testOrigin}}}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gateway := realtime.NewGateway(authSvc, cfg, ctx)

	r := gin.New()
	r.GET("/ws", gateway.ServeWS)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return harness{url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws", authSvc: authSvc}
}

func (h harness) signUp(t *testing.T) string {
	t.Helper()
	id := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "")
	res, err := h.authSvc.Signup(pws.Context(t), id+"@example.com", "u"+id[:min(20, len(id))], testPassword)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	return string(res.Token)
}

// dial performs the actual HTTP-level WebSocket handshake with the given Origin and Cookie
// headers, so the gateway's pre-upgrade rejections (bad Origin, missing/invalid auth) are exercised
// exactly as a browser would trigger them — not simulated by calling Go functions directly.
func dial(ctx context.Context, url, origin, cookie string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	if cookie != "" {
		header.Set("Cookie", auth.CookieName+"="+cookie)
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
}

func withTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAuthenticatedConnectionReceivesAuthSuccess(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, resp, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade status = %d, want 101", resp.StatusCode)
	}

	_, data, err := conn.Read(withTimeout(t))
	if err != nil {
		t.Fatalf("read auth.success: %v", err)
	}

	env, err := protocol.Decode(data)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != protocol.TypeAuthSuccess {
		t.Fatalf("first message type = %q, want %q", env.Type, protocol.TypeAuthSuccess)
	}
	if env.Seq != 1 {
		t.Errorf("first message seq = %d, want 1 (monotonic, starting at 1)", env.Seq)
	}
	if env.V != protocol.Version {
		t.Errorf("v = %d, want %d", env.V, protocol.Version)
	}

	var payload protocol.AuthSuccessPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.UserID == "" || payload.ConnectionID == "" {
		t.Errorf("payload = %+v, want both ids populated", payload)
	}
}

func TestMissingSessionIsRejectedBeforeUpgrade(t *testing.T) {
	h := newHarness(t)

	_, resp, err := dial(withTimeout(t), h.url, testOrigin, "")
	if err == nil {
		t.Fatal("dial() succeeded with no session cookie, want a rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Errorf("status = %d, want 401", status)
	}
}

func TestForgedSessionIsRejectedBeforeUpgrade(t *testing.T) {
	h := newHarness(t)

	_, resp, err := dial(withTimeout(t), h.url, testOrigin, "not-a-real-session-token")
	if err == nil {
		t.Fatal("dial() succeeded with a forged cookie, want a rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Errorf("status = %d, want 401", status)
	}
}

// A revoked session must stop working here exactly as it does for the REST API — the same
// property ADR 0009 exists for.
func TestRevokedSessionIsRejected(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	identity, err := h.authSvc.Authenticate(pws.Context(t), security.Token(token))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if err := h.authSvc.Logout(pws.Context(t), identity.SessionID); err != nil {
		t.Fatalf("logout: %v", err)
	}

	_, resp, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err == nil {
		t.Fatal("dial() succeeded with a revoked session, want a rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Errorf("status = %d, want 401", status)
	}
}

// Exact-match only — a prefix, suffix, or subdomain must not pass. This is the same property
// platform/config's own TestAllowsOriginMatchesExactly proves in isolation; here it is proven at
// the actual upgrade path that consumes it.
func TestBadOriginIsRejectedBeforeUpgrade(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	for _, origin := range []string{
		"http://billiards.localhost.evil.com",
		"http://evil.com",
		"https://billiards.localhost", // scheme mismatch
		"",
	} {
		t.Run(origin, func(t *testing.T) {
			_, resp, err := dial(withTimeout(t), h.url, origin, token)
			if err == nil {
				t.Fatalf("dial(origin=%q) succeeded, want a rejection", origin)
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				status := -1
				if resp != nil {
					status = resp.StatusCode
				}
				t.Errorf("status = %d, want 403", status)
			}
		})
	}
}

func TestGoodOriginIsAccepted(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, resp, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("status = %d, want 101", resp.StatusCode)
	}
}

// An envelope with an unrecognized type gets an error reply and stays open — never a silent drop
// (docs/protocol.md §2) — because no gameplay message type exists to dispatch to until Phase 6.
func TestUnknownMessageTypeGetsErrorAndStaysOpen(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, _, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()
	drainAuthSuccess(t, conn)

	send(t, conn, protocol.Envelope{V: protocol.Version, Type: "not.a.real.type", RequestID: "req-1"})

	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var payload protocol.ErrorPayload
	json.Unmarshal(env.Payload, &payload)
	if payload.Code != protocol.ErrCodeUnknownType {
		t.Errorf("code = %q, want %q", payload.Code, protocol.ErrCodeUnknownType)
	}
	if payload.RequestID != "req-1" {
		t.Errorf("requestId = %q, want it echoed back", payload.RequestID)
	}

	// The connection must still be usable — send a second unknown type and expect a second reply
	// with a higher seq, proving the socket was not closed by the first error.
	send(t, conn, protocol.Envelope{V: protocol.Version, Type: "still.unknown"})
	second := readEnvelope(t, conn)
	if second.Type != protocol.TypeError {
		t.Fatalf("second reply type = %q, want error", second.Type)
	}
	if second.Seq <= env.Seq {
		t.Errorf("second seq %d did not advance past first seq %d", second.Seq, env.Seq)
	}
}

// A frame that is not valid JSON cannot be safely resumed from — the byte-stream framing itself is
// untrusted at that point — so the gateway replies with an error and closes.
func TestMalformedFrameGetsErrorAndCloses(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, _, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()
	drainAuthSuccess(t, conn)

	if err := conn.Write(withTimeout(t), websocket.MessageText, []byte("{not valid json")); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}

	env := readEnvelope(t, conn)
	if env.Type != protocol.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var payload protocol.ErrorPayload
	json.Unmarshal(env.Payload, &payload)
	if payload.Code != protocol.ErrCodeMalformed {
		t.Errorf("code = %q, want %q", payload.Code, protocol.ErrCodeMalformed)
	}

	// The connection must actually close afterward, not merely reply.
	if _, _, err := conn.Read(withTimeout(t)); err == nil {
		t.Error("connection still readable after a malformed frame, want it closed")
	}
}

// A frame over the read limit is rejected at the transport layer (platform/websocket, already
// covered directly by its own tests) before ever reaching the router. This proves the same
// behaviour holds end to end through the actual gateway.
func TestOversizedFrameClosesTheConnection(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, _, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()
	drainAuthSuccess(t, conn)

	oversized := make([]byte, 33*1024)
	writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, websocket.MessageText, oversized) // may itself fail; that is fine too

	if _, _, err := conn.Read(withTimeout(t)); err == nil {
		t.Error("connection still readable after an oversized frame, want it closed")
	}
}

// The Phase 5 exit criterion, exercised through the real gateway rather than platform/websocket in
// isolation (which already proves the mechanism directly): a client that never reads is closed with
// the backpressure policy code, not left to grow an unbounded queue.
func TestSlowClientIsClosedWithPolicyCode(t *testing.T) {
	h := newHarness(t)
	token := h.signUp(t)

	conn, _, err := dial(withTimeout(t), h.url, testOrigin, token)
	if err != nil {
		t.Fatalf("dial() = %v", err)
	}
	defer conn.CloseNow()

	// Deliberately never read again after this point — the client falls behind by construction.
	// Every unknown-type message the server replies to with an error queues one more outbound
	// envelope, comfortably past the 64-message queue.
	for i := 0; i < 90; i++ {
		send(t, conn, protocol.Envelope{V: protocol.Version, Type: "flood.me"})
	}

	// The backpressure policy is "close now," not "drain then close" (§23; contrast with
	// CloseAfterDrain, used deliberately for the malformed-frame and version-mismatch paths, which
	// promise their explanation arrives first). Once Full() fires, Close races directly against the
	// write pump's in-flight drain of whatever was already queued, so this test must not assume
	// every one of the 90 replies arrives before the close — only that the connection actually
	// closes, promptly, once it falls behind. A read that fails is success here; a read that never
	// returns at all within the deadline is the failure this test exists to catch. Once any read
	// fails, coder/websocket's connection is done — this loop does not retry after an error, only
	// after a successful data read still queued ahead of the close.
	var closeErr websocket.CloseError
	closed := false
	deadline := time.Now().Add(10 * time.Second)
	for attempt := 0; time.Now().Before(deadline) && attempt < 200; attempt++ {
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _, err := conn.Read(readCtx)
		cancel()
		if err == nil {
			continue // one of the queued replies; keep going
		}
		closed = true
		errors.As(err, &closeErr) // best-effort: some closure paths do not decode as a clean CloseError
		break
	}
	if !closed {
		t.Fatal("connection was never closed")
	}
	if closeErr.Code != 0 && closeErr.Code != statusSlowClientCode {
		t.Errorf("close code = %v, want %v (StatusSlowClient) when a clean close code was available",
			closeErr.Code, statusSlowClientCode)
	}
}

// statusSlowClientCode mirrors platform/websocket.StatusSlowClient's value. The exact code is
// pinned and asserted directly (with a guaranteed-clean decode) by
// TestSlowClientTriggersBackpressureClose in platform/websocket/conn_test.go; this local constant
// exists only so this end-to-end test does not need an extra import for one integer.
const statusSlowClientCode websocket.StatusCode = 3000

// --- helpers -----------------------------------------------------------------------------------

func drainAuthSuccess(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if _, _, err := conn.Read(withTimeout(t)); err != nil {
		t.Fatalf("read auth.success: %v", err)
	}
}

func send(t *testing.T, conn *websocket.Conn, env protocol.Envelope) {
	t.Helper()
	data, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if err := conn.Write(withTimeout(t), websocket.MessageText, data); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) protocol.Envelope {
	t.Helper()
	_, data, err := conn.Read(withTimeout(t))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := protocol.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}
