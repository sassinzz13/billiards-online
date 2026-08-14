package matches_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// newFixture wires matches.Service and users.Service to the same rolled-back transaction, the same
// pattern internal/rooms and internal/auth use. registry is nil: these tests exercise persistence,
// not the actor — see matches.Service.StartActor's doc comment for why that is safe.
func newFixture(t *testing.T) (*matches.Service, *users.Service, pgx.Tx) {
	t.Helper()
	tx := pgtest.DB(t)
	return matches.NewService(tx, nil, context.Background(), nil), users.NewService(tx), tx
}

func newUser(t *testing.T, svc *users.Service) users.User {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, err := svc.Create(pgtest.Context(t), "player_"+id+"@example.com", "p"+id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestCreateInsertsMatchWithSidesAndParticipants(t *testing.T) {
	matchesSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)

	a1, a2 := newUser(t, usersSvc), newUser(t, usersSvc)
	b1, b2 := newUser(t, usersSvc), newUser(t, usersSvc)
	roomID := uuid.Must(uuid.NewV7())

	m, err := matchesSvc.Create(ctx, matches.CreateInput{
		RoomID: roomID, Mode: matches.Mode2v2, Ranked: true, Ruleset: "8ball", ShotTimerSeconds: 30,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{a1.ID, a2.ID}},
			{ID: matches.SideB, Players: []uuid.UUID{b1.ID, b2.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if m.State != matches.StateWaiting {
		t.Errorf("State = %v, want waiting (Create only inserts; the actor moves it onward)", m.State)
	}
	if m.RoomID != roomID {
		t.Errorf("RoomID = %v, want %v", m.RoomID, roomID)
	}
	if len(m.Sides[0].Players) != 2 || len(m.Sides[1].Players) != 2 {
		t.Fatalf("Sides = %+v, want 2 players per side", m.Sides)
	}
	if m.Sides[0].Players[0] != a1.ID || m.Sides[0].Players[1] != a2.ID {
		t.Errorf("side A players = %v, want [%v %v]", m.Sides[0].Players, a1.ID, a2.ID)
	}

	fetched, err := matchesSvc.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if fetched.ID != m.ID || fetched.Mode != matches.Mode2v2 || !fetched.Ranked {
		t.Errorf("Get() = %+v, want a match matching Create()'s input", fetched)
	}
}

func TestGetUnknownMatchReturnsErrNotFound(t *testing.T) {
	matchesSvc, _, _ := newFixture(t)
	ctx := pgtest.Context(t)

	_, err := matchesSvc.Get(ctx, uuid.Must(uuid.NewV7()))
	if err != matches.ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestListForUserReturnsOnlyMatchesTheyParticipatedIn(t *testing.T) {
	matchesSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)

	inIt := newUser(t, usersSvc)
	bystander := newUser(t, usersSvc)
	opponent := newUser(t, usersSvc)

	_, err := matchesSvc.Create(ctx, matches.CreateInput{
		RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball", ShotTimerSeconds: 30,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{inIt.ID}},
			{ID: matches.SideB, Players: []uuid.UUID{opponent.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	items, _, err := matchesSvc.ListForUser(ctx, inIt.ID, "", 0)
	if err != nil {
		t.Fatalf("ListForUser(inIt) = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListForUser(inIt) returned %d matches, want 1", len(items))
	}

	items, _, err = matchesSvc.ListForUser(ctx, bystander.ID, "", 0)
	if err != nil {
		t.Fatalf("ListForUser(bystander) = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("ListForUser(bystander) returned %d matches, want 0", len(items))
	}
}
