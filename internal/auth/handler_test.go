package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
	"github.com/sassinzz13/billiards-online/platform/security"
)

type api struct {
	router  *gin.Engine
	limiter *security.RateLimiter
	next    func() (email, handle string)
}

func newAPI(t *testing.T, rate float64, burst int) api {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tx := pgtest.DB(t)
	svc := auth.NewService(tx, users.NewService(tx))

	limiter := security.NewRateLimiter(rate, burst, time.Minute)
	t.Cleanup(limiter.Close)

	r := gin.New()
	// secureCookies=false mirrors local development; the production path is covered separately.
	auth.NewHandler(svc, false, limiter).RegisterRoutes(r.Group("/api/v1"))

	return api{
		router:  r,
		limiter: limiter,
		next: func() (string, string) {
			id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			return "player_" + id + "@example.com", "p" + id
		},
	}
}

func (a api) do(t *testing.T, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response", auth.CookieName)
	return nil
}

func (a api) signup(t *testing.T) (*http.Cookie, string, string) {
	t.Helper()
	email, handle := a.next()

	rec := a.do(t, http.MethodPost, "/api/v1/auth/signup", gin.H{
		"email": email, "handle": handle, "password": testPassword,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d, want 201: %s", rec.Code, rec.Body)
	}
	return sessionCookie(t, rec), email, handle
}

func TestSignupSetsHardenedCookie(t *testing.T) {
	a := newAPI(t, 100, 100)
	cookie, _, _ := a.signup(t)

	if cookie.Value == "" {
		t.Fatal("session cookie has no value")
	}
	// HttpOnly is what stops an XSS from reading the session. Losing it silently would undo the
	// main reason for choosing cookies over localStorage (ADR 0009).
	if !cookie.HttpOnly {
		t.Error("cookie is not HttpOnly — script could read the session token")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie Path = %q, want /", cookie.Path)
	}
	// No Domain: the cookie stays scoped to the exact host, rather than every sibling subdomain.
	if cookie.Domain != "" {
		t.Errorf("cookie Domain = %q, want empty so scope is not widened", cookie.Domain)
	}
	if !cookie.Expires.After(time.Now()) {
		t.Errorf("cookie Expires = %v, want a future time", cookie.Expires)
	}
}

// Secure must follow APP_ENV, so production cannot ship a cookie that travels in the clear.
func TestProductionCookieIsSecure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tx := pgtest.DB(t)
	svc := auth.NewService(tx, users.NewService(tx))
	limiter := security.NewRateLimiter(100, 100, time.Minute)
	t.Cleanup(limiter.Close)

	r := gin.New()
	auth.NewHandler(svc, true, limiter).RegisterRoutes(r.Group("/api/v1")) // secureCookies = true

	a := api{router: r, limiter: limiter, next: newAPI(t, 100, 100).next}
	cookie, _, _ := a.signup(t)

	if !cookie.Secure {
		t.Error("cookie is not Secure when built for production")
	}
}

