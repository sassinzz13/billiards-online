package matches_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// See internal/rooms/handler_test.go's roomsAPI for why real auth.Service and real sessions are
// used here rather than a stand-in: matches (L3) legally imports auth (L1) directly.
type matchesAPI struct {
	router  *gin.Engine
	auth    *auth.Service
	users   *users.Service
	matches *matches.Service
}

func newMatchesAPI(t *testing.T) matchesAPI {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tx := pgtest.DB(t)
	usersSvc := users.NewService(tx)
	authSvc := auth.NewService(tx, usersSvc)
	matchesSvc := matches.NewService(tx, nil, context.Background(), nil)

	r := gin.New()
	matches.NewHandler(matchesSvc, authSvc).RegisterRoutes(r.Group("/api/v1"))

	return matchesAPI{router: r, auth: authSvc, users: usersSvc, matches: matchesSvc}
}

func (a matchesAPI) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	res, err := a.auth.Signup(pgtest.Context(t), id+"@example.com", "u"+id, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: string(res.Token)}
}

func (a matchesAPI) do(t *testing.T, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func TestMatchesRoutesRequireAuth(t *testing.T) {
	a := newMatchesAPI(t)
	id := uuid.Must(uuid.NewV7())

	for _, path := range []string{"/api/v1/matches/" + id.String(), "/api/v1/users/" + id.String() + "/matches"} {
		rec := a.do(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
	}
}

func TestGetMatchReturnsSidesAndState(t *testing.T) {
	a := newMatchesAPI(t)
	cookie := a.signIn(t)
	ctx := pgtest.Context(t)

	p1, err := a.users.Create(ctx, "matchp1_"+uuid.NewString()[:8]+"@example.com", "mp1"+uuid.NewString()[:6])
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p2, err := a.users.Create(ctx, "matchp2_"+uuid.NewString()[:8]+"@example.com", "mp2"+uuid.NewString()[:6])
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	m, err := a.matches.Create(ctx, matches.CreateInput{
		RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball", ShotTimerSeconds: 30,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{p1.ID}},
			{ID: matches.SideB, Players: []uuid.UUID{p2.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	rec := a.do(t, http.MethodGet, "/api/v1/matches/"+m.ID.String(), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET match = %d, body %s", rec.Code, rec.Body.String())
	}

	var body struct {
		State string `json:"state"`
		Sides [2]struct {
			Players []uuid.UUID `json:"players"`
		} `json:"sides"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.State != string(matches.StateWaiting) {
		t.Errorf("state = %q, want %q", body.State, matches.StateWaiting)
	}
	if len(body.Sides[0].Players) != 1 || body.Sides[0].Players[0] != p1.ID {
		t.Errorf("side A = %+v, want [%v]", body.Sides[0].Players, p1.ID)
	}

	rec = a.do(t, http.MethodGet, "/api/v1/matches/"+uuid.Must(uuid.NewV7()).String(), cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown match = %d, want 404", rec.Code)
	}
}

func TestListMatchesForUserPaginates(t *testing.T) {
	a := newMatchesAPI(t)
	cookie := a.signIn(t)
	ctx := pgtest.Context(t)

	p1, err := a.users.Create(ctx, "listp1_"+uuid.NewString()[:8]+"@example.com", "lp1"+uuid.NewString()[:6])
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p2, err := a.users.Create(ctx, "listp2_"+uuid.NewString()[:8]+"@example.com", "lp2"+uuid.NewString()[:6])
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for range 3 {
		_, err := a.matches.Create(ctx, matches.CreateInput{
			RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball", ShotTimerSeconds: 30,
			Sides: [2]matches.Side{
				{ID: matches.SideA, Players: []uuid.UUID{p1.ID}},
				{ID: matches.SideB, Players: []uuid.UUID{p2.ID}},
			},
		})
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
	}

	rec := a.do(t, http.MethodGet, "/api/v1/users/"+p1.ID.String()+"/matches?limit=2", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET user matches = %d, body %s", rec.Code, rec.Body.String())
	}

	var page struct {
		Matches    []json.RawMessage `json:"matches"`
		NextCursor string            `json:"nextCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(page.Matches) != 2 {
		t.Fatalf("first page = %d matches, want 2", len(page.Matches))
	}
	if page.NextCursor == "" {
		t.Fatal("nextCursor is empty, want a cursor for the third match")
	}

	rec = a.do(t, http.MethodGet, "/api/v1/users/"+p1.ID.String()+"/matches?cursor="+page.NextCursor, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET second page = %d, body %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(page.Matches) != 1 {
		t.Errorf("second page = %d matches, want 1", len(page.Matches))
	}
}
