package users_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// requireAuthWithHeader is a stand-in for the composition root's real bridge middleware
// (apps/server/cmd/server/main.go). It reads a test-only header instead of a session cookie, so
// these tests exercise the users handler's own logic — "does /me act on whoever the context says,
// and never on anything from the request?" — without dragging in internal/auth. The real bridge is
// covered by the end-to-end verification in Phase 3's rollout.
func requireAuthWithHeader() gin.HandlerFunc {
	const header = "X-Test-User-Id"
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.GetHeader(header))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code": "auth.required", "message": "Authentication required.",
			}})
			return
		}
		c.Request = c.Request.WithContext(users.WithUserID(c.Request.Context(), id))
		c.Next()
	}
}

type api struct {
	router *gin.Engine
	svc    *users.Service
	next   func() (email, handle string)
}

func newAPI(t *testing.T) api {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tx := pgtest.DB(t)
	svc := users.NewService(tx)

	r := gin.New()
	users.NewHandler(svc, requireAuthWithHeader()).RegisterRoutes(r.Group("/api/v1"))

	return api{
		router: r,
		svc:    svc,
		next: func() (string, string) {
			id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			return "player_" + id + "@example.com", "p" + id
		},
	}
}

func (a api) do(t *testing.T, method, path string, body any, userID *uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if userID != nil {
		req.Header.Set("X-Test-User-Id", userID.String())
	}

	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func (a api) createUser(t *testing.T) users.User {
	t.Helper()
	email, handle := a.next()
	u, err := a.svc.Create(pgtest.Context(t), email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	return u
}

// Regression test for a real bug found during Phase 3 rollout: NewHandler accepts multiple
// middleware precisely because the composition root chains auth.RequireAuth() and its own
// identity-bridging middleware as two separate entries. An earlier version instead called
// RequireAuth's returned gin.HandlerFunc from *inside* a single bridge closure — and because that
// handler calls c.Next() itself, gin.Context.Next()'s shared index meant the nested call ran every
// later handler, including the real route handler, before the bridge got a chance to attach
// anything. /me then saw no identity at all despite RequireAuth having succeeded.
//
// This test builds a handler the same way main.go does: two independent gin.HandlerFunc values
// passed to NewHandler, the first simulating auth (sets one context value), the second simulating
// the bridge (reads it, sets another). If Gin's own chaining is used — plain entries in one
// []gin.HandlerFunc, never one middleware calling another's returned func directly — both run to
// completion, in order, before the route handler does.
func TestMultipleAuthMiddlewareChainInOrderNotNested(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tx := pgtest.DB(t)
	svc := users.NewService(tx)
	email, handle := "chain@example.com", "chainuser"
	u, err := svc.Create(pgtest.Context(t), email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	type marker struct{}
	simulateAuth := func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), marker{}, u.ID))
		c.Next()
	}
	simulateBridge := func(c *gin.Context) {
		id, ok := c.Request.Context().Value(marker{}).(uuid.UUID)
		if !ok {
			t.Error("bridge ran before auth's context value was visible — middlewares ran out of order")
		}
		c.Request = c.Request.WithContext(users.WithUserID(c.Request.Context(), id))
		c.Next()
	}

	r := gin.New()
	users.NewHandler(svc, simulateAuth, simulateBridge).RegisterRoutes(r.Group("/api/v1"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me through a two-middleware chain = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["handle"] != handle {
		t.Errorf("handle = %v, want %q — the bridged identity did not reach the route handler", body["handle"], handle)
	}
}

func TestGetMeRequiresAuth(t *testing.T) {
	a := newAPI(t)
	if rec := a.do(t, http.MethodGet, "/api/v1/users/me", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /me without auth = %d, want 401", rec.Code)
	}
}

func TestGetMeReturnsOwnAccountIncludingEmail(t *testing.T) {
	a := newAPI(t)
	u := a.createUser(t)

	rec := a.do(t, http.MethodGet, "/api/v1/users/me", nil, &u.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me = %d, want 200: %s", rec.Code, rec.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["email"] != u.Email {
		t.Errorf("email = %v, want %q — /me is the one endpoint allowed to show it", body["email"], u.Email)
	}
	if body["handle"] != u.Handle {
		t.Errorf("handle = %v, want %q", body["handle"], u.Handle)
	}
	if body["displayName"] != u.Handle {
		t.Errorf("displayName = %v, want fallback to handle %q", body["displayName"], u.Handle)
	}
}

// The exit criterion of this phase: a signed-in user can view and edit their own profile and
// cannot edit anyone else's. PATCH /me has no field for a target user id — it always acts on
// whichever identity the auth bridge attached — so there is structurally nothing for user A to put
// user B's id into.
func TestUpdateMeOnlyEverTargetsTheCaller(t *testing.T) {
	a := newAPI(t)
	alice := a.createUser(t)
	bob := a.createUser(t)

	aliceName := "Alice In Person"
	rec := a.do(t, http.MethodPatch, "/api/v1/users/me",
		gin.H{"displayName": aliceName}, &alice.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /me as alice = %d, want 200: %s", rec.Code, rec.Body)
	}

	// Bob's profile must be completely untouched.
	bobRec := a.do(t, http.MethodGet, "/api/v1/users/me", nil, &bob.ID)
	var bobBody map[string]any
	json.Unmarshal(bobRec.Body.Bytes(), &bobBody)
	if bobBody["displayName"] != bob.Handle {
		t.Errorf("bob's displayName = %v after alice's PATCH, want unchanged fallback %q",
			bobBody["displayName"], bob.Handle)
	}

	// And alice's own change did take effect.
	aliceRec := a.do(t, http.MethodGet, "/api/v1/users/me", nil, &alice.ID)
	var aliceBody map[string]any
	json.Unmarshal(aliceRec.Body.Bytes(), &aliceBody)
	if aliceBody["displayName"] != aliceName {
		t.Errorf("alice's displayName = %v, want %q", aliceBody["displayName"], aliceName)
	}
}

func TestUpdateMeRequiresAuth(t *testing.T) {
	a := newAPI(t)
	rec := a.do(t, http.MethodPatch, "/api/v1/users/me", gin.H{"displayName": "x"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("PATCH /me without auth = %d, want 401", rec.Code)
	}
}

func TestUpdateMeRejectsInvalidInput(t *testing.T) {
	a := newAPI(t)
	u := a.createUser(t)

	tooLong := strings.Repeat("a", users.MaxDisplayNameLen+1)
	rec := a.do(t, http.MethodPatch, "/api/v1/users/me", gin.H{"displayName": tooLong}, &u.ID)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("over-length display name = %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "users.invalid_display_name") {
		t.Errorf("body does not carry the error code: %s", rec.Body)
	}
}

func TestGetByIDReturnsPublicProjectionWithoutAuth(t *testing.T) {
	a := newAPI(t)
	u := a.createUser(t)

	rec := a.do(t, http.MethodGet, "/api/v1/users/"+u.ID.String(), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /users/:id without auth = %d, want 200: %s", rec.Code, rec.Body)
	}

	body := rec.Body.String()
	if strings.Contains(body, u.Email) {
		t.Errorf("public profile response leaked the email: %s", body)
	}
	if !strings.Contains(body, u.Handle) {
		t.Errorf("public profile response missing the handle: %s", body)
	}
}

// The public endpoint must show whichever name the account holder is currently presenting, not
// leak how a viewer happens to have last seen it — there is no caching layer here to worry about,
// but the assertion documents that byID always re-reads the current state.
func TestGetByIDReflectsCurrentDisplayName(t *testing.T) {
	a := newAPI(t)
	u := a.createUser(t)

	name := "Table Legend"
	a.do(t, http.MethodPatch, "/api/v1/users/me", gin.H{"displayName": name}, &u.ID)

	rec := a.do(t, http.MethodGet, "/api/v1/users/"+u.ID.String(), nil, nil)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["displayName"] != name {
		t.Errorf("public displayName = %v, want %q", body["displayName"], name)
	}
}

func TestGetByIDRejectsMalformedID(t *testing.T) {
	a := newAPI(t)
	rec := a.do(t, http.MethodGet, "/api/v1/users/not-a-uuid", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id = %d, want 400", rec.Code)
	}
}

func TestGetByIDReturns404ForUnknownUser(t *testing.T) {
	a := newAPI(t)
	unknown, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}

	rec := a.do(t, http.MethodGet, "/api/v1/users/"+unknown.String(), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
}

// /me and /:id must never disagree about what data they are allowed to show — this cross-checks
// the two response shapes structurally rather than by convention.
func TestMeAndPublicResponsesHaveDisjointSecrets(t *testing.T) {
	a := newAPI(t)
	u := a.createUser(t)

	meRec := a.do(t, http.MethodGet, "/api/v1/users/me", nil, &u.ID)
	pubRec := a.do(t, http.MethodGet, "/api/v1/users/"+u.ID.String(), nil, nil)

	var pubBody map[string]any
	if err := json.Unmarshal(pubRec.Body.Bytes(), &pubBody); err != nil {
		t.Fatalf("public response is not valid JSON: %v", err)
	}
	if _, present := pubBody["email"]; present {
		t.Error("public projection has an email field at all")
	}

	// /me must still show the owner everything.
	if !strings.Contains(meRec.Body.String(), u.Email) {
		t.Error("/me does not show the owner their own email")
	}
}