// The token goes out as a cookie and must never appear in the body, where script could read it.
func TestResponseBodyNeverContainsSecrets(t *testing.T) {
	a := newAPI(t, 100, 100)
	email, handle := a.next()

	rec := a.do(t, http.MethodPost, "/api/v1/auth/signup", gin.H{
		"email": email, "handle": handle, "password": testPassword,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d: %s", rec.Code, rec.Body)
	}

	body := rec.Body.String()
	cookie := sessionCookie(t, rec)

	for _, secret := range map[string]string{
		"session token":      cookie.Value,
		"plaintext password": testPassword,
		"argon2 hash marker": "$argon2id$",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("response body contains %q:\n%s", secret, body)
		}
	}

	// It should carry the account, though.
	var parsed struct {
		User  map[string]any `json:"user"`
		Email string         `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed.User["handle"] != handle {
		t.Errorf("user.handle = %v, want %q", parsed.User["handle"], handle)
	}
	if _, leaked := parsed.User["email"]; leaked {
		t.Error("the public user projection leaked an email field")
	}
}

func TestSessionEndpointRequiresAuth(t *testing.T) {
	a := newAPI(t, 100, 100)

	if rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("session without cookie = %d, want 401", rec.Code)
	}

	forged := &http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"}
	if rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil, forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("session with forged cookie = %d, want 401", rec.Code)
	}

	cookie, _, handle := a.signup(t)
	rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("session with valid cookie = %d, want 200: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), handle) {
		t.Errorf("session response does not name the account: %s", rec.Body)
	}
}

// The end-to-end property this phase exists to prove: a revoked session stops working immediately.
func TestLogoutEndsTheSessionImmediately(t *testing.T) {
	a := newAPI(t, 100, 100)
	cookie, _, _ := a.signup(t)

	if rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("session before logout = %d, want 200", rec.Code)
	}

	rec := a.do(t, http.MethodPost, "/api/v1/auth/logout", nil, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204: %s", rec.Code, rec.Body)
	}
	// The clearing cookie must also be HttpOnly, or the response weakens what it is replacing.
	cleared := sessionCookie(t, rec)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("logout cookie = %+v, want an immediate expiry", cleared)
	}

	if rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil, cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("session after logout = %d, want 401 — revocation is not immediate", rec.Code)
	}
}

// Logging out twice, or without a session, must succeed. Reporting "you were not logged in" would
// tell someone holding a stolen cookie whether it was still live.
func TestLogoutIsIdempotentOverHTTP(t *testing.T) {
	a := newAPI(t, 100, 100)
	cookie, _, _ := a.signup(t)

	for i := range 3 {
		if rec := a.do(t, http.MethodPost, "/api/v1/auth/logout", nil, cookie); rec.Code != http.StatusNoContent {
			t.Errorf("logout %d = %d, want 204", i+1, rec.Code)
		}
	}
	if rec := a.do(t, http.MethodPost, "/api/v1/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Errorf("logout with no cookie = %d, want 204", rec.Code)
	}
}

func TestLoginRejectsBadCredentialsUniformly(t *testing.T) {
	a := newAPI(t, 100, 100)
	_, email, _ := a.signup(t)

	cases := []struct{ name, email, password string }{
		{"wrong password", email, "totally-wrong-password"},
		{"unknown account", "ghost-" + email, testPassword},
	}

	var bodies []string
	for _, tc := range cases {
		rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{
			"email": tc.email, "password": tc.password,
		})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", tc.name, rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}

	// Identical responses: anything else lets an attacker enumerate registered addresses.
	if bodies[0] != bodies[1] {
		t.Errorf("wrong password and unknown account give different responses:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestSignupRejectsDuplicateAndInvalidInput(t *testing.T) {
	a := newAPI(t, 100, 100)
	_, email, handle := a.signup(t)
	otherEmail, otherHandle := a.next()

	tests := []struct {
		name      string
		body      gin.H
		wantCode  int
		wantCode2 string
	}{
		{"duplicate email", gin.H{"email": email, "handle": otherHandle, "password": testPassword}, http.StatusConflict, "auth.email_taken"},
		{"duplicate handle", gin.H{"email": otherEmail, "handle": handle, "password": testPassword}, http.StatusConflict, "auth.handle_taken"},
		{"bad email", gin.H{"email": "nope", "handle": otherHandle, "password": testPassword}, http.StatusBadRequest, "auth.invalid_email"},
		{"bad handle", gin.H{"email": otherEmail, "handle": "x", "password": testPassword}, http.StatusBadRequest, "auth.invalid_handle"},
		{"short password", gin.H{"email": otherEmail, "handle": otherHandle, "password": "short"}, http.StatusBadRequest, "auth.invalid_password"},
		{"missing fields", gin.H{"email": otherEmail}, http.StatusBadRequest, "auth.invalid_request"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := a.do(t, http.MethodPost, "/api/v1/auth/signup", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantCode2) {
				t.Errorf("body does not carry code %q: %s", tc.wantCode2, rec.Body)
			}
		})
	}
}

// Each login attempt costs 64 MiB of Argon2, so the rate limit protects memory as much as it
// protects accounts (§59).
func TestLoginIsRateLimited(t *testing.T) {
	a := newAPI(t, 0.001, 3) // 3 attempts, effectively no refill during the test
	_, email, _ := a.signup(t)

	for i := range 3 {
		rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{
			"email": email, "password": "wrong-password-here",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, rec.Code)
		}
	}

	rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{
		"email": email, "password": "wrong-password-here",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("attempt 4 = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "auth.rate_limited") {
		t.Errorf("429 body does not carry the code: %s", rec.Body)
	}

	// Even the correct password is refused while limited — otherwise the limit would be trivially
	// bypassed by a stuffing attack that happens to guess right.
	rec = a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{
		"email": email, "password": testPassword,
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("correct password while limited = %d, want 429", rec.Code)
	}
}

// A successful login clears the counter, so a real person who mistyped twice is not left limited.
func TestSuccessfulLoginResetsTheLimit(t *testing.T) {
	a := newAPI(t, 0.001, 3)
	_, email, _ := a.signup(t)

	for range 2 {
		a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{"email": email, "password": "wrong"})
	}

	if rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{
		"email": email, "password": testPassword,
	}); rec.Code != http.StatusOK {
		t.Fatalf("login with correct password = %d, want 200: %s", rec.Code, rec.Body)
	}

	// Budget restored.
	for i := range 3 {
		rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{"email": email, "password": "wrong"})
		if rec.Code == http.StatusTooManyRequests {
			t.Errorf("attempt %d after a successful login was rate limited", i+1)
		}
	}
}

func TestLogoutAllRevokesEveryDevice(t *testing.T) {
	a := newAPI(t, 100, 100)
	first, email, _ := a.signup(t)

	rec := a.do(t, http.MethodPost, "/api/v1/auth/login", gin.H{"email": email, "password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("second login = %d: %s", rec.Code, rec.Body)
	}
	second := sessionCookie(t, rec)

	if rec := a.do(t, http.MethodPost, "/api/v1/auth/logout-all", nil, first); rec.Code != http.StatusOK {
		t.Fatalf("logout-all = %d, want 200: %s", rec.Code, rec.Body)
	}

	for name, c := range map[string]*http.Cookie{"first": first, "second": second} {
		if rec := a.do(t, http.MethodGet, "/api/v1/auth/session", nil, c); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s session = %d after logout-all, want 401", name, rec.Code)
		}
	}
}
