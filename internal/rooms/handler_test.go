package rooms_test

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
	"github.com/sassinzz13/billiards-online/internal/rooms"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
	"github.com/sassinzz13/billiards-online/platform/security"
)

// rooms legally imports auth directly (L4 may depend on L1 — MEMORY.md §5), so these tests use the
// real auth.Service and real sessions rather than a stand-in, unlike internal/users' handler tests
// which need one because users (L0) cannot import auth at all.
type roomsAPI struct {
	router *gin.Engine
	auth   *auth.Service
	users  *users.Service
	rooms  *rooms.Service
}

func newRoomsAPI(t *testing.T) roomsAPI {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tx := pgtest.DB(t)
	usersSvc := users.NewService(tx)
	authSvc := auth.NewService(tx, usersSvc)
	roomsSvc := rooms.NewService(tx)

	limiter := security.NewRateLimiter(100, 100, time.Minute)
	t.Cleanup(limiter.Close)

	r := gin.New()
	rooms.NewHandler(roomsSvc, authSvc, limiter).RegisterRoutes(r.Group("/api/v1"))

	return roomsAPI{router: r, auth: authSvc, users: usersSvc, rooms: roomsSvc}
}

// signIn creates a fresh account and returns the session cookie a real client would carry — the
// same mechanism internal/auth's own tests use, so a bug in rooms' consumption of RequireAuth would
// be caught the same way a bug in RequireAuth itself would be.
//
// The handle is a fresh UUID fragment rather than derived from t.Name(): several tests sign in more
// than one account, and a long subtest name truncated to fit the handle length limit can collide
// with itself if the uniquifying part is cut off in the process.
func (a roomsAPI) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	res, err := a.auth.Signup(pgtest.Context(t), id+"@example.com", "u"+id, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: string(res.Token)}
}

func (a roomsAPI) do(t *testing.T, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func TestRoomsRoutesRequireAuth(t *testing.T) {
	a := newRoomsAPI(t)

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/rooms"},
		{http.MethodGet, "/api/v1/rooms"},
		{http.MethodPost, "/api/v1/rooms/join-by-code"},
		{http.MethodGet, "/api/v1/rooms/01a00000-0000-7000-8000-000000000000"},
		{http.MethodPost, "/api/v1/rooms/01a00000-0000-7000-8000-000000000000/join"},
		{http.MethodPost, "/api/v1/rooms/01a00000-0000-7000-8000-000000000000/leave"},
		{http.MethodPost, "/api/v1/rooms/01a00000-0000-7000-8000-000000000000/ready"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := a.do(t, tc.method, tc.path, nil, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestCreateAndGetRoomOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	cookie := a.signIn(t)

	rec := a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{
		"visibility": "public", "mode": "1v1",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", rec.Code, rec.Body)
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	roomID, _ := created["id"].(string)
	if roomID == "" {
		t.Fatalf("response has no id: %s", rec.Body)
	}
	if members, _ := created["members"].([]any); len(members) != 1 {
		t.Errorf("members = %v, want 1 (the host)", created["members"])
	}
	if _, leaked := created["joinCode"]; leaked {
		t.Error("public room response leaked a joinCode field")
	}

	getRec := a.do(t, http.MethodGet, "/api/v1/rooms/"+roomID, nil, cookie)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get = %d, want 200: %s", getRec.Code, getRec.Body)
	}
}

func TestCreateValidationErrorsOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	cookie := a.signIn(t)

	rec := a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{
		"visibility": "wat", "mode": "1v1",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad visibility = %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "rooms.invalid_visibility") {
		t.Errorf("body missing error code: %s", rec.Body)
	}
}

func TestJoinLeaveAndReadyOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	hostCookie := a.signIn(t)
	guestCookie := a.signIn(t)

	createRec := a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{
		"visibility": "public", "mode": "1v1",
	}, hostCookie)
	var created map[string]any
	json.Unmarshal(createRec.Body.Bytes(), &created)
	roomID := created["id"].(string)

	joinRec := a.do(t, http.MethodPost, "/api/v1/rooms/"+roomID+"/join", nil, guestCookie)
	if joinRec.Code != http.StatusOK {
		t.Fatalf("join = %d, want 200: %s", joinRec.Code, joinRec.Body)
	}

	readyRec := a.do(t, http.MethodPost, "/api/v1/rooms/"+roomID+"/ready",
		map[string]any{"ready": true}, guestCookie)
	if readyRec.Code != http.StatusOK {
		t.Fatalf("ready = %d, want 200: %s", readyRec.Code, readyRec.Body)
	}
	var readyBody map[string]any
	json.Unmarshal(readyRec.Body.Bytes(), &readyBody)
	youAre, _ := readyBody["youAre"].(map[string]any)
	if youAre == nil || youAre["ready"] != true {
		t.Errorf("youAre.ready = %v after marking ready, want true: %s", readyBody["youAre"], readyRec.Body)
	}

	leaveRec := a.do(t, http.MethodPost, "/api/v1/rooms/"+roomID+"/leave", nil, guestCookie)
	if leaveRec.Code != http.StatusNoContent {
		t.Fatalf("leave = %d, want 204: %s", leaveRec.Code, leaveRec.Body)
	}

	// The guest is gone; a second leave must be a clean conflict, not a crash.
	secondLeaveRec := a.do(t, http.MethodPost, "/api/v1/rooms/"+roomID+"/leave", nil, guestCookie)
	if secondLeaveRec.Code != http.StatusConflict {
		t.Errorf("second leave = %d, want 409: %s", secondLeaveRec.Code, secondLeaveRec.Body)
	}
}

func TestPrivateRoomJoinByCodeOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	hostCookie := a.signIn(t)
	guestCookie := a.signIn(t)

	createRec := a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{
		"visibility": "private", "mode": "1v1",
	}, hostCookie)
	var created map[string]any
	json.Unmarshal(createRec.Body.Bytes(), &created)
	code, _ := created["joinCode"].(string)
	if code == "" {
		t.Fatalf("private room response has no joinCode: %s", createRec.Body)
	}
	roomID := created["id"].(string)

	// The direct /:id/join path is public-only.
	wrongPathRec := a.do(t, http.MethodPost, "/api/v1/rooms/"+roomID+"/join", nil, guestCookie)
	if wrongPathRec.Code != http.StatusBadRequest {
		t.Errorf("private room via /:id/join = %d, want 400: %s", wrongPathRec.Code, wrongPathRec.Body)
	}

	joinRec := a.do(t, http.MethodPost, "/api/v1/rooms/join-by-code",
		map[string]any{"code": code}, guestCookie)
	if joinRec.Code != http.StatusOK {
		t.Fatalf("join-by-code = %d, want 200: %s", joinRec.Code, joinRec.Body)
	}
}

func TestPrivateRoomNotFoundForNonMembersOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	hostCookie := a.signIn(t)
	strangerCookie := a.signIn(t)

	createRec := a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{
		"visibility": "private", "mode": "1v1",
	}, hostCookie)
	var created map[string]any
	json.Unmarshal(createRec.Body.Bytes(), &created)
	roomID := created["id"].(string)

	rec := a.do(t, http.MethodGet, "/api/v1/rooms/"+roomID, nil, strangerCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("stranger viewing private room = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestListRoomsOverHTTP(t *testing.T) {
	a := newRoomsAPI(t)
	cookie := a.signIn(t)

	a.do(t, http.MethodPost, "/api/v1/rooms", map[string]any{"visibility": "public", "mode": "1v1"}, cookie)

	rec := a.do(t, http.MethodGet, "/api/v1/rooms", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := body["rooms"]; !ok {
		t.Errorf("response missing rooms field: %s", rec.Body)
	}
}
