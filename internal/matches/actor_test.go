package matches_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// These tests need a real, committed actor running against the real pool — the rolled-back
// transaction the rest of this package's tests share cannot be read by the actor's own goroutine,
// which persists through the Service's own db (the pool), not through a test's tx. Same reasoning
// as internal/rooms/concurrency_test.go.
func newRealUser(t *testing.T, ctx context.Context, svc *users.Service) users.User {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, err := svc.Create(ctx, "actor_"+id+"@example.com", "a"+id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	pool := pgtest.Pool(t)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID) })
	return u
}

// TestActorRunsMatchToInProgressAndAdvancesTurnsOnTimeout is the Phase 6 actor exit criterion:
// the actor moves Waiting -> Starting -> InProgress on its own, assigns the first turn, and
// advances it again once the (deliberately tiny, test-only) shot timer fires — all without a
// client ever sending anything, since there is no shot mechanism until Phase 9.
func TestActorRunsMatchToInProgressAndAdvancesTurnsOnTimeout(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := pgtest.Context(t)
	usersSvc := users.NewService(pool)

	a := newRealUser(t, ctx, usersSvc)
	b := newRealUser(t, ctx, usersSvc)

	registry := matches.NewRegistry()
	actorCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	matchesSvc := matches.NewService(pool, registry, actorCtx, nil)

	m, err := matchesSvc.Create(ctx, matches.CreateInput{
		RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball",
		// 1 second, not MEMORY.md §21's real 15-120s range — this test wants to actually observe a
		// timeout, not wait out a real shot clock.
		ShotTimerSeconds: 1,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{a.ID}},
			{ID: matches.SideB, Players: []uuid.UUID{b.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM matches WHERE id = $1`, m.ID) })

	waitForState(t, matchesSvc, m.ID, matches.StateInProgress, 3*time.Second)

	first, err := matchesSvc.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if first.Turn == nil {
		t.Fatal("Turn is nil after the match started, want the first turn assigned")
	}
	if first.StartedAt == nil {
		t.Error("StartedAt is nil after entering InProgress")
	}
	firstTurn := *first.Turn

	// Wait past the 1s shot timer for the actor to advance the turn on its own.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := matchesSvc.Get(ctx, m.ID)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		if cur.Turn != nil && *cur.Turn != firstTurn {
			return // turn advanced — the timer fired and the actor acted on it
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("turn never advanced after the shot timer should have fired")
}

// TestActorRemovesItselfFromRegistryOnExit is the "no leaks" half of the actor lifecycle checklist
// item: once ctx is cancelled, the actor's goroutine returns and the registry no longer references
// it — checked by polling Registry.Len rather than a fixed sleep, since exactly how long
// persistence + cleanup takes is not this test's business.
func TestActorRemovesItselfFromRegistryOnExit(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := pgtest.Context(t)
	usersSvc := users.NewService(pool)

	a := newRealUser(t, ctx, usersSvc)
	b := newRealUser(t, ctx, usersSvc)

	registry := matches.NewRegistry()
	actorCtx, cancel := context.WithCancel(context.Background())
	matchesSvc := matches.NewService(pool, registry, actorCtx, nil)

	m, err := matchesSvc.Create(ctx, matches.CreateInput{
		RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball", ShotTimerSeconds: 30,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{a.ID}},
			{ID: matches.SideB, Players: []uuid.UUID{b.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM matches WHERE id = $1`, m.ID) })

	if _, ok := registry.Get(m.ID); !ok {
		t.Fatal("actor was not registered immediately after Create")
	}

	cancel() // simulate server shutdown for this one actor

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if registry.Len() == 0 {
			return // the actor removed itself — no leak
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("actor still in registry %v after ctx was cancelled, want it removed", 3*time.Second)
}

func waitForState(t *testing.T, svc *matches.Service, id uuid.UUID, want matches.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		if m.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state never reached %s within %s", want, timeout)
}
