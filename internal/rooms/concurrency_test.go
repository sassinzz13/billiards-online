package rooms_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/rooms"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// This test needs genuinely concurrent transactions racing on the same room row. A single
// pgx.Tx wraps one connection and is not safe for concurrent use by multiple goroutines — every
// other test in this package runs inside one shared, rolled-back transaction for isolation, but
// that model cannot produce the race this test exists to prove doesn't happen. So this is the one
// test in the package that talks to the real pool, commits real rows, and cleans up after itself
// explicitly rather than relying on rollback.
//
// This is the Phase 4 exit criterion, verified directly: concurrent joins on the last open seat
// produce exactly one winner. The mechanism is the FOR UPDATE row lock taken in
// selectRoomForUpdate (repository.go) — every join transaction for a given room serializes behind
// it, so only one of the racing transactions ever observes the seat as free.
func TestConcurrentJoinsOnTheLastSeatProduceExactlyOneWinner(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := pgtest.Context(t)

	roomsSvc := rooms.NewService(pool)
	usersSvc := users.NewService(pool)

	host := newRealUser(t, ctx, usersSvc)
	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1, // capacity 2: host + exactly one more seat
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	t.Cleanup(func() {
		// Cascades to room_members; the contenders are cleaned up separately below.
		pool.Exec(context.Background(), `DELETE FROM rooms WHERE id = $1`, created.Room.ID)
	})

	const contenders = 12 // all racing for the single remaining seat
	contenderUsers := make([]users.User, contenders)
	for i := range contenderUsers {
		contenderUsers[i] = newRealUser(t, ctx, usersSvc)
	}

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		successes   []uuid.UUID
		fullErrors  int
		otherErrors []error
	)
	start := make(chan struct{}) // released only once every goroutine is ready, to align the race

	for _, u := range contenderUsers {
		wg.Add(1)
		go func(userID uuid.UUID) {
			defer wg.Done()
			<-start
			_, err := roomsSvc.Join(ctx, created.Room.ID, userID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes = append(successes, userID)
			case errors.Is(err, rooms.ErrRoomFull):
				fullErrors++
			default:
				otherErrors = append(otherErrors, err)
			}
		}(u.ID)
	}

	close(start) // all goroutines proceed together
	wg.Wait()

	for _, err := range otherErrors {
		t.Errorf("unexpected error from a racing Join: %v", err)
	}
	if len(successes) != 1 {
		t.Fatalf("%d concurrent joins succeeded for the one open seat, want exactly 1 (winners: %v)",
			len(successes), successes)
	}
	if fullErrors != contenders-1 {
		t.Errorf("%d joins got ErrRoomFull, want %d", fullErrors, contenders-1)
	}

	// The database must agree: exactly `capacity` rows, no more — the UNIQUE(room_id, side, slot)
	// backstop holding even under real concurrency, not just the row lock.
	var memberCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id = $1`, created.Room.ID).
		Scan(&memberCount); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if memberCount != rooms.Mode1v1.Capacity() {
		t.Errorf("final member count = %d, want capacity %d", memberCount, rooms.Mode1v1.Capacity())
	}
}

// newRealUser commits directly to the pool — see the package comment above for why this test
// cannot use the shared rolled-back transaction the rest of the suite relies on.
func newRealUser(t *testing.T, ctx context.Context, svc *users.Service) users.User {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, err := svc.Create(ctx, "racer_"+id+"@example.com", "r"+id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	pool := pgtest.Pool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	return u
}
